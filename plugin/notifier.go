package plugin

import (
	"strings"
	"time"
)

// ===== 通知渲染（N1–N6）=====
//
// 约束（附录 §7）：
//   - 模板只允许 7 个白名单占位符，保存时校验，渲染时未知占位符**原样保留**
//     （原样保留而不是留空：用户一眼能看出自己写错了名字）；
//   - 模型输出只作文本，绝不回填为配置、路径或命令 —— 所以这里的 Summary
//     只经过转义/压平/截断，不会被解释成任何指令；
//   - 标题 ≤40 字符、正文 ≤120 字、结论前置、失败必带原因。

// 通知模板种类。
const (
	TemplateReminder = "N1" // 定时提醒
	TemplateExecOK   = "N2" // 执行成功
	TemplateExecFail = "N3" // 执行失败
	TemplateNoResp   = "N4" // 长时间无响应告警
	TemplateMissed   = "N5" // 错过补偿
	TemplateSystem   = "N6" // 系统事件（含通知降级、熔断）
)

// 白名单占位符。
const (
	placeholderTask        = "{{task}}"
	placeholderTime        = "{{time}}"
	placeholderDate        = "{{date}}"
	placeholderWeekday     = "{{weekday}}"
	placeholderSnoozeCount = "{{snoozeCount}}"
	placeholderWorkspace   = "{{workspace}}"
	placeholderLastSummary = "{{lastSummary}}"
)

// allowedPlaceholders 是保存时用于校验的白名单。
var allowedPlaceholders = []string{
	placeholderTask, placeholderTime, placeholderDate, placeholderWeekday,
	placeholderSnoozeCount, placeholderWorkspace, placeholderLastSummary,
}

// ValidateTemplate 校验模板里没有未知占位符。
func ValidateTemplate(tpl string) error {
	for _, ph := range findPlaceholders(tpl) {
		if !isAllowedPlaceholder(ph) {
			return &UnknownPlaceholderError{Name: ph}
		}
	}
	return nil
}

// UnknownPlaceholderError 表示模板里出现了白名单外的占位符。
type UnknownPlaceholderError struct{ Name string }

func (e *UnknownPlaceholderError) Error() string {
	return "未知占位符 " + e.Name + "（可用：" + strings.Join(allowedPlaceholders, " ") + "）"
}

func isAllowedPlaceholder(ph string) bool {
	for _, a := range allowedPlaceholders {
		if a == ph {
			return true
		}
	}
	return false
}

// findPlaceholders 找出模板里的 {{name}} 形式的占位符。
func findPlaceholders(tpl string) []string {
	var out []string
	rest := tpl
	for {
		i := strings.Index(rest, "{{")
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], "}}")
		if j < 0 {
			break
		}
		out = append(out, rest[i:i+j+2])
		rest = rest[i+j+2:]
	}
	return out
}

// RenderTemplate 渲染模板；未知占位符原样保留。
func RenderTemplate(tpl string, vars map[string]string) string {
	out := tpl
	for _, ph := range findPlaceholders(tpl) {
		val, ok := vars[ph]
		if !ok {
			continue // 原样保留
		}
		out = strings.ReplaceAll(out, ph, val)
	}
	return out
}

// templateVars 生成白名单占位符的取值。
func templateVars(t *Task, title, body string) map[string]string {
	now := time.Now()
	weekday := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[int(now.Weekday())]
	taskName := ""
	snoozeCount := "0"
	workspace := ""
	lastSummary := body
	if t != nil {
		taskName = t.Title
		if t.State.Snooze != nil {
			snoozeCount = itoa(t.State.Snooze.Count)
		}
		if t.Exec != nil && t.Exec.WorkDir != "" {
			workspace = t.Exec.WorkDir
		}
	}
	return map[string]string{
		placeholderTask:        taskName,
		placeholderTime:        now.Format("15:04"),
		placeholderDate:        now.Format("2006-01-02"),
		placeholderWeekday:     weekday,
		placeholderSnoozeCount: snoozeCount,
		placeholderWorkspace:   workspace,
		placeholderLastSummary: lastSummary,
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// RenderNotification 渲染一类通知（N1–N6）。
//
// 返回的 NotifyRequest 已满足长度与格式约束；调用方只需决定投递方式。
func RenderNotification(kind string, t *Task, at time.Time, extra string) NotifyRequest {
	title, body, level := notificationContent(kind, t, at, extra)
	req := BuildNotification(t, level, title, body)
	if t != nil {
		triggerID := at.Local().Format("20060102T150405")
		req.ID = "task:" + t.ID + ":" + triggerID
		req.Data["triggerId"] = triggerID
	}
	return req
}

// notificationContent 给出每类通知的默认标题与正文。
//
// 失败类（N3）必须带原因：只显示「失败」是明令禁止的，所以这里把
// extra（失败原因）直接拼进正文，缺原因时也给一句可读兜底。
func notificationContent(kind string, t *Task, at time.Time, extra string) (title, body string, level NotifyLevel) {
	name := "定时任务"
	note := ""
	if t != nil {
		if strings.TrimSpace(t.Title) != "" {
			name = t.Title
		}
		note = oneLine(t.Note)
	}
	hhmm := at.Local().Format("15:04")

	switch kind {
	case TemplateReminder:
		level = LevelNormal
		title = "提醒 · " + name
		if note != "" {
			body = note
		} else {
			body = "到 " + hhmm + " 了，该处理这件事了。"
		}
	case TemplateExecOK:
		level = LevelNormal
		title = "完成 · " + name
		if strings.TrimSpace(extra) != "" {
			body = "已执行完成：" + extra
		} else {
			body = "任务已执行完成。"
		}
	case TemplateExecFail:
		level = LevelUrgent
		title = "失败 · " + name
		reason := strings.TrimSpace(extra)
		if reason == "" {
			reason = "未提供具体原因（请查看历史记录）"
		}
		body = "执行失败：" + reason
	case TemplateNoResp:
		level = LevelUrgent
		title = "无响应 · " + name
		reason := strings.TrimSpace(extra)
		if reason == "" {
			reason = "长时间没有进展"
		}
		body = reason + "，可中止后重新执行。"
	case TemplateMissed:
		level = LevelAttention
		title = "已补发 · " + name
		body = "错过了 " + hhmm + " 的提醒，现已补发。"
	case TemplateSystem:
		level = LevelAttention
		title = "定时任务 · 注意"
		body = extra
		if strings.TrimSpace(body) == "" {
			body = "插件发生了一处需要你了解的情况。"
		}
	default:
		level = LevelNormal
		title = name
		body = extra
	}
	return title, body, level
}
