package plugin

import (
	"sync"
	"testing"
	"time"
)

// ===== 调度循环（真实 goroutine + 可控时钟）=====
//
// 前面的用例都是直接调 tick()，绕过了 sched.go 的循环本身。但「不漏提醒」
// 的成败恰恰取决于这个循环：睡多久、什么时候重算、出错会不会把它打死。
// 所以这里用可控时钟驱动**真实的 goroutine**，把这几条钉住。

// manualClock 是完全受控的时钟：After 注册等待者，Advance 推进并唤醒到期的。
type manualClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []manualWaiter
	afters  int // After 被调用的次数，用于证明「没有轮询」
}

type manualWaiter struct {
	deadline time.Time
	ch       chan time.Time
}

func newManualClock(start time.Time) *manualClock {
	return &manualClock{now: start}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.afters++
	ch := make(chan time.Time, 1)
	deadline := c.now.Add(d)
	// 已经到点的直接立刻返回，避免测试里出现「注册完才推进」的竞态。
	if !deadline.After(c.now) {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, manualWaiter{deadline: deadline, ch: ch})
	return ch
}

// Advance 推进时钟并唤醒所有到期的等待者（非阻塞发送）。
func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.deadline.After(now) {
			select {
			case w.ch <- now:
			default:
			}
			continue
		}
		kept = append(kept, w)
	}
	c.waiters = kept
	c.mu.Unlock()
}

// afterCalls 返回 After 的调用次数。
func (c *manualClock) afterCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.afters
}

// waitFor 轮询等待条件成立（上限 timeout），避免用固定 sleep 造成偶发失败。
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

func TestSchedulerFiresAtDueTimeNotBefore(t *testing.T) {
	clock := newManualClock(baseTime())
	due := baseTime().Add(5 * time.Minute)

	var mu sync.Mutex
	fires := []time.Time{}
	next := func(time.Time) (time.Time, bool) { return due, true }
	s := NewScheduler(clock, next, func(now time.Time) {
		mu.Lock()
		fires = append(fires, now)
		mu.Unlock()
	}, nil)
	s.Start()
	defer s.Stop()

	// 先等循环注册好定时器，否则「推进时钟」可能跑在注册之前（测试自身的竞态）。
	waitFor(t, "进入睡眠", time.Second, func() bool { return clock.afterCalls() > 0 })

	// 还没到点：绝不能提前触发。
	clock.Advance(4 * time.Minute)
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	if len(fires) != 0 {
		t.Fatalf("未到点就触发了 %d 次", len(fires))
	}
	mu.Unlock()

	// 到点：应当触发一次。
	clock.Advance(time.Minute)
	waitFor(t, "到点后触发一次", time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(fires) >= 1
	})
}

func TestSchedulerDoesNotPoll(t *testing.T) {
	clock := newManualClock(baseTime())
	// 没有任何待触发任务：这是最容易写成「每秒检查一次」的场景。
	next := func(time.Time) (time.Time, bool) { return time.Time{}, false }
	s := NewScheduler(clock, next, func(time.Time) {
		t.Error("没有任务时不应触发")
	}, nil)
	s.Start()
	defer s.Stop()

	before := clock.afterCalls()
	// 推进 1 小时。若实现是每秒轮询，这里会产生 3600 次 After。
	for i := 0; i < 12; i++ {
		clock.Advance(5 * time.Minute)
		time.Sleep(5 * time.Millisecond)
	}
	calls := clock.afterCalls() - before
	// maxSleep=15min，1 小时最多约 4 次唤醒；给足余量也不该超过 10。
	if calls > 10 {
		t.Errorf("1 小时内唤醒了 %d 次，说明存在轮询（期望是个位数）", calls)
	}
}

