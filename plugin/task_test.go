package plugin

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// ===== 数据模型契约 =====

// TestTaskViewCoversEveryTaskField 是**字段漂移护栏**。
//
// TaskView 是 Task 的对外投影，字段是手写列出的（刻意不用内嵌，见 task.go 的注释）。
// 手写就会有「Task 加了字段、TaskView 忘了加」的风险 —— 而且这种遗漏很隐蔽：
// 编译通过、测试通过，只有界面上那个字段永远是空的。
//
// 所以这里用反射 + JSON 标签把两者的字段集钉死：
//
//	TaskView 的 JSON 字段集 == Task 的 JSON 字段集 + {nextFireAt, snoozeUntil}
func TestTaskViewCoversEveryTaskField(t *testing.T) {
	taskKeys := jsonKeys(reflect.TypeOf(Task{}))
	viewKeys := jsonKeys(reflect.TypeOf(TaskView{}))

	// TaskView 允许比 Task 多出的派生字段。
	extraAllowed := map[string]bool{"nextFireAt": true, "snoozeUntil": true}

	var missing []string
	for _, k := range taskKeys {
		if !contains(viewKeys, k) {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Errorf("TaskView 缺少 Task 的字段（会导致界面上该字段永远为空）: %v", missing)
	}

	var extra []string
	for _, k := range viewKeys {
		if !contains(taskKeys, k) && !extraAllowed[k] {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		t.Errorf("TaskView 出现未预期的字段: %v", extra)
	}

	// 反向钉一遍：派生的 nextFireAt / snoozeUntil **不得**出现在 Task 上，
	// 否则它们会被写进 tasks.json。
	for _, k := range []string{"nextFireAt", "snoozeUntil"} {
		if contains(taskKeys, k) {
			t.Errorf("Task 不应包含派生字段 %q —— 它会被持久化", k)
		}
	}
}

// TestTaskViewJSONRoundTrip 确认视图字段名与 JSON 键一一对应（界面按这些键取值）。
func TestTaskViewJSONRoundTrip(t *testing.T) {
	task := &Task{
		ID: "t1", Title: "标题", Note: "备注", Kind: KindReminder, Enabled: true,
		Trigger:   Trigger{Kind: TriggerOnce, Once: &OnceTrigger{At: "2099-01-01T09:00:00+08:00"}},
		CreatedAt: "2025-01-01T00:00:00+08:00",
		UpdatedAt: "2025-01-01T00:00:00+08:00",
	}
	task.State.LastResult = ResultNotified
	view := task.view(mustTime(t, "2099-01-01T09:00:00+08:00"), true)

	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}

	// 界面依赖这些键；键名一旦变化，界面会静默取到 undefined。
	for _, k := range []string{"id", "title", "kind", "trigger", "enabled", "state", "createdAt", "updatedAt", "nextFireAt"} {
		if _, ok := m[k]; !ok {
			t.Errorf("视图 JSON 缺少键 %q", k)
		}
	}
	if m["nextFireAt"] == "" || m["nextFireAt"] == nil {
		t.Error("有下次触发时 nextFireAt 不应为空")
	}
	// 有 snooze 时才出现 snoozeUntil（omitempty）。
	if _, ok := m["snoozeUntil"]; ok {
		t.Error("没有 snooze 时不应输出 snoozeUntil")
	}
	state, ok := m["state"].(map[string]any)
	if !ok {
		t.Fatal("state 应是对象")
	}
	if state["lastResult"] != ResultNotified {
		t.Errorf("state.lastResult = %v, want %s", state["lastResult"], ResultNotified)
	}
}

// TestTaskValidateRejectsBadKinds 覆盖 F1.1「校验通过才可存」的类型分支。
func TestTaskValidateRejectsBadKinds(t *testing.T) {
	base := func() *Task {
		return &Task{
			ID: "t1", Title: "x", Kind: KindReminder,
			Trigger: Trigger{Kind: TriggerOnce, Once: &OnceTrigger{At: "2099-01-01T09:00:00+08:00"}},
		}
	}

	cases := []struct {
		name    string
		mutate  func(*Task)
		wantErr bool
	}{
		{"正常提醒型", func(*Task) {}, false},
		{"名称过长", func(tk *Task) { tk.Title = string(make([]rune, maxTitleRunes+1)) }, true},
		{"未知类型", func(tk *Task) { tk.Kind = "weird" }, true},
		{"执行型无 prompt", func(tk *Task) { tk.Kind = KindExec }, true},
		{"执行型有 prompt", func(tk *Task) { tk.Kind = KindExec; tk.Exec = &ExecConf{Prompt: "跑"} }, false},
		{"执行型超时为负", func(tk *Task) { tk.Kind = KindExec; tk.Exec = &ExecConf{Prompt: "跑", TimeoutSeconds: -1} }, true},
		{"通知级别非法", func(tk *Task) { tk.Notify = &NotifyConf{Level: "L9"} }, true},
		{"通知级别合法", func(tk *Task) { tk.Notify = &NotifyConf{Level: LevelUrgent} }, false},
		{"DND 起止非法", func(tk *Task) {
			tk.Policy = &Policy{DNDOverride: &DNDConfig{Enabled: true, Start: "99:99", End: "07:00"}}
		}, true},
	}
	for _, c := range cases {
		tk := base()
		c.mutate(tk)
		err := tk.Validate()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

func jsonKeys(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Tag.Get("json")
		if name == "" {
			name = f.Name
		}
		if idx := indexByte(name, ','); idx >= 0 {
			name = name[:idx]
		}
		if name == "-" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
