package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ===== 插件的对外契约（唯一的依赖面）=====
//
// 本文件只放「接口 + 数据传输对象」，不含任何逻辑。
//
// 依赖方向是单向的：main → plugin。plugin **绝不** import main，
// 因此插件需要的所有宿主能力（发通知、跑 agent、推事件、显窗口）都在这里声明，
// 由 main 侧实现（见根目录 taskhost.go）。
//
// 与《附录》定义的 Host 相比，这里多了 1 个方法 Emit：
// 附录按「插件与主程序同包」书写，事件可直接 EventsEmit；本实现落在 plugin 子包，
// 子包拿不到 wails 的 ctx，故把「推送 task:event」也提升为宿主能力。
// 这是本实现相对文档的唯一接口扩展，其余 11 个方法与文档一致。

// EventChannel 是插件对外唯一的事件通道（F10.2）。
// 独立通道，不与 chat:event / diff:update 混用。
const EventChannel = "task:event"

// 事件子类型。
//
// 前 7 个来自附录「通道 2」；EventReveal / EventPluginReady 为本实现新增：
//   - EventReveal：点击通知后需要前端「定位到某个任务」，原契约无对应子类型；
//   - EventPluginReady：插件加载完成的注册信号，主程序与前端据此确认插件已生效。
const (
	EventPluginReady    = "task:plugin:ready"
	EventReveal         = "task:reveal"
	EventChanged        = "task:changed"
	EventFired          = "task:fired"
	EventRunStarted     = "task:run:started"
	EventRunFinished    = "task:run:finished"
	EventMissed         = "task:missed"
	EventNotifyFallback = "task:notify:fallback"
	// EventNotifyInApp 是应用内提醒（F3.5）。附录未列该子类型：
	// 系统通知发不出去、或配置要求只做应用内提醒时，必须有一条通道把内容交给界面。
	EventNotifyInApp = "task:notify:inapp"
)

// 通知打断级别（对应附录 §7 的 L0–L3）。
type NotifyLevel string

const (
	// LevelRecord 仅记录，不打扰用户（L0）。
	LevelRecord NotifyLevel = "L0"
	// LevelNormal 普通提醒（L1）。
	LevelNormal NotifyLevel = "L1"
	// LevelAttention 需要关注，如错过补偿（L2）。
	LevelAttention NotifyLevel = "L2"
	// LevelUrgent 严重事件，如连续失败熔断、重试耗尽（L3）。
	LevelUrgent NotifyLevel = "L3"
)

var (
	// ErrNotImplemented 表示宿主尚未接入该能力（骨架阶段的占位）。
	ErrNotImplemented = errors.New("plugin: 宿主尚未接入该能力")
	// ErrNotRunning 表示插件尚未启动或已停止。
	ErrNotRunning = errors.New("plugin: 插件未运行")
)

// NotifyAction 是通知上的一个动作按钮（最多 3 个）。
//
// ID 的语义由插件定义（done / snooze10 / open / view / rerun / abort / ackAll），
// 回传时按幂等处理。
type NotifyAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// NotifyCategory 是一组动作按钮的集合，须在启动时一次性注册。
type NotifyCategory struct {
	ID               string         `json:"id"`
	Label            string         `json:"label"`
	Actions          []NotifyAction `json:"actions,omitempty"`
	HasReplyField    bool           `json:"hasReplyField,omitempty"`
	ReplyPlaceholder string         `json:"replyPlaceholder,omitempty"`
	ReplyButtonTitle string         `json:"replyButtonTitle,omitempty"`
}

// NotifyRequest 是一次通知请求。
//
// ID 约定为 "task:<taskId>:<triggerId>"，Data 携带 taskId/triggerId/kind，
// 供点击回传时定位任务。
type NotifyRequest struct {
	ID       string            `json:"id"`
	Category string            `json:"category,omitempty"`
	Title    string            `json:"title"`
	Body     string            `json:"body"`
	Level    NotifyLevel       `json:"level"`
	Actions  []NotifyAction    `json:"actions,omitempty"`
	Data     map[string]string `json:"data,omitempty"`
}

// NotifyResponse 是用户对通知的响应（点击按钮或回复）。
type NotifyResponse struct {
	ID       string            `json:"id"`
	ActionID string            `json:"actionID"`
	Category string            `json:"category,omitempty"`
	Text     string            `json:"text,omitempty"`
	Data     map[string]string `json:"data,omitempty"`
}

// RevealPayload 请求宿主显示并聚焦窗口，并定位到某个任务/运行。
type RevealPayload struct {
	TaskID string `json:"taskID,omitempty"`
	RunID  string `json:"runID,omitempty"`
	Pane   string `json:"pane,omitempty"`
}

