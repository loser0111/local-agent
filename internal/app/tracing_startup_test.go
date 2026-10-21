package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"wails-tmp/internal/llm"
	"wails-tmp/internal/observability"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== 遥测绝不能影响启动与对话 =====
//
// 这是整个接入方案里最该被守住的一条：遥测是旁路，任何配置缺失、端点挂掉、
// 响应异常都不能让应用起不来、也不能让一轮对话变慢或失败。
// 三种状态逐个验，因为它们走的是三份不同的代码路径：
//
//	① 根本没配（tracing.json 不存在）→ tracer 为 nil，埋点全是判空
//	② 配了但残缺（enabled=true 缺密钥）→ New 返回 nil + 打一行警告
//	③ 配了但 Langfuse 没起（端口不可达）→ tracer 非 nil，但**发送在 goroutine 里**，
//	   对话不能被它拖住
//
// ③ 是唯一有真实风险的一条：如果哪天有人把 send 改成同步的，对话就会开始
// 卡在遥测上，而这种回归在没有这条测试时完全无声。

// TestTracingDisabledByDefaultStillWorks：没配置时一切照旧
func TestTracingDisabledByDefaultStillWorks(t *testing.T) {
	app, sess := newTracingTestApp(t, func(llmCalls int) string { return "完成" })

	// 模拟「从未配置过」：不设置 app.tracer
	if app.tracer != nil {
		t.Fatal("未配置时 tracer 应为 nil")
	}

	res := runToolLoop(app.newMainAgentRun(nil, sess, t.TempDir(), "hi", nil, mustModel(t, app), false, false))
	if res == nil || res.Error != "" {
		t.Fatalf("未配置遥测时对话应正常完成，实际 %+v", res)
	}
}

// TestInvalidTracingConfigDoesNotBlock：配了但残缺，只降级、不报错
func TestInvalidTracingConfigDoesNotBlock(t *testing.T) {
	// enabled=true 但缺 endpoint / 密钥
	tr := observability.New(observability.Config{Enabled: true})
	if tr != nil {
		t.Fatal("残缺配置应返回 nil tracer（New 里已打警告）")
	}

	app, sess := newTracingTestApp(t, func(llmCalls int) string { return "完成" })
	app.tracer = tr // nil
	res := runToolLoop(app.newMainAgentRun(nil, sess, t.TempDir(), "hi", nil, mustModel(t, app), false, false))
	if res == nil || res.Error != "" {
		t.Fatalf("配置残缺时对话应正常完成，实际 %+v", res)
	}
}

// TestUnreachableLangfuseDoesNotSlowChat：Langfuse 没起时，对话不能被拖住
func TestUnreachableLangfuseDoesNotSlowChat(t *testing.T) {
	// 指向一个必然连不上的地址（端口 1 上没有监听）
	tr := observability.New(observability.Config{
		Enabled:   true,
		Endpoint:  "http://127.0.0.1:1/api/public/otel/v1/traces",
		PublicKey: "pk-lf-x",
		SecretKey: "sk-lf-y",
		TimeoutMS: observability.DefaultTimeoutMS,
	})
	if tr == nil {
		t.Fatal("合法配置应构造出 tracer（能否连通是运行时的事）")
	}

	app, sess := newTracingTestApp(t, func(llmCalls int) string { return "完成" })
	app.tracer = tr

	start := time.Now()
	res := runToolLoop(app.newMainAgentRun(nil, sess, t.TempDir(), "hi", nil, mustModel(t, app), false, false))
	elapsed := time.Since(start)

	if res == nil || res.Error != "" {
		t.Fatalf("Langfuse 不可达时对话仍应正常完成，实际 %+v", res)
	}
	// 发送是异步的：一轮对话不该等遥测。给足余量（本机 mock 模型 + 一次工具循环
	// 正常在几百毫秒内），只要没接近 HTTP 超时值就说明没有同步等待。
	if elapsed > 2*time.Second {
		t.Errorf("Langfuse 不可达时对话被拖慢了：耗时 %v（遥测不应阻塞对话）", elapsed)
	}
}

// TestTracingStartupStates：三种配置状态下 App 初始化都不应 panic
func TestTracingStartupStates(t *testing.T) {
	cases := []struct {
		name    string
		file    string // 写入 tracing.json 的内容，空串表示不写文件
		wantNil bool
	}{
		{"文件不存在", "", true},
		{"文件损坏", "{not json", true},
		{"关闭", `{"enabled":false}`, true},
		{"开启但缺密钥", `{"enabled":true,"endpoint":"http://x/v1/traces"}`, true},
		{"正常开启", `{"enabled":true,"endpoint":"http://127.0.0.1:1/v1/traces","publicKey":"a","secretKey":"b"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 这里只验配置解析这一层：App.setup 里那几行的等价逻辑
			st := observability.NewStore(traceCfgPath(t, c.file))
			cfg := st.Get()
			tr := observability.New(cfg)
			if (tr == nil) != c.wantNil {
				t.Errorf("tracer nil = %v，期望 %v（cfg=%+v）", tr == nil, c.wantNil, cfg)
			}
		})
	}
}

// ===== 辅助 =====

func traceCfgPath(t *testing.T, content string) string {
	t.Helper()
	p := t.TempDir() + "/tracing.json"
	if content != "" {
		writeFile(t, p, content)
	}
	return p
}

// newTracingTestApp 造一个能跑完一轮对话的 App。
// reply 按调用序次返回模型回复，便于构造「一次工具调用 + 一次收尾」。
func newTracingTestApp(t *testing.T, reply func(llmCalls int) string) (*App, *store.Session) {
	t.Helper()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls++
		content := reply(calls)
		_ = json.NewEncoder(w).Encode(llm.LLMResp{
			Choices: []llm.LLMChoice{{
				FinishReason: llm.FinishReasonStop,
				Message:      llm.LLMMessage{Role: store.RoleAssistant, Content: content},
			}},
			Usage: llm.LLMUsage{PromptTokens: 10, CompletionTokens: 5},
		})
	}))
	t.Cleanup(srv.Close)

	app, sess := newPlanTestApp(t, srv.URL)
	app.fileChanges = tool.NewFileChangeLog(50)
	model, err := app.modelStore.GetModelForCall("mock")
	if err != nil {
		t.Fatal(err)
	}
	_ = model
	return app, sess
}

func mustModel(t *testing.T, app *App) *store.Model {
	t.Helper()
	m, err := app.modelStore.GetModelForCall("mock")
	if err != nil {
		t.Fatal(err)
	}
	return &m
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
