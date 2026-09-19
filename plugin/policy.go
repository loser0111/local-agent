package plugin

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ===== 通知决策：该不该打扰用户 =====
//
// 决策树严格按附录 §7 的顺序**命中即停**：
//
//	任务级覆盖 → 全局暂停 → L0 仅记录 → 全屏静默 → DND 时段
//	→ 频率上限合并 → 前台活跃仅应用内 → 否则发 Toast
//
// 顺序本身就是需求：把「全局暂停」放在「任务级覆盖」之后，是为了让
// 单个任务仍能通过显式配置在暂停期间被静默（不打扰）；放在前面则会让
// 任务级配置彻底失效。这类顺序问题最容易在重构中被无意改坏，所以有表驱动测试守着。

// Delivery 是决策结果。
type Delivery string

const (
	// DeliveryToast 弹系统通知。
	DeliveryToast Delivery = "toast"
	// DeliveryInApp 只做应用内提醒（不弹系统通知）。
	DeliveryInApp Delivery = "in-app"
	// DeliveryRecord 只记录（不打扰用户）。
	DeliveryRecord Delivery = "record"
	// DeliveryDrop 丢弃（已去重或超频率上限）。
	DeliveryDrop Delivery = "drop"
)

// Decision 是一次通知决策。
type Decision struct {
	Delivery Delivery
	Level    NotifyLevel
	// Reason 说明为什么这样决策，写进日志与历史，便于事后解释「为什么没弹」。
	Reason string
}

// DecisionInput 是决策所需的全部上下文（显式传参，方便表驱动测试）。
type DecisionInput struct {
	Task     *Task
	Settings *GlobalConfig
	Now      time.Time

	// DNDOverrideActive 表示任务级覆盖已经判定过（由调用方传入生效配置）。
	DND *DNDConfig

	// Fullscreen 表示当前有全屏应用在跑（游戏/演示），此时绝不弹通知。
	Fullscreen bool
	// Foreground 表示主窗口聚焦且可见。
	Foreground bool

	// 去重与频率限制的既有状态。
	DedupeHit   bool
	RateLimited bool
	// RateLimitReason 频率被限制的原因（哪个维度超了）。
	RateLimitReason string

	// NotifyEnabled 是全局是否允许系统通知（配置中心）。
	NotifyEnabled bool
}

// Decide 按决策树给出结论。
func Decide(in DecisionInput) Decision {
	level := LevelNormal
	if in.Task != nil && in.Task.Notify != nil && in.Task.Notify.Level != "" {
		level = in.Task.Notify.Level
	}

	// 1) 任务级覆盖：显式「只记录」或「只应用内」优先级最高。
	if in.Task != nil && in.Task.Notify != nil {
		if in.Task.Notify.Level == LevelRecord {
			return Decision{Delivery: DeliveryRecord, Level: level, Reason: "任务级配置：仅记录"}
		}
		if in.Task.Notify.InAppOnly {
			return Decision{Delivery: DeliveryInApp, Level: level, Reason: "任务级配置：仅应用内提醒"}
		}
	}

	// 2) 全局暂停：暂停期间一律不打扰，但仍要记录（到期任务不得凭空消失）。
	if in.Settings != nil && in.Settings.Paused {
		return Decision{Delivery: DeliveryRecord, Level: level, Reason: "全局暂停中"}
	}

	// 3) L0 仅记录。
	if level == LevelRecord {
		return Decision{Delivery: DeliveryRecord, Level: level, Reason: "打断级别为 L0"}
	}

	// 4) 全屏静默：游戏/演示时不弹任何系统通知。
	if in.Fullscreen {
		return Decision{Delivery: DeliveryRecord, Level: level, Reason: "检测到全屏应用"}
	}

	// 5) 免打扰时段。
	if in.DND != nil && in.DND.InDND(in.Now) {
		return Decision{Delivery: DeliveryInApp, Level: level, Reason: "处于免打扰时段"}
	}

	// 6) 频率上限（同一周期已通知过 / 超过每分钟或每 5 分钟上限）。
	if in.DedupeHit {
		return Decision{Delivery: DeliveryDrop, Level: level, Reason: "同一任务同一周期已通知"}
	}
	if in.RateLimited {
		reason := "触发通知频率上限"
		if in.RateLimitReason != "" {
			reason = in.RateLimitReason
		}
		return Decision{Delivery: DeliveryDrop, Level: level, Reason: reason}
	}

	// 7) 前台活跃：用户就在眼前，应用内提醒即可。
	if in.Foreground && (in.Settings == nil || in.Settings.ForegroundOnlyInApp) {
		return Decision{Delivery: DeliveryInApp, Level: level, Reason: "主窗口正在前台"}
	}

	// 8) 全局通知开关关闭。
	if in.Settings != nil && !in.Settings.NotifyEnabled {
		return Decision{Delivery: DeliveryInApp, Level: level, Reason: "系统通知已关闭"}
	}

	return Decision{Delivery: DeliveryToast, Level: level, Reason: "正常投递"}
}

