package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ===== 持久化、决策树与触发流水线 =====

func baseTime() time.Time {
	return time.Date(2025, 6, 10, 8, 0, 0, 0, time.Local)
}

func newServiceTestPlugin(t *testing.T, host *fakeHost, dir string) *Plugin {
	t.Helper()
	return newServiceTestPluginAt(t, host, dir, baseTime())
}

// newServiceTestPluginAt 用指定时刻启动插件 —— 用于模拟「应用关了一段时间再打开」。
func newServiceTestPluginAt(t *testing.T, host *fakeHost, dir string, now time.Time) *Plugin {
	t.Helper()
	p, err := New(Options{
		Host:       host,
		DataDir:    dir,
		AppVersion: "test",
		Clock:      &fakeClock{now: now},
		Logger:     testLogger{t},
	})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	return p
}

func (p *Plugin) testClock() *fakeClock { return p.clock.(*fakeClock) }

func reminderInput(title string, at time.Time) TaskInput {
	return TaskInput{
		Title:   title,
		Kind:    KindReminder,
		Trigger: Trigger{Kind: TriggerOnce, Once: &OnceTrigger{At: at.Format(time.RFC3339)}},
	}
}

func TestCreateTaskValidatesBeforeSaving(t *testing.T) {
	dir := t.TempDir()
	p := newServiceTestPlugin(t, &fakeHost{}, dir)

	// 名称为空 → 拒绝
	if _, err := p.CreateTask(reminderInput("   ", baseTime().Add(time.Hour))); err == nil {
		t.Error("空名称应当被拒绝")
	}
	// 规则非法 → 拒绝
	bad := reminderInput("坏规则", baseTime().Add(time.Hour))
	bad.Trigger = Trigger{Kind: TriggerInterval, Interval: &IntervalTrigger{EveryMinutes: 0}}
	if _, err := p.CreateTask(bad); err == nil {
		t.Error("非法间隔应当被拒绝")
	}
	// 执行型缺 prompt → 拒绝
	exec := reminderInput("执行型", baseTime().Add(time.Hour))
	exec.Kind = KindExec
	if _, err := p.CreateTask(exec); err == nil {
		t.Error("执行型缺 prompt 应当被拒绝")
	}
	// 模板占位符不在白名单 → 拒绝
	tpl := reminderInput("坏模板", baseTime().Add(time.Hour))
	tpl.Notify = &NotifyConf{BodyTemplate: "{{unknownVar}} 到点了"}
	if _, err := p.CreateTask(tpl); err == nil {
		t.Error("未知占位符应当被拒绝")
	}

	// 以上全部失败：不应留下任何任务，也不该写出数据文件。
	if got := p.ListTasks(); len(got) != 0 {
		t.Errorf("校验失败不应落盘，实际有 %d 个任务", len(got))
	}
	if _, err := os.Stat(filepath.Join(dir, taskFileName)); err == nil {
		t.Error("校验全部失败时不应创建 tasks.json")
	}
}

