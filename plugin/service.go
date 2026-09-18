package plugin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ===== 任务服务：CRUD + 触发流水线 =====
//
// 本文件是插件的业务核心。所有对外操作都遵循同一条流水线：
//
//	校验 → 落盘（原子写）→ 重算下次触发 → 唤醒调度循环 → 广播 task:changed
//
// 「重算下次触发」不是可选项：编辑规则后若沿用旧定时器，就会出现
// 「改了时间但还是按老时间弹」这类最难排查的问题（F1.2）。

// maxFiresForSnooze 是单任务的 snooze 次数上限，防止用户连点把提醒无限推迟。
const maxFiresForSnooze = 50

// TaskInput 是新建/编辑任务的输入。
type TaskInput struct {
	Title    string        `json:"title"`
	Note     string        `json:"note,omitempty"`
	Kind     Kind          `json:"kind"`
	Trigger  Trigger       `json:"trigger"`
	Enabled  *bool         `json:"enabled,omitempty"`
	Reminder *ReminderConf `json:"reminder,omitempty"`
	Exec     *ExecConf     `json:"exec,omitempty"`
	Notify   *NotifyConf   `json:"notify,omitempty"`
	Policy   *Policy       `json:"policy,omitempty"`
}

// ===== 查询 =====

// ListTasks 返回全部任务视图（含派生的 nextFireAt），按下次触发时间升序。
func (p *Plugin) ListTasks() []TaskView {
	tasks, settings, _ := p.store.snapshot()
	now := p.clock.Now()
	views := make([]TaskView, 0, len(tasks))
	for _, t := range tasks {
		next, ok := p.computeNext(t, settings, now)
		views = append(views, t.view(next, ok))
	}
	sortViews(views)
	return views
}

// GetTask 返回单个任务视图。
func (p *Plugin) GetTask(id string) (TaskView, error) {
	tasks, settings, _ := p.store.snapshot()
	for _, t := range tasks {
		if t.ID == id {
			next, ok := p.computeNext(t, settings, p.clock.Now())
			return t.view(next, ok), nil
		}
	}
	return TaskView{}, fmt.Errorf("任务不存在: %s", id)
}

// Settings 返回全局配置副本。
func (p *Plugin) Settings() *GlobalConfig {
	_, settings, _ := p.store.snapshot()
	return settings
}

// UpdateSettings 更新全局配置并落盘。改动即时生效（F9.3），无需重启。
func (p *Plugin) UpdateSettings(cfg *GlobalConfig) error {
	if cfg == nil {
		return fmt.Errorf("全局配置为空")
	}
	if err := cfg.DND.Validate(); err != nil {
		return err
	}
	cfg.applyDefaults()
	if err := p.store.mutate(func(f *TaskFile) error {
		f.Settings = cfg
		return nil
	}); err != nil {
		return err
	}
	p.wake()
	p.emit(EventChanged, "settings", cfg)
	return nil
}

// PauseAll 设置/解除全局暂停（F7.3）。暂停状态持久化。
func (p *Plugin) PauseAll(paused bool) error {
	if err := p.store.mutate(func(f *TaskFile) error {
		if f.Settings == nil {
			f.Settings = NewGlobalConfig()
		}
		f.Settings.Paused = paused
		return nil
	}); err != nil {
		return err
	}
	p.wake()
	p.emit(EventChanged, "pause", map[string]bool{"paused": paused})
	return nil
}

// TaskCount 返回任务数与软上限，供界面提示「任务太多」。
func (p *Plugin) TaskCount() (int, int) {
	tasks, settings, _ := p.store.snapshot()
	return len(tasks), settings.TaskSoftLimit
}

// ===== 新建 / 编辑 / 删除 =====

// CreateTask 新建任务：校验通过才落盘（F1.1）。
func (p *Plugin) CreateTask(in TaskInput) (TaskView, error) {
	now := p.clock.Now()
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	t := &Task{
		ID:       newTaskID(),
		Title:    strings.TrimSpace(in.Title),
		Note:     in.Note,
		Kind:     in.Kind,
		Trigger:  in.Trigger,
		Enabled:  enabled,
		Reminder: in.Reminder,
		Exec:     in.Exec,
		Notify:   in.Notify,
		Policy:   in.Policy,
	}
	t.CreatedAt = formatTime(now)
	t.UpdatedAt = t.CreatedAt
	applyTaskDefaults(t, now)

	if err := t.Validate(); err != nil {
		return TaskView{}, err
	}
	if err := p.validateTemplates(t); err != nil {
		return TaskView{}, err
	}

	if err := p.store.mutate(func(f *TaskFile) error {
		f.Tasks = append(f.Tasks, t)
		return nil
	}); err != nil {
		return TaskView{}, err
	}
	p.afterMutation(t.ID, "created", nil)
	return p.GetTask(t.ID)
}

