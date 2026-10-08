package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ===== Token 用量统计与缓存状态 =====
//
// 这一层的存在理由：两家协议的 usage 字段**同名不同义**，不能直接混用。
//
// 最要命的一处是「输入总量」：
//   - Anthropic 的 input_tokens **只是未命中缓存的那部分**，总量要把三者相加；
//   - OpenAI 兼容的 prompt_tokens 是**总量**（含 cached）；
//   - DeepSeek 的 prompt_tokens 也是总量（= hit + miss）。
//
// 也就是说同一个语义，在两个协议里的算法是相反的。归一化只做一次、只在一个地方做，
// 否则每个调用方各写一遍，必然各写错一遍——本项目已经吃过两次「两处实现悄悄漂移」的亏
// （权限网关、工具曝光策略），这里不再重复。
//
// 归一化之后必须成立的不变式：
//
//	LLMUsage.PromptTokens 恒为「输入总量」。
//
// 于是「未命中部分 = PromptTokens − CacheRead − CacheWrite」对三家都成立，
// tokenUsageOf 里不需要任何协议判断。

// TokenUsage 一次 LLM 请求的用量，供应商中立。
type TokenUsage struct {
	InputUncached int `json:"inputUncached"`
	CacheRead     int `json:"cacheRead"`
	CacheWrite    int `json:"cacheWrite"`

	// CacheWrite5m / CacheWrite1h 仅 Anthropic 提供（usage.cache_creation 的拆分），
	// 同一时刻只有一个非零。它直接回答「这轮是按 1.25x 还是 2x 计的价」——
	// 也是检验用户配的 1h TTL 到底有没有生效的唯一手段（服务端会单方面调整默认 TTL）。
	CacheWrite5m int `json:"cacheWrite5m,omitempty"`
	CacheWrite1h int `json:"cacheWrite1h,omitempty"`

	Output int `json:"output"`
	// OutputReasoning 推理 token。计费算输出，但不进正文——
	// 对用户是**完全看不见的成本**（回复很短，背后可能想了很多），必须单独列出来。
	OutputReasoning int `json:"outputReasoning,omitempty"`

	ServerToolUseWebSearch int    `json:"serverToolUseWebSearch,omitempty"`
	ServiceTier            string `json:"serviceTier,omitempty"`
}

// TotalInput 输入总量。
// ⚠️ 不要写成 InputUncached —— 那只是未命中部分。
func (u TokenUsage) TotalInput() int {
	return u.InputUncached + u.CacheRead + u.CacheWrite
}

// HasData 本次响应是否带回了用量。全零时前端应显示「未返回用量明细」而不是一排 0。
func (u TokenUsage) HasData() bool {
	return u.TotalInput() > 0 || u.Output > 0
}

// CacheHitRate 缓存命中率（按输入 token 计），取值 0..1。
func (u TokenUsage) CacheHitRate() float64 {
	if t := u.TotalInput(); t > 0 {
		return float64(u.CacheRead) / float64(t)
	}
	return 0
}

// InputCostMultiplier 相对全价的输入成本倍数（1.0 = 没省，0.1 = 省 90%）。
//
// 刻意只依赖 1.0 / 1.25 / 2.0 / 0.1 这几个**不随定价变化**的倍率，
// 而不是折算成金额：金额需要维护一张跨厂商、跨时期、频繁变动的单价表，
// 表一旦过期，展示的数字比没有更糟。倍数永远不过期。
func (u TokenUsage) InputCostMultiplier(writeRate float64) float64 {
	t := u.TotalInput()
	if t == 0 {
		return 1
	}
	cost := float64(u.InputUncached) + float64(u.CacheWrite)*writeRate + float64(u.CacheRead)*0.1
	return cost / float64(t)
}

// cacheWriteRate 缓存写入的计价倍数。1h TTL 是 2.0，默认 5 分钟档是 1.25。
func cacheWriteRate(ttl string) float64 {
	if strings.TrimSpace(ttl) == promptCacheTTL1h {
		return 2.0
	}
	return 1.25
}