func TestCreateTaskPersistsWithoutDerivedField(t *testing.T) {
	dir := t.TempDir()
	p := newServiceTestPlugin(t, &fakeHost{}, dir)

	view, err := p.CreateTask(reminderInput("喝水", baseTime().Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if view.ID == "" {
		t.Error("应分配任务 ID")
	}
	if view.NextFireAt == "" {
		t.Error("视图应带上派生字段 nextFireAt")
	}

	raw, err := os.ReadFile(filepath.Join(dir, taskFileName))
	if err != nil {
		t.Fatalf("读取数据文件失败: %v", err)
	}
	// 铁律：nextFireAt 是导数，**绝不落盘**。
	if strings.Contains(string(raw), "nextFireAt") {
		t.Error("nextFireAt 不应被持久化")
	}
	if !strings.Contains(string(raw), "\"version\": 1") {
		t.Errorf("数据文件应带版本号:\n%s", raw)
	}

	// 重新载入（模拟重启）：任务仍在。
	p2 := newServiceTestPlugin(t, &fakeHost{}, dir)
	got := p2.ListTasks()
	if len(got) != 1 || got[0].Title != "喝水" {
		t.Fatalf("重启后任务丢失: %+v", got)
	}
}

func TestUpdateTaskRecomputesNextFire(t *testing.T) {
	dir := t.TempDir()
	p := newServiceTestPlugin(t, &fakeHost{}, dir)

	view, err := p.CreateTask(reminderInput("改时间", baseTime().Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	before := view.NextFireAt

	in := reminderInput("改时间", baseTime().Add(5*time.Hour))
	if _, err := p.UpdateTask(view.ID, in); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	after, err := p.GetTask(view.ID)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if after.NextFireAt == before {
		t.Error("编辑时间后必须重算下次触发（不能沿用旧定时）")
	}
	if got, want := after.NextFireAt, baseTime().Add(5*time.Hour).Format(time.RFC3339); got != want {
		t.Errorf("NextFireAt = %q, want %q", got, want)
	}
}

func TestDeleteTaskKeepsHistoryByDefault(t *testing.T) {
	dir := t.TempDir()
	p := newServiceTestPlugin(t, &fakeHost{}, dir)

	view, _ := p.CreateTask(reminderInput("待删", baseTime().Add(time.Hour)))
	// 先制造一条历史。
	p.testClock().set(baseTime().Add(2 * time.Hour))
	p.tick(p.testClock().now)
	recs, _ := p.ListRuns(view.ID, 10)
	if len(recs) == 0 {
		t.Fatal("应先产生一条历史")
	}

	if err := p.DeleteTask(view.ID, false); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if recs, _ := p.ListRuns(view.ID, 10); len(recs) == 0 {
		t.Error("默认应保留历史（历史是排查依据）")
	}
	if err := p.DeleteTask(view.ID, false); err == nil {
		t.Error("重复删除应当报错（动作需可判定）")
	}
}

func TestFireSendsNotificationAndAdvancesState(t *testing.T) {
	dir := t.TempDir()
	host := &fakeHost{}
	p := newServiceTestPlugin(t, host, dir)

	at := baseTime().Add(time.Hour)
	view, _ := p.CreateTask(reminderInput("开会", at))

	// 推进到触发时刻之后，驱动一次调度。
	p.testClock().set(at.Add(30 * time.Second))
	p.tick(p.testClock().now)

	if host.notifyCalls != 1 {
		t.Fatalf("应发出 1 条通知，实际 %d", host.notifyCalls)
	}
	got, _ := p.GetTask(view.ID)
	if got.State.FiredCount != 1 {
		t.Errorf("FiredCount = %d, want 1", got.State.FiredCount)
	}
	if got.State.LastResult != ResultNotified {
		t.Errorf("LastResult = %q, want %q", got.State.LastResult, ResultNotified)
	}
	if !got.State.Finished {
		t.Error("一次性任务触发后应终结")
	}
	if got.NextFireAt != "" {
		t.Errorf("已终结的任务不应再有下次触发，实际 %q", got.NextFireAt)
	}
	// 历史落盘。
	recs, err := p.ListRuns(view.ID, 10)
	if err != nil || len(recs) != 1 {
		t.Fatalf("应落盘 1 条历史: %v %d", err, len(recs))
	}
	if recs[0].ScheduledAt == "" {
		t.Error("历史应记录计划触发时刻（便于看出延迟）")
	}
	// 事件：task:fired 必须推到统一通道。
	if !host.hasEvent(EventFired) {
		t.Errorf("应广播 %s", EventFired)
	}
}

func TestFireIsIdempotentPerCycle(t *testing.T) {
	dir := t.TempDir()
	host := &fakeHost{}
	p := newServiceTestPlugin(t, host, dir)

	at := baseTime().Add(time.Hour)
	view, _ := p.CreateTask(reminderInput("别重复", at))
	task := p.findTask(view.ID)
	settings := p.Settings()

	if _, err := p.fire(task, settings, at, TriggerSourceScheduled); err != nil {
		t.Fatalf("首次触发失败: %v", err)
	}
	if _, err := p.fire(task, settings, at, TriggerSourceScheduled); err != nil {
		t.Fatalf("二次触发失败: %v", err)
	}
	if host.notifyCalls != 1 {
		t.Errorf("同一周期只应通知一次，实际 %d", host.notifyCalls)
	}
}

func TestPauseStopsSchedulingButKeepsTasks(t *testing.T) {
	dir := t.TempDir()
	p := newServiceTestPlugin(t, &fakeHost{}, dir)
	if _, err := p.CreateTask(reminderInput("暂停中", baseTime().Add(time.Hour))); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if _, ok := p.nearest(baseTime()); !ok {
		t.Fatal("暂停前应有待触发任务")
	}
	if err := p.PauseAll(true); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	if _, ok := p.nearest(baseTime()); ok {
		t.Error("暂停后不应再安排触发")
	}
	// 暂停不能把任务弄丢（到期任务不得凭空消失）。
	if len(p.ListTasks()) != 1 {
		t.Error("暂停不应删除任务")
	}
	// 暂停状态必须持久化。
	p2 := newServiceTestPlugin(t, &fakeHost{}, dir)
	if !p2.Settings().Paused {
		t.Error("暂停状态应持久化")
	}
}

func TestSnoozeCreatesTemporaryTriggerOnly(t *testing.T) {
	dir := t.TempDir()
	p := newServiceTestPlugin(t, &fakeHost{}, dir)

	// 每天 09:00；当前 08:00。
	in := TaskInput{Title: "喝水", Kind: KindReminder,
		Trigger: Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "daily", TimeOfDay: "09:00"}}}
	view, err := p.CreateTask(in)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	ruleNext := view.NextFireAt

	if err := p.SnoozeTask(view.ID, 10); err != nil {
		t.Fatalf("snooze 失败: %v", err)
	}
	got, _ := p.GetTask(view.ID)
	if got.SnoozeUntil == "" {
		t.Fatal("应记录 snooze 的临时触发时刻")
	}
	if got.NextFireAt != got.SnoozeUntil {
		t.Errorf("snooze 期间下次触发应取临时时刻: %q vs %q", got.NextFireAt, got.SnoozeUntil)
	}
	if got.NextFireAt == ruleNext {
		t.Error("snooze 应生成比原规则更早的临时触发")
	}
	// 原规则本身不能被改掉。
	if got.Trigger.Recurring.TimeOfDay != "09:00" {
		t.Error("snooze 不得修改原规则")
	}
}

func TestMissedCatchUpNotifiesOnce(t *testing.T) {
	dir := t.TempDir()
	host := &fakeHost{}
	p := newServiceTestPlugin(t, host, dir)

	// 每天 09:00 建好任务，然后「关掉应用」三天再打开。
	in := TaskInput{Title: "日会", Kind: KindReminder,
		Trigger: Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "daily", TimeOfDay: "09:00"}}}
	if _, err := p.CreateTask(in); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("停止失败: %v", err)
	}

	// 三天后重新启动：这期间的 3 次提醒都应被认作「错过」。
	host2 := &fakeHost{}
	p2 := newServiceTestPluginAt(t, host2, dir, baseTime().AddDate(0, 0, 3))
	defer p2.Stop()

	if host2.notifyCalls != 1 {
		t.Fatalf("补发只应弹一次（避免一次炸出多条），实际 %d", host2.notifyCalls)
	}
	if !host2.hasEvent(EventMissed) {
		t.Error("应广播错过事件")
	}
	if host2.notifyCalls != 1 && !host2.hasEvent(EventNotifyInApp) {
		t.Error("补发必须有可见的投递路径（弹通知或应用内提醒）")
	}
}

