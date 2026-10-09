package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/media"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// 注：本测试已按依赖边界拆分——「协议组装」的纯 llm 用例在
// internal/llm/imageinput_test.go，「看图结论 / 消息落库」的纯 store 用例在
// internal/store/imageinput_test.go。本文件只保留需要根包装配
// （buildLLMMessages / imageLoaderFor / NewToolManager）以及因在基线失败名单里
// 而刻意保留的用例。

// ===== 图片输入：从"字节进来"到"发给模型"的全链路测试 =====
//
// 这条链路的四段各自有独立的失败模式，所以分开测：
//
//	1. imageproc       规整（尺寸/格式/上限）——纯计算
//	2. AttachmentStore 落盘与读取（含路径穿越）
//	3. 协议组装        llm.LLMMessage 序列化 / Anthropic 块转换
//	4. 工具与消息       read_image 产图 → 消息附件 → 请求图片
//
// 最值钱的两条是**形状断言**：无图时 llm.LLMMessage 的 JSON 必须与加字段之前完全一致
// （快照/测试/网关都依赖它），以及 Anthropic 侧 tool_result 必须在同一个 user 回合里
// 且排在图片之前（顺序错了 API 直接拒）。

// makeTestPNG 生成一张可指定尺寸的 PNG。渐变内容避免被 PNG 压成极小体积，
// 也让"是否重编码"这类判断不至于因为内容退化而失真。
func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x % 251),
				G: uint8(y % 241),
				B: uint8((x + y) % 231),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("生成测试 PNG 失败: %v", err)
	}
	return buf.Bytes()
}

// 图片规整与附件存储两节的用例已迁到 internal/media/media_test.go
// （它们只依赖 media 包的导出 API；此处留下的用例都要 main 的协议/工具层）

// 图片 token 口径的实现在 internal/agent（ImageTokens / StoredMessageTokens）。
// 这条用例经**导出名**断言，因此留在根包：它的名字是基线失败名单里的一项，
// 迁包会让"根包失败名单与基线逐条一致"这个验收口径失去可比性。
func TestImageTokensFollowPixelCountNotBytes(t *testing.T) {
	// Anthropic 口径：(宽 × 高) / 750，向上取整
	if got := agent.ImageTokens(1568, 1568); got != 3278 {
		t.Errorf("1568×1568 应约 3278 token，实际 %d", got)
	}
	if got := agent.ImageTokens(0, 0); got != 0 {
		t.Errorf("缺尺寸时不应凭空计费，实际 %d", got)
	}
	// 关键性质：一张大尺寸的图必须比一张小尺寸的贵，与字节数无关
	big := agent.StoredMessageTokens(store.Message{
		Role:        store.RoleUser,
		Content:     "看图",
		Attachments: []media.Attachment{{Width: 4000, Height: 3000}},
	})
	small := agent.StoredMessageTokens(store.Message{
		Role:        store.RoleUser,
		Content:     "看图",
		Attachments: []media.Attachment{{Width: 200, Height: 200}},
	})
	if big <= small {
		t.Fatalf("大图应比小图贵：%d vs %d", big, small)
	}
}

// 工具产出的图在 Anthropic 侧必须落在**同一个 user 回合**里，且 tool_result 排在图片之前。
// 这条一旦破了，表现是 API 直接 400（tool_result 必须在内容块最前），而不是悄悄少一张图。
func TestAnthropicMergesToolResultAndToolImageIntoOneTurn(t *testing.T) {
	st := media.NewAttachmentStore(t.TempDir())
	att, err := st.Save("s1", "shot.png", makeTestPNG(t, 64, 64), media.AttachmentSourceTool)
	if err != nil {
		t.Fatalf("存图失败: %v", err)
	}
	history := []store.Message{
		{ID: "m1", Role: store.RoleAssistant, ToolCalls: []store.ToolCall{{ID: "c1", Name: tool.ToolReadImage, Status: "success"}}},
		{ID: "m2", Role: store.RoleTool, Content: "已读取图片", ToolCallID: "c1", Attachments: []media.Attachment{*att}},
		{ID: "m3", Role: store.RoleUser, Content: "接下来怎么办"},
	}
	neutral := buildLLMMessages(history, "SYS", imageLoaderFor(st))
	// 中立形状：tool 文本 + 一条承载图片的 user 消息（OpenAI 的 tool 消息装不下图片）
	if len(neutral) != 5 {
		t.Fatalf("中立序列应有 system + assistant + tool + 图片 user + 用户提问，实际 %d 条", len(neutral))
	}
	if neutral[3].Role != store.RoleUser || len(neutral[3].Images) != 1 {
		t.Fatalf("第 4 条应是承载图片的 user 消息，实际 %+v", neutral[3])
	}
	if !strings.Contains(neutral[3].Content, tool.ToolReadImage) {
		t.Errorf("图片前的说明应点名是哪个工具给的图，实际 %q", neutral[3].Content)
	}

	out := llm.ToAnthropicRequest(&llm.LLMReq{Model: "m", Messages: neutral})
	if len(out.Messages) != 2 {
		t.Fatalf("Anthropic 侧应合并成 assistant + user 两条，实际 %d 条", len(out.Messages))
	}
	user := out.Messages[1]
	if user.Role != store.RoleUser {
		t.Fatalf("第 2 条应是 user，实际 %q", user.Role)
	}
	blocks, ok := user.Content.([]llm.AnthropicContentBlock)
	if !ok {
		t.Fatalf("content 应为块数组，实际 %T", user.Content)
	}
	if blocks[0].Type != "tool_result" {
		t.Fatalf("tool_result 必须排在内容块最前，实际第 0 块是 %q", blocks[0].Type)
	}
	hasImage := false
	for _, b := range blocks {
		if b.Type == "image" {
			hasImage = true
		}
	}
	if !hasImage {
		t.Fatalf("同一个 user 回合里应带上工具产出的图片，实际 %+v", blocks)
	}
}