// CacheState 缓存状态。前端的徽标按它分色，而不是自己去推断——
// 「谁在什么条件下算命中」只应该有一处定义。
type CacheState string

const (
	CacheStateOff         CacheState = "off"         // 配置未启用
	CacheStateIdle        CacheState = "idle"        // 已启用，但本轮没有用量数据
	CacheStateMiss        CacheState = "miss"        // 已启用，本轮有写入、无读取
	CacheStateHit         CacheState = "hit"         // 已启用，本轮有读取
	CacheStateUnsupported CacheState = "unsupported" // 已启用但连续多轮读写全零
)

// usageZeroStreakThreshold 连续多少轮读写全零就判定「网关可能不支持」。
//
// 取 3 而不是 1：单轮零命中是完全正常的（首轮必然只写不读，前缀变化也会重建），
// 只有连续多轮都毫无动静才值得提示。
const usageZeroStreakThreshold = 3

// UsageTotals 会话累计用量。
//
// 只累加、不回退：压缩与撤销改变的是「上下文里还剩多少」，不改变「已经花掉了多少」。
// 把这两件事混在一起会让用户在撤销一次改动后看到用量倒退，而那既不真实也没有意义。
type UsageTotals struct {
	Turns           int   `json:"turns"`
	InputUncached   int   `json:"inputUncached"`
	CacheRead       int   `json:"cacheRead"`
	CacheWrite      int   `json:"cacheWrite"`
	Output          int   `json:"output"`
	OutputReasoning int   `json:"outputReasoning,omitempty"`
	FirstAt         int64 `json:"firstAt,omitempty"`
	LastAt          int64 `json:"lastAt,omitempty"`

	// CacheZeroStreak 连续「有真实请求但缓存读写全零」的轮数，用于判定「疑似网关不支持」。
	// 它**必须落盘**：不落盘的话每次重启都会把 streak 清零，
	// 于是「连续 3 轮」这个条件永远凑不齐，红色提示也就永远不会出现。
	CacheZeroStreak int `json:"cacheZeroStreak,omitempty"`
}

// Add 累加一轮用量。now 由调用方给（Unix 毫秒），便于测试注入。
//
// cached 表示本轮请求是否**真的带了缓存断点**（即缓存功能已启用）。
// 它只影响 streak 的推进：没启用缓存时读写必然全零，那种零不能算作「网关不支持」的证据，
// 否则用户一开统计就会看到一个假的红色警告。
func (t *UsageTotals) Add(u TokenUsage, now int64, cached bool) {
	if t == nil {
		return
	}
	t.Turns++
	t.InputUncached += u.InputUncached
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
	t.Output += u.Output
	t.OutputReasoning += u.OutputReasoning
	if t.FirstAt == 0 {
		t.FirstAt = now
	}
	t.LastAt = now

	switch {
	case !cached:
		t.CacheZeroStreak = 0
	case u.CacheRead == 0 && u.CacheWrite == 0:
		t.CacheZeroStreak++
	default:
		t.CacheZeroStreak = 0
	}
}

// TotalInput 会话累计输入总量
func (t UsageTotals) TotalInput() int {
	return t.InputUncached + t.CacheRead + t.CacheWrite
}

// CacheHitRate 会话累计缓存命中率
func (t UsageTotals) CacheHitRate() float64 {
	if in := t.TotalInput(); in > 0 {
		return float64(t.CacheRead) / float64(in)
	}
	return 0
}