func TestLongSleepDoesNotFireHundredsOfTimes(t *testing.T) {
	dir := t.TempDir()
	host := &fakeHost{}
	p := newServiceTestPlugin(t, host, dir)

	// 每分钟一次；「休眠」6 小时。
	in := TaskInput{Title: "每分钟", Kind: KindReminder,
		Trigger: Trigger{Kind: TriggerInterval, Interval: &IntervalTrigger{EveryMinutes: 1, StartAt: baseTime().Format(time.RFC3339)}}}
	if _, err := p.CreateTask(in); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	p.testClock().set(baseTime().Add(6 * time.Hour))
	p.tick(p.testClock().now)

	// 一次唤醒绝不能触发上百次：积压一律折成「错过」，按策略补发一次。
	if host.notifyCalls > 1 {
		t.Errorf("长时间休眠后最多补发一次，实际 %d", host.notifyCalls)
	}
}

func TestExecTaskIsNotCaughtUp(t *testing.T) {
	dir := t.TempDir()
	p := newServiceTestPlugin(t, &fakeHost{}, dir)

	in := TaskInput{Title: "巡检", Kind: KindExec,
		Exec:    &ExecConf{Prompt: "检查磁盘"},
		Trigger: Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "daily", TimeOfDay: "09:00"}}}
	view, err := p.CreateTask(in)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("停止失败: %v", err)
	}

	host2 := &fakeHost{}
	p2 := newServiceTestPluginAt(t, host2, dir, baseTime().AddDate(0, 0, 3))
	defer p2.Stop()

	// 错过的执行型任务不补跑：绝不能因为「补跑」而在无人值守下批量执行。
	if host2.notifyCalls != 0 {
		t.Errorf("错过的执行型任务不应产生通知（更不应补跑），实际 %d", host2.notifyCalls)
	}
	recs, err := p2.ListRuns(view.ID, 10)
	if err != nil || len(recs) != 1 {
		t.Fatalf("应留下恰好一条「未补跑」的记录: %v %d", err, len(recs))
	}
	if recs[0].Source != TriggerSourceCatchUp || recs[0].Status != ResultSkipped {
		t.Errorf("记录应说明跳过原因: %+v", recs[0])
	}
	if recs[0].Error == "" {
		t.Error("必须写明为什么没执行（不允许只写状态）")
	}
}