// UnattendedPolicy 是无人值守权限策略（执行型任务专用）。
//
// 只允许预授权白名单内的操作；未允许的操作**降级为带理由的拒绝**，
// 被拒项须写入运行结果，绝不静默。
type UnattendedPolicy struct {
	Preauthorized []string `json:"preauthorized,omitempty"`
	// AskFallback 描述 ask 类请求的降级方式，固定为「带理由的拒绝」。
	AskFallback string `json:"askFallback,omitempty"`
	// MaxConcurrency 并发上限，M-A/M-B 阶段恒为 1（串行）。
	MaxConcurrency int `json:"maxConcurrency,omitempty"`
}

// RunRequest 请求宿主以独立会话跑一次 agent。
//
// SessionID 由插件生成并保持独立，绝不写入用户的会话。
type RunRequest struct {
	Prompt    string            `json:"prompt"`
	Model     string            `json:"model,omitempty"`
	WorkDir   string            `json:"workDir,omitempty"`
	SessionID string            `json:"sessionID"`
	Policy    UnattendedPolicy  `json:"policy,omitempty"`
	Timeout   time.Duration     `json:"timeout,omitempty"`
	TaskID    string            `json:"taskID,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// RunResult 是一次执行的最终结果。
type RunResult struct {
	RunID      string        `json:"runID"`
	SessionID  string        `json:"sessionID"`
	Status     string        `json:"status"` // succeeded | failed | aborted | timeout
	Summary    string        `json:"summary,omitempty"`
	OutputPath string        `json:"outputPath,omitempty"`
	Error      string        `json:"error,omitempty"`
	Duration   time.Duration `json:"duration"`
}

// RunEvent 是运行生命周期事件（F10.4）。
type RunEvent struct {
	Type      string    `json:"type"`
	RunID     string    `json:"runID"`
	SessionID string    `json:"sessionID"`
	TaskID    string    `json:"taskID,omitempty"`
	Message   string    `json:"message,omitempty"`
	At        time.Time `json:"at"`
}

// Event 是经 EventChannel 推送的统一信封。
type Event struct {
	Type    string    `json:"type"`
	Reason  string    `json:"reason,omitempty"`
	Payload any       `json:"payload,omitempty"`
	At      time.Time `json:"at"`
}

// Host 是插件对主程序的全部依赖（附录「通道 1」）。
//
// 由 main 侧实现，插件只依赖本接口 —— 这是插件与主程序之间唯一的耦合点。
type Host interface {
	// NotifyAvailable 报告本平台是否具备通知通道。
	//
	// 注意：Windows 上底层 IsNotificationAvailable 恒为 true，**不可**用它
	// 判断通知是否真的送达；可用性以 Notify 的返回值为准（F3.4）。
	NotifyAvailable() bool

	// Notify 发送一条系统通知。返回错误即视为发送失败，调用方据此降级为应用内提醒。
	Notify(req NotifyRequest) error

	// RegisterCategories 注册通知分类（动作按钮）。注册失败不得阻塞启动，但须记录。
	RegisterCategories(cats []NotifyCategory) error

	// OnNotifyResponse 注册通知响应的回调。
	OnNotifyResponse(fn func(NotifyResponse))

	// RevealWindow 显示并聚焦主窗口。
	RevealWindow(payload RevealPayload)

	// RunAgent 以独立会话跑一次 agent。
	RunAgent(ctx context.Context, req RunRequest) (RunResult, error)

	// CancelRun 按 runID 取消一次运行。
	CancelRun(runID string) error

	// SubscribeRuns 订阅运行生命周期事件。
	SubscribeRuns(fn func(RunEvent))

	// Emit 经 EventChannel 推送事件。
	Emit(name string, payload any)

	// BaseDir 返回本地数据目录（~/.local-agent）。
	BaseDir() string

	// AppVersion 返回主程序版本，供插件记录与兼容性判断。
	AppVersion() string
}

// Logger 是插件的最小日志接口，避免插件直接依赖具体日志实现。
type Logger interface {
	Printf(format string, args ...any)
}

// Options 是插件的初始化参数。
type Options struct {
	// Host 宿主能力，必填。
	Host Host
	// DataDir 数据目录，必填（主程序传 ~/.local-agent）。
	DataDir string
	// AppVersion 主程序版本，可选。
	AppVersion string
	// Clock 可注入时钟，测试用；为空则使用系统时钟。
	Clock Clock
	// Logger 日志，测试用；为空则写标准输出。
	Logger Logger
}

// Info 是插件的运行时概况，供主程序与前端查询（也是「插件已被识别」的证据）。
type Info struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	HostAPIVersion string   `json:"hostAPIVersion"`
	Kind           string   `json:"kind"`
	State          string   `json:"state"`
	DataDir        string   `json:"dataDir"`
	AppVersion     string   `json:"appVersion"`
	Capabilities   []string `json:"capabilities"`
	StartedAt      string   `json:"startedAt,omitempty"`
}

// String 便于日志打印。
func (i Info) String() string {
	return fmt.Sprintf("%s v%s (kind=%s, hostAPI=%s, state=%s, dataDir=%s)",
		i.ID, i.Version, i.Kind, i.HostAPIVersion, i.State, i.DataDir)
}
