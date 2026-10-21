package observability

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ===== 编码约束的回归测试 =====
//
// 这些测试守的是 OTLP 规范里那几条「写错不报错、只是静默不认」的约束。
// 没有它们的话，改动 otlpPayload 的形状时不会有任何东西阻止你把枚举写成字符串，
// 而症状只会在 Langfuse UI 里表现为「类型不对」或「整条 span 消失」。

func TestPayloadEnumAndKeyShape(t *testing.T) {
	tr := &Tracer{maxText: DefaultMaxTextBytes}
	run := tr.StartRun("sess-1", "chat")
	run.Generation(GenerationData{
		Name:  "turn-0",
		Model: "m",
		Usage: map[string]int{"input": 10, "output": 5},
		Start: time.Now(),
		End:   time.Now(),
	})

	b, err := json.Marshal(otlpPayload([]spanData{
		testSpan(run.traceID, "x"),
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)

	// kind 必须是整数，不能是 "SPAN_KIND_INTERNAL"
	if !strings.Contains(s, `"kind":1`) {
		t.Errorf("kind 必须是整数 1，实际 payload: %s", s)
	}
	// status.code 必须是整数 0（未设置），不能是 "STATUS_CODE_UNSET"
	if !strings.Contains(s, `"code":0`) {
		t.Errorf("status.code 必须是整数 0，实际 payload: %s", s)
	}
	// 时间戳必须是十进制字符串（64 位整数在 OTLP/JSON 里编码为字符串）
	if !strings.Contains(s, `"startTimeUnixNano":"`) {
		t.Errorf("startTimeUnixNano 必须是字符串，实际 payload: %s", s)
	}
	// 键名必须是 lowerCamelCase
	if strings.Contains(s, "start_time_unix_nano") || strings.Contains(s, "trace_id") {
		t.Errorf("出现了 snake_case 键名，实际 payload: %s", s)
	}
}

func TestTraceIDAndSpanIDAreHex(t *testing.T) {
	tid := newTraceID()
	sid := newSpanID()
	if len(tid) != 32 {
		t.Errorf("traceId 应为 32 位十六进制，实际 %q（%d 位）", tid, len(tid))
	}
	if len(sid) != 16 {
		t.Errorf("spanId 应为 16 位十六进制，实际 %q（%d 位）", sid, len(sid))
	}
	for _, c := range tid + sid {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("ID 含非十六进制字符: %q", tid+sid)
		}
	}
	// 唯一性：两次取值不应相同（crypto/rand 正常时）
	if newTraceID() == tid {
		t.Error("两次 traceId 相同，随机源可能失效")
	}
}

// 工具 span 的父级必须是**最近一次 generation**，不是根 span。
// 这决定 Langfuse 的 Agent Graph 画出来是「generation → 它要求的工具」还是
// 一堆全部挂在根下的扁平子节点。
func TestToolNestsUnderLatestGeneration(t *testing.T) {
	tr := &Tracer{maxText: DefaultMaxTextBytes}
	run := tr.StartRun("sess", "chat")

	run.Generation(GenerationData{Name: "turn-0", Start: time.Now(), End: time.Now()})
	genID := run.lastGenID
	if genID == "" {
		t.Fatal("generation 之后 lastGenID 不应为空")
	}
	run.Tool(ToolData{Name: "read_file", Start: time.Now(), End: time.Now()})

	run.mu.Lock()
	defer run.mu.Unlock()
	if len(run.pending) != 2 {
		t.Fatalf("应有 2 条 span，实际 %d", len(run.pending))
	}
	if run.pending[1].ParentSpanID != genID {
		t.Errorf("工具 span 的父级应是最近的 generation %q，实际 %q",
			genID, run.pending[1].ParentSpanID)
	}
}

// 没有 generation 时（只跑工具的场景）工具挂根 span，不能挂空父级
// ——空 parentSpanId 会让 Langfuse 把它当根节点，trace 里就多出一个游离的根。
func TestToolFallsBackToRootParent(t *testing.T) {
	tr := &Tracer{maxText: DefaultMaxTextBytes}
	run := tr.StartRun("sess", "chat")
	run.Tool(ToolData{Name: "exec_shell", Start: time.Now(), End: time.Now()})

	run.mu.Lock()
	defer run.mu.Unlock()
	if run.pending[0].ParentSpanID != run.rootID {
		t.Errorf("无 generation 时工具应挂根 span，实际父级 %q", run.pending[0].ParentSpanID)
	}
}

// ===== 内容截断 =====

func TestTruncateMarksOriginalLength(t *testing.T) {
	long := strings.Repeat("a", 5000)
	got := truncate(long, 100)

	if !strings.HasPrefix(got, strings.Repeat("a", 100)) {
		t.Error("截断后应保留前 100 字节")
	}
	// 标注原长不是装饰：少了它，「只返回了一点点」与「返回很多但被截了」无法区分
	if !strings.Contains(got, "原长 5000 字节") {
		t.Errorf("截断标注里应含原长，实际: %s", got[len(got)-40:])
	}
}

// 截断点必须回退到 UTF-8 边界，否则会切出半个汉字
func TestTruncateDoesNotSplitRune(t *testing.T) {
	// 每个汉字 3 字节，取 4 字节会把第二个字切一半
	got := truncate("中文测试", 4)
	for _, r := range got {
		if r == '\uFFFD' {
			t.Fatalf("截断产生了替换字符（切碎了 UTF-8）: %q", got)
		}
	}
	if strings.Contains(got, "\ufffd") {
		t.Fatalf("截断产生了替换字符: %q", got)
	}
}

func TestTruncateKeepsShortTextIntact(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Errorf("未超长的文本不应被改动，实际 %q", got)
	}
}