// UpdateTask 编辑任务。编辑后**必须重算下次触发**（F1.2）。
func (p *Plugin) UpdateTask(id string, in TaskInput) (TaskView, error) {
	now := p.clock.Now()
	var updated *Task
	if err := p.store.mutate(func(f *TaskFile) error {
		old := f.get(id)
		if old == nil {
			return fmt.Errorf("任务不存在: %s", id)
		}
		cp := old.clone()
		cp.Title = strings.TrimSpace(in.Title)
		cp.Note = in.Note
		cp.Kind = in.Kind
		cp.Trigger = in.Trigger
		cp.Reminder = in.Reminder
		cp.Exec = in.Exec
		cp.Notify = in.Notify
		cp.Policy = in.Policy
		if in.Enabled != nil {
			cp.Enabled = *in.Enabled
		}
		cp.UpdatedAt = formatTime(now)
		applyTaskDefaults(cp, now)
		if err := cp.Validate(); err != nil {
			return err
		}
		if err := p.validateTemplates(cp); err != nil {
			return err
		}
		// 规则变了：原先的「已完成」与 snooze 都不再适用，清掉以免误导。
		cp.State.Finished = false
		cp.State.Snooze = nil
		replaceTask(f, cp)
		updated = cp
		return nil
	}); err != nil {
		return TaskView{}, err
	}
	p.afterMutation(id, "updated", nil)
	return p.GetTask(updated.ID)
}

// DeleteTask 删除任务。
//
// deleteHistory 默认 false：历史是排查问题的依据，不该因为删任务就消失（F1.3）。
// 二次确认由界面负责（界面必须说明历史是否一并删除）。
func (p *Plugin) DeleteTask(id string, deleteHistory bool) error {
	var removed bool
	if err := p.store.mutate(func(f *TaskFile) error {
		removed = f.remove(id)
		return nil
	}); err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("任务不存在: %s", id)
	}
	if deleteHistory {
		if err := p.history.DeleteByTask(id); err != nil {
			p.logf("删除任务历史失败: %v", err)
		}
	}
	p.afterMutation(id, "deleted", nil)
	return nil
}

// SetTaskEnabled 启用/停用任务（F1.4）。停用后立即不再参与调度。
func (p *Plugin) SetTaskEnabled(id string, enabled bool) (TaskView, error) {
	if err := p.store.mutate(func(f *TaskFile) error {
		t := f.get(id)
		if t == nil {
			return fmt.Errorf("任务不存在: %s", id)
		}
		t.Enabled = enabled
		t.UpdatedAt = formatTime(p.clock.Now())
		if enabled {
			// 重新启用：清掉「已终结」标记，让它重新参与调度。
			t.State.Finished = false
		}
		return nil
	}); err != nil {
		return TaskView{}, err
	}
	p.afterMutation(id, "enabled", map[string]bool{"enabled": enabled})
	return p.GetTask(id)
}

// RunTaskNow 立即触发一次（手动，不计入规则的正常排期）。
func (p *Plugin) RunTaskNow(id string) (RunRecord, error) {
	tasks, settings, _ := p.store.snapshot()
	var target *Task
	for _, t := range tasks {
		if t.ID == id {
			target = t
			break
		}
	}
	if target == nil {
		return RunRecord{}, fmt.Errorf("任务不存在: %s", id)
	}
	return p.fire(target, settings, p.clock.Now(), TriggerSourceManual)
}

// SnoozeTask 稍后提醒（F2.6）。
//
// 关键点：**不改原规则**，只生成一个临时触发时刻；到时按原来的规则继续走。
func (p *Plugin) SnoozeTask(id string, minutes int) error {
	now := p.clock.Now()
	if err := p.store.mutate(func(f *TaskFile) error {
		t := f.get(id)
		if t == nil {
			return fmt.Errorf("任务不存在: %s", id)
		}
		settings := f.Settings
		if settings == nil {
			settings = NewGlobalConfig()
		}
		if minutes <= 0 {
			minutes = t.EffectiveSnoozeMinutes(settings)
		}
		count := 0
		if t.State.Snooze != nil {
			count = t.State.Snooze.Count
		}
		if count >= maxFiresForSnooze {
			return fmt.Errorf("该任务已连续推迟 %d 次，请先处理它", count)
		}
		original := t.State.LastFiredAt
		t.State.Snooze = &SnoozeState{
			Until:    formatTime(now.Add(time.Duration(minutes) * time.Minute)),
			Original: original,
			Count:    count + 1,
		}
		t.State.LastResult = ResultSkipped
		t.UpdatedAt = formatTime(now)
		return nil
	}); err != nil {
		return err
	}
	p.wake()
	p.emit(EventChanged, "snoozed", map[string]any{"taskID": id, "minutes": minutes})
	return nil
}

