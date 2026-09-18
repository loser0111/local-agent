package plugin

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ===== 调度计算（纯函数）=====
//
// 本文件是调度器的「心脏」，刻意保持**纯函数**：
//   - 不碰 IO、不碰 Clock、不用全局状态；
//   - 时间一律由参数传入，返回时间一律是本机本地时间。
//
// 这样月末、闰年、跨年、多选星期、工作日、Until/MaxFires 等边界
// 都能被表驱动单测穷举（附录 L1），而调度循环本身只剩「睡到下一个时刻」。

// NextAfter 返回规则在 now 之后（含 now 这一刻）的下一个触发时刻。
//
// 参数：
//   - tr          触发规则
//   - now         参照时刻
//   - lastFiredAt 上次触发时刻，零值表示从未触发
//   - firedCount  已触发次数（用于 MaxFires）
//
// 返回 ok=false 表示「规则已终结」：一次性已触发过、超过 Until、或达到 MaxFires。
//
// 注意：**已经过去且从未触发的时刻不在本函数的返回范围内** —— 那属于「错过」，
// 由 MissedBetween 找出并按 F2.7 的补偿策略处理。这样职责边界清晰：
// NextAfter 只管「未来」，MissedBetween 只管「过去」。
func NextAfter(tr Trigger, now, lastFiredAt time.Time, firedCount int) (time.Time, bool) {
	now = now.Local()
	if !tr.allowsMoreFires(now, firedCount) {
		return time.Time{}, false
	}

	switch tr.Kind {
	case TriggerOnce:
		at, err := parseTime(tr.Once.At)
		if err != nil || tr.Once == nil {
			return time.Time{}, false
		}
		if !at.After(now) {
			// 已过去：要么已经触发过（firedCount>0，前面已拦），要么属于「错过」范畴。
			return time.Time{}, false
		}
		return at.Local(), true

	case TriggerInterval:
		return nextInterval(tr.Interval, now, lastFiredAt)

	case TriggerRecurring:
		return nextRecurring(tr.Recurring, now, lastFiredAt, tr.Until)

	case TriggerCron:
		return nextCron(tr.Cron, now, tr.Until)

	default:
		return time.Time{}, false
	}
}

// allowsMoreFires 判断规则本身是否还允许触发。
func (tr Trigger) allowsMoreFires(now time.Time, firedCount int) bool {
	if tr.MaxFires > 0 && firedCount >= tr.MaxFires {
		return false
	}
	if until, err := parseTime(tr.Until); err == nil && !until.IsZero() {
		if !now.Before(until.Local()) {
			// 已到截止时刻：不再安排新触发。
			return false
		}
	}
	switch tr.Kind {
	case TriggerOnce:
		// 一次性任务只要触发过一次就终结（与 MaxFires 无关）。
		return tr.Once != nil && firedCount == 0
	case TriggerInterval:
		return tr.Interval != nil && tr.Interval.EveryMinutes >= minIntervalMinutes
	case TriggerRecurring:
		return tr.Recurring != nil
	case TriggerCron:
		return tr.Cron != nil
	}
	return false
}

// nextInterval 计算固定间隔的下一次。
//
// 锚点是 StartAt（为空则用 lastFiredAt，再为空则无法计算）。
// 用算术推进而不是循环累加：间隔 1 分钟、时钟跳变一年时循环要跑 52 万次。
func nextInterval(it *IntervalTrigger, now, lastFiredAt time.Time) (time.Time, bool) {
	if it == nil || it.EveryMinutes < minIntervalMinutes {
		return time.Time{}, false
	}
	every := time.Duration(it.EveryMinutes) * time.Minute

	anchor, err := parseTime(it.StartAt)
	if err != nil {
		return time.Time{}, false
	}
	if anchor.IsZero() {
		anchor = lastFiredAt
	}
	if anchor.IsZero() {
		// 既没有锚点也没有历史：无法推导出确定时刻（保存时 StartAt 必填，走到这里说明数据被改坏了）。
		return time.Time{}, false
	}
	anchor = anchor.Local()

	// 用整数算术求「第一个不早于 now 的 candidate」，而不是循环累加：
	// 间隔 1 分钟、时钟跳变一年时，循环要跑 52 万次。
	d := now.Sub(anchor)
	if d < 0 {
		return anchor.Add(every), true
	}
	steps := int64(d+every-1) / int64(every) // 向上取整
	if steps < 1 {
		steps = 1
	}
	candidate := anchor.Add(time.Duration(steps) * every)

	// 已触发过的时刻之后才算「下一次」。
	if !lastFiredAt.IsZero() && !candidate.After(lastFiredAt.Local()) {
		steps = int64(lastFiredAt.Sub(anchor)+every-1)/int64(every) + 1
		candidate = anchor.Add(time.Duration(steps) * every)
	}
	return candidate, true
}

