package plugin

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ===== 定时任务的数据模型 =====
//
// 三条铁律（附录 §8）：
//  1. 定义与状态分开：Enabled 是「用户想不想让它跑」，State 是「它跑到哪了」，
//     两者绝不合并成一个字段 —— 合并后「停用再启用」这类操作必然丢信息。
//  2. nextFireAt 不落盘：它是从 Trigger + State 推导出来的派生值。
//     落盘会立刻引入「存的和算的不一致」这一整类 bug。
//  3. Until / MaxFires 放 Trigger（决定「还有没有下次」），
//     错过补偿 / 重试 / DND 覆盖放 Policy（决定「下次该怎么处理」）。

// Kind 是任务类型。两种类型**不可直接互转**（F5.1），只能复制转换。
type Kind string

const (
	KindReminder Kind = "reminder"
	KindExec     Kind = "exec"
)

// TriggerKind 是时间规则的种类。
type TriggerKind string

const (
	TriggerOnce      TriggerKind = "once"
	TriggerInterval  TriggerKind = "interval"
	TriggerRecurring TriggerKind = "recurring"
	TriggerCron      TriggerKind = "cron"
)

// 触发与执行的结果取值。
const (
	ResultSucceeded   = "succeeded"
	ResultFailed      = "failed"
	ResultTimeout     = "timeout"
	ResultAborted     = "aborted"
	ResultRunning     = "running"
	ResultMissed      = "missed"
	ResultSkipped     = "skipped"
	ResultInterrupted = "interrupted"
	ResultNotified    = "notified"
)

// 错过补偿策略（F2.7）。
const (
	MissedCatchUp = "catchup" // 补发
	MissedSkip    = "skip"    // 跳过
	MissedDefer   = "defer"   // 顺延到下一个周期
)

// Trigger 是触发规则。
//
// 写法照搬仓库里 ToolSource 的约定：一个判别字段 + 若干类型化指针，
// 同一时刻只有一个指针非空。这样 JSON 里既省地方，又能让「Kind 与字段不匹配」
// 被 Validate 拦下来。
type Trigger struct {
	Kind      TriggerKind       `json:"kind"`
	Once      *OnceTrigger      `json:"once,omitempty"`
	Interval  *IntervalTrigger  `json:"interval,omitempty"`
	Recurring *RecurringTrigger `json:"recurring,omitempty"`
	Cron      *CronTrigger      `json:"cron,omitempty"`

	// Until 到此为止（RFC3339，本地时间）；为空表示不设上限。
	Until string `json:"until,omitempty"`
	// MaxFires 最多触发次数；<=0 表示不限。
	MaxFires int `json:"maxFires,omitempty"`
}

// OnceTrigger 一次性触发。
type OnceTrigger struct {
	At string `json:"at"` // RFC3339
}

// IntervalTrigger 固定间隔。
//
// StartAt 是**锚点**而非首次触发时刻：首次触发 = StartAt + EveryMinutes。
// 这样「现在开始，每小时一次」的语义不会出现「保存后立刻弹一次」的意外。
type IntervalTrigger struct {
	EveryMinutes int    `json:"everyMinutes"`
	StartAt      string `json:"startAt,omitempty"`
}

// RecurringTrigger 每日 / 每周 / 每月（F2.3）。
type RecurringTrigger struct {
	Period    string `json:"period"`    // daily | weekly | monthly
	TimeOfDay string `json:"timeOfDay"` // "HH:MM"，本机本地时间
	// Weekdays 0=周日 … 6=周六，weekly 用（可多选）。
	Weekdays []int `json:"weekdays,omitempty"`
	// DayOfMonth 1-31，monthly 用；超过当月天数时夹到当月最后一天。
	DayOfMonth int `json:"dayOfMonth,omitempty"`
}

// CronTrigger 五段 cron（P1，仅支持子集；保存前必须能给出预览）。
type CronTrigger struct {
	Expr string `json:"expr"`
}