// CompleteTask 标记任务已处理（「完成」动作，必须幂等）。
func (p *Plugin) CompleteTask(id string) error {
	if id == "" {
		return fmt.Errorf("缺少任务 ID")
	}
	err := p.store.mutate(func(f *TaskFile) error {
		t := f.get(id)
		if t == nil {
			return fmt.Errorf("任务不存在: %s", id)
		}
		t.State.Snooze = nil
		if t.State.LastResult == ResultNotified || t.State.LastResult == "" {
			t.State.LastResult = ResultSucceeded
		}
		// 一次性任务被确认后即终结。
		if t.Trigger.Kind == TriggerOnce {
			t.State.Finished = true
		}
		t.UpdatedAt = formatTime(p.clock.Now())
		return nil
	})
	if err != nil {
		return err
	}
	p.wake()
	p.emit(EventChanged, "completed", map[string]string{"taskID": id})
	return nil
}

// ListRuns 返回任务历史（最新在前）。
func (p *Plugin) ListRuns(taskID string, limit int) ([]RunRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	return p.history.ListByTask(taskID, limit)
}

// PreviewTriggerTimes 返回未来 n 次触发时刻的字符串形式（F2.8：保存前必须能预览）。
func (p *Plugin) PreviewTriggerTimes(tr Trigger, n int) ([]string, error) {
	if err := ValidateTrigger(tr); err != nil {
		return nil, err
	}
	if n <= 0 {
		n = 3
	}
	times := Preview(tr, p.clock.Now(), n)
	out := make([]string, 0, len(times))
	for _, t := range times {
		out = append(out, t.Format(time.RFC3339))
	}
	return out, nil
}

// ===== 调度支撑 =====

// nearest 返回全部任务里最近的下次触发时刻（调度循环的唯一查询入口）。
//
// 除「未来的下次」以外，还必须报告**已经积压的待处理触发**：
// NextAfter 只看未来，若某任务的触发时刻已经过去（刚睡醒、时钟往前跳、
// 关着应用错过了），光看 NextAfter 会得到「没有下一次」，调度器于是永远不再
// 处理它。因此这里先用 firstPending 兜一层，有积压就把「现在」作为触发时刻。
func (p *Plugin) nearest(now time.Time) (time.Time, bool) {
	tasks, settings, _ := p.store.snapshot()
	if settings != nil && (!settings.Enabled || settings.Paused) {
		// 全局关闭/暂停：不安排任何触发；恢复时由 processDue 的窗口补齐（不丢提醒）。
		return time.Time{}, false
	}
	var best time.Time
	found := false
	for _, t := range tasks {
		if _, ok := firstPending(t, settings, now); ok {
			// 有积压：立刻处理，不再睡。
			return now, true
		}
		next, ok := p.computeNext(t, settings, now)
		if !ok {
			continue
		}
		if !found || next.Before(best) {
			best = next
			found = true
		}
	}
	return best, found
}

// firstPending 返回任务当前**最早待处理**的触发时刻。
//
// 返回 ok=true 且时刻 <= now 表示「有积压，需要马上处理」。
// 实现上是「从上次触发时刻（或创建时刻）出发算第一次触发」，
// 因此它天然覆盖了「刚睡醒」「时钟往前跳」「关着应用错过了」三种情况。
func firstPending(t *Task, settings *GlobalConfig, now time.Time) (time.Time, bool) {
	if t == nil || !t.Enabled || t.State.Finished {
		return time.Time{}, false
	}
	if settings != nil && (!settings.Enabled || settings.Paused) {
		return time.Time{}, false
	}
	// snooze 到期优先：它是一次临时触发，早于规则的下一次。
	if t.State.Snooze != nil {
		if until, err := parseTime(t.State.Snooze.Until); err == nil && !until.IsZero() && !until.After(now) {
			return until.Local(), true
		}
	}
	lastFired, _ := parseTime(t.State.LastFiredAt)
	start := lastFired
	if start.IsZero() {
		if created, err := parseTime(t.CreatedAt); err == nil {
			start = created
		}
	}
	if start.IsZero() || start.After(now) {
		return time.Time{}, false
	}
	next, ok := NextAfter(t.Trigger, start, lastFired, t.State.FiredCount)
	if !ok || next.After(now) {
		return time.Time{}, false
	}
	return next, true
}

