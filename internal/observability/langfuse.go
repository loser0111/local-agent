package observability

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ===== 极简 OTLP/HTTP(JSON) 导出器 =====
//
// 为什么自己写而不引 go.opentelemetry.io/otel：本项目直接用 net/http 把 OTLP/JSON
// 打到 Langfuse 的 /api/public/otel/v1/traces 即可，约 400 行标准库代码；而官方 SDK
// 会带来 5 个新模块 + 一个进程级全局 TracerProvider，与 go.mod 只列 5 个直接依赖的
// 克制风格不符。
//
// 三条协议硬约束（照 OTLP 规范原文核实，写错是**静默**失败，不会有报错）：
//  1. traceId / spanId 是**十六进制字符串**（大小写不敏感），不是 Protobuf 标准
//     JSON 映射里的 base64。这是规范对标准映射的刻意偏离。
//  2. **枚举字段必须是整数值，枚举名字符串 MUST NOT 使用**。所以 span.kind 写 1
//     而不是 "SPAN_KIND_INTERNAL"，status.code 写 2 而不是 "STATUS_CODE_ERROR"。
//  3. 对象键名是 lowerCamelCase（startTimeUnixNano），snake_case 非法。
//     64 位整数按规范编码为十进制字符串——这里一律用字符串，规避各实现差异。
//
// 另一条与本项目强相关的取舍：**发送在独立 goroutine 里做，失败只打日志**。
// 与 agentrun.go 的 noteUsage 同一条原则——回复已经拿到了，为遥测去报错是本末倒置。
// 因此导出这侧不做重试（OTLP 规范定义了可重试状态码，但遥测丢了就丢了，
// 不值得为它拖住对话线程）。

// Langfuse observation 类型。合法值是固定的十种，写了非法值不会被拒，
// 而是回落到「按 gen_ai 属性推断 → 再回落到 span」，表现为「类型不对但没报错」。
const (
	TypeSpan       = "span"
	TypeGeneration = "generation"
	TypeAgent      = "agent"
	TypeTool       = "tool"
)

// OTLP 的 span.kind（枚举，必须是整数）
const spanKindInternal = 1

// OTLP 的 status.code（枚举，必须是整数）
const (
	statusCodeUnset = 0
	statusCodeError = 2
)

// scopeName 在 Langfuse 里表现为 instrumentation scope，便于区分数据来源。
const scopeName = "local-agent"

// Tracer 导出器。nil 值安全：所有方法在 nil 接收者上都是空操作，
// 于是调用点可以无条件写成 ar.Trace.XXX(...)——与 ReqLog / UsageLog 同一个模式。
type Tracer struct {
	cfg       Config
	client    *http.Client
	endpoint  string
	authHdr   string
	maxText   int
	mu        sync.Mutex
	failCount int
}

// New 按配置构造导出器。关闭或配置不可用时返回 nil（调用点因此天然走空操作）。
//
// 配置错误在这里**不能静默**：enabled=true 却缺 endpooint/密钥时必须打日志，
// 否则表现就是「打开了开关但 Langfuse 里什么都没有」，而用户没有任何线索。
func New(cfg Config) *Tracer {
	if !cfg.Enabled {
		return nil
	}
	if err := cfg.Validate(); err != nil {
		fmt.Printf("[tracing] 配置无效，遥测不会启用: %v\n", err)
		return nil
	}
	// 归一化必须在构造处做，不能只依赖 Store.Get()：
	// 零值 TimeoutMS 会让 context.WithTimeout(ctx, 0) 立刻过期，
	// 表现为「每次导出都超时」——而这条路径只有直接调 New 的调用方踩得到。
	cfg = normalize(cfg)
	return &Tracer{
		cfg:      cfg,
		endpoint: cfg.Endpoint,
		// Langfuse 用 Basic Auth 校验项目密钥：base64("<publicKey>:<secretKey>")
		authHdr: "Basic " + base64.StdEncoding.EncodeToString(
			[]byte(cfg.PublicKey+":"+cfg.SecretKey)),
		maxText: cfg.MaxTextBytes,
		client:  &http.Client{Timeout: time.Duration(cfg.TimeoutMS) * time.Millisecond},
	}
}

