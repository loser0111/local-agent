package observability

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
)

// ===== Traces 导出配置（~/.local-agent/tracing.json）=====
//
// 与本项目其他偏好文件（context.json / prompt-cache.json，见 store/contextprefs.go、
// store/usage.go）保持同一个失败方向：**文件不存在、解析失败、字段越界，一律回落到
// 「关闭」**。这里尤其重要——遥测是本项目里唯一会把对话内容发到网络上的功能，
// 一个被手改坏的配置文件绝不该把系统推向「开始外发」的那一侧。
//
// 为什么不做设置界面（当前阶段）：配置是三段凭据 + 一个开关，改它的频率接近于零；
// 而一个能开「外发对话正文」的 UI 开关值得单独设计（权限提示、明文确认），
// 顺手加在设置页里反而更危险。代价是配置错误只能从日志发现，因此 Enabled()
// 为真时启动阶段必须打一行明确的「已启用」——见 app.go 的初始化处。

// Config tracing.json 的形状
type Config struct {
	// Enabled 总开关，默认 false。
	Enabled bool `json:"enabled"`

	// Endpoint OTLP/HTTP traces 端点。自建 Langfuse 是
	// http://<host>:3000/api/public/otel/v1/traces（需 Langfuse >= v3.22.0）。
	//
	// **必须带 /v1/traces**：本项目直接用 net/http 发，不像官方 SDK 那样会自动补路径。
	Endpoint string `json:"endpoint"`

	// PublicKey / SecretKey Langfuse 项目密钥（pk-lf-… / sk-lf-…），
	// 发请求时拼成 Basic base64("<public>:<secret>")。
	PublicKey string `json:"publicKey"`
	SecretKey string `json:"secretKey"`

	// CaptureContent 是否导出对话正文（messages / 系统提示词 / 工具结果）。
	//
	// 默认 false，只发元数据：模型名、token 用量、耗时、状态、工具名与参数体积。
	// 打开后系统提示词（含长期记忆召回内容、AGENTS.md 内容）会一并入库，
	// 应当视为**工作区内容的二次留存**——这是 docs/langfuse-integration-survey.md
	// 里唯一需要人工确认的合规项，所以默认关、且改它需要动文件而不是点开关。
	CaptureContent bool `json:"captureContent"`

	// MaxTextBytes 单个字段（input/output/statusMessage）的最大字节数，超出截断。
	//
	// 存在的理由：本项目的工具会返回终端输出、整份文件内容、web_search 的整页正文，
	// 原样上报会让单条 span 涨到几十 KB 至 MB 级，把 Langfuse 的入库和 UI 一起拖垮。
	// 0 表示用默认值（DefaultMaxTextBytes）。
	MaxTextBytes int `json:"maxTextBytes"`

	// TimeoutMS 单次导出请求的超时（毫秒）。0 表示用默认值。
	TimeoutMS int `json:"timeoutMs"`
}

const (
	// DefaultMaxTextBytes 单字段默认截断长度。4 KB 足够装下一段工具调用的
	// 参数与结果摘要，又不至于让一条 trace 超出 Langfuse 的舒适区。
	DefaultMaxTextBytes = 4096
	// maxTextBytesMin 截断下限：再小就基本看不出是什么内容了。
	maxTextBytesMin = 256
	// maxTextBytesMax 截断上限：再大就等于不截断，多半是手滑多敲了几个 0。
	maxTextBytesMax = 1 << 20

	// DefaultTimeoutMS 单次导出请求默认超时。遥测是旁路，宁可丢也不能拖住对话，
	// 所以这个值刻意取得比一次模型调用短得多。
	DefaultTimeoutMS = 5000
	timeoutMSMin     = 500
	timeoutMSMax     = 60000
)

// Store tracing.json 的读写（内存为唯一真源，读盘只发生在构造时）
type Store struct {
	path string

	mu  sync.Mutex
	cfg Config
}

// NewStore 建存储并尝试读盘。读失败不报错——见文件头的失败方向说明。
func NewStore(path string) *Store {
	s := &Store{path: path}
	s.load()
	return s
}

func (s *Store) load() {
	if s == nil || s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		// 文件不存在是最常见的情况（从未启用过），不值得打日志。
		if !os.IsNotExist(err) {
			fmt.Printf("[tracing] 读取 %s 失败（保持关闭）: %v\n", s.path, err)
		}
		return
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		fmt.Printf("[tracing] 解析 %s 失败（保持关闭）: %v\n", s.path, err)
		return
	}
	s.cfg = c
}

// Get 取当前配置。返回值一定已归一化，调用方不必再校验。
func (s *Store) Get() Config {
	if s == nil {
		return Config{MaxTextBytes: DefaultMaxTextBytes, TimeoutMS: DefaultTimeoutMS}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return normalize(s.cfg)
}

// normalize 越界与零值统一回落到默认，并剔除字符串两端空白。
//
// 密钥与端点做 TrimSpace 是必要的：从文档里复制粘贴时常带尾随空格或换行，
// 而它们会直接拼进 HTTP 头与 URL，表现为 401 与 404 这两种最费劲的错误。
func normalize(c Config) Config {
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	c.PublicKey = strings.TrimSpace(c.PublicKey)
	c.SecretKey = strings.TrimSpace(c.SecretKey)
	if c.MaxTextBytes < maxTextBytesMin || c.MaxTextBytes > maxTextBytesMax {
		c.MaxTextBytes = DefaultMaxTextBytes
	}
	if c.TimeoutMS < timeoutMSMin || c.TimeoutMS > timeoutMSMax {
		c.TimeoutMS = DefaultTimeoutMS
	}
	return c
}

// Validate 判断这份配置是否**足以真正发起导出**。
//
// 与 Get/normalize 分开：normalize 只保证「值合理」，这里回答「能不能用」。
// 两者的差别就是「enabled=true 但端点写错」这种情况——它必须被当成配置错误
// 显式报出来，而不是安静地什么都不做。
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Endpoint == "" {
		return fmt.Errorf("未配置 endpoint")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return fmt.Errorf("endpoint 不是合法 URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint 必须以 http:// 或 https:// 开头，当前是 %q", u.Scheme)
	}
	if c.PublicKey == "" || c.SecretKey == "" {
		return fmt.Errorf("未配置 publicKey / secretKey")
	}
	return nil
}