// computeNext 计算单个任务的下次触发时刻。
//
// 两条来源取更早者：
//  1. 规则推导的 nextFireAt（**不落盘**，每次都重算）；
//  2. snooze 产生的临时触发时刻。
func (p *Plugin) computeNext(t *Task, settings *GlobalConfig, now time.Time) (time.Time, bool) {
	if t == nil || !t.Enabled || t.State.Finished {
		return time.Time{}, false
	}
	if settings != nil && !settings.Enabled {
		return time.Time{}, false
	}
	var best time.Time
	found := false

	if t.State.Snooze != nil {
		if until, err := parseTime(t.State.Snooze.Until); err == nil && !until.IsZero() {
			best = until.Local()
			found = true
		}
	}

	lastFired, _ := parseTime(t.State.LastFiredAt)
	if next, ok := NextAfter(t.Trigger, now, lastFired, t.State.FiredCount); ok {
		if !found || next.Before(best) {
			best = next
			found = true
		}
	}
	return best, found
}

// tick 是调度循环的到点回调：把「所有待处理的任务」触发掉。
//
// 每次 tick 都从「上次触发时刻」重新推导全部待处理时刻，因此
// 休眠唤醒、系统时间跳变、关着应用错过，三种情况走的是同一条代码路径 ——
// 这比给每种情况各写一套补偿逻辑要可靠得多。
func (p *Plugin) tick(now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			p.logf("触发流水线 panic 已隔离: %v", r)
		}
	}()

	settings := p.Settings()
	if settings != nil && (!settings.Enabled || settings.Paused) {
		p.markChecked(now)
		return
	}
	p.processDue(settings, now)
	p.markChecked(now)
}

// markChecked 记录本次检查时刻，用于区分「刚刚到点」与「早就该触发」。
func (p *Plugin) markChecked(now time.Time) {
	p.mu.Lock()
	p.lastCheck = now
	p.mu.Unlock()
}

// processDue 处理所有待触发的任务。
//
// 对每个任务：
//  1. 从上次触发时刻（或创建时刻）到 now，列出全部本应触发的时刻；
//  2. 其中「早于上次检查」的属于**错过**（应用没在跑），按策略补偿；
//  3. 「晚于上次检查」的属于**正常到点**，逐个触发。
func (p *Plugin) processDue(settings *GlobalConfig, now time.Time) {
	tasks, _, _ := p.store.snapshot()

	p.mu.RLock()
	lastCheck := p.lastCheck
	p.mu.RUnlock()

	for _, t := range tasks {
		if !t.Enabled || t.State.Finished {
			continue
		}
		lastFired, _ := parseTime(t.State.LastFiredAt)
		start := lastFired
		if start.IsZero() {
			if created, err := parseTime(t.CreatedAt); err == nil {
				start = created
			}
		}
		if start.IsZero() || !now.After(start) {
			continue
		}

		pending := MissedBetween(t.Trigger, start, now, lastFired, t.State.FiredCount, maxMissedPending)
		var missed, due []time.Time
		for _, at := range pending {
			if lastCheck.IsZero() || at.After(lastCheck) {
				due = append(due, at)
				continue
			}
			missed = append(missed, at)
		}
		// 一次唤醒只「正常触发」最近的那一个，更早的一律走错过补偿。
		//
		// 这条护栏很关键：机器休眠 6 小时、而任务间隔是 1 分钟时，
		// 若把 360 个积压时刻全部当作正常触发，就会瞬间触发 360 次 ——
		// 通知炸弹、历史被灌满。折进「错过」后只按策略补发一次。
		if len(due) > 1 {
			missed = append(missed, due[:len(due)-1]...)
			due = due[len(due)-1:]
		}

		if len(missed) > 0 {
			p.handleMissed(t, settings, missed, now)
			// 错过处理已推进过状态，重新取一份，避免用旧状态继续判断。
			if fresh := p.findTask(t.ID); fresh != nil {
				t = fresh
			} else {
				continue
			}
		}

		for _, at := range due {
			if !t.Enabled || t.State.Finished {
				break
			}
			source := TriggerSourceScheduled
			if t.State.Snooze != nil {
				if until, err := parseTime(t.State.Snooze.Until); err == nil && !until.After(now) {
					source = TriggerSourceSnooze
				}
			}
			// 每个任务单独隔离：一个任务出错不连累同一轮里的其他任务。
			p.fireSafely(t, settings, at, source)
			if fresh := p.findTask(t.ID); fresh != nil {
				t = fresh
			}
		}
	}
}

