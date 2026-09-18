package main

import "wails-tmp/plugin"

// ===== 桌面插件对前端的导出方法 =====
//
// 这些方法会被 Wails 自动绑定到 frontend/wailsjs/go/main/App，
// 前端不需要手写任何 HTTP 客户端（附录「通道 3」）。
//
// 本层保持「薄委托」：只做参数搬运与空值保护，业务判断一律在 plugin 内。
// 插件不可用时统一返回可读错误，而不是 panic —— 插件异常不应影响主程序功能。

// taskErrUnavailable 是插件未启用时的统一错误。
var taskErrUnavailable = plugin.ErrNotRunning

// TaskPluginInfo 返回桌面插件的清单与运行状态。
//
// 这是「插件已被主程序识别」的对外证据：前端据此确认插件已加载，
// 并拿到插件版本、宿主接口版本、数据目录与当前状态。
// 插件不可用时返回 state=unavailable 而不是报错（否则前端拿不到任何信息）。
func (a *App) TaskPluginInfo() plugin.Info {
	p := currentDesktopPlugin()
	if p == nil {
		return plugin.Info{
			State:      "unavailable",
			AppVersion: appVersion,
			DataDir:    a.dataDirOrEmpty(),
		}
	}
	return p.Info()
}

// TaskPluginManifest 返回插件清单（身份、能力、数据文件、事件通道、所需宿主方法）。
func (a *App) TaskPluginManifest() plugin.Manifest {
	p := currentDesktopPlugin()
	if p == nil {
		return plugin.Manifest{}
	}
	return p.Manifest()
}

// ListTasks 返回全部定时任务（含派生的 nextFireAt）。
func (a *App) ListTasks() ([]plugin.TaskView, error) {
	p := currentDesktopPlugin()
	if p == nil {
		return nil, taskErrUnavailable
	}
	return p.ListTasks(), nil
}

// GetTask 返回单个定时任务。
func (a *App) GetTask(id string) (plugin.TaskView, error) {
	p := currentDesktopPlugin()
	if p == nil {
		return plugin.TaskView{}, taskErrUnavailable
	}
	return p.GetTask(id)
}

// CreateTask 新建定时任务（校验不通过会返回错误，前端据此提示）。
func (a *App) CreateTask(in plugin.TaskInput) (plugin.TaskView, error) {
	p := currentDesktopPlugin()
	if p == nil {
		return plugin.TaskView{}, taskErrUnavailable
	}
	return p.CreateTask(in)
}

// UpdateTask 编辑定时任务（编辑后会重算下次触发）。
func (a *App) UpdateTask(id string, in plugin.TaskInput) (plugin.TaskView, error) {
	p := currentDesktopPlugin()
	if p == nil {
		return plugin.TaskView{}, taskErrUnavailable
	}
	return p.UpdateTask(id, in)
}

// DeleteTask 删除定时任务；deleteHistory=true 时一并删除历史。
//
// 界面必须在二次确认里说明历史是否一并删除（默认保留）。
func (a *App) DeleteTask(id string, deleteHistory bool) error {
	p := currentDesktopPlugin()
	if p == nil {
		return taskErrUnavailable
	}
	return p.DeleteTask(id, deleteHistory)
}

// SetTaskEnabled 启用/停用定时任务。
func (a *App) SetTaskEnabled(id string, enabled bool) (plugin.TaskView, error) {
	p := currentDesktopPlugin()
	if p == nil {
		return plugin.TaskView{}, taskErrUnavailable
	}
	return p.SetTaskEnabled(id, enabled)
}

// RunTaskNow 立即触发一次定时任务。
func (a *App) RunTaskNow(id string) (plugin.RunRecord, error) {
	p := currentDesktopPlugin()
	if p == nil {
		return plugin.RunRecord{}, taskErrUnavailable
	}
	return p.RunTaskNow(id)
}

// SnoozeTask 稍后提醒；minutes<=0 时使用配置的默认时长。
func (a *App) SnoozeTask(id string, minutes int) error {
	p := currentDesktopPlugin()
	if p == nil {
		return taskErrUnavailable
	}
	return p.SnoozeTask(id, minutes)
}

// CompleteTask 标记任务已处理。
func (a *App) CompleteTask(id string) error {
	p := currentDesktopPlugin()
	if p == nil {
		return taskErrUnavailable
	}
	return p.CompleteTask(id)
}

// ListTaskRuns 返回任务历史（最新在前）。
func (a *App) ListTaskRuns(taskID string, limit int) ([]plugin.RunRecord, error) {
	p := currentDesktopPlugin()
	if p == nil {
		return nil, taskErrUnavailable
	}
	return p.ListRuns(taskID, limit)
}

// PreviewTaskTriggerTimes 返回未来 n 次触发时刻（保存前的预览，F2.8）。
//
// 规则非法时返回错误而不是空列表：cron 与日历规则「没有预览就不允许保存」。
func (a *App) PreviewTaskTriggerTimes(tr plugin.Trigger, n int) ([]string, error) {
	p := currentDesktopPlugin()
	if p == nil {
		return nil, taskErrUnavailable
	}
	return p.PreviewTriggerTimes(tr, n)
}

// GetTaskSettings 返回插件全局配置。
func (a *App) GetTaskSettings() (*plugin.GlobalConfig, error) {
	p := currentDesktopPlugin()
	if p == nil {
		return nil, taskErrUnavailable
	}
	return p.Settings(), nil
}

// UpdateTaskSettings 更新插件全局配置（改动即时生效，无需重启）。
func (a *App) UpdateTaskSettings(cfg plugin.GlobalConfig) error {
	p := currentDesktopPlugin()
	if p == nil {
		return taskErrUnavailable
	}
	return p.UpdateSettings(&cfg)
}

// PauseAllTasks 全局暂停/恢复。
func (a *App) PauseAllTasks(paused bool) error {
	p := currentDesktopPlugin()
	if p == nil {
		return taskErrUnavailable
	}
	return p.PauseAll(paused)
}

// TaskCounts 返回任务数与软上限（界面用于提示「任务偏多」）。
func (a *App) TaskCounts() (int, int) {
	p := currentDesktopPlugin()
	if p == nil {
		return 0, 0
	}
	count, limit := p.TaskCount()
	return count, limit
}

// dataDirOrEmpty 在插件不可用时也尽量给出数据目录，便于前端提示。
func (a *App) dataDirOrEmpty() string {
	if a == nil {
		return ""
	}
	return a.baseDir
}
