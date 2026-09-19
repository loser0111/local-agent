package plugin

import "time"

// ===== 可注入时钟 =====
//
// 约束（附录 §6 可测试性）：插件内部**不得直接调用 time.Now()**，
// 一律走本接口，测试可注入假时钟来驱动月末/闰年/跨年/休眠唤醒等边界。
//
// NextAfter 等调度计算是纯函数，直接接收 now 参数，连 Clock 都不依赖。

// Clock 是插件使用的最小时间源。
type Clock interface {
	// Now 返回当前时间。
	Now() time.Time
	// After 返回一个在 d 之后触发的通道（用于调度循环睡眠）。
	After(d time.Duration) <-chan time.Time
}

// systemClock 是 Clock 的默认实现。
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// defaultClock 在 Options 未指定时使用。
func defaultClock() Clock { return systemClock{} }