// Dedupe 是「同一任务同一周期只弹一次」的台账（F8.5）。
//
// 台账直接存在 Task.State.Notified 里并随 tasks.json 落盘 ——
// 因此应用重启后也不会因为「内存台账清空」而重复提醒。
type Dedupe struct{}

// Key 生成去重键：任务 + 触发时刻（精确到秒）。
//
// 用「触发时刻」而不是「任务 + 周期序号」：触发时刻是规则推导出来的确定值，
// 不依赖序号推进是否成功，重启后仍能得到同一个键。
func (Dedupe) Key(taskID string, fired time.Time) string {
	return fmt.Sprintf("%s@%s", taskID, fired.Local().Format(time.RFC3339))
}

// Hit 判断该键是否已经通知过。
func (Dedupe) Hit(t *Task, key string) bool {
	if t == nil || t.State.Notified == nil {
		return false
	}
	_, ok := t.State.Notified[key]
	return ok
}

// Mark 记录已通知。
func (Dedupe) Mark(t *Task, key string, at time.Time) {
	if t == nil {
		return
	}
	if t.State.Notified == nil {
		t.State.Notified = make(map[string]string)
	}
	t.State.Notified[key] = at.Local().Format(time.RFC3339)
}

// RateLimiter 是滑动窗口限流器（F7.4）。
//
// 维度：同一任务每分钟 1 条、全局每 5 分钟 3 条。
// 用时间戳切片而不是计数器：窗口滑动才准确，计数器在窗口边界会漏放。
//
// 必须带锁：它会被**两个 goroutine** 同时访问 —— 调度循环（到点自动触发）
// 与界面线程（用户点「立即执行」），两者都会走投递逻辑。
type RateLimiter struct {
	mu      sync.Mutex
	perTask map[string][]time.Time
	global  []time.Time
}

// NewRateLimiter 构造限流器。
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{perTask: map[string][]time.Time{}}
}

