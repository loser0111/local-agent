package plugin

import (
	"testing"
	"time"
)

// ===== 调度计算（纯函数）=====
//
// 这一层是「提醒漏不漏、重复不重复」的根因所在，所以按附录 L1 的要求
// 把边界穷举成表：月末、闰年、跨年、多选星期、Until、MaxFires、夏令时无关的本地时间。

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation(time.RFC3339, s, time.Local)
	if err != nil {
		t.Fatalf("测试时间 %q 解析失败: %v", s, err)
	}
	return at
}

func TestNextAfterOnce(t *testing.T) {
	tr := Trigger{Kind: TriggerOnce, Once: &OnceTrigger{At: "2025-03-01T09:00:00+08:00"}}
	now := mustTime(t, "2025-03-01T08:00:00+08:00")

	got, ok := NextAfter(tr, now, time.Time{}, 0)
	if !ok || !got.Equal(mustTime(t, "2025-03-01T09:00:00+08:00")) {
		t.Fatalf("NextAfter = %v, %v", got, ok)
	}
	// 时刻已过且从未触发 → 属于「错过」范畴，NextAfter 只管未来。
	if _, ok := NextAfter(tr, mustTime(t, "2025-03-01T10:00:00+08:00"), time.Time{}, 0); ok {
		t.Error("已过去的一次性触发不应由 NextAfter 返回（应由 MissedBetween 处理）")
	}
	// 已触发过 → 终结。
	if _, ok := NextAfter(tr, now, now, 1); ok {
		t.Error("已触发过的一次性任务应终结")
	}
}

func TestNextAfterIntervalAdvancesWithoutLooping(t *testing.T) {
	// 锚点 + 每 1 小时；把 now 推到一年后，验证不会靠循环累加（会超时）也不出错。
	tr := Trigger{Kind: TriggerInterval, Interval: &IntervalTrigger{
		EveryMinutes: 60,
		StartAt:      "2024-01-01T00:00:00+08:00",
	}}
	now := mustTime(t, "2025-01-01T00:00:00+08:00")
	got, ok := NextAfter(tr, now, time.Time{}, 0)
	if !ok {
		t.Fatal("应能算出下一次")
	}
	if got.Before(now) {
		t.Errorf("下一次不应早于 now: %v < %v", got, now)
	}
	if got.Sub(now) >= time.Hour {
		t.Errorf("下一次应在 1 小时内: 差 %v", got.Sub(now))
	}
}

func TestNextAfterIntervalRespectsMinInterval(t *testing.T) {
	tr := Trigger{Kind: TriggerInterval, Interval: &IntervalTrigger{EveryMinutes: 0, StartAt: "2025-01-01T00:00:00+08:00"}}
	if err := ValidateTrigger(tr); err == nil {
		t.Error("间隔小于 1 分钟应当校验失败")
	}
	if _, ok := NextAfter(tr, mustTime(t, "2025-01-01T00:00:00+08:00"), time.Time{}, 0); ok {
		t.Error("非法间隔不应返回触发时刻")
	}
}

func TestNextAfterDaily(t *testing.T) {
	tr := Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "daily", TimeOfDay: "09:30"}}

	// 当天还没到 → 就是今天。
	got, ok := NextAfter(tr, mustTime(t, "2025-06-10T08:00:00+08:00"), time.Time{}, 0)
	if !ok || got.Format("2006-01-02 15:04") != "2025-06-10 09:30" {
		t.Fatalf("got %v, %v", got, ok)
	}
	// 当天已过 → 顺延到明天。
	got, ok = NextAfter(tr, mustTime(t, "2025-06-10T10:00:00+08:00"), time.Time{}, 0)
	if !ok || got.Format("2006-01-02 15:04") != "2025-06-11 09:30" {
		t.Fatalf("got %v, %v", got, ok)
	}
}

func TestNextAfterWeeklyMultipleWeekdays(t *testing.T) {
	// 周一(1) 与 周三(3)，09:00。2025-06-10 是周二。
	tr := Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{
		Period: "weekly", TimeOfDay: "09:00", Weekdays: []int{1, 3},
	}}
	got, ok := NextAfter(tr, mustTime(t, "2025-06-10T10:00:00+08:00"), time.Time{}, 0)
	if !ok {
		t.Fatal("应能算出下一次")
	}
	if got.Weekday() != time.Wednesday || got.Format("2006-01-02") != "2025-06-11" {
		t.Fatalf("应为 2025-06-11 周三，实际 %v(%v)", got, got.Weekday())
	}
}

