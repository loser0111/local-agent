package plugin

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// ===== 数据文件的外部变更监听（watch.go）=====
//
// 四个用例，对应四条必须成立的语义：
//  1. 外部原子写 → 防抖后自动重新加载，新任务出现在内存里；
//  2. 自己写盘 → 不被误判成外部变更（防回环）；
//  3. 半截写入（非原子写的中间态）→ 内存与文件都不许动，更不许归档成 .broken；
//  4. Stop 之后彻底安静，且 Stop 幂等。
//
// 异步结果一律「轮询等待 + 超时」，不靠固定 sleep 硬等（fsnotify 的送达时间
// 不可控，固定的 sleep 要么慢要么脆）。

// watchTestTimeout 是等待异步结果的超时。
const watchTestTimeout = 5 * time.Second

// watchWaitFor 轮询等待条件成立；超时即失败。
func watchWaitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(watchTestTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待超时（%v）: %s", watchTestTimeout, desc)
}

// watchWriteTasks 用生产同款原子写（临时文件 → Sync → rename）落一份 tasks.json，
// 模拟脚本 / 外部程序改文件。
func watchWriteTasks(t *testing.T, dir, content string) {
	t.Helper()
	if err := writeFileAtomic(filepath.Join(dir, taskFileName), []byte(content), 0o600); err != nil {
		t.Fatalf("原子写测试数据失败: %v", err)
	}
}

// watchHasTask 判断内存里是否存在某个任务 id。
func watchHasTask(st *TaskStore, id string) bool {
	tasks, _, _ := st.snapshot()
	for _, x := range tasks {
		if x != nil && x.ID == id {
			return true
		}
	}
	return false
}

// watchGoodTasks 是一份合法、含一条任务的 tasks.json 内容。
const watchGoodTasks = `{
  "version": 1,
  "settings": {},
  "tasks": [
    {
      "id": "task_keep",
      "title": "要保留的任务",
      "kind": "reminder",
      "enabled": true,
      "trigger": {"kind": "once", "once": {"at": "2030-01-01T00:00:00+08:00"}}
    }
  ]
}`

// 1) 外部原子写 → 自动重新加载。
func TestWatchExternalAtomicWriteIsPickedUp(t *testing.T) {
	dir := t.TempDir()
	st := NewTaskStore(dir)
	if err := st.Save(); err != nil {
		t.Fatalf("初始保存失败: %v", err)
	}

	var reloaded int32
	w := newFileWatcher(dir, taskFileName, 50*time.Millisecond, func() {
		if changed, err := st.ReloadIfChanged(); err == nil && changed {
			atomic.AddInt32(&reloaded, 1)
		}
	}, t.Logf)
	w.Start()
	defer w.Stop()

	watchWriteTasks(t, dir, watchGoodTasks)

	watchWaitFor(t, "外部写入的任务出现在内存里", func() bool {
		return watchHasTask(st, "task_keep")
	})
	if n := atomic.LoadInt32(&reloaded); n == 0 {
		t.Fatal("外部变更应当至少触发一次重新加载")
	}
}

// 2) 自己写盘不触发内存替换（防自写回环）。
func TestWatchSelfWriteIsNotTreatedAsExternal(t *testing.T) {
	dir := t.TempDir()
	st := NewTaskStore(dir)
	if err := st.Save(); err != nil {
		t.Fatalf("初始保存失败: %v", err)
	}

	var changedTimes int32
	w := newFileWatcher(dir, taskFileName, 50*time.Millisecond, func() {
		if changed, err := st.ReloadIfChanged(); err == nil && changed {
			atomic.AddInt32(&changedTimes, 1)
		}
	}, t.Logf)
	w.Start()
	defer w.Stop()

	// 连写三次：事件一定会到监听线程，但每一次都必须被认出来「这是我自己写的」。
	for i := 0; i < 3; i++ {
		if err := st.Save(); err != nil {
			t.Fatalf("第 %d 次保存失败: %v", i+1, err)
		}
	}
	time.Sleep(500 * time.Millisecond)

	if n := atomic.LoadInt32(&changedTimes); n != 0 {
		t.Fatalf("自己写盘不该被当成外部变更，实际替换内存 %d 次", n)
	}
	// 再从 store 侧直问一次：磁盘内容就是自己刚写的，必须不算变更。
	changed, err := st.ReloadIfChanged()
	if err != nil {
		t.Fatalf("自己写盘后重新加载不该报错: %v", err)
	}
	if changed {
		t.Fatal("磁盘内容就是自己刚写的，不该被判为外部变更")
	}
}