// SnoozeState 是一次待处理的「稍后提醒」。
type SnoozeState struct {
	Until    string `json:"until"`    // RFC3339
	Original string `json:"original"` // 原定触发时刻，仅作展示
	Count    int    `json:"count"`
}

// MissedEntry 是一条待补偿的错过记录（F2.7）。
type MissedEntry struct {
	At      string `json:"at"`
	Handled string `json:"handled,omitempty"` // catchup | skip | defer | pending
}

// TaskState 是运行时状态（高频写）。
type TaskState struct {
	LastFiredAt         string            `json:"lastFiredAt,omitempty"`
	LastResult          string            `json:"lastResult,omitempty"`
	LastError           string            `json:"lastError,omitempty"`
	LastRunID           string            `json:"lastRunID,omitempty"`
	FiredCount          int               `json:"firedCount,omitempty"`
	ConsecutiveFailures int               `json:"consecutiveFailures,omitempty"`
	NextRetryAt         string            `json:"nextRetryAt,omitempty"`
	Snooze              *SnoozeState      `json:"snooze,omitempty"`
	MissedPending       []MissedEntry     `json:"missedPending,omitempty"`
	Notified            map[string]string `json:"notified,omitempty"`
	Finished            bool              `json:"finished,omitempty"`
}

// ReminderConf 是提醒型任务的配置。
type ReminderConf struct {
	// SnoozeMinutes 覆盖全局默认的稍后提醒时长；0 表示用全局默认。
	SnoozeMinutes int `json:"snoozeMinutes,omitempty"`
}