// nextRecurring 计算每日 / 每周 / 每月的下一次。
func nextRecurring(rt *RecurringTrigger, now, lastFiredAt time.Time, until string) (time.Time, bool) {
	if rt == nil {
		return time.Time{}, false
	}
	minuteOfDay, err := parseClock(rt.TimeOfDay)
	if err != nil {
		return time.Time{}, false
	}
	var limit time.Time
	if u, err := parseTime(until); err == nil {
		limit = u
	}
	// 最多向前找 5 年，足够覆盖「每月 31 日遇上连续小月」之类的极端情况。
	maxDays := 366 * 5
	for d := 0; d <= maxDays; d++ {
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, d)
		if !limit.IsZero() && !day.Before(limit) {
			return time.Time{}, false
		}
		cand, ok := occurrenceOn(day, rt, minuteOfDay)
		if !ok {
			continue
		}
		if cand.Before(now) {
			continue
		}
		if !lastFiredAt.IsZero() && !cand.After(lastFiredAt.Local()) {
			continue
		}
		return cand, true
	}
	return time.Time{}, false
}

// occurrenceOn 判断某一天是否存在符合规则的触发时刻。
//
// 每月规则里「31 日」遇上只有 30 天的月份：夹到当月最后一天。
// 这是日历类应用最不容易让用户困惑的处理方式（不会整月不触发）。
func occurrenceOn(day time.Time, rt *RecurringTrigger, minuteOfDay int) (time.Time, bool) {
	switch strings.ToLower(strings.TrimSpace(rt.Period)) {
	case "daily", "":
		return day.Add(time.Duration(minuteOfDay) * time.Minute), true
	case "weekly":
		if len(rt.Weekdays) == 0 {
			return time.Time{}, false
		}
		wd := int(day.Weekday())
		for _, w := range rt.Weekdays {
			if w == wd {
				return day.Add(time.Duration(minuteOfDay) * time.Minute), true
			}
		}
		return time.Time{}, false
	case "monthly":
		dom := rt.DayOfMonth
		if dom <= 0 {
			return time.Time{}, false
		}
		lastDay := time.Date(day.Year(), day.Month()+1, 0, 0, 0, 0, 0, time.Local).Day()
		want := dom
		if want > lastDay {
			want = lastDay
		}
		if day.Day() != want {
			return time.Time{}, false
		}
		return day.Add(time.Duration(minuteOfDay) * time.Minute), true
	}
	return time.Time{}, false
}

// MissedBetween 找出 (from, to] 区间内规则本应触发、却没有触发的时刻。
//
// 用于休眠唤醒、系统时间跳变、应用未运行时段的补偿（F2.7 / F2.10）。
// 返回结果按时间升序，最多 cap 条（防止长时间关机后炸出上万条）。
func MissedBetween(tr Trigger, from, to time.Time, lastFiredAt time.Time, firedCount int, cap int) []time.Time {
	from, to = from.Local(), to.Local()
	if !to.After(from) {
		return nil
	}
	if cap <= 0 {
		cap = maxMissedPending
	}
	var out []time.Time
	cur := from
	// 逐次推进：每次都基于上一次结果算下一次，天然处理间隔与日历规则。
	for {
		next, ok := NextAfter(tr, cur, lastFiredAt, firedCount+len(out))
		if !ok {
			break
		}
		if next.After(to) {
			break
		}
		out = append(out, next)
		if len(out) >= cap {
			break
		}
		// 前进一个最小刻度，避免 next == cur 时死循环。
		cur = next.Add(time.Second)
	}
	return out
}

// Preview 返回从现在起的未来 n 次触发时刻（F2.8：保存前必须能展示）。
//
// 前端拿它做「未来 3 次触发时间」预览；cron 与 calendar 规则没有预览就不允许保存。
func Preview(tr Trigger, now time.Time, n int) []time.Time {
	if n <= 0 {
		n = 3
	}
	out := make([]time.Time, 0, n)
	cur := now.Local()
	for i := 0; i < n; i++ {
		next, ok := NextAfter(tr, cur, time.Time{}, i)
		if !ok {
			break
		}
		out = append(out, next)
		cur = next.Add(time.Second)
	}
	return out
}

