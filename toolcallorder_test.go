package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/media"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 工具消息的配对顺序 =====
//
// 这是一条硬约束：assistant(tool_calls) 之后必须紧跟**连续**的 tool 消息，且覆盖它声明的
// 每一个 tool_call_id。一轮里若有多个工具调用、而产出图片的又不是最后一个，承载图片的
// user 消息一旦插在两条 tool 消息中间，配对就被切断，OpenAI 系网关直接报 400：
//
//	An assistant message with 'tool_calls' must be followed by tool messages responding
//	to each 'tool_call_id'. (insufficient tool messages following tool_calls message)
//
// 阴险的地方是它**当次不报错**（断裂的序列已经落库），此后每次从历史重建请求都被拒。
// 用户看到的是"这次发送失败"，实际是整个会话废掉——重启应用也救不回来。
// 真机上它表现为"偶发失败"，因为只有「多工具调用 + 产图的不是最后一个」才触发。

// assertToolCallPairing 复刻 OpenAI 系的配对校验，本地先按网关的规则断言。
// 不这么测的话，错误只会在真机上以 400 的形式出现——而且是在**下一次**请求里。
func assertToolCallPairing(t *testing.T, msgs []llm.LLMMessage, where string) {
	t.Helper()
	for i, m := range msgs {
		if m.Role != store.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		answered := map[string]bool{}
		// 只数**紧跟其后、连续**的 tool 消息：中间插进任何别的角色，配对就断在这里
		for j := i + 1; j < len(msgs) && msgs[j].Role == store.RoleTool; j++ {
			answered[msgs[j].ToolCallID] = true
		}
		for _, tc := range m.ToolCalls {
			if !answered[tc.ID] {
				t.Fatalf("%s：第 %d 条消息声明的 tool_call %q 没有被紧随其后的 tool 消息应答（网关会报 400）",
					where, i, tc.ID)
			}
		}
	}
}

// wireMessage 请求体里的一条消息，按**线上格式**解析。
//
// 刻意不解成 llm.LLMMessage：带图的消息 content 是块数组，解回中立结构会失败
// （LLMMessage 只自定义了 MarshalJSON），于是那条消息会静默变成空壳——
// 而网关看到的正是这份原始形状，所以断言也该落到这一层。
type wireMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  []struct {
		ID string `json:"id"`
	} `json:"tool_calls"`
}

func (m wireMessage) hasImage() bool {
	return bytes.Contains(m.Content, []byte(`"image_url"`))
}

// neutral 只保留配对判定需要的字段，好复用同一份规则实现
func (m wireMessage) neutral() llm.LLMMessage {
	if len(m.ToolCalls) == 0 {
		return llm.LLMMessage{Role: m.Role, ToolCallID: m.ToolCallID}
	}
	calls := make([]llm.LLMToolCall, 0, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		calls = append(calls, llm.LLMToolCall{ID: tc.ID})
	}
	return llm.LLMMessage{Role: m.Role, ToolCalls: calls}
}

func neutralSeq(wire []wireMessage) []llm.LLMMessage {
	out := make([]llm.LLMMessage, 0, len(wire))
	for _, m := range wire {
		out = append(out, m.neutral())
	}
	return out
}

func rolesOf(msgs []llm.LLMMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Role)
	}
	return out
}