// UsageDetail 「用量明细」弹窗的数据。
//
// 刻意**不并进 ContextStat**：后者是「切会话即拉」的高频轻量接口，
// 而明细要额外取请求快照、算派生指标，塞进去会拖慢每一次切换。
type UsageDetail struct {
	SessionID string `json:"sessionId"`
	// HasData 会话累计轮数为 0。老会话（统计上线前创建）以及从未真正发过请求的会话为 false，
	// 前端据此显示空态而不是一排 0。
	HasData bool `json:"hasData"`

	Last   TokenUsage  `json:"last"`   // 本轮（最近一次请求）
	Totals UsageTotals `json:"totals"` // 会话累计
	Cache  CacheView   `json:"cache"`

	// Context 上下文构成与校准系数。与指示器用的是同一份计算，
	// 保证弹窗里的百分比和指示器上的百分比**必然一致**（否则用户会以为哪里出错了）。
	Context *ContextStat `json:"context,omitempty"`
	// Request 最近一次真实发出的请求快照（工具定义占用、摘要覆盖范围等）。
	// 为 nil 表示本进程还没发过请求（重启后即丢失，该快照只在内存里）。
	Request *LLMRequestSnapshot `json:"request,omitempty"`

	// RawUsage 最近一次响应的原始 usage JSON，排障用。
	// 成本极低，但能省掉未来所有「到底是哪一层错了」的扯皮。
	RawUsage string `json:"rawUsage,omitempty"`
}

// CacheView 缓存状态的派生视图。派生指标只在后端算一次，前端不重复实现——
// 前端各算一份的下场是两处口径迟早不一致，而用户只会相信其中一处。
type CacheView struct {
	State        CacheState `json:"state"`
	Enabled      bool       `json:"enabled"`
	TTL          string     `json:"ttl"`      // 生效的 TTL（"" = 5 分钟档，见 promptCacheTTL1h）
	WriteRate    float64    `json:"writeRate"` // 本轮写入计价倍数（1.25 / 2.0）
	HitRate      float64    `json:"hitRate"`   // 本轮命中率
	TotalHitRate float64    `json:"totalHitRate"`
	CostMultiple float64    `json:"costMultiple"` // 本轮相对全价的输入倍数
	ZeroStreak   int        `json:"zeroStreak"`
	// Note 一句给用户看的人话解释。由后端给出而非前端拼：
	// 文案要同时覆盖「未启用」「未命中」「未达最小长度」「疑似不支持」四种成因，
	// 放在前端拼必然散落在多个分支里。
	Note string `json:"note"`
}

const promptCacheTTL1h = "1h"

// tokenUsageOf 把统一形状的 LLMUsage 折算成 TokenUsage。
//
// 前提：LLMUsage.PromptTokens 已被各协议的解析层归一成「输入总量」
// （见 anthropic.go 的 llmUsageFromAnthropic）。这里只做减法，不做任何协议判断。
func tokenUsageOf(u LLMUsage) TokenUsage {
	uncached := u.PromptTokens - u.CacheRead - u.CacheWrite
	if uncached < 0 {
		// 网关给的数不自洽（缓存量大于总量）时宁可归零：
		// 负数会让「未命中」这一格无法解释，也会把成本倍数算成负数。
		uncached = 0
	}
	return TokenUsage{
		InputUncached:          uncached,
		CacheRead:              u.CacheRead,
		CacheWrite:             u.CacheWrite,
		CacheWrite5m:           u.CacheWrite5m,
		CacheWrite1h:           u.CacheWrite1h,
		Output:                 u.CompletionTokens,
		OutputReasoning:        u.ReasoningTokens,
		ServerToolUseWebSearch: u.ServerToolUseWebSearch,
		ServiceTier:            u.ServiceTier,
	}
}