// findTask 取任务快照（用于在状态推进后刷新本地副本）。
func (p *Plugin) findTask(id string) *Task {
	tasks, _, _ := p.store.snapshot()
	for _, t := range tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// fireSafely 包一层 panic 隔离（F8.1）。
func (p *Plugin) fireSafely(t *Task, settings *GlobalConfig, at time.Time, source string) {
	defer func() {
		if r := recover(); r != nil {
			p.logf("任务 %s 触发时 panic 已隔离: %v", t.ID, r)
			p.recordPanic(t, at, fmt.Sprintf("panic: %v", r))
		}
	}()
	if _, err := p.fire(t, settings, at, source); err != nil {
		p.logf("任务 %s 触发失败: %v", t.ID, err)
	}
}

// fire 执行一次触发：写历史 → 决策 → 投递通知 → 推进状态。
func (p *Plugin) fire(t *Task, settings *GlobalConfig, at time.Time, source string) (RunRecord, error) {
	now := p.clock.Now()
	rec := RunRecord{
		RunID:       newRunID(),
		TaskID:      t.ID,
		TaskTitle:   t.Title,
		Kind:        t.Kind,
		Source:      source,
		TriggeredAt: formatTime(now),
		ScheduledAt: formatTime(at),
		Status:      ResultNotified,
	}

	// 执行型任务尚未接入（属后续里程碑）。这里明确记录而不是假装成功 ——
	// 静默的「看似成功」比明确的「未接入」危险得多。
	if t.Kind == KindExec {
		rec.Status = ResultSkipped
		rec.Error = "执行型任务将在后续里程碑接入，本次未执行"
		_, _ = p.history.Append(rec)
		p.finishFire(t, now, ResultSkipped, rec.Error, rec.RunID)
		p.emit(EventFired, "exec-not-implemented", rec)
		return rec, nil
	}

	// 1) 通知决策（决策树）。
	key := Dedupe{}.Key(t.ID, at)
	in := DecisionInput{
		Task:          t,
		Settings:      settings,
		Now:           now,
		DND:           t.EffectiveDND(settings),
		NotifyEnabled: settings == nil || settings.NotifyEnabled,
	}
	if (Dedupe{}).Hit(t, key) {
		in.DedupeHit = true
	} else if settings != nil {
		allowed, reason := p.limiter.Allow(t.ID, now, settings.RateLimitPerTaskPerMinute, settings.RateLimitGlobalPer5Minutes)
		if !allowed {
			in.RateLimited = true
			in.RateLimitReason = reason
		}
	}
	decision := Decide(in)

	// 2) 渲染通知。
	req := RenderNotification(TemplateReminder, t, at, "")
	req.Level = decision.Level

	// 3) 投递。
	fallback, err := p.deliver(t, req, decision, at, now)
	if err != nil {
		rec.Error = err.Error()
	}
	rec.NotifyFallback = fallback

	// 4) 写历史 + 推进状态。
	if _, err := p.history.Append(rec); err != nil {
		p.logf("写历史失败: %v", err)
	}
	p.finishFire(t, now, ResultNotified, "", rec.RunID)
	p.emit(EventFired, string(decision.Delivery), rec)
	return rec, nil
}

// deliver 按决策投递通知，返回降级原因（非空表示降级为应用内提醒）。
//
// 可用性以**发送结果**为准：Windows 上 IsNotificationAvailable 恒为 true，
// 用它判断只会得到一个永远乐观的答案（F3.4）。
//
// at 是这次通知对应的**计划触发时刻**：去重台账用它做键，
// 这样「同一周期只弹一次」在重启后依然成立（键是可重算的确定值，不是内存序号）。
func (p *Plugin) deliver(t *Task, req NotifyRequest, decision Decision, at, now time.Time) (string, error) {
	switch decision.Delivery {
	case DeliveryDrop:
		return decision.Reason, nil
	case DeliveryRecord:
		return "", nil
	case DeliveryInApp:
		p.emit(EventNotifyInApp, decision.Reason, req)
		return decision.Reason, nil
	case DeliveryToast:
		if err := p.opts.Host.Notify(req); err != nil {
			// 被动降级：发送失败才发现通知不可用。
			reason := fmt.Sprintf("系统通知发送失败（%v），已改为应用内提醒", err)
			p.emitNotifyFallback("notify-send-failed", reason)
			p.emit(EventNotifyInApp, reason, req)
			return reason, nil
		}
		// 发送成功才记台账与限流：失败的不该占用配额，否则降级期间会把额度耗光。
		Dedupe{}.Mark(t, Dedupe{}.Key(t.ID, at), now)
		if settings := p.Settings(); settings != nil {
			p.limiter.Record(t.ID, now)
		}
		return "", nil
	}
	return "", nil
}

// finishFire 推进任务状态并落盘。
//
// 状态机由插件单点驱动：LastFiredAt / FiredCount / LastResult / 终结标记
// 都只在这里改，界面只负责展示。
func (p *Plugin) finishFire(t *Task, now time.Time, result, errMsg, runID string) {
	if err := p.store.mutate(func(f *TaskFile) error {
		cur := f.get(t.ID)
		if cur == nil {
			return nil
		}
		cur.State.LastFiredAt = formatTime(now)
		cur.State.LastResult = result
		cur.State.LastError = errMsg
		cur.State.LastRunID = runID
		cur.State.FiredCount++
		if cur.State.Snooze != nil {
			// snooze 已被消费。
			if until, err := parseTime(cur.State.Snooze.Until); err == nil && !until.After(now) {
				cur.State.Snooze = nil
			}
		}
		if result == ResultFailed || result == ResultTimeout {
			cur.State.ConsecutiveFailures++
		} else {
			cur.State.ConsecutiveFailures = 0
		}
		cur.State.MissedPending = nil
		// 去重台账与通知限流是在投递成功时记在本地状态上的，
		// 这里必须把它带进落盘的副本，否则「同一周期只弹一次」在重启后会失效。
		if t.State.Notified != nil {
			cur.State.Notified = t.State.Notified
		}
		// 一次性任务触发后即为终结。
		if cur.Trigger.Kind == TriggerOnce {
			cur.State.Finished = true
		}
		if cur.Trigger.MaxFires > 0 && cur.State.FiredCount >= cur.Trigger.MaxFires {
			cur.State.Finished = true
		}
		cur.UpdatedAt = formatTime(now)
		t.State = cur.State
		return nil
	}); err != nil {
		p.logf("推进任务状态失败: %v", err)
	}
	// 熔断检查（安全工作包：连续失败 N 次自动停用）。
	p.checkCircuitBreaker(t.ID)
	p.wake()
}

// checkCircuitBreaker 连续失败达到阈值时自动停用并发 L3 通知。
func (p *Plugin) checkCircuitBreaker(taskID string) {
	tasks, _, _ := p.store.snapshot()
	for _, t := range tasks {
		if t.ID != taskID {
			continue
		}
		threshold := t.DisableAfterFailures()
		if threshold <= 0 || t.State.ConsecutiveFailures < threshold || !t.Enabled {
			continue
		}
		if err := p.store.mutate(func(f *TaskFile) error {
			cur := f.get(taskID)
			if cur == nil {
				return nil
			}
			cur.Enabled = false
			cur.UpdatedAt = formatTime(p.clock.Now())
			return nil
		}); err != nil {
			p.logf("熔断停用失败: %v", err)
			continue
		}
		p.logf("任务 %s 连续失败 %d 次，已自动停用", taskID, t.State.ConsecutiveFailures)
		req := RenderNotification(TemplateSystem, t, p.clock.Now(),
			fmt.Sprintf("任务「%s」连续失败 %d 次，已自动停用，请检查后再手动启用。", t.Title, t.State.ConsecutiveFailures))
		req.Level = LevelUrgent
		if err := p.opts.Host.Notify(req); err != nil {
			p.emitNotifyFallback("circuit-breaker-notify-failed", err.Error())
		}
		p.emit(EventChanged, "circuit-breaker", map[string]any{"taskID": taskID, "failures": t.State.ConsecutiveFailures})
	}
}

// handleMissed 按策略处理错过项：补发 / 跳过 / 顺延。
//
// 铁律：**错过的执行型任务不补跑**（附录 §6 离线约束）——
// 无人值守下补跑一批过期任务，是这套系统最危险的失败模式。
func (p *Plugin) handleMissed(t *Task, settings *GlobalConfig, missed []time.Time, now time.Time) {
	policy := t.EffectiveMissedPolicy(settings)

	if t.Kind == KindExec {
		rec := RunRecord{
			RunID:       newRunID(),
			TaskID:      t.ID,
			TaskTitle:   t.Title,
			Kind:        t.Kind,
			Source:      TriggerSourceCatchUp,
			TriggeredAt: formatTime(now),
			ScheduledAt: formatTime(missed[0]),
			Status:      ResultSkipped,
			Error:       fmt.Sprintf("错过了 %d 次执行机会，按策略不补跑", len(missed)),
		}
		_, _ = p.history.Append(rec)
		p.emit(EventMissed, "exec-not-caught-up", rec)
		p.advanceAfterMissed(t, missed, now, ResultSkipped)
		return
	}

	switch policy {
	case MissedSkip:
		p.emit(EventMissed, MissedSkip, map[string]any{"taskID": t.ID, "count": len(missed)})
		p.advanceAfterMissed(t, missed, now, ResultSkipped)

	case MissedDefer:
		// 顺延：不补发，但把错过记下来，让状态里看得见。
		p.emit(EventMissed, MissedDefer, map[string]any{"taskID": t.ID, "count": len(missed)})
		p.advanceAfterMissed(t, missed, now, ResultMissed)

	default: // catchup 补发：只补最近一次，避免一次弹出十几条。
		latest := missed[len(missed)-1]
		req := RenderNotification(TemplateMissed, t, latest, "")
		decision := Decide(DecisionInput{
			Task:     t,
			Settings: settings,
			Now:      now,
			DND:      t.EffectiveDND(settings),
		})
		req.Level = decision.Level
		if _, err := p.deliver(t, req, decision, latest, now); err != nil {
			p.logf("补发通知失败: %v", err)
		}
		rec := RunRecord{
			RunID:       newRunID(),
			TaskID:      t.ID,
			TaskTitle:   t.Title,
			Kind:        t.Kind,
			Source:      TriggerSourceCatchUp,
			TriggeredAt: formatTime(now),
			ScheduledAt: formatTime(latest),
			Status:      ResultNotified,
			Summary:     fmt.Sprintf("错过 %d 次，已补发最近一次", len(missed)),
		}
		_, _ = p.history.Append(rec)
		p.emit(EventMissed, MissedCatchUp, map[string]any{"taskID": t.ID, "count": len(missed), "latest": latest.Format(time.RFC3339)})
		p.advanceAfterMissed(t, missed, now, ResultMissed)
	}
}

// advanceAfterMissed 把错过项记入状态并推进计数（避免同一批错过被反复补偿）。
func (p *Plugin) advanceAfterMissed(t *Task, missed []time.Time, now time.Time, result string) {
	last := missed[len(missed)-1]
	entries := make([]MissedEntry, 0, len(missed))
	for _, m := range missed {
		entries = append(entries, MissedEntry{At: formatTime(m), Handled: "handled"})
	}
	if err := p.store.mutate(func(f *TaskFile) error {
		cur := f.get(t.ID)
		if cur == nil {
			return nil
		}
		cur.State.MissedPending = append(cur.State.MissedPending, entries...)
		if len(cur.State.MissedPending) > maxMissedPending {
			cur.State.MissedPending = cur.State.MissedPending[len(cur.State.MissedPending)-maxMissedPending:]
		}
		cur.State.LastFiredAt = formatTime(last)
		cur.State.FiredCount += len(missed)
		cur.State.LastResult = result
		if result == ResultMissed {
			cur.State.LastError = fmt.Sprintf("错过了 %d 次触发", len(missed))
		}
		if cur.Trigger.Kind == TriggerOnce {
			cur.State.Finished = true
		}
		if cur.Trigger.MaxFires > 0 && cur.State.FiredCount >= cur.Trigger.MaxFires {
			cur.State.Finished = true
		}
		cur.UpdatedAt = formatTime(now)
		return nil
	}); err != nil {
		p.logf("记录错过项失败: %v", err)
	}
}

// recordPanic 在触发路径 panic 时留下可查的痕迹（异常只写历史）。
func (p *Plugin) recordPanic(t *Task, at time.Time, msg string) {
	rec := RunRecord{
		RunID:       newRunID(),
		TaskID:      t.ID,
		TaskTitle:   t.Title,
		Kind:        t.Kind,
		Source:      TriggerSourceScheduled,
		TriggeredAt: formatTime(p.clock.Now()),
		ScheduledAt: formatTime(at),
		Status:      ResultFailed,
		Error:       msg,
	}
	_, _ = p.history.Append(rec)
	p.finishFire(t, p.clock.Now(), ResultFailed, msg, rec.RunID)
	p.emit(EventFired, "panic-isolated", rec)
}

// recordRunEvent 把运行生命周期事件并入任务历史（F10.4）。
func (p *Plugin) recordRunEvent(ev RunEvent) {
	rec := RunRecord{
		RunID:       ev.RunID,
		TaskID:      ev.TaskID,
		TriggeredAt: formatTime(ev.At),
		Status:      ev.Type,
		Summary:     ev.Message,
	}
	if ev.SessionID != "" {
		sid := ev.SessionID
		rec.RunSessionID = &sid
	}
	if _, err := p.history.Append(rec); err != nil {
		p.logf("写运行事件历史失败: %v", err)
	}
}

// ===== 内部工具 =====

// afterMutation 是「改完数据之后」的统一收尾：唤醒调度 + 广播变更。
func (p *Plugin) afterMutation(taskID, reason string, extra any) {
	p.wake()
	payload := map[string]any{"taskID": taskID}
	if extra != nil {
		payload["detail"] = extra
	}
	p.emit(EventChanged, reason, payload)
}

func (p *Plugin) wake() {
	p.mu.RLock()
	sched := p.sched
	p.mu.RUnlock()
	if sched != nil {
		sched.Wake()
	}
}

// validateTemplates 校验通知模板里的占位符都在白名单内。
func (p *Plugin) validateTemplates(t *Task) error {
	if t == nil || t.Notify == nil {
		return nil
	}
	if err := ValidateTemplate(t.Notify.TitleTemplate); err != nil {
		return fmt.Errorf("标题模板: %w", err)
	}
	if err := ValidateTemplate(t.Notify.BodyTemplate); err != nil {
		return fmt.Errorf("正文模板: %w", err)
	}
	return nil
}

// applyTaskDefaults 给任务补缺省值（snooze 时长、重试、熔断等）。
func applyTaskDefaults(t *Task, now time.Time) {
	if t.Policy == nil {
		t.Policy = &Policy{}
	}
	if t.Policy.RetryBackoffMinutes <= 0 {
		t.Policy.RetryBackoffMinutes = defaultRetryBackoffMin
	}
	if t.Policy.DisableAfterFailures <= 0 {
		t.Policy.DisableAfterFailures = defaultDisableAfterFail
	}
	if t.Policy.MaxRetries < 0 {
		t.Policy.MaxRetries = defaultMaxRetries
	}
	if t.Kind == KindExec && t.Exec != nil && t.Exec.TimeoutSeconds <= 0 {
		t.Exec.TimeoutSeconds = defaultExecTimeoutSeconds
	}
	if t.Trigger.Kind == TriggerInterval && t.Trigger.Interval != nil && t.Trigger.Interval.StartAt == "" {
		// 以创建时刻为锚点，保证「每 N 分钟」有确定的首次触发。
		t.Trigger.Interval.StartAt = formatTime(now)
	}
}

// defaultExecTimeoutSeconds 是执行型任务的默认超时（F5.5）。
const defaultExecTimeoutSeconds = 600

func replaceTask(f *TaskFile, t *Task) {
	for i, cur := range f.Tasks {
		if cur != nil && cur.ID == t.ID {
			f.Tasks[i] = t
			return
		}
	}
	f.Tasks = append(f.Tasks, t)
}

func sortViews(v []TaskView) {
	// 简单的插入排序：任务数量级是几十到几百，不值得引 sort 的比较函数开销之外的东西。
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && viewLess(v[j], v[j-1]); j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// viewLess 排序规则：有下次触发的在前（按时间升序），没有的（已终结/停用）沉底。
func viewLess(a, b TaskView) bool {
	az, bz := a.NextFireAt == "", b.NextFireAt == ""
	if az != bz {
		return !az
	}
	return a.NextFireAt < b.NextFireAt
}

// newTaskID 生成任务 ID：前缀 + 随机十六进制，避免与手工构造的 ID 混淆。
func newTaskID() string { return "task_" + randomHex(8) }

// newRunID 生成运行 ID。
func newRunID() string {
	return time.Now().Format("20060102T150405") + "_" + randomHex(4)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// 熵源不可用是极端情况：退化成时间戳也比 panic 好。
		return fmt.Sprintf("%x", time.Now().UnixNano())[:n*2]
	}
	return hex.EncodeToString(buf)
}
