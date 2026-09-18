package plugin

import (
	"sync"
	"time"
)

// ===== 调度循环（单一 goroutine）=====
//
// 硬性约束（附录 §6）：
//   - **禁止轮询**：绝不为每个任务起 time.Ticker，也绝不用「每秒检查一次」兜底；
//   - 只有一个循环，睡到「所有任务里最近的下次触发时刻」；
//   - 时钟跳变鲁棒：不信任已排定的绝对时刻。睡眠用的是**时长**（Clock.After(d)），
//     醒来后用当下的 now 重新计算全部下次时刻 —— 因此系统时间往前跳会立刻触发，
//     往后跳会自然顺延，不需要信任任何先前算好的绝对时间。

// maxSleep 是单次睡眠的上限。
//
// 这不是轮询兜底：它把「时钟被往后调」造成的长时间失联限制在可控范围内，
// 代价是最多每小时唤醒 4 次（一次计算，几乎零开销），换来的是
// 「用户把系统时间调回去一小时、任务不会因此晚一小时才醒」。
const maxSleep = 15 * time.Minute

// minOverdueWait 是「已经到点/有积压」时的最小等待间隔。
//
// 已经到点时**不能睡**（睡了就等于把提醒静默丢掉），但也**不能空转**：
// 万一 nextFn 因为上游 bug 持续返回过去时刻，零等待会把 CPU 打满，
// 直接违反「空闲 CPU < 0.1%」的硬指标。200ms 既保证很快处理，
// 又把最坏情况限制在 5 次/秒的自愈循环上。
const minOverdueWait = 200 * time.Millisecond

// Scheduler 是单 goroutine 调度循环。
//
// nextFn 由调用方提供：返回「全部任务里最近的下次触发时刻」。
// fireFn 在到点时被调用一次（由调用方负责做错过检查与实际触发）。
// 两个回调都在调度 goroutine 里执行，必须自己保证不阻塞太久。
type Scheduler struct {
	clock  Clock
	nextFn func(now time.Time) (time.Time, bool)
	fireFn func(now time.Time)
	logf   func(format string, args ...any)

	mu      sync.Mutex
	stopCh  chan struct{}
	wakeCh  chan struct{}
	doneCh  chan struct{}
	running bool
}

// NewScheduler 构造调度器。
func NewScheduler(clock Clock, nextFn func(time.Time) (time.Time, bool), fireFn func(time.Time), logf func(string, ...any)) *Scheduler {
	if clock == nil {
		clock = defaultClock()
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Scheduler{clock: clock, nextFn: nextFn, fireFn: fireFn, logf: logf}
}

// Start 启动调度循环（幂等）。
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.stopCh = make(chan struct{})
	s.wakeCh = make(chan struct{}, 1)
	s.doneCh = make(chan struct{})
	s.running = true
	stopCh, wakeCh, doneCh := s.stopCh, s.wakeCh, s.doneCh
	s.mu.Unlock()

	go s.loop(stopCh, wakeCh, doneCh)
}

// Stop 停止调度循环并等待其退出（幂等）。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	stopCh, doneCh := s.stopCh, s.doneCh
	s.mu.Unlock()

	close(stopCh)
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		// 回调可能正卡在别处；不能在这里无限等，否则退出流程会被拖住。
		s.logf("调度循环未在超时内退出")
	}
}

// Wake 立即唤醒调度循环重算（任务是新增/修改/删除后调用）。
//
// 用带缓冲的通道 + 非阻塞发送：连点保存不会堆积，最多一次待处理唤醒。
func (s *Scheduler) Wake() {
	s.mu.Lock()
	wakeCh := s.wakeCh
	running := s.running
	s.mu.Unlock()
	if !running || wakeCh == nil {
		return
	}
	select {
	case wakeCh <- struct{}{}:
	default:
	}
}

func (s *Scheduler) loop(stopCh, wakeCh, doneCh chan struct{}) {
	defer close(doneCh)
	for {
		now := s.clock.Now()
		next, ok := s.nextFn(now)

		var wait time.Duration
		switch {
		case !ok:
			// 没有任何待触发任务：睡到被唤醒为止（不轮询）。
			wait = maxSleep
		case !next.After(now):
			// 已经到点或有积压（休眠唤醒、系统时间往前跳、刚启动时补齐错过）：
			// 必须尽快处理。这里**绝不能**退化成「无限期睡下去」——
			// 那等于把提醒静默丢掉，而这正是本系统最不可接受的失败。
			wait = minOverdueWait
		default:
			wait = next.Sub(now)
			if wait > maxSleep {
				wait = maxSleep
			}
		}

		// 注意 wait 恒为正：零等待会让 After(0) 变成忙等，
		// 而 nil channel 又会永久阻塞 —— 两者都不可接受，所以上面统一收口。
		timer := s.clock.After(wait)

		select {
		case <-stopCh:
			return
		case <-wakeCh:
			// 配置变了，立刻重算。
			continue
		case <-timer:
			if !ok {
				// 没有待触发任务，只是来兜一次时钟跳变检查。
				continue
			}
			if !s.clock.Now().Before(next) {
				s.fire(now)
			}
			// 若时钟往后跳导致尚未到点，下一轮会重新计算等待时长。
		}
	}
}

// fire 调用触发回调，并做 panic 隔离（F8.1）。
//
// 触发路径里任何一处 panic 都不允许掀翻整个调度 goroutine —— 否则一次异常
// 会让插件此后完全静默，而用户毫无察觉。
func (s *Scheduler) fire(now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("调度回调 panic 已隔离: %v", r)
		}
	}()
	s.fireFn(now)
}