// cacheViewOf 由「本轮用量 + 会话累计 + 配置」算出状态视图。
func cacheViewOf(last TokenUsage, totals UsageTotals, prefs PromptCachePrefs) CacheView {
	rate := cacheWriteRate(prefs.TTL)
	v := CacheView{
		Enabled:      prefs.Enabled,
		TTL:          prefs.TTL,
		WriteRate:    rate,
		HitRate:      last.CacheHitRate(),
		TotalHitRate: totals.CacheHitRate(),
		CostMultiple: last.InputCostMultiplier(rate),
		ZeroStreak:   totals.CacheZeroStreak,
	}

	switch {
	// 观测到的命中**优先于配置**。
	//
	// 命中是事实，配置只说明我们有没有主动打断点。本项目实测所用的网关会自行做
	// 前缀缓存（原始 usage 里能看到 cache_read_input_tokens），所以"本机没开"不等于
	// "没有缓存"。早先把配置判断放在第一位，结果是一次真实命中了 3840 token 的请求，
	// 界面却写着「未启用」加「成本与全价持平」——把一个已经省下的钱读成了没省。
	// 统计面板最不该犯的错就是这类"把好消息读成坏消息"。
	case last.HasData() && last.CacheRead > 0:
		v.State = CacheStateHit
		if prefs.Enabled {
			v.Note = fmt.Sprintf("已命中：本轮 %.0f%% 的输入 token 走了缓存，按 0.1x 计价。",
				last.CacheHitRate()*100)
		} else {
			v.Note = fmt.Sprintf("网关侧已命中：本轮 %.0f%% 的输入 token 走了缓存。"+
				"本机的显式断点尚未启用，命中的是网关自己的前缀缓存策略。",
				last.CacheHitRate()*100)
		}
	case !last.HasData():
		v.State = CacheStateIdle
		v.Note = "已启用，但本次响应没有带回用量明细（部分网关的流式响应不带 usage）。"
	case !prefs.Enabled:
		v.State = CacheStateOff
		v.Note = "本机未启用显式断点，且本轮没有观测到缓存命中。" +
			"开启后同一会话的连续轮次可复用已计算的前缀（若网关本身就带缓存，开启后可省下写入那部分）。"
	case totals.CacheZeroStreak >= usageZeroStreakThreshold:
		v.State = CacheStateUnsupported
		v.Note = "已启用且连续多轮没有任何缓存读写。可能是网关不支持该能力，也可能是 prompt 太短未达最小可缓存长度（约 1024 token）——两种情况的表现相同，需先确认。"
	default:
		v.State = CacheStateMiss
		v.Note = "本轮未命中，已写入缓存供后续轮次复用。前缀发生变化（记忆召回、压缩、工具集变动）会导致缓存重建。"
	}
	return v
}

// ===== Prompt Cache 偏好 =====
//
// 与 ContextPrefs 同样的取舍：全局策略、独立文件、文件不存在即取默认。
// 本方案的统计部分（本文件其余内容）**不依赖**这里的开关；
// 开关只在真正打缓存断点时被读取，默认关（见 docs/prompt-cache-design.md §9）。

// PromptCachePrefs 提示缓存偏好
type PromptCachePrefs struct {
	Enabled bool   `json:"enabled"`
	TTL     string `json:"ttl"`     // "" 或 "1h"
	Rolling bool   `json:"rolling"` // 是否启用 messages 滚动断点（P3/P4）
	// RollingGuardBlocks 单轮新增 block 数超过它时在中部补一个中间断点。
	// Anthropic 的缓存命中只向前回溯约 20 个 block，而本项目鼓励一轮批量发起多个工具调用，
	// 一轮新增十几二十个 block 是常态——没有这个守卫会表现为「什么都没改却突然不命中」。
	RollingGuardBlocks int `json:"rollingGuardBlocks"`
}

const (
	promptCacheRollingGuardDefault = 15
	promptCacheRollingGuardMin     = 4
	promptCacheRollingGuardMax     = 19 // 超过 20 的回溯上限就没有意义了
)

// PromptCachePrefsStore prompt-cache.json 的读写（内存为唯一真源）
type PromptCachePrefsStore struct {
	path  string
	mu    sync.Mutex
	prefs PromptCachePrefs
}

// NewPromptCachePrefsStore 建存储并尝试读盘。读失败不报错——与 ContextPrefs 同一个失败方向：
// 配置坏了就回默认值（这里是「关闭」），绝不会因为一个解析错误把系统推向更不安全的一侧。
func NewPromptCachePrefsStore(path string) *PromptCachePrefsStore {
	s := &PromptCachePrefsStore{path: path}
	s.load()
	return s
}

func (s *PromptCachePrefsStore) load() {
	if s == nil || s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var p PromptCachePrefs
	if err := json.Unmarshal(b, &p); err != nil {
		fmt.Printf("[promptcache] 解析 %s 失败（改用默认：关闭）: %v\n", s.path, err)
		return
	}
	s.prefs = p
}