// 纯装配路径：一轮两个调用、产图的是**第一个**——图片消息必须排在两条 tool 之后。
func TestBuildLLMMessagesKeepsToolMessagesContiguous(t *testing.T) {
	st := media.NewAttachmentStore(t.TempDir())
	att, err := st.Save("s1", "shot.png", makeTestPNG(t, 64, 64), media.AttachmentSourceTool)
	if err != nil {
		t.Fatalf("存图失败: %v", err)
	}
	history := []store.Message{
		{ID: "m1", Role: store.RoleAssistant, ToolCalls: []store.ToolCall{
			{ID: "c1", Name: tool.ToolReadImage, Status: "success"},
			{ID: "c2", Name: tool.ToolReadFile, Status: "success"},
		}},
		{ID: "m2", Role: store.RoleTool, Content: "已读取图片", ToolCallID: "c1", Attachments: []media.Attachment{*att}},
		{ID: "m3", Role: store.RoleTool, Content: "文件内容", ToolCallID: "c2"},
		{ID: "m4", Role: store.RoleUser, Content: "接着做"},
	}

	msgs := buildLLMMessages(history, "SYS", imageLoaderFor(st))
	want := []string{store.RoleSystem, store.RoleAssistant, store.RoleTool, store.RoleTool, store.RoleUser, store.RoleUser}
	if got := rolesOf(msgs); !reflect.DeepEqual(got, want) {
		t.Fatalf("图片消息必须排在两条 tool 消息之后，实际序列: %v", got)
	}
	if len(msgs[4].Images) != 1 || !strings.Contains(msgs[4].Content, tool.ToolReadImage) {
		t.Fatalf("第 5 条应是承载图片、并点名 read_image 的 user 消息，实际 %+v", msgs[4])
	}
	assertToolCallPairing(t, msgs, "buildLLMMessages")
}

// 历史以工具结果收尾（运行被中断留下的一段）时，挂起的图片也要补上，不能丢。
func TestBuildLLMMessagesFlushesTrailingToolImages(t *testing.T) {
	st := media.NewAttachmentStore(t.TempDir())
	att, err := st.Save("s1", "shot.png", makeTestPNG(t, 64, 64), media.AttachmentSourceTool)
	if err != nil {
		t.Fatalf("存图失败: %v", err)
	}
	history := []store.Message{
		{ID: "m1", Role: store.RoleAssistant, ToolCalls: []store.ToolCall{{ID: "c1", Name: tool.ToolReadImage}}},
		{ID: "m2", Role: store.RoleTool, Content: "已读取图片", ToolCallID: "c1", Attachments: []media.Attachment{*att}},
	}
	msgs := buildLLMMessages(history, "SYS", imageLoaderFor(st))
	if len(msgs) != 4 {
		t.Fatalf("应是 system + assistant + tool + 图片 user 共 4 条，实际 %d", len(msgs))
	}
	if msgs[3].Role != store.RoleUser || len(msgs[3].Images) != 1 {
		t.Fatalf("历史以工具结果收尾时也要补上图片消息，实际 %+v", msgs[3])
	}
	assertToolCallPairing(t, msgs, "历史以工具结果收尾")
}

