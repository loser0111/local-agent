package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ===== 插件入口与生命周期 =====
//
// 加载流程：
//
//	LoadManifest()        解析 + 校验内嵌清单（身份 / 兼容性 / hostMethods 一致）
//	  → New(Options)      装配：绑定时钟与日志、校验宿主与数据目录（不做 IO）
//	  → Start(ctx)        加载数据 → 崩溃恢复 → 错过补偿 → 注册通知 → 启动调度循环 → 广播就绪
//
// 铁律：
//   - 状态机只由插件**单点驱动并持久化**，前端不得推断状态；
//   - 触发与通知路径必须 panic 隔离（异常只写历史，不掀翻进程）；
//   - 任一步失败都不得让主程序崩溃或退出。

// State 是插件自身的生命周期状态。
type State string

const (
	StateCreated State = "created"
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateFailed  State = "failed"
)

// Plugin 是桌面插件的实例。零值不可用；须经 New 构造、Start 启动。
type Plugin struct {
	opts     Options
	manifest Manifest
	logger   Logger
	clock    Clock

	store    *TaskStore
	history  *History
	sched    *Scheduler
	limiter  *RateLimiter
	fallback *FallbackNotice
	// watcher 监听数据文件的外部变更（外部写完 tasks.json 后免重启生效）。
	// 读写都持 p.mu：Start 里赋值，Stop 里取走并置 nil。
	watcher *fileWatcher

	mu        sync.RWMutex
	state     State
	lastErr   error
	startedAt time.Time
	// lastCheck 是上一次「错过扫描」的时刻，用于界定扫描窗口。
	lastCheck time.Time
}

// New 装配插件：解析清单、校验依赖、准备持久化组件。
//
// 本函数**不做**任何耗时 IO（不建目录、不读数据文件），以确保不阻塞窗口首帧；
// 真正的加载放在 Start 里。
func New(opts Options) (*Plugin, error) {
	if err := embedCheck(); err != nil {
		return nil, err
	}
	if opts.Host == nil {
		return nil, fmt.Errorf("插件初始化失败: 缺少宿主实现（Options.Host）")
	}
	if opts.DataDir == "" {
		return nil, fmt.Errorf("插件初始化失败: 缺少数据目录（Options.DataDir）")
	}
	manifest, err := LoadManifest()
	if err != nil {
		return nil, err
	}
	logger := opts.Logger
	if logger == nil {
		logger = printLogger{}
	}
	clock := opts.Clock
	if clock == nil {
		clock = defaultClock()
	}
	p := &Plugin{
		opts:     opts,
		manifest: manifest,
		logger:   logger,
		clock:    clock,
		state:    StateCreated,
		store:    NewTaskStore(opts.DataDir),
		history:  NewHistory(opts.DataDir),
		limiter:  NewRateLimiter(),
		fallback: NewFallbackNotice(),
	}
	p.logf("已加载清单 %s（%s）", manifestSource(), manifest)
	return p, nil
}

// Manifest 返回插件清单副本。
func (p *Plugin) Manifest() Manifest {
	m := p.manifest
	m.Capabilities = append([]string(nil), p.manifest.Capabilities...)
	m.DataFiles = append([]string(nil), p.manifest.DataFiles...)
	m.Emits = append([]string(nil), p.manifest.Emits...)
	m.HostMethods = append([]string(nil), p.manifest.HostMethods...)
	return m
}

// State 返回当前生命周期状态。
func (p *Plugin) State() State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

// Info 返回插件概况（身份 + 状态 + 数据目录），供主程序与前端查询。
func (p *Plugin) Info() Info {
	p.mu.RLock()
	state, startedAt := p.state, p.startedAt
	p.mu.RUnlock()

	info := Info{
		ID:             p.manifest.ID,
		Name:           p.manifest.Name,
		Version:        p.manifest.Version,
		HostAPIVersion: p.manifest.HostAPIVersion,
		Kind:           p.manifest.Kind,
		State:          string(state),
		DataDir:        p.opts.DataDir,
		AppVersion:     p.opts.AppVersion,
		Capabilities:   append([]string(nil), p.manifest.Capabilities...),
	}
	if !startedAt.IsZero() {
		info.StartedAt = startedAt.Format(time.RFC3339)
	}
	return info
}