func TestFireNotifiesInAppWhenDNDActive(t *testing.T) {
	dir := t.TempDir()
	host := &fakeHost{}
	p := newServiceTestPlugin(t, host, dir)

	// 全天免打扰。
	if err := p.UpdateSettings(&GlobalConfig{
		Enabled: true, NotifyEnabled: true,
		DND: &DNDConfig{Enabled: true, Start: "08:00", End: "08:00"},
	}); err != nil {
		t.Fatalf("设置失败: %v", err)
	}
	at := baseTime().Add(time.Hour)
	if _, err := p.CreateTask(reminderInput("免打扰时段", at)); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	p.testClock().set(at.Add(time.Second))
	p.tick(p.testClock().now)

	if host.notifyCalls != 0 {
		t.Errorf("免打扰时段不应弹系统通知，实际 %d", host.notifyCalls)
	}
	if !host.hasEvent(EventNotifyInApp) {
		t.Error("免打扰时段应改为应用内提醒")
	}
}

func TestFireDegradesWhenNotifyFails(t *testing.T) {
	dir := t.TempDir()
	host := &fakeHost{notifyErr: errTestCategory}
	p := newServiceTestPlugin(t, host, dir)

	at := baseTime().Add(time.Hour)
	view, _ := p.CreateTask(reminderInput("发不出去", at))
	p.testClock().set(at.Add(time.Second))
	p.tick(p.testClock().now)

	// 被动降级：发送失败必须可见，且降级为应用内提醒。
	if !host.hasEvent(EventNotifyFallback) {
		t.Error("发送失败应广播降级事件")
	}
	if !host.hasEvent(EventNotifyInApp) {
		t.Error("发送失败应改为应用内提醒")
	}
	recs, _ := p.ListRuns(view.ID, 5)
	if len(recs) == 0 || recs[0].NotifyFallback == "" {
		t.Error("历史应记录降级原因")
	}
}

// ===== 持久化容错 =====

func TestStoreRecoversFromCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, taskFileName)
	if err := os.WriteFile(path, []byte("{这不是 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewTaskStore(dir)
	err := s.Load()
	if err == nil {
		t.Fatal("损坏文件应返回错误（供上层记录）")
	}
	if !isCorrupted(err) {
		t.Errorf("应返回 ErrCorrupted，实际 %v", err)
	}
	// 坏文件改名保留，而不是删掉 —— 用户的任务定义值得人工抢救一次。
	if _, statErr := os.Stat(path + ".broken"); statErr != nil {
		t.Errorf("坏文件应备份为 .broken: %v", statErr)
	}
	// 以空集启动，不阻断应用。
	tasks, _, _ := s.snapshot()
	if len(tasks) != 0 {
		t.Errorf("损坏后应以空集启动，实际 %d 个任务", len(tasks))
	}
}

func TestStoreMigrationWritesBakOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, taskFileName)
	// v0：没有 version 字段。
	old := `{"tasks":[{"id":"t1","title":"老任务","kind":"reminder","enabled":true,
		"trigger":{"kind":"once","once":{"at":"2099-01-01T09:00:00+08:00"}},"state":{},"createdAt":"2025-01-01T00:00:00+08:00"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewTaskStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("迁移不应失败: %v", err)
	}
	tasks, _, _ := s.snapshot()
	if len(tasks) != 1 || tasks[0].Title != "老任务" {
		t.Fatalf("迁移不应丢数据: %+v", tasks)
	}
	bak := path + ".bak"
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("首迁应留备份: %v", err)
	}
	// 备份内容是迁移前的原文（可回退）。
	raw, _ := os.ReadFile(bak)
	if !strings.Contains(string(raw), "老任务") || strings.Contains(string(raw), "version") {
		t.Errorf("备份应是迁移前的原文: %s", raw)
	}
	// 已存在备份时不覆盖（保护上次的现场）。
	if err := os.WriteFile(bak, []byte("SENTINEL"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2 := NewTaskStore(dir)
	_ = s2.Load()
	raw, _ = os.ReadFile(bak)
	if string(raw) != "SENTINEL" {
		t.Error("已存在的 .bak 不应被覆盖")
	}
}

func TestStoreRecoversRunningStateOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, taskFileName)
	// 崩溃现场：状态停在 running。
	content := `{"version":1,"settings":{"enabled":true},"tasks":[{"id":"t1","title":"半途","kind":"reminder",
		"enabled":true,"trigger":{"kind":"interval","interval":{"everyMinutes":30,"startAt":"2025-01-01T00:00:00+08:00"}},
		"state":{"lastResult":"running","firedCount":1},"createdAt":"2025-01-01T00:00:00+08:00"}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewTaskStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	tasks, _, _ := s.snapshot()
	if tasks[0].State.LastResult != ResultInterrupted {
		t.Errorf("残留的 running 应改为 interrupted，实际 %q", tasks[0].State.LastResult)
	}
	if tasks[0].State.LastError == "" {
		t.Error("应说明中断原因（不允许只写状态不写原因）")
	}
}

func TestStoreKeepsUnknownTopLevelFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, taskFileName)
	content := `{"version":1,"futureFeature":{"x":1},"tasks":[]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewTaskStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	raw, _ := os.ReadFile(path)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	// 前向兼容：本版本不认识的字段必须原样写回，否则「旧版一存就抹掉新版数据」。
	if _, ok := m["futureFeature"]; !ok {
		t.Errorf("未知顶层字段应保留: %s", raw)
	}
}

// ===== 决策树（表驱动，顺序即需求）=====

func TestDecisionTreeOrder(t *testing.T) {
	reminder := func() *Task {
		return &Task{ID: "t1", Kind: KindReminder, Enabled: true}
	}
	on := NewGlobalConfig()

	cases := []struct {
		name string
		in   DecisionInput
		want Delivery
	}{
		{
			name: "任务级L0覆盖优先于一切",
			in:   DecisionInput{Task: taskLevel(reminder(), LevelRecord), Settings: on, Now: baseTime()},
			want: DeliveryRecord,
		},
		{
			name: "任务级仅应用内优先于全局暂停",
			in: DecisionInput{Task: taskInApp(reminder()), Settings: func() *GlobalConfig {
				c := NewGlobalConfig()
				c.Paused = true
				return c
			}(), Now: baseTime()},
			want: DeliveryInApp,
		},
		{
			name: "全局暂停只记录",
			in: DecisionInput{Task: reminder(), Settings: func() *GlobalConfig {
				c := NewGlobalConfig()
				c.Paused = true
				return c
			}(), Now: baseTime()},
			want: DeliveryRecord,
		},
		{
			name: "全屏静默只记录",
			in:   DecisionInput{Task: reminder(), Settings: on, Now: baseTime(), Fullscreen: true},
			want: DeliveryRecord,
		},
		{
			name: "免打扰改为应用内",
			in: DecisionInput{Task: reminder(), Settings: on, Now: baseTime(),
				DND: &DNDConfig{Enabled: true, Start: "00:00", End: "23:59"}},
			want: DeliveryInApp,
		},
		{
			name: "同周期去重丢弃",
			in:   DecisionInput{Task: reminder(), Settings: on, Now: baseTime(), DedupeHit: true},
			want: DeliveryDrop,
		},
		{
			name: "频率超限丢弃",
			in:   DecisionInput{Task: reminder(), Settings: on, Now: baseTime(), RateLimited: true},
			want: DeliveryDrop,
		},
		{
			name: "前台活跃仅应用内",
			in:   DecisionInput{Task: reminder(), Settings: on, Now: baseTime(), Foreground: true},
			want: DeliveryInApp,
		},
		{
			name: "通知开关关闭仅应用内",
			in: DecisionInput{Task: reminder(), Settings: func() *GlobalConfig {
				c := NewGlobalConfig()
				c.NotifyEnabled = false
				return c
			}(), Now: baseTime()},
			want: DeliveryInApp,
		},
		{
			name: "其他情况弹通知",
			in:   DecisionInput{Task: reminder(), Settings: on, Now: baseTime()},
			want: DeliveryToast,
		},
	}
	for _, c := range cases {
		if got := Decide(c.in).Delivery; got != c.want {
			t.Errorf("%s: Delivery = %q, want %q（决策树顺序即需求）", c.name, got, c.want)
		}
	}
}

func taskLevel(t *Task, l NotifyLevel) *Task {
	t.Notify = &NotifyConf{Level: l}
	return t
}

func taskInApp(t *Task) *Task {
	t.Notify = &NotifyConf{InAppOnly: true}
	return t
}

func TestRateLimiterWindows(t *testing.T) {
	r := NewRateLimiter()
	now := baseTime()

	if ok, _ := r.Allow("t1", now, 1, 3); !ok {
		t.Fatal("首条应放行")
	}
	r.Record("t1", now)
	// 同一任务每分钟 1 条。
	if ok, reason := r.Allow("t1", now.Add(time.Second), 1, 3); ok || reason == "" {
		t.Errorf("同任务超限应被拦下并给原因，got ok=%v reason=%q", ok, reason)
	}
	// 另一任务在全局额度内仍可放行。
	if ok, _ := r.Allow("t2", now.Add(time.Second), 1, 3); !ok {
		t.Error("全局额度未超，其他任务应放行")
	}
	r.Record("t2", now.Add(time.Second))
	// 窗口滑过之后恢复。
	if ok, _ := r.Allow("t1", now.Add(2*time.Minute), 1, 3); !ok {
		t.Error("时间窗口滑过后应恢复放行")
	}
}

func TestDedupeKeyIsRecomputable(t *testing.T) {
	at := baseTime()
	k1 := (Dedupe{}).Key("t1", at)
	k2 := (Dedupe{}).Key("t1", at)
	if k1 != k2 {
		t.Error("同一触发时刻的去重键必须稳定（重启后才能去重）")
	}
	if k1 == (Dedupe{}).Key("t1", at.Add(time.Second)) {
		t.Error("不同触发时刻应是不同的键")
	}
}

func TestTruncateRunesAndOneLine(t *testing.T) {
	if got := truncateRunes("一二三四五", 3); got != "一二…" {
		t.Errorf("按字符截断: %q", got)
	}
	if got := oneLine("第一行\n第二行\t带制表符"); got != "第一行 第二行 带制表符" {
		t.Errorf("应压成单行: %q", got)
	}
	req := BuildNotification(&Task{ID: "t1", Title: strings.Repeat("长", 60), Kind: KindReminder},
		LevelNormal, strings.Repeat("标", 100), strings.Repeat("文", 300))
	if len([]rune(req.Title)) > maxNotifyTitleRunes {
		t.Errorf("标题应截断到 %d 字，实际 %d", maxNotifyTitleRunes, len([]rune(req.Title)))
	}
	if len([]rune(req.Body)) > maxNotifyBodyRunes {
		t.Errorf("正文应截断到 %d 字，实际 %d", maxNotifyBodyRunes, len([]rune(req.Body)))
	}
}

func TestValidateTemplateRejectsUnknownPlaceholder(t *testing.T) {
	if err := ValidateTemplate("{{task}} 到点了"); err != nil {
		t.Errorf("白名单占位符应通过: %v", err)
	}
	if err := ValidateTemplate("{{nope}} 到点了"); err == nil {
		t.Error("未知占位符应被拒绝")
	}
	// 未知占位符在渲染时原样保留（让用户看出自己写错了）。
	got := RenderTemplate("{{task}}/{{nope}}", map[string]string{"{{task}}": "喝水"})
	if got != "喝水/{{nope}}" {
		t.Errorf("渲染结果 = %q", got)
	}
}

func isCorrupted(err error) bool {
	return err != nil && strings.Contains(err.Error(), "已损坏")
}