// Enabled 是否真的会导出
func (t *Tracer) Enabled() bool { return t != nil }

// CaptureContent 是否导出对话正文。
//
// 埋在 Tracer 上而不是让调用方去读配置：调用方拿到的只是一个 *Tracer，
// 不该为了一个布尔值再去持有一份 Config（两份状态必然漂移）。
func (t *Tracer) CaptureContent() bool {
	return t != nil && t.cfg.CaptureContent
}

// Describe 返回一行人类可读的启用说明，供启动日志使用。
//
// 这一行是有意为之：当前阶段不做设置界面，配置错了用户唯一的线索就是日志。
// 把「打到哪个端点」和「是否含正文」显式写出来，配置错误的发现时间能从半天降到 30 秒。
func (t *Tracer) Describe() string {
	if t == nil {
		return "未启用"
	}
	content := "仅元数据"
	if t.cfg.CaptureContent {
		content = "含对话正文"
	}
	return fmt.Sprintf("%s（%s）", t.endpoint, content)
}

// Run 一次运行的 trace 句柄。nil 值安全（Tracer 为 nil 时 StartRun 返回 nil）。
type Run struct {
	t         *Tracer
	traceID   string
	rootID    string
	sessionID string
	name      string
	start     time.Time

	mu sync.Mutex
	// pending 尚未发出的 span。按轮次 flush，从而在长任务里也能尽早看到数据。
	pending []spanData
	// lastGenID 最近一次 generation 的 spanId。工具的父级取它，
	// 于是 trace 呈「generation → 它要求的那批工具」的嵌套，Langfuse 的
	// Agent Graph 才画得对（工具挂在根 span 下会变成一堆扁平子节点）。
	lastGenID string
}

// StartRun 开始一次 trace。name 会显示为 trace 名。
func (t *Tracer) StartRun(sessionID, name string) *Run {
	if t == nil {
		return nil
	}
	return &Run{
		t:         t,
		traceID:   newTraceID(),
		rootID:    newSpanID(),
		sessionID: sessionID,
		name:      name,
		start:     time.Now(),
	}
}

// TraceID 供上层记录/排障（例如把它放进日志，便于与 Langfuse UI 对照）
func (r *Run) TraceID() string {
	if r == nil {
		return ""
	}
	return r.traceID
}

// Generation 记录一次模型调用。返回它自己的 spanId，
// 但调用点通常不用它——工具 span 会自动认最近一次 generation 为父级。
//
// usage 的键**必须与 Langfuse 模型定义里的价格键逐字一致**才会被用于成本计算；
// 本项目用内网网关 + 自定义模型名，Langfuse 内置价格表里没有，cost 会是空——
// 这是预期行为，token 量本身已经足够用（见 docs/langfuse-integration-survey.md §2.4）。
func (r *Run) Generation(g GenerationData) {
	if r == nil {
		return
	}
	attrs := []attribute{
		strAttr("langfuse.observation.type", TypeGeneration),
	}
	if g.Model != "" {
		attrs = append(attrs, strAttr("langfuse.observation.model.name", g.Model))
	}
	if len(g.Usage) > 0 {
		attrs = append(attrs, strAttr("langfuse.observation.usage_details", jsonText(g.Usage)))
	}
	if len(g.Params) > 0 {
		attrs = append(attrs, strAttr("langfuse.observation.model_parameters", jsonText(g.Params)))
	}
	attrs = append(attrs, r.commonAttrs(g.Metadata)...)
	attrs = append(attrs, observationIO(r.t.maxText, g.Input, g.Output)...)

	sp := r.makeSpan(g.Name, TypeGeneration, r.rootID, g.Start, g.End, g.ErrMsg, attrs)

	r.mu.Lock()
	r.pending = append(r.pending, sp)
	r.lastGenID = sp.SpanID
	r.mu.Unlock()
}

