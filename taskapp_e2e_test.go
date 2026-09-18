package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-tmp/plugin"
)

// ===== 端到端验收：真实装配路径 + 真实导出方法 =====
//
// 与 plugin 包内的单元测试不同，这里走的是**主程序真正会走的那条路**：
//
//	startDesktopPlugin(app) → plugin.New → plugin.Start → 调度循环
//	  → 真实 tasks.json / task-runs 落盘 → App 上的导出方法（前端调的那些）
//
// 之所以不需要窗口：宿主适配器在 wails 运行时缺席时（app.ctx == nil）
// 会优雅降级（通知注册失败只记录、Emit 静默返回），插件仍应正常加载 ——
// 这本身就是「插件异常不影响主程序、主程序缺环境也不影响插件加载」的验证。
//
// 本用例会操作包级 desktopPluginState（插件是进程级单例），因此
// 结束前必须复位，避免影响同包其他用例。

func TestDesktopPluginEndToEndWithRealBootstrap(t *testing.T) {
	dir := t.TempDir()
	app := &App{baseDir: dir}

	// 复位单例，保证本用例从干净状态开始。
	desktopPluginState.mu.Lock()
	desktopPluginState.p = nil
	desktopPluginState.mu.Unlock()
	t.Cleanup(func() {
		stopDesktopPlugin()
		desktopPluginState.mu.Lock()
		desktopPluginState.p = nil
		desktopPluginState.mu.Unlock()
	})

	// 1) 真实装配。
	startDesktopPlugin(app)

	p := currentDesktopPlugin()
	if p == nil {
		t.Fatal("插件未加载：startDesktopPlugin 应完成真实装配")
	}
	if got := p.State(); got != plugin.StateRunning {
		t.Fatalf("插件状态 = %q, want running", got)
	}

	// 2) 插件身份可被识别（前端的入口方法）。
	info := app.TaskPluginInfo()
	if info.ID != "desktop-plugin" {
		t.Errorf("插件 ID = %q", info.ID)
	}
	if info.State != string(plugin.StateRunning) {
		t.Errorf("Info.State = %q, want running", info.State)
	}
	if info.DataDir != dir {
		t.Errorf("DataDir = %q, want %q", info.DataDir, dir)
	}
	if info.AppVersion != appVersion {
		t.Errorf("AppVersion = %q, want %q", info.AppVersion, appVersion)
	}
	if info.StartedAt == "" {
		t.Error("启动后应有 StartedAt")
	}

	manifest := app.TaskPluginManifest()
	if manifest.ID != "desktop-plugin" || manifest.Kind != "builtin" {
		t.Errorf("清单未正确加载: %+v", manifest)
	}
	if manifest.HostAPIVersion != plugin.HostAPIVersion {
		t.Errorf("hostAPIVersion = %q", manifest.HostAPIVersion)
	}

	// 3) 通过导出方法创建一个「1 秒后」的真实一次性提醒。
	at := time.Now().Add(time.Second)
	view, err := app.CreateTask(plugin.TaskInput{
		Title: "验收提醒",
		Kind:  plugin.KindReminder,
		Trigger: plugin.Trigger{
			Kind: plugin.TriggerOnce,
			Once: &plugin.OnceTrigger{At: at.Format(time.RFC3339)},
		},
	})
	if err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}
	if view.ID == "" || view.NextFireAt == "" {
		t.Fatalf("创建结果不完整: %+v", view)
	}

	// 数据文件确实落了盘（真实路径，不是内存态）。
	if _, err := os.Stat(filepath.Join(dir, "tasks.json")); err != nil {
		t.Fatalf("tasks.json 未生成: %v", err)
	}

	// 4) 列表与触发预览（前端依赖）。
	tasks, err := app.ListTasks()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks = %d 条, err=%v", len(tasks), err)
	}
	previews, err := app.PreviewTaskTriggerTimes(view.Trigger, 3)
	if err != nil {
		t.Fatalf("PreviewTaskTriggerTimes 失败: %v", err)
	}
	if len(previews) != 1 { // 一次性任务只有 1 次
		t.Errorf("一次性任务预览 = %d 条, want 1", len(previews))
	}

	// 5) 等真实调度循环把它触发掉 —— 这是「插件可正常运行」的硬证据。
	var runs []plugin.RunRecord
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runs, err = app.ListTaskRuns(view.ID, 10)
		if err == nil && len(runs) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(runs) == 0 {
		t.Fatal("等待超时：真实调度循环没有触发该任务（提醒漏报）")
	}
	if runs[0].Status != plugin.ResultNotified {
		t.Errorf("历史状态 = %q, want %q", runs[0].Status, plugin.ResultNotified)
	}
	// 宿主没有 wails 上下文 → 通知必然失败 → 必须降级并写明原因，不得静默。
	if runs[0].NotifyFallback == "" {
		t.Error("通知发送失败时应记录降级原因（不允许静默）")
	}

	// 状态机由插件单点推进：触发后状态与终结标记都应落盘。
	after, err := app.GetTask(view.ID)
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}
	if after.State.FiredCount != 1 {
		t.Errorf("FiredCount = %d, want 1", after.State.FiredCount)
	}
	if !after.State.Finished {
		t.Error("一次性任务触发后应标记为已结束")
	}
	if after.NextFireAt != "" {
		t.Errorf("已结束的任务不应再有下次触发: %q", after.NextFireAt)
	}

	// 6) 其余导出方法冒烟（启停 / 全局配置 / 暂停 / 计数 / 删除）。
	if _, err := app.SetTaskEnabled(view.ID, false); err != nil {
		t.Errorf("SetTaskEnabled 失败: %v", err)
	}
	settings, err := app.GetTaskSettings()
	if err != nil || settings == nil {
		t.Fatalf("GetTaskSettings 失败: %v", err)
	}
	cfg := *settings
	cfg.DefaultSnoozeMinutes = 30
	if err := app.UpdateTaskSettings(cfg); err != nil {
		t.Errorf("UpdateTaskSettings 失败: %v", err)
	}
	if err := app.PauseAllTasks(true); err != nil {
		t.Errorf("PauseAllTasks 失败: %v", err)
	}
	if err := app.PauseAllTasks(false); err != nil {
		t.Errorf("恢复失败: %v", err)
	}
	if total, limit := app.TaskCounts(); total != 1 || limit <= 0 {
		t.Errorf("TaskCounts = (%d, %d)", total, limit)
	}
	if err := app.CompleteTask(view.ID); err != nil {
		t.Errorf("CompleteTask 失败: %v", err)
	}
	if err := app.DeleteTask(view.ID, false); err != nil {
		t.Errorf("DeleteTask 失败: %v", err)
	}
	// 默认保留历史（历史是排查依据）。
	if runs, err := app.ListTaskRuns(view.ID, 10); err != nil || len(runs) == 0 {
		t.Error("删除任务后默认应保留历史")
	}

	// 7) 停止后再调用导出方法：应返回可读错误而不是 panic。
	stopDesktopPlugin()
	desktopPluginState.mu.Lock()
	desktopPluginState.p = nil
	desktopPluginState.mu.Unlock()

	if _, err := app.ListTasks(); err == nil {
		t.Error("插件不可用时 ListTasks 应返回错误，供前端提示")
	}
	if got := app.TaskPluginInfo().State; got != "unavailable" {
		t.Errorf("插件不可用时 TaskPluginInfo().State = %q, want unavailable", got)
	}
}

