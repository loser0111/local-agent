package plugin

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ===== 数据文件的外部变更监听 =====
//
// 为什么必须监听（而不是轮询）：任务定义是用户的资产，脚本/别的程序改完
// tasks.json 之后，用户期待「马上就生效」。但 sched.go 的硬性约束是
// **禁止轮询**（绝不用「每秒检查一次」兜底），所以这里用 fsnotify 的
// 事件通知 —— 空闲时零 CPU，变更时毫秒级到达，两者都不牺牲。
//
// 三个容易踩的坑，都是本实现必须处理的：
//  1. **必须监听目录，不能监听文件**：本项目的写盘是
//     「同目录临时文件 → rename」（writeFileAtomic），rename 之后
//     tasks.json 的 inode 变了，监听文件本身会立刻失效。所以 watch 的是
//     数据目录，再按文件名过滤。
//  2. **防抖**：一次保存会冒出一串事件（Create/Write/Rename 混合），
//     200ms 窗口把它们合并成一次回调；窗口内继续消费事件，
//     否则 fsnotify 的事件通道会被堵住、后续事件全部丢掉。
//  3. **失败只降级**：监听起不来（目录权限、平台不支持）只记日志，
//     让「外部改动要重启才生效」这一条能力缺失，而不是让插件挂掉。

// fileWatcher 监听某个目录下指定文件名的变更，防抖后回调一次。
//
// 包内私有：外部只通过 Plugin 使用，不需要暴露。
type fileWatcher struct {
	dir      string
	fileName string
	debounce time.Duration
	onChange func()
	logf     func(format string, args ...any)

	mu      sync.Mutex
	fsw     *fsnotify.Watcher
	stopCh  chan struct{}
	doneCh  chan struct{}
	running bool
}

// newFileWatcher 构造监听器（不启动；Start 才真正开始监听）。
//
// onChange 在防抖窗口结束后被调用一次；logf 用于记日志（可为 nil）。
func newFileWatcher(dir, fileName string, debounce time.Duration, onChange func(), logf func(format string, args ...any)) *fileWatcher {
	if debounce <= 0 {
		debounce = 200 * time.Millisecond
	}
	if onChange == nil {
		onChange = func() {}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &fileWatcher{
		dir:      dir,
		fileName: fileName,
		debounce: debounce,
		onChange: onChange,
		logf:     logf,
	}
}

// Start 启动监听（幂等）。
//
// 建 watcher / Add(dir) 都在锁内完成：两者都不做目录扫描，开销极小，
// 换来的是「Start 与 Stop 并发调用也不会漏停一个 goroutine」。
// 失败时 running 保持 false，只记日志降级（调用方照常继续启动流程）。
func (w *fileWatcher) Start() {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		w.mu.Unlock()
		w.logf("任务文件监听不可用（外部修改需重启生效）: %v", err)
		return
	}
	if err := fsw.Add(w.dir); err != nil {
		// 目录不存在/无权限：降级，但绝不阻塞插件启动。
		_ = fsw.Close()
		w.mu.Unlock()
		w.logf("监听数据目录 %s 失败（外部修改需重启生效）: %v", w.dir, err)
		return
	}
	w.fsw = fsw
	w.stopCh = make(chan struct{})
	w.doneCh = make(chan struct{})
	w.running = true
	stopCh, doneCh := w.stopCh, w.doneCh
	w.mu.Unlock()

	go w.loop(fsw, stopCh, doneCh)
}

// Stop 停止监听（幂等）。
//
// 关闭 fsnotify watcher、等 goroutine 退出，最多等 2 秒：
// 回调可能正卡在别处，退出流程不能被无限拖住（与 Scheduler.Stop 同一策略）。
func (w *fileWatcher) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	fsw, stopCh, doneCh := w.fsw, w.stopCh, w.doneCh
	w.fsw = nil
	w.mu.Unlock()

	close(stopCh)
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		w.logf("文件监听循环未在超时内退出")
	}
	if fsw != nil {
		// 关闭 watcher 会关掉 Events/Errors 通道，释放目录句柄。
		if err := fsw.Close(); err != nil {
			w.logf("关闭文件监听失败: %v", err)
		}
	}
}

// loop 是监听主循环：消费事件、防抖、到点回调。
func (w *fileWatcher) loop(fsw *fsnotify.Watcher, stopCh, doneCh chan struct{}) {
	defer close(doneCh)

	var (
		timer  *time.Timer
		timerC <-chan time.Time
	)
	// 退出前把计时器停掉，避免它持有资源到超时。
	defer func() {
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}()

	for {
		select {
		case <-stopCh:
			return

		case ev, ok := <-fsw.Events:
			if !ok {
				// 事件通道被关闭（watcher 已 Close）：安全退出。
				return
			}
			if !w.interested(ev) {
				continue
			}
			// 防抖：重置计时器，窗口内继续消费事件（不能被堵住）。
			if timer == nil {
				timer = time.NewTimer(w.debounce)
			} else {
				if !timer.Stop() {
					// 已经触发过：排空通道，避免 Reset 后立刻又触发。
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(w.debounce)
			}
			timerC = timer.C

		case err, ok := <-fsw.Errors:
			if !ok {
				return
			}
			// 事件溢出等错误只记录：丢了事件不致命（下一次变更还会到）。
			w.logf("文件监听出错: %v", err)

		case <-timerC:
			timerC = nil
			w.fire()
		}
	}
}

// interested 判断事件是否属于「我们关心的那个文件的写/建/改名」。
func (w *fileWatcher) interested(ev fsnotify.Event) bool {
	if filepath.Base(ev.Name) != w.fileName {
		// 临时文件（.tasks.json.tmp-*）、历史文件等一律忽略。
		return false
	}
	return ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0
}

// fire 调用回调，并做 panic 隔离：监听 goroutine 里的一次异常
// 不允许把插件此后所有的外部变更监听都带走。
func (w *fileWatcher) fire() {
	defer func() {
		if r := recover(); r != nil {
			w.logf("文件变更回调 panic 已隔离: %v", r)
		}
	}()
	w.onChange()
}