// 端到端：走真实的 runToolLoop（mock LLM 返回两个工具调用，产图的排第一），
// 断言第二次请求**实际发出去的字节**，以及"同一批历史从会话重建"后的序列。
//
// 后者才是这个 bug 最要命的一面：落库的序列一旦断裂，之后每次发送都会被拒。
func TestToolLoopKeepsToolMessagesContiguousWhenImageIsNotFromLastCall(t *testing.T) {
	var (
		mu       sync.Mutex
		captured [][]wireMessage
		callSeq  int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []wireMessage `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")

		var resp llm.LLMResp
		if atomic.AddInt32(&callSeq, 1) == 1 {
			// 第一轮：两个工具调用，产图的排在前面 —— 正是触发 bug 的形状
			resp = llm.LLMResp{Choices: []llm.LLMChoice{{
				FinishReason: llm.FinishReasonToolCalls,
				Message: llm.LLMMessage{Role: store.RoleAssistant, ToolCalls: []llm.LLMToolCall{
					{ID: "call_img", Type: llm.ToolTypeFunction,
						Function: llm.LLMToolFunction{Name: tool.ToolReadImage, Arguments: `{"path":"shot.png"}`}},
					{ID: "call_read", Type: llm.ToolTypeFunction,
						Function: llm.LLMToolFunction{Name: tool.ToolReadFile, Arguments: `{"path":"notes.txt"}`}},
				}},
			}}}
		} else {
			mu.Lock()
			captured = append(captured, req.Messages)
			mu.Unlock()
			resp = llm.LLMResp{Choices: []llm.LLMChoice{{
				FinishReason: llm.FinishReasonStop,
				Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: "看完了"},
			}}}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	app, sess := newPlanTestApp(t, srv.URL)
	app.runs = &agent.RunRegistry{}
	app.baseDir = t.TempDir()
	app.fileChanges = tool.NewFileChangeLog(50)
	app.attachments = media.NewAttachmentStore(t.TempDir())

	// 工作区里放两个文件：一张真图（read_image 得能读出来）与一个文本文件。
	// 两个都是只读工具：即便在 manual 模式下也会直接放行，不必等用户批准。
	if err := os.WriteFile(filepath.Join(sess.Project, "shot.png"), makeTestPNG(t, 64, 64), 0o644); err != nil {
		t.Fatalf("写测试图片失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sess.Project, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("写测试文本失败: %v", err)
	}

	// 对齐前端行为：用户消息先落库，再发起对话
	if _, err := app.sessionStore.AppendMessage(sess.ID, store.Message{Role: store.RoleUser, Content: "看看这张图"}); err != nil {
		t.Fatal(err)
	}
	res := app.executeChat(sess.ID, "看看这张图", false)
	if res.Error != "" {
		t.Fatalf("对话失败: %s", res.Error)
	}
	if res.Reply != "看完了" {
		t.Fatalf("应拿到最终回复，实际 %q", res.Reply)
	}
	if len(res.ToolCalls) != 2 {
		t.Fatalf("应执行 2 个工具调用，实际 %d", len(res.ToolCalls))
	}

	mu.Lock()
	got := append([][]wireMessage(nil), captured...)
	mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("应捕获到 1 次「带工具结果」的请求，实际 %d", len(got))
	}

	// 第二次请求：system → user → assistant(2 个调用) → tool → tool → user(图片)
	msgs := got[0]
	want := []string{store.RoleSystem, store.RoleUser, store.RoleAssistant, store.RoleTool, store.RoleTool, store.RoleUser}
	roles := make([]string, 0, len(msgs))
	for _, m := range msgs {
		roles = append(roles, m.Role)
	}
	if !reflect.DeepEqual(roles, want) {
		t.Fatalf("图片消息必须排在两条 tool 消息之后，实际序列: %v", roles)
	}
	last := msgs[len(msgs)-1]
	if !last.hasImage() || !bytes.Contains(last.Content, []byte(tool.ToolReadImage)) {
		t.Fatalf("最后一条应是承载图片、并点名 read_image 的 user 消息，实际 %s", last.Content)
	}
	assertToolCallPairing(t, neutralSeq(msgs), "运行中（第二次请求）")

	// 同一批历史从会话重建：必须是同一个形状，否则这个会话此后每次发送都会被网关拒绝
	reloaded, err := app.sessionStore.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := buildRunMessages(reloaded, "SYS", false, imageLoaderFor(app.attachments))
	want = []string{store.RoleSystem, store.RoleUser, store.RoleAssistant, store.RoleTool, store.RoleTool, store.RoleUser, store.RoleAssistant}
	if got := rolesOf(rebuilt); !reflect.DeepEqual(got, want) {
		t.Fatalf("从历史重建的序列错误: %v", got)
	}
	if rebuilt[5].Role != store.RoleUser || len(rebuilt[5].Images) != 1 ||
		!strings.Contains(rebuilt[5].Content, tool.ToolReadImage) {
		t.Fatalf("重建后带图的那条仍应紧跟两条 tool 消息，实际 %+v", rebuilt[5])
	}
	assertToolCallPairing(t, rebuilt, "从历史重建（下一次发送）")
}