// Get 取当前偏好。返回值一定已归一化，调用方不必再校验。
func (s *PromptCachePrefsStore) Get() PromptCachePrefs {
	if s == nil {
		return PromptCachePrefs{RollingGuardBlocks: promptCacheRollingGuardDefault}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.prefs
	p.TTL = normalizeCacheTTL(p.TTL)
	p.RollingGuardBlocks = normalizeRollingGuard(p.RollingGuardBlocks)
	return p
}

// Set 写入并落盘。TTL 与守卫阈值在这里就被归一，而不是存下去再靠 Get 纠正——
// 否则用户改完看不到反馈，还以为自己设的值生效了。
func (s *PromptCachePrefsStore) Set(p PromptCachePrefs) error {
	if s == nil {
		return fmt.Errorf("提示缓存配置存储未初始化")
	}
	p.TTL = normalizeCacheTTL(p.TTL)
	p.RollingGuardBlocks = normalizeRollingGuard(p.RollingGuardBlocks)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefs = p
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(s.path, b, 0o644)
}

// normalizeCacheTTL 只接受 "" 与 "1h"，其余一律回落到默认（5 分钟档）。
// 刻意不接受 "5m" 这种写法：默认档就是空串，多一个等价写法只会在比较时踩坑。
func normalizeCacheTTL(ttl string) string {
	if strings.TrimSpace(ttl) == promptCacheTTL1h {
		return promptCacheTTL1h
	}
	return ""
}

func normalizeRollingGuard(n int) int {
	if n < promptCacheRollingGuardMin || n > promptCacheRollingGuardMax {
		return promptCacheRollingGuardDefault
	}
	return n
}

// ===== 每会话最近一次的用量（内存态）=====

// usageRecord 一次请求的用量快照
type usageRecord struct {
	Usage    TokenUsage
	RawUsage string
	At       int64
}

// usageLog 每会话最近一次的用量，与 llmRequestLog 同一个定位与生命周期。
//
// 为什么不落进会话文件：它是"看最近这次"的排障视图，不是审计日志。
// 落盘会让每一轮都多一次整份会话文件的写 I/O，而这个信息只在刚跑完那几分钟里有价值。
// 会话累计（需要跨重启保留）走的是 Session.UsageTotals，两者刻意分开。
type usageLog struct {
	mu    sync.Mutex
	items map[string]*usageRecord
}

// record 记下一次用量。对 nil 接收者安全：测试里会直接构造零值 App，那一路径不该 panic。
func (l *usageLog) record(sessionID string, u TokenUsage, raw string, at int64) {
	if l == nil || sessionID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.items == nil {
		l.items = map[string]*usageRecord{}
	}
	l.items[sessionID] = &usageRecord{Usage: u, RawUsage: raw, At: at}
}

// get 取某会话最近一次用量；没有则返回 nil（调用方据此显示空态）
func (l *usageLog) get(sessionID string) *usageRecord {
	if l == nil || sessionID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.items[sessionID]
}

// forget 丢弃某会话的用量记录（删除会话时调用）。
// 不清理的话内存里会留下永远够不着的条目——量小，但"删了还在"是会被发现的那类残留。
func (l *usageLog) forget(sessionID string) {
	if l == nil || sessionID == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.items, sessionID)
}

// ===== 用量事件 =====

// UsageEvent 一轮对话的用量推送（ChatEvent.Type = ChatEventUsage）。
//
// 带上会话累计值，是为了让开着的弹窗实时更新——只发本轮的话前端得自己累加，
// 而"哪些轮算进累计"这个口径只应该有一处定义（在后端）。
type UsageEvent struct {
	// Turn 这是本会话的第几次模型请求（即累计轮数），便于前端判断"更新的是不是新的一轮"。
	Turn   int         `json:"turn"`
	Last   TokenUsage  `json:"last"`
	Totals UsageTotals `json:"totals"`
	Cache  CacheView   `json:"cache"`
}