// 3) 半截写入不破坏内存，也不归档文件。
func TestWatchPartialWriteKeepsMemoryAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, taskFileName)
	watchWriteTasks(t, dir, watchGoodTasks)

	st := NewTaskStore(dir)
	if err := st.Load(); err != nil {
		t.Fatalf("加载正常数据失败: %v", err)
	}
	if !watchHasTask(st, "task_keep") {
		t.Fatal("前置条件不成立：内存里应当有 task_keep")
	}

	const partial = `{"version":1,"settings":{},"tasks":[{"id":"task_ke`
	var parseErrs, unexpected int32
	w := newFileWatcher(dir, taskFileName, 50*time.Millisecond, func() {
		changed, err := st.ReloadIfChanged()
		switch {
		case err != nil:
			atomic.AddInt32(&parseErrs, 1)
		case changed:
			atomic.AddInt32(&unexpected, 1)
		}
	}, t.Logf)
	w.Start()
	defer w.Stop()

	// 模拟写入方还没写完就落盘：直接覆盖，不给完整 JSON。
	if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
		t.Fatalf("写半截文件失败: %v", err)
	}

	watchWaitFor(t, "监听线程报出解析失败", func() bool {
		return atomic.LoadInt32(&parseErrs) > 0
	})
	if n := atomic.LoadInt32(&unexpected); n != 0 {
		t.Fatalf("半截内容绝不该替换内存，实际替换 %d 次", n)
	}
	if !watchHasTask(st, "task_keep") {
		t.Fatal("半截写入后内存里的任务必须原样保留")
	}
	if _, err := os.Stat(path + ".broken"); err == nil {
		t.Fatal("半截写入绝不该把 tasks.json 归档成 .broken")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("原文件必须原地保留: %v", err)
	}
	if string(data) != partial {
		t.Fatalf("磁盘上的半截内容不该被程序改写，实际: %s", string(data))
	}
}

// 4) Stop 之后彻底安静，且 Stop 幂等。
func TestWatchStopIsSilentAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	st := NewTaskStore(dir)
	if err := st.Save(); err != nil {
		t.Fatalf("初始保存失败: %v", err)
	}

	var changedTimes int32
	w := newFileWatcher(dir, taskFileName, 50*time.Millisecond, func() {
		if changed, err := st.ReloadIfChanged(); err == nil && changed {
			atomic.AddInt32(&changedTimes, 1)
		}
	}, t.Logf)
	w.Start()

	// 先确认它确实在工作 —— 否则「停了之后没反应」这一条毫无意义。
	watchWriteTasks(t, dir, watchGoodTasks)
	watchWaitFor(t, "首次外部变更生效", func() bool {
		return atomic.LoadInt32(&changedTimes) > 0
	})

	w.Stop()
	before := atomic.LoadInt32(&changedTimes)

	watchWriteTasks(t, dir, `{"version":1,"settings":{},"tasks":[{"id":"task_late","kind":"reminder","enabled":true,"trigger":{"kind":"once","once":{"at":"2030-01-01T00:00:00+08:00"}}}]}`)
	time.Sleep(600 * time.Millisecond)

	if after := atomic.LoadInt32(&changedTimes); after != before {
		t.Fatalf("Stop 之后不该再有变更生效，%d -> %d", before, after)
	}
	w.Stop() // 幂等：再调一次不应 panic / 阻塞
}

// 5) 插件级链路：Start 之后外部写文件 → 内存即刻生效（无需重启进程）。
//
// 前四个用例验证的是 watch.go 与 store 两个零件，这一条验证它们被 Plugin 真的
// 接上了（Start 里启动监听）—— 零件再好，没接上也白搭。
func TestPluginPicksUpExternalFileChange(t *testing.T) {
	host := &fakeHost{}
	p := newTestPlugin(t, host)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("插件启动失败: %v", err)
	}
	defer func() { _ = p.Stop() }()

	if got := p.State(); got != StateRunning {
		t.Fatalf("插件应处于 running，实际 %s", got)
	}

	// 关键：插件已经在跑，不重启进程，直接改文件。
	watchWriteTasks(t, p.store.dir, watchGoodTasks)

	watchWaitFor(t, "外部写入的任务被运行中的插件加载", func() bool {
		tasks, _, _ := p.store.snapshot()
		for _, x := range tasks {
			if x != nil && x.ID == "task_keep" {
				return true
			}
		}
		return false
	})
}
