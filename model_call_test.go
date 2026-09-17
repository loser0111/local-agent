package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// capturedHeader 并发安全地保存假网关收到的请求头（便于 go test -race）。
type capturedHeader struct {
	mu   sync.Mutex
	head http.Header
}

func (c *capturedHeader) store(h http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.head = h.Clone()
}

func (c *capturedHeader) get(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.head.Get(key)
}

// captureLLMServer 启动一个假的 LLM 网关，记录收到的鉴权头并回一个合法响应。
func captureLLMServer(t *testing.T, respBody string) (*httptest.Server, *capturedHeader) {
	t.Helper()
	got := &capturedHeader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.store(r.Header)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// TestCallModelForOpenAI_SendsPlaintextKey 回归测试：OpenAI 协议发出的 Bearer token
// 必须是明文 API Key。
//
// 背景：GetModel 会给 APIKey 脱敏（只留后 4 位）。executeChat / ChatPlan /
// ExecutePlan 曾误用 GetModel 取模型，把 "****abcd" 这种占位串当鉴权头发出去，
// 网关直接返回 401 {"error":{"message":"Invalid API key format"}}。
// 本测试在 HTTP 边界上钉住两条约定：发请求走 GetModelForCall（明文），
// 展示走 GetModel / GetModels（脱敏，且不得泄漏明文）。
func TestCallModelForOpenAI_SendsPlaintextKey(t *testing.T) {
	const secret = "sk-live-abcdefghijklmn"

	respBody := `{"id":"test","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"OK"}}]}`
	srv, gotHeader := captureLLMServer(t, respBody)

	store := NewModelStore(filepath.Join(t.TempDir(), "models.json"))
	if err := store.AddModel(Model{
		Name:     "m1",
		APIKey:   secret,
		URL:      srv.URL,
		Protocol: "openai",
	}); err != nil {
		t.Fatalf("添加模型失败: %v", err)
	}

	model, err := store.GetModelForCall("m1")
	if err != nil {
		t.Fatalf("GetModelForCall 失败: %v", err)
	}
	if model.APIKey != secret {
		t.Fatalf("GetModelForCall 应返回明文 Key，实际: %q", model.APIKey)
	}

	req := &LLMReq{
		Model:    "m1",
		Messages: []LLMMessage{{Role: RoleUser, Content: "ping"}},
	}
	if _, err := callLLMForModel(&model, req); err != nil {
		t.Fatalf("callLLMForModel 失败: %v", err)
	}

	if got, want := gotHeader.get("Authorization"), "Bearer "+secret; got != want {
		t.Errorf("Authorization 头应为 %q，实际 %q", want, got)
	}
	if got := gotHeader.get("X-Api-Key"); got != "" {
		t.Errorf("OpenAI 协议不应发送 x-api-key，实际 %q", got)
	}

	// 反例留证：展示用的 GetModel 返回脱敏串，绝不能用于发请求。
	masked, err := store.GetModel("m1")
	if err != nil {
		t.Fatalf("GetModel 失败: %v", err)
	}
	if masked.APIKey == secret {
		t.Fatal("GetModel 应返回脱敏 Key；若已改为不脱敏，请同步修订调用点约定与本测试")
	}
	if !strings.HasPrefix(masked.APIKey, "****") || !strings.HasSuffix(masked.APIKey, "klmn") {
		t.Errorf("GetModel 脱敏结果异常: %q", masked.APIKey)
	}

	// 展示链路不得把明文 Key 序列化给前端。
	displayJSON, err := json.Marshal(store.GetModels())
	if err != nil {
		t.Fatalf("序列化模型列表失败: %v", err)
	}
	if bytes.Contains(displayJSON, []byte(secret)) {
		t.Errorf("GetModels 序列化结果泄漏了明文 Key: %s", displayJSON)
	}
}

// TestCallModelForAnthropic_SendsPlaintextKey Anthropic 协议走 x-api-key 头，
// 同样必须是明文；这也是 ada-cli / ccdesktop 一类网关报 401 的路径。
func TestCallModelForAnthropic_SendsPlaintextKey(t *testing.T) {
	const secret = "sk-ant-0123456789xyz"

	respBody := `{"id":"msg_1","model":"m1","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`
	srv, gotHeader := captureLLMServer(t, respBody)

	store := NewModelStore(filepath.Join(t.TempDir(), "models.json"))
	if err := store.AddModel(Model{
		Name:     "m1",
		APIKey:   secret,
		URL:      srv.URL,
		Protocol: "anthropic",
	}); err != nil {
		t.Fatalf("添加模型失败: %v", err)
	}

	model, err := store.GetModelForCall("m1")
	if err != nil {
		t.Fatalf("GetModelForCall 失败: %v", err)
	}

	req := &LLMReq{
		Model:    "m1",
		Messages: []LLMMessage{{Role: RoleUser, Content: "ping"}},
	}
	if _, err := callLLMForModel(&model, req); err != nil {
		t.Fatalf("callLLMForModel 失败: %v", err)
	}

	if got := gotHeader.get("X-Api-Key"); got != secret {
		t.Errorf("x-api-key 头应为明文 %q，实际 %q", secret, got)
	}
	if got := gotHeader.get("Authorization"); got != "" {
		t.Errorf("Anthropic 协议不应发送 Authorization 头，实际 %q", got)
	}
	if got := gotHeader.get("Anthropic-Version"); got == "" {
		t.Error("缺少 anthropic-version 头")
	}
}