// Tool 记录一次工具调用。父级自动取最近一次 generation（没有则挂根 span）。
//
// 工具是本项目里**最容易把数据量搞爆**的地方：终端输出、整份文件内容、
// web_search 的整页正文都可能几十 KB 起步。截断在 observationIO 里统一做。
func (r *Run) Tool(t ToolData) {
	if r == nil {
		return
	}
	attrs := []attribute{
		strAttr("langfuse.observation.type", TypeTool),
	}
	attrs = append(attrs, r.commonAttrs(t.Metadata)...)
	attrs = append(attrs, observationIO(r.t.maxText, t.Input, t.Output)...)

	r.mu.Lock()
	parent := r.lastGenID
	if parent == "" {
		parent = r.rootID
	}
	r.mu.Unlock()

	sp := r.makeSpan(t.Name, TypeTool, parent, t.Start, t.End, t.ErrMsg, attrs)

	r.mu.Lock()
	r.pending = append(r.pending, sp)
	r.mu.Unlock()
}

// Finish 收尾：写入根 observation 并按结果 flush。
//
// 根 observation 的 type 用 agent 而不是 span：tool 的存在已经足以让 Langfuse
// 判定为 agentic 并画出 Graph，根显式标 agent 能让这个循环呈现为
// 「agent 节点 + 其下的工具节点」，而不是一个匿名的通用 span。
func (r *Run) Finish(output, errMsg string) {
	if r == nil {
		return
	}
	attrs := []attribute{
		strAttr("langfuse.observation.type", TypeAgent),
	}
	attrs = append(attrs, r.commonAttrs(nil)...)
	attrs = append(attrs, observationIO(r.t.maxText, "", output)...)

	root := r.makeSpan(r.name, TypeAgent, "", r.start, time.Now(), errMsg, attrs)
	r.mu.Lock()
	r.pending = append(r.pending, root)
	r.mu.Unlock()

	r.Flush()
}

// Flush 把已积累的 span 异步发出去。
// 无论成败都会清空缓冲：失败重试会把「本轮」的数据带到下一轮，产生重复与错位。
func (r *Run) Flush() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if len(r.pending) == 0 {
		r.mu.Unlock()
		return
	}
	batch := r.pending
	r.pending = nil
	r.mu.Unlock()

	go r.t.send(r.traceID, batch)
}

// commonAttrs 每条 span 都要带的 trace 级属性。
//
// session.id **必须挂在每条 span 上**：v4 的筛选与聚合主要作用于单条 observation，
// 只挂根 span 不会让会话聚合生效（这也是把它放进 commonAttrs 而不是根 span 的原因）。
func (r *Run) commonAttrs(meta map[string]any) []attribute {
	attrs := []attribute{
		strAttr("langfuse.session.id", r.sessionID),
	}
	for k, v := range meta {
		// 只有带 langfuse.observation.metadata. 前缀的键在 UI 里可筛选；
		// 直接塞普通 key 会掉进 metadata.attributes 兜底桶，筛不出来。
		attrs = append(attrs, valueAttr("langfuse.observation.metadata."+k, v))
	}
	return attrs
}

func (r *Run) makeSpan(name, typ, parentID string, start, end time.Time, errMsg string, attrs []attribute) spanData {
	_ = typ // 类型已作为属性写入，这里保留参数是为了调用点的可读性
	if name == "" {
		name = "unnamed"
	}
	status := spanStatus{Code: statusCodeUnset}
	if errMsg != "" {
		status = spanStatus{Code: statusCodeError, Message: truncate(errMsg, r.t.maxText)}
	}
	return spanData{
		TraceID:           r.traceID,
		SpanID:            newSpanID(),
		ParentSpanID:      parentID,
		Name:              name,
		Kind:              spanKindInternal,
		StartTimeUnixNano: strconv.FormatInt(start.UnixNano(), 10),
		EndTimeUnixNano:   strconv.FormatInt(end.UnixNano(), 10),
		Attributes:        attrs,
		Status:            status,
	}
}