// ValidateTrigger 校验触发规则；新建/编辑任务时调用，不通过不允许保存。
func ValidateTrigger(tr Trigger) error {
	switch tr.Kind {
	case TriggerOnce:
		if tr.Once == nil {
			return fmt.Errorf("一次性任务缺少触发时间")
		}
		at, err := parseTime(tr.Once.At)
		if err != nil {
			return err
		}
		if at.IsZero() {
			return fmt.Errorf("一次性任务必须指定触发时间")
		}
	case TriggerInterval:
		if tr.Interval == nil {
			return fmt.Errorf("间隔任务缺少间隔配置")
		}
		if tr.Interval.EveryMinutes < minIntervalMinutes {
			return fmt.Errorf("间隔不能小于 %d 分钟", minIntervalMinutes)
		}
		if tr.Interval.StartAt != "" {
			if _, err := parseTime(tr.Interval.StartAt); err != nil {
				return err
			}
		}
	case TriggerRecurring:
		if tr.Recurring == nil {
			return fmt.Errorf("周期任务缺少周期配置")
		}
		if _, err := parseClock(tr.Recurring.TimeOfDay); err != nil {
			return fmt.Errorf("触发时刻非法: %w", err)
		}
		switch strings.ToLower(strings.TrimSpace(tr.Recurring.Period)) {
		case "daily", "":
		case "weekly":
			if len(tr.Recurring.Weekdays) == 0 {
				return fmt.Errorf("每周任务至少要选择一个星期")
			}
			for _, w := range tr.Recurring.Weekdays {
				if w < 0 || w > 6 {
					return fmt.Errorf("星期取值非法: %d（应为 0-6）", w)
				}
			}
		case "monthly":
			if tr.Recurring.DayOfMonth < 1 || tr.Recurring.DayOfMonth > 31 {
				return fmt.Errorf("每月任务必须指定 1-31 的日期")
			}
		default:
			return fmt.Errorf("未知周期 %q（只支持 daily / weekly / monthly）", tr.Recurring.Period)
		}
	case TriggerCron:
		if tr.Cron == nil || strings.TrimSpace(tr.Cron.Expr) == "" {
			return fmt.Errorf("cron 任务缺少表达式")
		}
		if _, err := parseCron(tr.Cron.Expr); err != nil {
			return err
		}
	default:
		return fmt.Errorf("未知触发类型 %q", tr.Kind)
	}

	if tr.MaxFires < 0 {
		return fmt.Errorf("最大触发次数不能为负")
	}
	if tr.Until != "" {
		if _, err := parseTime(tr.Until); err != nil {
			return err
		}
	}
	return nil
}

// ===== cron 子集（P1）=====
//
// 只支持五段表达式的常见子集：* / 单值 / a-b / a,b,c / */n / a-b/n。
// 不支持 L、W、?、@monthly、月份与星期英文名等 —— 明确的错误提示好过静默算错。
// 与文档一致：**没有预览就不允许保存**，所以 ValidateTrigger 会拒绝不支持的写法。

const cronMaxSearchDays = 366

type cronSpec struct {
	minute, hour, dom, month, dow [64]bool
	domStar, dowStar              bool
}

func (c *cronSpec) matches(t time.Time) bool {
	if !c.minute[t.Minute()] || !c.hour[t.Hour()] || !c.month[int(t.Month())] {
		return false
	}
	domOK := c.domStar || c.dom[t.Day()]
	dowOK := c.dowStar || c.dow[int(t.Weekday())]
	// 标准 cron 语义：dom 与 dow 都非 * 时取「或」，否则取「与」。
	if !c.domStar && !c.dowStar {
		return domOK || dowOK
	}
	return domOK && dowOK
}

func parseCron(expr string) (*cronSpec, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron 表达式必须是 5 段（分 时 日 月 周），实际 %d 段", len(fields))
	}
	spec := &cronSpec{}
	var err error
	if spec.minute, err = parseCronField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("cron 分钟段非法: %w", err)
	}
	if spec.hour, err = parseCronField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("cron 小时段非法: %w", err)
	}
	if spec.dom, err = parseCronField(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("cron 日期段非法: %w", err)
	}
	if spec.month, err = parseCronField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("cron 月份段非法: %w", err)
	}
	// 星期允许 7 表示周日。
	if spec.dow, err = parseCronField(fields[4], 0, 7); err != nil {
		return nil, fmt.Errorf("cron 星期段非法: %w", err)
	}
	if spec.dow[7] {
		spec.dow[0] = true
	}
	spec.domStar = fields[2] == "*"
	spec.dowStar = fields[4] == "*"
	return spec, nil
}