// ExecConf 是执行型任务的配置（F5.2）。
type ExecConf struct {
	Prompt string `json:"prompt,omitempty"`
	Model  string `json:"model,omitempty"`
	// WorkDir 工作区目录；为空表示沿用创建时的默认工作区。
	WorkDir string `json:"workDir,omitempty"`
	// TimeoutSeconds 超时；<=0 表示用默认 600s（F5.5）。
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

// NotifyConf 是通知配置。
type NotifyConf struct {
	Level NotifyLevel `json:"level,omitempty"`
	// InAppOnly 强制只做应用内提醒，不弹系统通知（P2，此处先留字段）。
	InAppOnly bool `json:"inAppOnly,omitempty"`
	// TitleTemplate / BodyTemplate 覆盖内置模板（F3.10）。
	TitleTemplate string `json:"titleTemplate,omitempty"`
	BodyTemplate  string `json:"bodyTemplate,omitempty"`
}

// Policy 是逐任务的处理策略，可覆盖全局配置。
type Policy struct {
	// MissedOverride 覆盖全局的错过补偿策略。
	MissedOverride string `json:"missedOverride,omitempty"`
	// DNDOverride 覆盖全局免打扰时段。
	DNDOverride *DNDConfig `json:"dndOverride,omitempty"`
	// MaxRetries 失败重试次数（F8.4）；0 表示不重试。
	MaxRetries int `json:"maxRetries,omitempty"`
	// RetryBackoffMinutes 重试间隔，默认 5 分钟。
	RetryBackoffMinutes int `json:"retryBackoffMinutes,omitempty"`
	// DisableAfterFailures 连续失败多少次后自动停用（安全工作包 §6.6，默认 3；<=0 表示不熔断）。
	DisableAfterFailures int `json:"disableAfterFailures,omitempty"`
}

// Task 是一条定时任务。
type Task struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Note     string        `json:"note,omitempty"`
	Kind     Kind          `json:"kind"`
	Trigger  Trigger       `json:"trigger"`
	Enabled  bool          `json:"enabled"`
	Reminder *ReminderConf `json:"reminder,omitempty"`
	Exec     *ExecConf     `json:"exec,omitempty"`
	Notify   *NotifyConf   `json:"notify,omitempty"`
	Policy   *Policy       `json:"policy,omitempty"`

	// State 是运行时状态；与 Enabled 严格分开。
	State TaskState `json:"state"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// TaskView 是交给前端/外部的任务视图：在 Task 之上补一个**导数**字段 nextFireAt。
//
// 为什么是独立结构体而不是「内嵌 Task」：
//  1. 「nextFireAt 不落盘」的保证来自「持久化的是 Task、Task 上没有这个字段」，
//     而不是来自内嵌 —— 独立结构体同样成立，且更直白；
//  2. 内嵌会让 Wails 的 TS 绑定生成器无法解析被提升的 TaskState，
//     生成出 `state: any` 并打印 "Not found: plugin.TaskState" 警告，
//     等于把最关键的字段类型丢掉；
//  3. 字段一一列出后，TaskView 与 Task 的字段漂移由测试兜住（见 task_test.go），
//     这比依赖内嵌的隐式继承更容易发现遗漏。
type TaskView struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Note    string  `json:"note,omitempty"`
	Kind    Kind    `json:"kind"`
	Trigger Trigger `json:"trigger"`
	Enabled bool    `json:"enabled"`

	Reminder *ReminderConf `json:"reminder,omitempty"`
	Exec     *ExecConf     `json:"exec,omitempty"`
	Notify   *NotifyConf   `json:"notify,omitempty"`
	Policy   *Policy       `json:"policy,omitempty"`

	// State 是运行时状态；与 Enabled 严格分开。
	State TaskState `json:"state"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`

	// NextFireAt 是派生字段：每次由 NextAfter 重算，**绝不落盘**。
	NextFireAt string `json:"nextFireAt,omitempty"`
	// SnoozeUntil 是 snooze 生效时的临时触发时刻，便于前端直接展示。
	SnoozeUntil string `json:"snoozeUntil,omitempty"`
}

// GlobalConfig 是插件的全局配置（与任务同文件，附录 §8.6）。
type GlobalConfig struct {
	// Enabled 是插件总开关；关闭时不调度、不通知。
	Enabled bool `json:"enabled"`
	// Paused 全局暂停（F7.3），暂停状态必须持久化。
	Paused bool `json:"paused"`
	// DefaultSnoozeMinutes 默认稍后提醒时长，默认 10。
	DefaultSnoozeMinutes int `json:"defaultSnoozeMinutes,omitempty"`
	// MissedPolicy 错过补偿默认策略：catchup | skip | defer。
	MissedPolicy string `json:"missedPolicy,omitempty"`
	// DND 免打扰时段（F7.1）。
	DND *DNDConfig `json:"dnd,omitempty"`
	// MaxConcurrency 执行型并发上限，恒为 1（串行，F5.6）。
	MaxConcurrency int `json:"maxConcurrency,omitempty"`
	// NotifyEnabled 是否允许弹系统通知；关闭后仍写历史。
	NotifyEnabled bool `json:"notifyEnabled"`
	// RateLimitPerTaskPerMinute 同一任务每分钟最多几条，默认 1（F7.4）。
	RateLimitPerTaskPerMinute int `json:"rateLimitPerTaskPerMinute,omitempty"`
	// RateLimitGlobalPer5Minutes 全局每 5 分钟最多几条，默认 3。
	RateLimitGlobalPer5Minutes int `json:"rateLimitGlobalPer5Minutes,omitempty"`
	// ForegroundOnlyInApp 前台活跃时只做应用内提醒（F7.5）。
	ForegroundOnlyInApp bool `json:"foregroundOnlyInApp,omitempty"`
	// KeepHistoryDays 历史保留天数，<=0 表示不清理（F6.4）。
	KeepHistoryDays int `json:"keepHistoryDays,omitempty"`
	// DeleteHistoryWithTask 删除任务时是否一并删除历史；默认 false（保留，F1.3）。
	DeleteHistoryWithTask bool `json:"deleteHistoryWithTask,omitempty"`
	// TaskSoftLimit 任务数软上限，超出只提示不禁止（默认 200）。
	TaskSoftLimit int `json:"taskSoftLimit,omitempty"`
	// UnhandledWindowMinutes 未处理窗口，默认 120（用于「错过」判定）。
	UnhandledWindowMinutes int `json:"unhandledWindowMinutes,omitempty"`
}

// DNDConfig 是免打扰时段配置。
type DNDConfig struct {
	Enabled bool `json:"enabled"`
	// Start / End 形如 "22:00" / "07:30"；跨零点时 Start > End 也成立。
	Start string `json:"start"`
	End   string `json:"end"`
	// Weekdays 生效的星期；为空表示每天。
	Weekdays []int `json:"weekdays,omitempty"`
}

// TaskFile 是 tasks.json 的顶层结构。
//
// Version 用于迁移（F9.4）；Unknown 兜住本版本不认识的顶层字段，
// 保存时原样写回 —— 避免「新版写了新字段、旧版一存就抹掉」。
type TaskFile struct {
	Version  int           `json:"version"`
	Settings *GlobalConfig `json:"settings,omitempty"`
	Tasks    []*Task       `json:"tasks"`

	// unknown 兜住本版本不认识的顶层字段，保存时原样写回。
	unknown map[string]json.RawMessage
}

// 默认值。
const (
	defaultSnoozeMinutes     = 10
	defaultMaxConcurrency    = 1
	defaultRatePerTaskPerMin = 1
	defaultRateGlobalPer5Min = 3
	defaultTaskSoftLimit     = 200
	defaultUnhandledWindow   = 120
	defaultMaxRetries        = 0
	defaultRetryBackoffMin   = 5
	defaultDisableAfterFail  = 3
	maxMissedPending         = 50
	maxTitleRunes            = 50
	maxNotifyTitleRunes      = 40
	maxNotifyBodyRunes       = 120
	minIntervalMinutes       = 1
)

// NewGlobalConfig 返回带默认值的全局配置。
func NewGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		Enabled:                    true,
		DefaultSnoozeMinutes:       defaultSnoozeMinutes,
		MissedPolicy:               MissedCatchUp,
		MaxConcurrency:             defaultMaxConcurrency,
		NotifyEnabled:              true,
		RateLimitPerTaskPerMinute:  defaultRatePerTaskPerMin,
		RateLimitGlobalPer5Minutes: defaultRateGlobalPer5Min,
		ForegroundOnlyInApp:        true,
		TaskSoftLimit:              defaultTaskSoftLimit,
		UnhandledWindowMinutes:     defaultUnhandledWindow,
	}
}

// applyDefaults 补齐缺省值（对从磁盘读入的配置做兜底）。
func (c *GlobalConfig) applyDefaults() {
	if c.DefaultSnoozeMinutes <= 0 {
		c.DefaultSnoozeMinutes = defaultSnoozeMinutes
	}
	if !validMissedPolicy(c.MissedPolicy) {
		c.MissedPolicy = MissedCatchUp
	}
	if c.MaxConcurrency <= 0 {
		// 安全工作包要求串行执行，这里把非法值强制拉回 1 而不是放宽。
		c.MaxConcurrency = defaultMaxConcurrency
	}
	if c.RateLimitPerTaskPerMinute <= 0 {
		c.RateLimitPerTaskPerMinute = defaultRatePerTaskPerMin
	}
	if c.RateLimitGlobalPer5Minutes <= 0 {
		c.RateLimitGlobalPer5Minutes = defaultRateGlobalPer5Min
	}
	if c.TaskSoftLimit <= 0 {
		c.TaskSoftLimit = defaultTaskSoftLimit
	}
	if c.UnhandledWindowMinutes <= 0 {
		c.UnhandledWindowMinutes = defaultUnhandledWindow
	}
}

func validMissedPolicy(p string) bool {
	switch p {
	case MissedCatchUp, MissedSkip, MissedDefer:
		return true
	}
	return false
}

// EffectivePolicy 返回任务实际生效的策略值（任务级覆盖 → 全局 → 内置默认）。
func (t *Task) EffectiveMissedPolicy(global *GlobalConfig) string {
	if t.Policy != nil && validMissedPolicy(t.Policy.MissedOverride) {
		return t.Policy.MissedOverride
	}
	if global != nil && validMissedPolicy(global.MissedPolicy) {
		return global.MissedPolicy
	}
	return MissedCatchUp
}

// EffectiveDND 返回任务实际生效的免打扰配置。
func (t *Task) EffectiveDND(global *GlobalConfig) *DNDConfig {
	if t.Policy != nil && t.Policy.DNDOverride != nil {
		return t.Policy.DNDOverride
	}
	if global != nil {
		return global.DND
	}
	return nil
}

// EffectiveSnoozeMinutes 返回任务实际生效的 snooze 时长。
func (t *Task) EffectiveSnoozeMinutes(global *GlobalConfig) int {
	if t.Reminder != nil && t.Reminder.SnoozeMinutes > 0 {
		return t.Reminder.SnoozeMinutes
	}
	if global != nil && global.DefaultSnoozeMinutes > 0 {
		return global.DefaultSnoozeMinutes
	}
	return defaultSnoozeMinutes
}

// DisableAfterFailures 返回熔断阈值（<=0 表示不熔断）。
func (t *Task) DisableAfterFailures() int {
	if t.Policy != nil && t.Policy.DisableAfterFailures > 0 {
		return t.Policy.DisableAfterFailures
	}
	return defaultDisableAfterFail
}

// Validate 校验任务定义。新建与编辑共用，**校验不通过绝不允许落盘**（F1.1）。
func (t *Task) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("任务缺少 id")
	}
	title := strings.TrimSpace(t.Title)
	if title == "" {
		return fmt.Errorf("任务名称不能为空")
	}
	if len([]rune(title)) > maxTitleRunes {
		return fmt.Errorf("任务名称过长（最多 %d 个字符）", maxTitleRunes)
	}
	switch t.Kind {
	case KindReminder:
		// 提醒型只需要时间规则（正文由 Note 提供，可为空）。
	case KindExec:
		if t.Exec == nil || strings.TrimSpace(t.Exec.Prompt) == "" {
			return fmt.Errorf("执行型任务必须填写 prompt")
		}
		if t.Exec.TimeoutSeconds < 0 {
			return fmt.Errorf("超时时间不能为负")
		}
	default:
		return fmt.Errorf("未知任务类型 %q（只支持 reminder / exec）", t.Kind)
	}
	if err := ValidateTrigger(t.Trigger); err != nil {
		return err
	}
	if err := validateDND(t.Policy); err != nil {
		return err
	}
	if err := validateNotify(t.Notify); err != nil {
		return err
	}
	return nil
}

func validateDND(p *Policy) error {
	if p == nil || p.DNDOverride == nil {
		return nil
	}
	return p.DNDOverride.Validate()
}

func validateNotify(n *NotifyConf) error {
	if n == nil {
		return nil
	}
	switch n.Level {
	case "", LevelRecord, LevelNormal, LevelAttention, LevelUrgent:
	default:
		return fmt.Errorf("未知通知级别 %q", n.Level)
	}
	return nil
}

// Validate 校验免打扰时段。
func (d *DNDConfig) Validate() error {
	if d == nil || !d.Enabled {
		return nil
	}
	if _, err := parseClock(d.Start); err != nil {
		return fmt.Errorf("免打扰开始时间非法: %w", err)
	}
	if _, err := parseClock(d.End); err != nil {
		return fmt.Errorf("免打扰结束时间非法: %w", err)
	}
	for _, w := range d.Weekdays {
		if w < 0 || w > 6 {
			return fmt.Errorf("免打扰星期取值非法: %d（应为 0-6）", w)
		}
	}
	return nil
}

// InDND 判断某时刻是否落在免打扰时段内（跨零点同样成立）。
func (d *DNDConfig) InDND(at time.Time) bool {
	if d == nil || !d.Enabled {
		return false
	}
	start, err := parseClock(d.Start)
	if err != nil {
		return false
	}
	end, err := parseClock(d.End)
	if err != nil {
		return false
	}
	if len(d.Weekdays) > 0 {
		wd := int(at.Weekday())
		hit := false
		for _, w := range d.Weekdays {
			if w == wd {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	local := at
	cur := local.Hour()*60 + local.Minute()
	if start == end {
		// 起止相同视为「全天免打扰」，比「永不生效」更符合直觉。
		return true
	}
	if start < end {
		return cur >= start && cur < end
	}
	// 跨零点：晚于 start 或早于 end 都算。
	return cur >= start || cur < end
}

// clone 返回任务的深拷贝，避免把内部指针交给调用方后又被外部改写。
func (t *Task) clone() *Task {
	if t == nil {
		return nil
	}
	cp := *t
	if t.Trigger.Once != nil {
		v := *t.Trigger.Once
		cp.Trigger.Once = &v
	}
	if t.Trigger.Interval != nil {
		v := *t.Trigger.Interval
		cp.Trigger.Interval = &v
	}
	if t.Trigger.Recurring != nil {
		v := *t.Trigger.Recurring
		v.Weekdays = append([]int(nil), t.Trigger.Recurring.Weekdays...)
		cp.Trigger.Recurring = &v
	}
	if t.Trigger.Cron != nil {
		v := *t.Trigger.Cron
		cp.Trigger.Cron = &v
	}
	if t.Reminder != nil {
		v := *t.Reminder
		cp.Reminder = &v
	}
	if t.Exec != nil {
		v := *t.Exec
		cp.Exec = &v
	}
	if t.Notify != nil {
		v := *t.Notify
		cp.Notify = &v
	}
	if t.Policy != nil {
		v := *t.Policy
		if t.Policy.DNDOverride != nil {
			d := *t.Policy.DNDOverride
			d.Weekdays = append([]int(nil), t.Policy.DNDOverride.Weekdays...)
			v.DNDOverride = &d
		}
		cp.Policy = &v
	}
	if t.State.Snooze != nil {
		v := *t.State.Snooze
		cp.State.Snooze = &v
	}
	cp.State.MissedPending = append([]MissedEntry(nil), t.State.MissedPending...)
	if t.State.Notified != nil {
		cp.State.Notified = make(map[string]string, len(t.State.Notified))
		for k, v := range t.State.Notified {
			cp.State.Notified[k] = v
		}
	}
	return &cp
}

// view 把 Task 包装成带派生字段的对外视图。
func (t *Task) view(next time.Time, hasNext bool) TaskView {
	cp := t.clone()
	v := TaskView{
		ID:        cp.ID,
		Title:     cp.Title,
		Note:      cp.Note,
		Kind:      cp.Kind,
		Trigger:   cp.Trigger,
		Enabled:   cp.Enabled,
		Reminder:  cp.Reminder,
		Exec:      cp.Exec,
		Notify:    cp.Notify,
		Policy:    cp.Policy,
		State:     cp.State,
		CreatedAt: cp.CreatedAt,
		UpdatedAt: cp.UpdatedAt,
	}
	if hasNext {
		v.NextFireAt = next.Format(time.RFC3339)
	}
	if t.State.Snooze != nil {
		v.SnoozeUntil = t.State.Snooze.Until
	}
	return v
}

// parseClock 把 "HH:MM" 解析成「当天的第几分钟」。
func parseClock(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("时间不能为空")
	}
	var hh, mm int
	if _, err := fmt.Sscanf(s, "%d:%d", &hh, &mm); err != nil {
		return 0, fmt.Errorf("%q 不是合法的 HH:MM", s)
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("%q 超出时间范围", s)
	}
	return hh*60 + mm, nil
}

// parseTime 解析 RFC3339；空串返回零值。
func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q 不是合法的 RFC3339 时间", s)
	}
	return t, nil
}

// formatTime 以本地时间写出 RFC3339；零值写成空串。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format(time.RFC3339)
}
