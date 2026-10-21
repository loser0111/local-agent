package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"wails-tmp/internal/llm"
	"wails-tmp/internal/observability"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 端到端：跑一次真实工具循环，验证实际发出的 trace 结构 =====
//
// 与 internal/observability 里的 exporter 单测互补：那边验「OTLP 编码对不对」，
// 这边验「接进 chat.go 之后真的会发出 generation 与 tool，且父子关系正确」。
// 中间任何一环漏接（忘了传 Tracer、忘了调 traceTool、父级挂错、每轮忘了 flush）
// 都只有这里能发现——而这些都是**静默**失效：不报错，UI 里少一类数据而已。

// traceSpan 从导出的 payload 里解出来的最小形状
type traceSpan struct {
	SpanID       string
	ParentSpanID string
	Type         string
	SessionID    string
	HasInput     bool
}

func TestTraceEndToEnd(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies [][]byte
	)

	// 假 OTLP 接收端 + 假模型网关：两者分开，因为要断言的是「我们发出去的东西」
	otlp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer otlp.Close()

	var llmCalls int
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		llmCalls++
		// 第 1 次返回一个工具调用，第 2 次返回纯文本收尾 —— 走完「模型→工具→模型」
		if llmCalls == 1 {
			_ = json.NewEncoder(w).Encode(llm.LLMResp{
				Choices: []llm.LLMChoice{{
					FinishReason: llm.FinishReasonToolCalls,
					Message: llm.LLMMessage{Role: store.RoleAssistant, ToolCalls: []llm.LLMToolCall{{
						ID:       "call_1",
						Type:     llm.ToolTypeFunction,
						Function: llm.LLMToolFunction{Name: tool.ToolReadFile, Arguments: `{"path":"a.txt"}`},
					}}},
				}},
				Usage: llm.LLMUsage{PromptTokens: 100, CompletionTokens: 20},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(llm.LLMResp{
			Choices: []llm.LLMChoice{{
				FinishReason: llm.FinishReasonStop,
				Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: "完成"},
			}},
			Usage: llm.LLMUsage{PromptTokens: 120, CompletionTokens: 8},
		})
	}))
	defer llmSrv.Close()

	app, sess := newPlanTestApp(t, llmSrv.URL)
	app.fileChanges = tool.NewFileChangeLog(50)

	tracer := observability.New(observability.Config{
		Enabled:        true,
		Endpoint:       otlp.URL + "/api/public/otel/v1/traces",
		PublicKey:      "pk-lf-test",
		SecretKey:      "sk-lf-test",
		CaptureContent: false, // 同时验证元数据模式下不发正文
	})
	if tracer == nil {
		t.Fatal("tracer 未构造成功")
	}
	app.tracer = tracer

	model, err := app.modelStore.GetModelForCall("mock")
	if err != nil {
		t.Fatal(err)
	}

	registry := map[string]tool.ToolInterface{
		tool.ToolReadFile: tool.NewGuardedTool(newEchoTool(tool.ToolReadFile), tool.AllowAllEnforcer{}),
	}
	view := tool.NewSessionView(registry, nil, []string{tool.ToolReadFile}, nil)

	ar := app.newMainAgentRun(nil, sess, t.TempDir(), "做个事", nil, &model, false, false)
	// newMainAgentRun 里 toolView 要求由调用方装配，这里覆盖成我们手工造的那个
	ar.ToolView = view
	ar.Control = app.runs.Begin(sess.ID, "")
	defer app.runs.End(ar.Control)

	res := runToolLoop(ar)
	if res == nil {
		t.Fatal("运行未返回结果")
	}

	// 导出是异步的：等到至少收到两批（轮次 flush + Finish 的根 span）
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(bodies)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	spans := parseSpans(t, bodies)
	mu.Unlock()

	if len(spans) == 0 {
		t.Fatal("没有收到任何 span")
	}

	// ① 类型齐全：generation 与 tool 都必须在
	types := map[string]int{}
	for _, sp := range spans {
		types[sp.Type]++
	}
	if types["generation"] < 2 {
		t.Errorf("应有 2 条 generation（工具前后各一次），实际 %d，类型分布 %v", types["generation"], types)
	}
	if types["tool"] < 1 {
		t.Errorf("应有至少 1 条 tool，类型分布 %v", types)
	}

	// ② 每条 span 都带 session.id（只挂根 span 不生效，会话聚合会失效）
	for _, sp := range spans {
		if sp.SessionID != sess.ID {
			t.Errorf("span 的 session.id = %q，期望 %q", sp.SessionID, sess.ID)
		}
	}

	// ③ 工具 span 的父级必须是某个 generation，而不是根、也不能为空
	byID := map[string]traceSpan{}
	for _, sp := range spans {
		byID[sp.SpanID] = sp
	}
	toolChecked := 0
	for _, sp := range spans {
		if sp.Type != "tool" {
			continue
		}
		toolChecked++
		if sp.ParentSpanID == "" {
			t.Error("工具 span 没有父级，会在 Langfuse 里变成游离的根节点")
			continue
		}
		if parent, ok := byID[sp.ParentSpanID]; ok && parent.Type != "generation" {
			t.Errorf("工具 span 的父级类型是 %q，期望 generation", parent.Type)
		}
	}
	if toolChecked == 0 {
		t.Error("没有校验到任何工具 span")
	}

	// ④ CaptureContent=false 时不能有 input 正文
	for _, sp := range spans {
		if sp.HasInput {
			t.Error("CaptureContent=false 却发出了 input 正文（应当整体不出现）")
		}
	}
}

// ===== 辅助 =====

// parseSpans 把我们自己发出的 OTLP/JSON 解回结构化形状。
// 刻意用匿名 struct 而不是复用 otlpPayloadRoot：这个测试要独立地校验线上格式，
// 复用内部类型会让「格式改坏了」和「测试跟着改」同时发生，测试就失去意义了。
func parseSpans(t *testing.T, bodies [][]byte) []traceSpan {
	t.Helper()
	var out []traceSpan
	for _, b := range bodies {
		var payload struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []struct {
						SpanID       string `json:"spanId"`
						ParentSpanID string `json:"parentSpanId"`
						Attributes   []struct {
							Key   string `json:"key"`
							Value struct {
								StringValue *string `json:"stringValue"`
							} `json:"value"`
						} `json:"attributes"`
					} `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal(b, &payload); err != nil {
			t.Fatalf("发出的 body 不是合法 JSON: %v\nbody=%s", err, b)
		}
		for _, rs := range payload.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				for _, sp := range ss.Spans {
					ts := traceSpan{SpanID: sp.SpanID, ParentSpanID: sp.ParentSpanID}
					for _, a := range sp.Attributes {
						v := ""
						if a.Value.StringValue != nil {
							v = *a.Value.StringValue
						}
						switch a.Key {
						case "langfuse.observation.type":
							ts.Type = v
						case "langfuse.session.id":
							ts.SessionID = v
						case "langfuse.observation.input":
							ts.HasInput = true
						}
					}
					out = append(out, ts)
				}
			}
		}
	}
	return out
}

// newEchoTool 一个立刻返回固定结果的只读假工具
func newEchoTool(name string) *echoTool {
	return &echoTool{BaseTool: &tool.BaseTool{
		Name:        name,
		Description: "test tool",
		Parameters:  map[string]*tool.ToolArgDef{},
	}}
}

type echoTool struct {
	*tool.BaseTool
}

func (e *echoTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	return "file content", nil
}