// ===== 配置 =====

func TestStoreFallsBackToDisabledOnBrokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tracing.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := NewStore(path).Get()
	// 失败方向必须是「关闭」：一个被手改坏的文件绝不该让系统开始外发数据
	if cfg.Enabled {
		t.Error("配置文件损坏时应保持关闭")
	}
	if cfg.MaxTextBytes != DefaultMaxTextBytes || cfg.TimeoutMS != DefaultTimeoutMS {
		t.Errorf("越界/缺失的字段应回落到默认值，实际 %+v", cfg)
	}
}

func TestStoreMissingFileIsDisabled(t *testing.T) {
	cfg := NewStore(filepath.Join(t.TempDir(), "nope.json")).Get()
	if cfg.Enabled {
		t.Error("文件不存在时应保持关闭")
	}
}

// 端点和密钥两端的空白必须被剔除：从文档复制粘贴常带尾随空格，
// 而它们会直接拼进 URL 与 Authorization 头，表现为 404 / 401 这两种最费劲的错误。
func TestStoreTrimsCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tracing.json")
	body := `{"enabled":true,"endpoint":" http://x/api/public/otel/v1/traces ","publicKey":" pk-lf-1 ","secretKey":" sk-lf-2 "}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := NewStore(path).Get()
	if cfg.Endpoint != "http://x/api/public/otel/v1/traces" {
		t.Errorf("endpoint 未 TrimSpace: %q", cfg.Endpoint)
	}
	if cfg.PublicKey != "pk-lf-1" || cfg.SecretKey != "sk-lf-2" {
		t.Errorf("密钥未 TrimSpace: %q / %q", cfg.PublicKey, cfg.SecretKey)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"关闭时永不报错", Config{Enabled: false}, false},
		{"缺端点", Config{Enabled: true, PublicKey: "a", SecretKey: "b"}, true},
		{"端点缺协议", Config{Enabled: true, Endpoint: "x/api/public/otel/v1/traces", PublicKey: "a", SecretKey: "b"}, true},
		{"缺密钥", Config{Enabled: true, Endpoint: "http://x/v1/traces", PublicKey: "a"}, true},
		{"完整", Config{Enabled: true, Endpoint: "http://x/api/public/otel/v1/traces", PublicKey: "a", SecretKey: "b"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if (err != nil) != c.wantErr {
				t.Errorf("Validate() 错误 = %v, 期望有错 = %v", err, c.wantErr)
			}
		})
	}
}

// enabled=true 但配置不可用时，New 必须返回 nil（而不是一个会静默失败的 Tracer）
func TestNewReturnsNilOnInvalidConfig(t *testing.T) {
	if tr := New(Config{Enabled: true}); tr != nil {
		t.Error("配置不可用时应返回 nil")
	}
	if tr := New(Config{Enabled: false, Endpoint: "http://x", PublicKey: "a", SecretKey: "b"}); tr != nil {
		t.Error("未启用时应返回 nil")
	}
}

// ===== nil 安全 =====
//
// 这三条守的是调用点的前提：agentRun.Trace 为 nil 时整条埋点链不能 panic。
// 测试里会直接构造零值 agentRun，那一路径必须安全。
func TestNilSafety(t *testing.T) {
	var tr *Tracer
	if tr.Enabled() || tr.CaptureContent() || tr.Describe() != "未启用" {
		t.Error("nil Tracer 的查询方法应安全")
	}
	var run *Run
	run.Generation(GenerationData{})
	run.Tool(ToolData{})
	run.Flush()
	run.Finish("", "")
	if run.TraceID() != "" {
		t.Error("nil Run 的 TraceID 应为空串")
	}
	if tr.StartRun("s", "n") != nil {
		t.Error("nil Tracer 的 StartRun 应返回 nil")
	}
}

// ===== 端到端：真的发一个 HTTP 请求 =====

func TestSendPostsValidPayloadWithHeaders(t *testing.T) {
	type captured struct {
		header http.Header
		body   []byte
	}
	var (
		mu   sync.Mutex
		got  captured
		done = make(chan struct{})
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = captured{header: r.Header.Clone(), body: body}
		mu.Unlock()
		close(done)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := New(Config{
		Enabled:   true,
		Endpoint:  srv.URL + "/api/public/otel/v1/traces",
		PublicKey: "pk-lf-1",
		SecretKey: "sk-lf-2",
	})
	if tr == nil {
		t.Fatal("配置合法时 New 不应返回 nil")
	}

	run := tr.StartRun("sess-42", "chat")
	run.Generation(GenerationData{
		Name:  "turn-0",
		Model: "glm-4",
		Usage: map[string]int{"input": 120, "output": 30},
		Start: time.Now(),
		End:   time.Now(),
	})
	run.Flush()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("5 秒内没有收到导出请求")
	}

	mu.Lock()
	defer mu.Unlock()

	// 头三项直接决定"能不能进 Langfuse"，缺一项就是静默失败
	if ct := got.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if auth := got.header.Get("Authorization"); !strings.HasPrefix(auth, "Basic ") {
		t.Errorf("Authorization 应为 Basic，实际 %q", auth)
	} else {
		decoded, err := decodeBasic(auth)
		if err != nil {
			t.Fatalf("解码 Basic 失败: %v", err)
		}
		if decoded != "pk-lf-1:sk-lf-2" {
			t.Errorf("Basic 凭据 = %q，期望 pk-lf-1:sk-lf-2", decoded)
		}
	}
	// 少了这个头，数据在 Langfuse 里最多延迟 10 分钟才可见
	if v := got.header.Get("x-langfuse-ingestion-version"); v != "4" {
		t.Errorf("x-langfuse-ingestion-version = %q，期望 4", v)
	}

	// body 必须是能解出来的合法 JSON，且带上 session.id
	var payload otlpPayloadRoot
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatalf("body 不是合法 JSON: %v\nbody=%s", err, got.body)
	}
	spans := payload.ResourceSpans[0].ScopeSpans[0].Spans
	if len(spans) == 0 {
		t.Fatal("payload 里没有 span")
	}
	found := false
	for _, a := range spans[0].Attributes {
		if a.Key == "langfuse.session.id" && a.Value.StringValue != nil && *a.Value.StringValue == "sess-42" {
			found = true
		}
	}
	if !found {
		t.Error("span 上缺 langfuse.session.id 或值不对（会话聚合会失效）")
	}
}

// 内容开关关着时，input/output 必须**整体不出现**，而不是发空串：
// UI 对「没有 input」与「input 是空字符串」呈现不同，后者会让人以为抓到了空内容。
func TestCaptureContentOffOmitsIO(t *testing.T) {
	tr := &Tracer{maxText: DefaultMaxTextBytes} // CaptureContent 默认 false

	// traceGeneration 里靠 CaptureContent() 决定要不要填 Input，这里直接验属性组装
	attrs := observationIO(tr.maxText, "", "")
	if len(attrs) != 0 {
		t.Errorf("input/output 都为空时不应产生属性，实际 %d 条", len(attrs))
	}
	if tr.CaptureContent() {
		t.Error("零值配置的 CaptureContent 应为 false")
	}
}

// ===== 辅助 =====

// decodeBasic 从 "Basic xxx" 里解出 "user:pass"
func decodeBasic(h string) (string, error) {
	const prefix = "Basic "
	if !strings.HasPrefix(h, prefix) {
		return "", os.ErrInvalid
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, prefix))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func testSpan(traceID, name string) spanData {
	return spanData{
		TraceID:           traceID,
		SpanID:            newSpanID(),
		Name:              name,
		Kind:              spanKindInternal,
		StartTimeUnixNano: "1",
		EndTimeUnixNano:   "2",
		Attributes:        []attribute{strAttr("langfuse.observation.type", TypeTool)},
		Status:            spanStatus{Code: statusCodeUnset},
	}
}