// Start 启动插件：加载数据 → 崩溃恢复 → 错过补偿 → 注册通知 → 启动调度 → 广播就绪。
//
// 任一步失败都不得导致主程序崩溃或退出：能降级的降级，能记录的记录。
func (p *Plugin) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	if p.state == StateRunning {
		p.mu.Unlock()
		return nil
	}
	p.state = StateRunning
	p.startedAt = p.clock.Now()
	p.lastCheck = p.startedAt
	p.mu.Unlock()

	now := p.clock.Now()

	// 1) 加载任务数据。文件损坏已由 store 恢复成空集，这里只记录、不阻断。
	if err := p.store.Load(); err != nil {
		if errors.Is(err, ErrCorrupted) {
			p.logf("任务数据已损坏并重建: %v", err)
			p.emitNotifyFallback("data-corrupted", err.Error())
		} else {
			p.setErr(err)
			p.logf("加载任务数据失败（插件将以空数据继续）: %v", err)
			p.emitNotifyFallback("data-load-failed", err.Error())
		}
	}

	// 2) 崩溃恢复：上次被硬杀时残留的 running 记录改为 interrupted。
	if fixed, err := p.history.RecoverRunning(); err != nil {
		p.logf("历史崩溃恢复失败: %v", err)
	} else if fixed > 0 {
		p.logf("崩溃恢复：%d 条残留的执行记录已标记为中断", fixed)
	}

	// 3) 历史清理（按配置的保留天数）。
	_, settings, _ := p.store.snapshot()
	if removed, err := p.history.Prune(settings.KeepHistoryDays); err != nil {
		p.logf("历史清理失败: %v", err)
	} else if removed > 0 {
		p.logf("历史清理：移除 %d 条过期记录", removed)
	}

	// 4) 错过补偿：应用不在的那段时间里本应触发的提醒，按策略处理。
	//    与后续调度走同一条代码路径（从上次触发时刻重新推导）。
	p.processDue(settings, now)

	// 5) 注册通知分类与响应回调（F3.2）。注册失败不阻塞启动，但必须可见。
	if err := p.opts.Host.RegisterCategories(defaultCategories()); err != nil {
		p.logf("通知分类注册失败（将以无按钮通知降级）: %v", err)
		p.emitNotifyFallback("category-register-failed", err.Error())
	}
	p.opts.Host.OnNotifyResponse(p.handleNotifyResponse)

	// 6) 订阅运行生命周期事件。
	p.opts.Host.SubscribeRuns(p.handleRunEvent)

	// 7) 启动调度循环。
	//
	// 赋值必须在锁内：调度循环起来之后，界面线程可能立刻创建任务并调用 wake()
	// 读取 p.sched，此刻的无锁写入就是一个真实的数据竞争。
	sched := NewScheduler(p.clock, p.nearest, p.tick, p.logf)
	p.mu.Lock()
	p.sched = sched
	p.mu.Unlock()
	sched.Start()

	// 8) 监听数据文件的外部变更：外部（脚本 / 编辑器）写完 tasks.json 后免重启生效。
	//    监听起不来只记日志降级 —— 能力退回「重启才生效」，插件照常可用。
	watcher := newFileWatcher(p.store.dir, taskFileName, watchDebounce, p.onTasksFileChanged, p.logf)
	p.mu.Lock()
	p.watcher = watcher
	p.mu.Unlock()
	watcher.Start()

	// 9) 广播就绪。
	p.emit(EventPluginReady, "", p.Info())
	p.logf("已启动: %s", p.Info().String())
	return nil
}

// Stop 停止插件：停掉调度循环、刷盘、收尾。
func (p *Plugin) Stop() error {
	p.mu.Lock()
	if p.state == StateStopped {
		p.mu.Unlock()
		return nil
	}
	p.state = StateStopped
	sched := p.sched
	watcher := p.watcher
	p.watcher = nil
	p.mu.Unlock()

	// 先停监听、再落盘：顺序反了的话，下面这次 Save 自己会触发一次文件事件，
	// 让一个正在关闭的插件再走一遍「外部变更 → 重新加载」。
	if watcher != nil {
		watcher.Stop()
	}
	if sched != nil {
		sched.Stop()
	}
	// 退出前把内存里的状态落盘 —— 否则刚发生的触发/失败会丢。
	if err := p.store.Save(); err != nil {
		p.logf("退出前保存失败: %v", err)
	}
	p.logf("已停止")
	return nil
}

// watchDebounce 是外部文件变更的防抖窗口。
//
// 一次原子写在文件系统层面会冒出多个事件（临时文件 Create/Write + 目标文件
// Rename），200ms 足够把它们合并成一次加载，又短到用户感觉不出延迟。
const watchDebounce = 200 * time.Millisecond

// onTasksFileChanged 处理 tasks.json 的外部变更（脚本 / 编辑器直接改文件）。
//
// 语义是「磁盘为准」：能解析成功就整份替换内存中的任务表，唤醒调度器重算下次
// 触发时刻，并广播变更让前端刷新。解析失败（多半是写入方还没写完）只记一条日志，
// 等下一次事件 —— 绝不动内存，也绝不把文件归档成 .broken（那是 Load 的启动语义）。
func (p *Plugin) onTasksFileChanged() {
	changed, err := p.store.ReloadIfChanged()
	if err != nil {
		p.logf("外部修改 tasks.json 未生效: %v", err)
		return
	}
	if !changed {
		return
	}
	p.logf("检测到 tasks.json 被外部修改，已重新加载并重算调度")
	p.wake()
	p.emit(EventChanged, "reloaded", nil)
}