func parseCronField(field string, min, max int) ([64]bool, error) {
	var out [64]bool
	if strings.TrimSpace(field) == "" {
		return out, fmt.Errorf("字段为空")
	}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return out, fmt.Errorf("含空白的列表项")
		}
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			s, err := strconv.Atoi(part[i+1:])
			if err != nil || s <= 0 {
				return out, fmt.Errorf("%q 的步长非法", part)
			}
			step = s
			part = part[:i]
		}
		lo, hi := min, max
		switch {
		case part == "*":
			// 保持全范围
		case strings.Contains(part, "-"):
			bounds := strings.SplitN(part, "-", 2)
			a, err1 := strconv.Atoi(strings.TrimSpace(bounds[0]))
			b, err2 := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if err1 != nil || err2 != nil {
				return out, fmt.Errorf("%q 不是合法的区间", part)
			}
			lo, hi = a, b
		default:
			v, err := strconv.Atoi(part)
			if err != nil {
				return out, fmt.Errorf("%q 不是数字（本实现不支持名称写法）", part)
			}
			lo, hi = v, v
		}
		if lo < min || hi > max || lo > hi {
			return out, fmt.Errorf("%q 超出取值范围 %d-%d", part, min, max)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	return out, nil
}

// nextCron 逐分钟搜索下一次匹配。
//
// 这是纯函数内部的搜索（只在重算时执行一次），不是运行期轮询。
// 一年之内找不到就判定为「没有下次」，避免写错的表达式把界面拖死。
func nextCron(ct *CronTrigger, now time.Time, until string) (time.Time, bool) {
	if ct == nil {
		return time.Time{}, false
	}
	spec, err := parseCron(ct.Expr)
	if err != nil {
		return time.Time{}, false
	}
	var limit time.Time
	if u, err := parseTime(until); err == nil {
		limit = u
	}
	cur := now.Local().Truncate(time.Minute).Add(time.Minute)
	deadline := cur.AddDate(0, 0, cronMaxSearchDays)
	for !cur.After(deadline) {
		if !limit.IsZero() && !cur.Before(limit) {
			return time.Time{}, false
		}
		if spec.matches(cur) {
			return cur, true
		}
		cur = cur.Add(time.Minute)
	}
	return time.Time{}, false
}

// SortedWeekdays 返回排序去重后的星期列表，供界面展示。
func SortedWeekdays(in []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(in))
	for _, w := range in {
		if w < 0 || w > 6 || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	sort.Ints(out)
	return out
}

// DescribeTrigger 生成人类可读的规则描述，用于列表与通知。
func DescribeTrigger(tr Trigger) string {
	switch tr.Kind {
	case TriggerOnce:
		if at, err := parseTime(tr.Once.At); err == nil && !at.IsZero() {
			return "一次性 · " + at.Local().Format("2006-01-02 15:04")
		}
		return "一次性"
	case TriggerInterval:
		if tr.Interval != nil {
			return fmt.Sprintf("每 %d 分钟", tr.Interval.EveryMinutes)
		}
		return "固定间隔"
	case TriggerRecurring:
		if tr.Recurring == nil {
			return "周期"
		}
		switch strings.ToLower(strings.TrimSpace(tr.Recurring.Period)) {
		case "weekly":
			names := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
			var parts []string
			for _, w := range SortedWeekdays(tr.Recurring.Weekdays) {
				parts = append(parts, names[w])
			}
			return fmt.Sprintf("每周 %s %s", strings.Join(parts, "、"), tr.Recurring.TimeOfDay)
		case "monthly":
			return fmt.Sprintf("每月 %d 日 %s", tr.Recurring.DayOfMonth, tr.Recurring.TimeOfDay)
		default:
			return "每天 " + tr.Recurring.TimeOfDay
		}
	case TriggerCron:
		if tr.Cron != nil {
			return "cron · " + tr.Cron.Expr
		}
		return "cron"
	}
	return "未知规则"
}