func TestSchedulerWakeForcesRecompute(t *testing.T) {
	clock := newManualClock(baseTime())
	var mu sync.Mutex
	var due time.Time
	var has bool
	fired := 0

	next := func(time.Time) (time.Time, bool) {
		mu.Lock()
		defer mu.Unlock()
		return due, has
	}
	s := NewScheduler(clock, next, func(time.Time) {
		mu.Lock()
		fired++
		mu.Unlock()
	}, nil)
	s.Start()
	defer s.Stop()

	// 先让它按「无任务」进入长睡眠（15 分钟）。
	waitFor(t, "调度循环已启动", time.Second, func() bool { return clock.afterCalls() > 0 })
	registered := clock.afterCalls()

	// 用户在 1 分钟后新增了一条任务 —— 若不唤醒，循环会一直睡到 15 分钟后。
	mu.Lock()
	due = baseTime().Add(time.Minute)
	has = true
	mu.Unlock()
	s.Wake()

	// 等它按新规则重新注册定时器（等待时长从 15 分钟变成 1 分钟）。
	waitFor(t, "被唤醒后重算等待时长", time.Second, func() bool { return clock.afterCalls() > registered })

	clock.Advance(time.Minute)
	waitFor(t, "被唤醒后按新规则触发", time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return fired >= 1
	})
}

func TestSchedulerSurvivesClockJumpForward(t *testing.T) {
	clock := newManualClock(baseTime())
	due := baseTime().Add(10 * time.Hour) // 远在未来

	var mu sync.Mutex
	fired := false
	next := func(now time.Time) (time.Time, bool) { return due, true }
	s := NewScheduler(clock, next, func(time.Time) {
		mu.Lock()
		fired = true
		mu.Unlock()
	}, nil)
	s.Start()
	defer s.Stop()

	waitFor(t, "进入睡眠", time.Second, func() bool { return clock.afterCalls() > 0 })

	// 用户把系统时间往前调了 11 小时（或机器休眠后唤醒）：
	// 已排定的绝对时刻不再可信，必须基于新的 now 立刻处理。
	clock.Advance(11 * time.Hour)
	waitFor(t, "时钟跳变后尽快触发", time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return fired
	})
}

func TestSchedulerIsolatesPanicInCallback(t *testing.T) {
	clock := newManualClock(baseTime())
	due := baseTime().Add(time.Minute)

	var mu sync.Mutex
	calls := 0
	next := func(time.Time) (time.Time, bool) { return due, true }
	s := NewScheduler(clock, next, func(time.Time) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			panic("第一次触发故意 panic")
		}
	}, nil)
	s.Start()
	defer s.Stop()

	waitFor(t, "进入睡眠", time.Second, func() bool { return clock.afterCalls() > 0 })

	clock.Advance(time.Minute)
	waitFor(t, "第一次触发", time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls >= 1
	})

	// 关键：panic 不得把调度 goroutine 打死 —— 否则此后完全静默，
	// 而用户毫无察觉（这正是「漏提醒」最危险的形态）。
	clock.Advance(time.Minute)
	waitFor(t, "panic 后仍能继续触发", time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls >= 2
	})
}

func TestSchedulerStopIsPromptWhileSleeping(t *testing.T) {
	clock := newManualClock(baseTime())
	// 让它睡满 maxSleep（15 分钟）。
	next := func(time.Time) (time.Time, bool) { return time.Time{}, false }
	s := NewScheduler(clock, next, func(time.Time) {}, nil)
	s.Start()

	waitFor(t, "进入睡眠", time.Second, func() bool { return clock.afterCalls() > 0 })

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop 未及时返回（退出流程会被拖住）")
	}
	// 幂等：重复 Stop 不应 panic 或阻塞。
	s.Stop()
}

func TestSchedulerStartStopIdempotent(t *testing.T) {
	clock := newManualClock(baseTime())
	s := NewScheduler(clock, func(time.Time) (time.Time, bool) { return time.Time{}, false }, func(time.Time) {}, nil)
	s.Start()
	s.Start() // 不应起第二个循环
	s.Stop()
	s.Stop()
	// 停止后再 Wake 不应 panic。
	s.Wake()
}