// send 真正发出一个 OTLP/JSON 请求。
//
// 失败只打日志，且做节流：连续失败时前 3 次全打，之后每 20 次打一次。
// 不加节流的话，一个配错的端点会在每次对话里刷屏，反而把真正有用的日志淹掉。
func (t *Tracer) send(traceID string, spans []spanData) {
	// 防御：即便调用方把 t 判空写漏了，也不要 panic
	if t == nil || len(spans) == 0 {
		return
	}
	body, err := json.Marshal(otlpPayload(spans))
	if err != nil {
		fmt.Printf("[tracing] 序列化失败（本次不上报）: %v\n", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(t.cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		t.noteFailure("构造请求失败: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", t.authHdr)
	// 少了这个头，直连 OTLP 的数据在 Langfuse 里**最多延迟 10 分钟**才可见。
	// 这是「响应 200、UI 却空着」的头号原因，务必保留。
	req.Header.Set("x-langfuse-ingestion-version", "4")

	resp, err := t.client.Do(req)
	if err != nil {
		t.noteFailure("请求失败: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return
	}
	// 读一小段响应体：Langfuse 的错误说明通常就前一两百字节，这是排障的关键信息。
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	t.noteFailure("HTTP %d: %s", resp.StatusCode, string(snippet))
}

func (t *Tracer) noteFailure(format string, args ...any) {
	t.mu.Lock()
	t.failCount++
	n := t.failCount
	t.mu.Unlock()

	if n <= 3 || n%20 == 0 {
		fmt.Printf("[tracing] 导出失败（第 %d 次，不影响对话）: %s\n", n, fmt.Sprintf(format, args...))
	}
}

// ===== 数据形状 =====

// GenerationData 一次模型调用的描述
type GenerationData struct {
	Name     string
	Model    string
	Input    string
	Output   string
	Usage    map[string]int
	Params   map[string]any
	Metadata map[string]any
	Start    time.Time
	End      time.Time
	ErrMsg   string
}

// ToolData 一次工具调用的描述
type ToolData struct {
	Name     string
	Input    string
	Output   string
	Metadata map[string]any
	Start    time.Time
	End      time.Time
	ErrMsg   string
}

// ===== OTLP/JSON 结构（字段名必须是 lowerCamelCase）=====

type otlpPayloadRoot struct {
	ResourceSpans []resourceSpans `json:"resourceSpans"`
}

type resourceSpans struct {
	Resource   resource     `json:"resource"`
	ScopeSpans []scopeSpans `json:"scopeSpans"`
}

type resource struct {
	Attributes []attribute `json:"attributes,omitempty"`
}

type scopeSpans struct {
	Scope scope      `json:"scope"`
	Spans []spanData `json:"spans"`
}

type scope struct {
	Name string `json:"name"`
}

type spanData struct {
	TraceID           string      `json:"traceId"`
	SpanID            string      `json:"spanId"`
	ParentSpanID      string      `json:"parentSpanId,omitempty"`
	Name              string      `json:"name"`
	Kind              int         `json:"kind"`
	StartTimeUnixNano string      `json:"startTimeUnixNano"`
	EndTimeUnixNano   string      `json:"endTimeUnixNano"`
	Attributes        []attribute `json:"attributes,omitempty"`
	Status            spanStatus  `json:"status"`
}

type spanStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type attribute struct {
	Key   string    `json:"key"`
	Value attrValue `json:"value"`
}

// attrValue 用指针区分「未设置」与「零值」——stringValue 为空串是合法的，
// 不能用 omitempty 表达（否则空字符串会被整个丢掉）。
type attrValue struct {
	StringValue *string   `json:"stringValue,omitempty"`
	IntValue    *string   `json:"intValue,omitempty"`
	BoolValue   *bool     `json:"boolValue,omitempty"`
	DoubleValue *float64  `json:"doubleValue,omitempty"`
	ArrayValue  *[]string `json:"-"`
}

func otlpPayload(spans []spanData) otlpPayloadRoot {
	return otlpPayloadRoot{
		ResourceSpans: []resourceSpans{{
			Resource: resource{Attributes: []attribute{
				strAttr("service.name", "local-agent"),
			}},
			ScopeSpans: []scopeSpans{{
				Scope: scope{Name: scopeName},
				Spans: spans,
			}},
		}},
	}
}

func strAttr(key, val string) attribute {
	v := val
	return attribute{Key: key, Value: attrValue{StringValue: &v}}
}

// valueAttr 把任意 Go 值映射成 OTLP 标量属性。
// metadata 里放的都是标量（状态、文件数、时长），因此不实现嵌套结构——
// 真有复杂结构就用 strAttr + jsonText 自己序列化，比在这里猜形状可控。
func valueAttr(key string, val any) attribute {
	switch v := val.(type) {
	case nil:
		return strAttr(key, "")
	case string:
		return strAttr(key, v)
	case bool:
		b := v
		return attribute{Key: key, Value: attrValue{BoolValue: &b}}
	case int:
		s := strconv.Itoa(v)
		return attribute{Key: key, Value: attrValue{IntValue: &s}}
	case int64:
		s := strconv.FormatInt(v, 10)
		return attribute{Key: key, Value: attrValue{IntValue: &s}}
	case float64:
		return attribute{Key: key, Value: attrValue{DoubleValue: &v}}
	case float32:
		f := float64(v)
		return attribute{Key: key, Value: attrValue{DoubleValue: &f}}
	default:
		return strAttr(key, fmt.Sprint(v))
	}
}

// observationIO 拼出 input/output 两个属性（按配置截断）。
//
// 关掉 CaptureContent 时这两项**整体不出现**，而不是发空串：Langfuse UI 对
// 「没有 input」和「input 是空字符串」的呈现不同，后者会让人以为抓到了空内容。
func observationIO(maxBytes int, input, output string) []attribute {
	if input == "" && output == "" {
		return nil
	}
	attrs := make([]attribute, 0, 2)
	if input != "" {
		attrs = append(attrs, strAttr("langfuse.observation.input", truncate(input, maxBytes)))
	}
	if output != "" {
		attrs = append(attrs, strAttr("langfuse.observation.output", truncate(output, maxBytes)))
	}
	return attrs
}

// truncate 按**字节**截断并标注原长。
//
// 标注原长不是装饰：排障时「这里本来有 120 KB」与「这里就是 4 KB」是完全不同的信息，
// 少了它会把「工具只返回了一点点」和「返回了很多但被截了」混为一谈。
// 截断点回退到一个 UTF-8 边界，避免切出半个汉字导致 JSON 里出现替换字符。
func truncate(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + fmt.Sprintf("…（已截断，原长 %d 字节）", len(s))
}

// jsonText 序列化成 JSON 字符串（失败时退化为 Go 的默认格式，绝不返回错误——
// 遥测这侧没有「失败」这个档位，只有「少一点细节」）
func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// newTraceID 32 位十六进制（16 字节）
func newTraceID() string { return randHex(16) }

// newSpanID 16 位十六进制（8 字节）
func newSpanID() string { return randHex(8) }

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败在实践中意味着系统熵源不可用，此时用一个时间派生值兜底，
		// 保证 ID 非空（空 traceId 会被 Langfuse 直接丢掉，比重复 ID 更糟）。
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(now >> (8 * (i % 8)))
		}
	}
	return hex.EncodeToString(b)
}