// Allow 判断是否放行；不放行时返回可读原因。
func (r *RateLimiter) Allow(taskID string, now time.Time, perTaskPerMinute, globalPer5Minutes int) (bool, string) {
	if r == nil {
		return true, ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if perTaskPerMinute > 0 {
		cutoff := now.Add(-time.Minute)
		r.perTask[taskID] = pruneBefore(r.perTask[taskID], cutoff)
		if len(r.perTask[taskID]) >= perTaskPerMinute {
			return false, fmt.Sprintf("同一任务 %d 分钟内已通知 %d 次", 1, len(r.perTask[taskID]))
		}
	}
	if globalPer5Minutes > 0 {
		cutoff := now.Add(-5 * time.Minute)
		r.global = pruneBefore(r.global, cutoff)
		if len(r.global) >= globalPer5Minutes {
			return false, fmt.Sprintf("全局 5 分钟内已通知 %d 条", len(r.global))
		}
	}
	return true, ""
}

// Record 记录一次放行的通知。
func (r *RateLimiter) Record(taskID string, now time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.perTask[taskID] = append(r.perTask[taskID], now)
	r.global = append(r.global, now)
}

func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

// ===== 通知内容规范（F3.10）=====
//
// 标题 ≤40 字符（含任务名）、正文 ≤120 字、结论前置、失败必带原因、
// 不用 Subtitle / Markdown。截断必须按 rune 做，否则中文会被切成乱码。

// truncateRunes 按字符数截断，超出部分用省略号。
func truncateRunes(s string, max int) string {
	rs := []rune(strings.TrimSpace(s))
	if max <= 0 || len(rs) <= max {
		return string(rs)
	}
	if max == 1 {
		return "…"
	}
	return string(rs[:max-1]) + "…"
}

// oneLine 把多行文本压成单行（通知正文不支持换行展示）。
func oneLine(s string) string {
	replaced := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")
	out := replaced.Replace(strings.TrimSpace(s))
	for strings.Contains(out, "  ") {
		out = strings.ReplaceAll(out, "  ", " ")
	}
	return strings.TrimSpace(out)
}

// BuildNotification 组装一条通知（N1–N6 六类模板的公共入口）。
//
// level 决定标题前缀，失败类通知必须带原因 —— 只显示「失败」是明确禁止的。
func BuildNotification(t *Task, level NotifyLevel, title, body string) NotifyRequest {
	if t != nil && t.Notify != nil {
		if strings.TrimSpace(t.Notify.TitleTemplate) != "" {
			title = RenderTemplate(t.Notify.TitleTemplate, templateVars(t, title, body))
		}
		if strings.TrimSpace(t.Notify.BodyTemplate) != "" {
			body = RenderTemplate(t.Notify.BodyTemplate, templateVars(t, title, body))
		}
	}
	title = truncateRunes(oneLine(title), maxNotifyTitleRunes)
	body = truncateRunes(oneLine(body), maxNotifyBodyRunes)

	ctx := ""
	taskID := ""
	if t != nil {
		taskID = t.ID
		ctx = t.ID
	}
	// ID 形如 task:<taskId>:<triggerId>，回传时据此定位任务。
	id := fmt.Sprintf("task:%s:%s", taskID, ctx)

	return NotifyRequest{
		ID:       id,
		Category: categoryFor(level),
		Title:    title,
		Body:     body,
		Level:    level,
		Actions:  actionsFor(t),
		Data: map[string]string{
			"taskId": taskID,
			"kind":   string(taskKind(t)),
		},
	}
}

func taskKind(t *Task) Kind {
	if t == nil {
		return KindReminder
	}
	return t.Kind
}

// categoryFor 按级别选通知分类（分类决定按钮集合）。
func categoryFor(level NotifyLevel) string {
	switch level {
	case LevelUrgent:
		return "exec.abort"
	default:
		return "reminder.basic"
	}
}

// actionsFor 按任务类型给出动作按钮（最多 3 个，F3.2）。
//
// 提醒型：完成 / 稍后提醒 / 打开 agent；执行型：查看结果 / 重新执行 / 打开 agent。
func actionsFor(t *Task) []NotifyAction {
	if t != nil && t.Kind == KindExec {
		return []NotifyAction{
			{ID: "view", Label: "查看结果"},
			{ID: "rerun", Label: "重新执行"},
			{ID: "open", Label: "打开 agent"},
		}
	}
	return []NotifyAction{
		{ID: "done", Label: "完成"},
		{ID: "snooze", Label: "稍后提醒"},
		{ID: "open", Label: "打开 agent"},
	}
}

// FallbackNotice 负责「每会话只提示一次」的降级提示（F3.4）。
//
// 通知不可用时不能静默：用户会以为插件坏了。但也不能每条都提示原因，
// 否则降级本身变成了新的打扰源。
//
// 带锁的原因同 RateLimiter：启动流程与调度循环都可能触发降级提示。
type FallbackNotice struct {
	mu       sync.Mutex
	notified map[string]bool
}

// NewFallbackNotice 构造降级提示器。
func NewFallbackNotice() *FallbackNotice {
	return &FallbackNotice{notified: map[string]bool{}}
}

// Once 返回 true 表示「本次应当提示降级原因」（同一会话内只提示一次）。
func (f *FallbackNotice) Once(sessionKey string) bool {
	if f == nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notified[sessionKey] {
		return false
	}
	f.notified[sessionKey] = true
	return true
}

// Reset 在会话变更时调用，让新会话可以再提示一次。
func (f *FallbackNotice) Reset() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notified = map[string]bool{}
}