func TestNextAfterMonthlyClampsToLastDay(t *testing.T) {
	tr := Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{
		Period: "monthly", TimeOfDay: "08:00", DayOfMonth: 31,
	}}
	// 2 月只有 28 天（2025 非闰年）→ 夹到 2 月 28 日，而不是整月不触发。
	got, ok := NextAfter(tr, mustTime(t, "2025-02-01T00:00:00+08:00"), time.Time{}, 0)
	if !ok || got.Format("2006-01-02") != "2025-02-28" {
		t.Fatalf("31 日在 2 月应夹到 28 日，实际 %v, %v", got, ok)
	}
	// 闰年 2 月 29 日。
	got, ok = NextAfter(tr, mustTime(t, "2024-02-01T00:00:00+08:00"), time.Time{}, 0)
	if !ok || got.Format("2006-01-02") != "2024-02-29" {
		t.Fatalf("闰年应夹到 2 月 29 日，实际 %v, %v", got, ok)
	}
}

func TestNextAfterMonthlyAcrossYearBoundary(t *testing.T) {
	tr := Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{
		Period: "monthly", TimeOfDay: "07:00", DayOfMonth: 15,
	}}
	got, ok := NextAfter(tr, mustTime(t, "2025-12-20T00:00:00+08:00"), time.Time{}, 0)
	if !ok || got.Format("2006-01-02") != "2026-01-15" {
		t.Fatalf("应跨年到 2026-01-15，实际 %v, %v", got, ok)
	}
}

func TestNextAfterRespectsUntilAndMaxFires(t *testing.T) {
	tr := Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "daily", TimeOfDay: "09:00"},
		Until: "2025-06-12T23:59:00+08:00"}
	// 截止日之前还有一次。
	if _, ok := NextAfter(tr, mustTime(t, "2025-06-12T08:00:00+08:00"), time.Time{}, 0); !ok {
		t.Error("Until 之前应还有一次触发")
	}
	// 到了截止时刻 → 结束。
	if _, ok := NextAfter(tr, mustTime(t, "2025-06-13T00:30:00+08:00"), time.Time{}, 0); ok {
		t.Error("超过 Until 之后不应再安排触发")
	}

	tr2 := Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "daily", TimeOfDay: "09:00"}, MaxFires: 3}
	if _, ok := NextAfter(tr2, mustTime(t, "2025-06-10T08:00:00+08:00"), time.Time{}, 3); ok {
		t.Error("达到 MaxFires 之后不应再安排触发")
	}
}

func TestMissedBetween(t *testing.T) {
	tr := Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "daily", TimeOfDay: "09:00"}}
	from := mustTime(t, "2025-06-01T00:00:00+08:00")
	to := mustTime(t, "2025-06-04T00:00:00+08:00")

	missed := MissedBetween(tr, from, to, time.Time{}, 0, 50)
	if len(missed) != 3 {
		t.Fatalf("应错过 3 次（6/1、6/2、6/3），实际 %d: %v", len(missed), missed)
	}
	// 上限生效。
	if got := MissedBetween(tr, from, to, time.Time{}, 0, 2); len(got) != 2 {
		t.Errorf("上限应为 2，实际 %d", len(got))
	}
}

func TestPreviewReturnsRequestedCount(t *testing.T) {
	tr := Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "daily", TimeOfDay: "09:00"}}
	got := Preview(tr, mustTime(t, "2025-06-10T12:00:00+08:00"), 3)
	if len(got) != 3 {
		t.Fatalf("应给出 3 次，实际 %d", len(got))
	}
	if !got[0].Before(got[1]) || !got[1].Before(got[2]) {
		t.Errorf("预览必须严格递增: %v", got)
	}
	// 一次性任务只能预览出 1 次。
	once := Trigger{Kind: TriggerOnce, Once: &OnceTrigger{At: "2025-06-11T09:00:00+08:00"}}
	if got := Preview(once, mustTime(t, "2025-06-10T12:00:00+08:00"), 3); len(got) != 1 {
		t.Errorf("一次性任务应只有 1 次预览，实际 %d", len(got))
	}
}

func TestValidateTriggerRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		tr   Trigger
	}{
		{"一次性缺时间", Trigger{Kind: TriggerOnce}},
		{"间隔为0", Trigger{Kind: TriggerInterval, Interval: &IntervalTrigger{EveryMinutes: 0}}},
		{"周期缺配置", Trigger{Kind: TriggerRecurring}},
		{"每周没选星期", Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "weekly", TimeOfDay: "09:00"}}},
		{"每周星期越界", Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "weekly", TimeOfDay: "09:00", Weekdays: []int{9}}}},
		{"每月日期越界", Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "monthly", TimeOfDay: "09:00", DayOfMonth: 40}}},
		{"时刻非法", Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "daily", TimeOfDay: "25:00"}}},
		{"未知类型", Trigger{Kind: "hoop"}},
		{"cron段数不对", Trigger{Kind: TriggerCron, Cron: &CronTrigger{Expr: "* * *"}}},
		{"cron名称写法不支持", Trigger{Kind: TriggerCron, Cron: &CronTrigger{Expr: "0 9 * * MON"}}},
		{"cron范围越界", Trigger{Kind: TriggerCron, Cron: &CronTrigger{Expr: "99 * * * *"}}},
	}
	for _, c := range cases {
		if err := ValidateTrigger(c.tr); err == nil {
			t.Errorf("%s：应当校验失败", c.name)
		}
	}
	valid := []Trigger{
		{Kind: TriggerOnce, Once: &OnceTrigger{At: "2025-06-11T09:00:00+08:00"}},
		{Kind: TriggerInterval, Interval: &IntervalTrigger{EveryMinutes: 1}},
		{Kind: TriggerRecurring, Recurring: &RecurringTrigger{Period: "weekly", TimeOfDay: "09:00", Weekdays: []int{0, 6}}},
		{Kind: TriggerCron, Cron: &CronTrigger{Expr: "*/15 9-18 * * 1-5"}},
	}
	for _, tr := range valid {
		if err := ValidateTrigger(tr); err != nil {
			t.Errorf("应当校验通过，却失败: %v (%+v)", err, tr)
		}
	}
}

func TestCronSubsetNext(t *testing.T) {
	// 工作日上午 9 点。
	tr := Trigger{Kind: TriggerCron, Cron: &CronTrigger{Expr: "0 9 * * 1-5"}}
	// 2025-06-13 是周五，10:00 已过 → 下一次是周一 6/16 09:00。
	got, ok := NextAfter(tr, mustTime(t, "2025-06-13T10:00:00+08:00"), time.Time{}, 0)
	if !ok {
		t.Fatal("应能算出下一次")
	}
	if got.Format("2006-01-02 15:04") != "2025-06-16 09:00" {
		t.Fatalf("应为 2025-06-16 09:00（周一），实际 %v", got)
	}
}

func TestDNDCrossMidnight(t *testing.T) {
	dnd := &DNDConfig{Enabled: true, Start: "22:00", End: "07:30"}
	cases := map[string]bool{
		"2025-06-10T23:00:00+08:00": true,
		"2025-06-10T02:00:00+08:00": true,
		"2025-06-10T07:29:00+08:00": true,
		"2025-06-10T07:30:00+08:00": false,
		"2025-06-10T12:00:00+08:00": false,
		"2025-06-10T21:59:00+08:00": false,
	}
	for s, want := range cases {
		if got := dnd.InDND(mustTime(t, s)); got != want {
			t.Errorf("InDND(%s) = %v, want %v", s, got, want)
		}
	}
	// 起止相同视为全天免打扰。
	all := &DNDConfig{Enabled: true, Start: "09:00", End: "09:00"}
	if !all.InDND(mustTime(t, "2025-06-10T03:00:00+08:00")) {
		t.Error("起止相同应视为全天免打扰")
	}
}

func TestDescribeTrigger(t *testing.T) {
	if got := DescribeTrigger(Trigger{Kind: TriggerInterval, Interval: &IntervalTrigger{EveryMinutes: 30}}); got != "每 30 分钟" {
		t.Errorf("got %q", got)
	}
	got := DescribeTrigger(Trigger{Kind: TriggerRecurring, Recurring: &RecurringTrigger{
		Period: "weekly", TimeOfDay: "09:00", Weekdays: []int{5, 1, 3},
	}})
	if got != "每周 周一、周三、周五 09:00" {
		t.Errorf("星期应排序去重: %q", got)
	}
}