func (p *Plugin) setErr(err error) {
	p.mu.Lock()
	p.lastErr = err
	p.mu.Unlock()
}

// handleNotifyResponse 处理用户对通知的响应（点击按钮 / 回复文本）。
//
// 信任边界：回传的动作与 taskId 不完全可信，因此所有动作都走「查得到才执行」，
// 并且**必须幂等** —— 重复点击「完成」不应产生副作用。
func (p *Plugin) handleNotifyResponse(resp NotifyResponse) {
	p.logf("收到通知响应: id=%s action=%s category=%s text=%q", resp.ID, resp.ActionID, resp.Category, resp.Text)

	taskID := resp.Data["taskId"]
	if taskID == "" {
		taskID = taskIDFromNotifyID(resp.ID)
	}

	switch resp.ActionID {
	case "done":
		if err := p.CompleteTask(taskID); err != nil {
			p.logf("「完成」处理失败: %v", err)
		}
	case "snooze", "snooze10":
		if err := p.SnoozeTask(taskID, 0); err != nil {
			p.logf("「稍后提醒」处理失败: %v", err)
		}
	case "open", "view":
		p.opts.Host.RevealWindow(RevealPayload{TaskID: taskID, Pane: "tasks"})
	case "rerun":
		// 执行型重新执行属于后续里程碑；此处只定位，不静默失败。
		p.opts.Host.RevealWindow(RevealPayload{TaskID: taskID, Pane: "tasks"})
	case "ackAll":
		p.emit(EventChanged, "ackAll", nil)
	default:
		p.logf("未知通知动作 %q（已忽略）", resp.ActionID)
	}
	p.emit(EventChanged, "notify:"+resp.ActionID, resp)
}

// taskIDFromNotifyID 从 "task:<taskId>:<triggerId>" 里取 taskId。
func taskIDFromNotifyID(id string) string {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) >= 3 && parts[0] == "task" {
		return parts[1]
	}
	return ""
}

// handleRunEvent 处理运行生命周期事件（F10.4）。
func (p *Plugin) handleRunEvent(ev RunEvent) {
	if ev.TaskID != "" {
		p.recordRunEvent(ev)
	}
	p.emit(runSubtype(ev.Type), "", ev)
}

// runSubtype 把宿主的运行事件类型映射到 task:event 子类型。
func runSubtype(hostType string) string {
	switch hostType {
	case "started", EventRunStarted:
		return EventRunStarted
	case "finished", "succeeded", "failed", "aborted", "timeout", EventRunFinished:
		return EventRunFinished
	default:
		return EventRunFinished
	}
}

// emit 经统一通道推送事件。
func (p *Plugin) emit(subtype, reason string, payload any) {
	p.opts.Host.Emit(EventChannel, Event{
		Type:    subtype,
		Reason:  reason,
		Payload: payload,
		At:      p.clock.Now(),
	})
}

// emitNotifyFallback 广播通知降级/系统事件。
func (p *Plugin) emitNotifyFallback(reason, detail string) {
	p.emit(EventNotifyFallback, reason, map[string]string{"reason": reason, "detail": detail})
	// 降级原因每个会话只提示一次：降级本身不该变成新的打扰源（F3.4）。
	if p.fallback.Once("session") {
		p.logf("通知降级（仅提示一次）: %s - %s", reason, detail)
	}
}

// logf 统一日志前缀，便于在混杂的主程序日志里定位插件。
func (p *Plugin) logf(format string, args ...any) {
	p.logger.Printf("[desktop-plugin] "+format, args...)
}

// printLogger 是 Logger 的默认实现。
type printLogger struct{}

func (printLogger) Printf(format string, args ...any) { fmt.Printf(format+"\n", args...) }

// defaultCategories 返回启动时一次性注册的通知分类（附录 §7）。
func defaultCategories() []NotifyCategory {
	return []NotifyCategory{
		{
			ID:    "reminder.basic",
			Label: "定时提醒",
			Actions: []NotifyAction{
				{ID: "done", Label: "完成"},
				{ID: "snooze", Label: "稍后提醒"},
				{ID: "open", Label: "打开 agent"},
			},
		},
		{
			ID:    "exec.result",
			Label: "任务结果",
			Actions: []NotifyAction{
				{ID: "view", Label: "查看结果"},
				{ID: "rerun", Label: "重新执行"},
				{ID: "open", Label: "打开 agent"},
			},
		},
		{
			ID:    "exec.abort",
			Label: "需要处理",
			Actions: []NotifyAction{
				{ID: "view", Label: "查看详情"},
				{ID: "open", Label: "打开 agent"},
			},
		},
	}
}