// 图读不出来时必须留下痕迹：静默跳过会让模型对着一张不存在的图瞎答。
func TestBuildLLMMessagesDegradesWhenImageMissing(t *testing.T) {
	st := media.NewAttachmentStore(t.TempDir())
	history := []store.Message{{
		ID: "m1", Role: store.RoleUser, Content: "看这张图",
		Attachments: []media.Attachment{{ID: "dead", Kind: media.AttachmentKindImage, Name: "gone.png", Path: "s1/gone.png"}},
	}}

	msgs := buildLLMMessages(history, "SYS", imageLoaderFor(st))
	if len(msgs[1].Images) != 0 {
		t.Fatal("读不到的图不该被当成图片送出去")
	}
	if !strings.Contains(msgs[1].Content, "未能载入") {
		t.Fatalf("应把失败写进消息文本，实际 %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "看这张图") {
		t.Error("原始正文不能被附注覆盖掉")
	}

	// 装配缺失（loader 为 nil）同样要显式说明，而不是静默无声
	nilLoader := buildLLMMessages(history, "SYS", nil)
	if !strings.Contains(nilLoader[1].Content, "未装配附件存储") {
		t.Fatalf("没有附件存储时应明确说明，实际 %q", nilLoader[1].Content)
	}
}

// ===== 5. read_image 工具 =====

func TestReadImageToolProducesAttachment(t *testing.T) {
	dir := t.TempDir()
	raw := makeTestPNG(t, 3200, 2400) // 故意超尺寸，验证走的是规整后的那份
	imgPath := filepath.Join(dir, "screenshot.png")
	if err := os.WriteFile(imgPath, raw, 0o644); err != nil {
		t.Fatalf("写测试图片失败: %v", err)
	}

	st := media.NewAttachmentStore(t.TempDir())
	tm, _ := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir:  dir,
		SessionID:   "s1",
		Enforcer:    tool.AllowAllEnforcer{},
		Attachments: st,
	})

	out, err := view.ExecuteTool(tool.ToolReadImage, map[string]interface{}{"path": "screenshot.png"})
	if err != nil {
		t.Fatalf("read_image 失败: %v", err)
	}
	if !strings.Contains(out, "screenshot.png") || !strings.Contains(out, "已等比缩小") {
		t.Fatalf("结果文本应说明读了哪张图以及做过缩放，实际: %s", out)
	}

	produced := view.TakeImages()
	if len(produced) != 1 {
		t.Fatalf("应产出 1 张图，实际 %d", len(produced))
	}
	att := produced[0]
	if att.Source != media.AttachmentSourceTool {
		t.Errorf("来源应记为 tool，实际 %q", att.Source)
	}
	if media.MaxEdgeOf(att.Width, att.Height) != media.ImageModelMaxEdge {
		t.Errorf("存下来的应是规整后的尺寸，实际 %d×%d", att.Width, att.Height)
	}
	// 取走之后不能再取出第二次（"执行一次取一次"是归因准确的前提）
	if again := view.TakeImages(); len(again) != 0 {
		t.Errorf("take 之后应为空，实际 %d", len(again))
	}
	// 落盘的那份能读回来（发请求时走的就是 st.Load）
	if _, err := st.Load(att); err != nil {
		t.Fatalf("产出的附件应可读回: %v", err)
	}
}

func TestReadImageToolNotRegisteredWithoutStore(t *testing.T) {
	dir := t.TempDir()
	tm, _ := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: dir,
		SessionID:  "s1",
		Enforcer:   tool.AllowAllEnforcer{},
		// 刻意不给 Attachments：没有地方存图，注册出来只会每次调用都失败
	})
	for _, d := range view.GetToolsForLLM() {
		if d.Function.Name == tool.ToolReadImage {
			t.Fatal("没有附件存储时 read_image 不应注册")
		}
	}
	if _, err := view.ExecuteTool(tool.ToolReadImage, map[string]interface{}{"path": "x.png"}); err == nil {
		t.Fatal("未注册的工具调用应报「未找到工具」")
	}
}

func TestReadImageToolRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("这不是图片"), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	st := media.NewAttachmentStore(t.TempDir())
	tm, _ := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir:  dir,
		SessionID:   "s1",
		Enforcer:    tool.AllowAllEnforcer{},
		Attachments: st,
	})
	if _, err := view.ExecuteTool(tool.ToolReadImage, map[string]interface{}{"path": "notes.txt"}); err == nil {
		t.Fatal("非图片文件应被拒绝（并提示改用 read_file）")
	}
	if len(view.TakeImages()) != 0 {
		t.Error("失败的调用不应产出图片")
	}
}