// TestDesktopPluginRejectsInvalidTaskThroughExport 确认校验错误会穿透到前端。
func TestDesktopPluginRejectsInvalidTaskThroughExport(t *testing.T) {
	dir := t.TempDir()
	app := &App{baseDir: dir}
	desktopPluginState.mu.Lock()
	desktopPluginState.p = nil
	desktopPluginState.mu.Unlock()
	t.Cleanup(func() {
		stopDesktopPlugin()
		desktopPluginState.mu.Lock()
		desktopPluginState.p = nil
		desktopPluginState.mu.Unlock()
	})

	startDesktopPlugin(app)
	if currentDesktopPlugin() == nil {
		t.Fatal("插件未加载")
	}

	// 名称为空 → 后端必须拒绝（界面的保存按钮依赖这个错误）。
	_, err := app.CreateTask(plugin.TaskInput{
		Title: "   ",
		Kind:  plugin.KindReminder,
		Trigger: plugin.Trigger{
			Kind: plugin.TriggerOnce,
			Once: &plugin.OnceTrigger{At: time.Now().Add(time.Hour).Format(time.RFC3339)},
		},
	})
	if err == nil {
		t.Error("空名称应被拒绝")
	}

	// 非法规则同样要被拒绝，并给出可读原因。
	_, err = app.CreateTask(plugin.TaskInput{
		Title: "坏规则",
		Kind:  plugin.KindReminder,
		Trigger: plugin.Trigger{
			Kind:     plugin.TriggerInterval,
			Interval: &plugin.IntervalTrigger{EveryMinutes: 0},
		},
	})
	if err == nil {
		t.Error("非法间隔应被拒绝")
	}

	// 反复失败不应留下脏数据。
	if tasks, _ := app.ListTasks(); len(tasks) != 0 {
		t.Errorf("校验失败不应落盘，实际 %d 条", len(tasks))
	}
}
