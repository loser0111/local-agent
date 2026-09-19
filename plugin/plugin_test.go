package plugin

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// ===== 骨架测试：清单自检 + 加载/注册链路 =====

// fakeHost 记录宿主被调用的情况，用于验证插件的注册行为。
type fakeHost struct {
	categories   []NotifyCategory
	onResponse   func(NotifyResponse)
	onRun        func(RunEvent)
	emitted      []emittedEvent
	catErr       error
	baseDir      string
	appVersion   string
	notifyCalls  int
	notifyErr    error
	revealedWith []RevealPayload
}

type emittedEvent struct {
	name    string
	payload any
}

func (h *fakeHost) NotifyAvailable() bool { return true }

func (h *fakeHost) Notify(req NotifyRequest) error {
	h.notifyCalls++
	return h.notifyErr
}

func (h *fakeHost) RegisterCategories(cats []NotifyCategory) error {
	h.categories = append(h.categories, cats...)
	return h.catErr
}

func (h *fakeHost) OnNotifyResponse(fn func(NotifyResponse)) { h.onResponse = fn }

func (h *fakeHost) RevealWindow(payload RevealPayload) {
	h.revealedWith = append(h.revealedWith, payload)
}

func (h *fakeHost) RunAgent(ctx context.Context, req RunRequest) (RunResult, error) {
	return RunResult{}, ErrNotImplemented
}

func (h *fakeHost) CancelRun(runID string) error { return ErrNotImplemented }

func (h *fakeHost) SubscribeRuns(fn func(RunEvent)) { h.onRun = fn }

func (h *fakeHost) Emit(name string, payload any) {
	h.emitted = append(h.emitted, emittedEvent{name: name, payload: payload})
}

func (h *fakeHost) BaseDir() string { return h.baseDir }

func (h *fakeHost) AppVersion() string { return h.appVersion }

// hasEvent 判断是否推过某子类型的事件（便于断言「必须可见」的行为）。
func (h *fakeHost) hasEvent(subtype string) bool {
	for _, e := range h.emitted {
		if ev, ok := e.payload.(Event); ok && ev.Type == subtype {
			return true
		}
		if e.name == subtype {
			return true
		}
	}
	return false
}

// fakeClock 是可控时钟：骨架测试不依赖真实时间。
//
// 带锁：插件启动后会有调度 goroutine 并发调用 Now()，
// 而测试线程会推进它 —— 无锁的话 -race 会（正确地）报出竞争。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	// 返回一个永不触发的通道：这些用例都手动驱动 tick()，
	// 不需要调度循环自己醒来（那部分由 sched_test.go 的 manualClock 覆盖）。
	return make(chan time.Time)
}

// set 推进时钟（供测试线程调用）。
func (c *fakeClock) set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

func newTestPlugin(t *testing.T, host Host) *Plugin {
	t.Helper()
	p, err := New(Options{
		Host:       host,
		DataDir:    t.TempDir(),
		AppVersion: "test",
		Clock:      &fakeClock{now: time.Date(2025, 1, 1, 9, 0, 0, 0, time.Local)},
		Logger:     testLogger{t},
	})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	return p
}

type testLogger struct{ t *testing.T }

func (l testLogger) Printf(format string, args ...any) { l.t.Logf(format, args...) }

func TestLoadManifestMatchesHostInterface(t *testing.T) {
	m, err := LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest 失败: %v", err)
	}
	if m.ID != "desktop-plugin" {
		t.Errorf("ID = %q, want desktop-plugin", m.ID)
	}
	if m.Kind != "builtin" {
		t.Errorf("Kind = %q, want builtin（ADR-4：编译期内置）", m.Kind)
	}
	if !m.SupportsCapability("notify.toast") {
		t.Error("清单应声明 notify.toast 能力")
	}
	// hostMethods 必须与接口实际方法一一对应（清单不许失真）。
	if len(m.HostMethods) != len(hostMethodNames()) {
		t.Errorf("hostMethods 数量 = %d, Host 接口方法数 = %d", len(m.HostMethods), len(hostMethodNames()))
	}
}

func TestManifestRejectsMismatchedHostMethods(t *testing.T) {
	m, err := LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest 失败: %v", err)
	}
	m.HostMethods = append(m.HostMethods, "NotARealMethod")
	if err := m.Validate(); err == nil {
		t.Fatal("清单声明了不存在的宿主方法，Validate 应当报错")
	}
}

func TestManifestRejectsHotPlugKind(t *testing.T) {
	m, err := LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest 失败: %v", err)
	}
	m.Kind = "hotplug"
	if err := m.Validate(); err == nil {
		t.Fatal("kind=hotplug 应当被拒绝（本插件是编译期内置）")
	}
}

func TestHostAPICompatibility(t *testing.T) {
	cases := []struct {
		declared string
		wantErr  bool
	}{
		{"1.0", false},
		{"1.7", false}, // 次版本向后兼容
		{"2.0", true},  // 主版本不同 = 不兼容
		{"", true},
		{"abc", true},
	}
	for _, c := range cases {
		err := checkHostAPICompatible(c.declared)
		if (err != nil) != c.wantErr {
			t.Errorf("checkHostAPICompatible(%q) err=%v, wantErr=%v", c.declared, err, c.wantErr)
		}
	}
}

func TestNewRequiresHostAndDataDir(t *testing.T) {
	if _, err := New(Options{DataDir: t.TempDir()}); err == nil {
		t.Error("缺少 Host 应当报错")
	}
	if _, err := New(Options{Host: &fakeHost{}}); err == nil {
		t.Error("缺少 DataDir 应当报错")
	}
}

func TestStartRegistersAndBroadcastsReady(t *testing.T) {
	host := &fakeHost{baseDir: "C:/tmp/.local-agent"}
	p := newTestPlugin(t, host)

	if p.State() != StateCreated {
		t.Fatalf("初始状态 = %q, want created", p.State())
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if p.State() != StateRunning {
		t.Errorf("状态 = %q, want running", p.State())
	}
	// 注册了通知分类
	if len(host.categories) == 0 || host.categories[0].ID != "reminder.basic" {
		t.Errorf("通知分类未正确注册: %+v", host.categories)
	}
	// 接上了回调
	if host.onResponse == nil {
		t.Error("未注册通知响应回调")
	}
	if host.onRun == nil {
		t.Error("未订阅运行事件")
	}
	// 广播了就绪事件，且走统一通道
	if len(host.emitted) != 1 {
		t.Fatalf("事件数 = %d, want 1", len(host.emitted))
	}
	ev := host.emitted[0]
	if ev.name != EventChannel {
		t.Errorf("事件通道 = %q, want %q", ev.name, EventChannel)
	}
	if e, ok := ev.payload.(Event); !ok || e.Type != EventPluginReady {
		t.Errorf("事件负载 = %+v, want type=%s", ev.payload, EventPluginReady)
	}
	// 幂等：重复 Start 不应重复注册
	catsBefore := len(host.categories)
	eventsBefore := len(host.emitted)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("重复 Start 失败: %v", err)
	}
	if len(host.categories) != catsBefore || len(host.emitted) != eventsBefore {
		t.Errorf("重复 Start 产生了副作用: categories=%d events=%d", len(host.categories), len(host.emitted))
	}
}

func TestStartSurvivesCategoryRegistrationFailure(t *testing.T) {
	host := &fakeHost{catErr: errTestCategory}
	p := newTestPlugin(t, host)

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("分类注册失败不得阻塞启动，却返回了错误: %v", err)
	}
	if p.State() != StateRunning {
		t.Errorf("状态 = %q, want running", p.State())
	}
	// 必须可见：既记录，也广播降级事件
	var fallback bool
	for _, ev := range host.emitted {
		if e, ok := ev.payload.(Event); ok && e.Type == EventNotifyFallback {
			fallback = true
		}
	}
	if !fallback {
		t.Error("分类注册失败应广播 task:notify:fallback")
	}
}

func TestStopIsIdempotent(t *testing.T) {
	p := newTestPlugin(t, &fakeHost{})
	if err := p.Stop(); err != nil {
		t.Fatalf("未启动时 Stop 不应报错: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("重复 Stop 不应报错: %v", err)
	}
	if p.State() != StateStopped {
		t.Errorf("状态 = %q, want stopped", p.State())
	}
}

func TestInfoAndManifestAreRecognizable(t *testing.T) {
	host := &fakeHost{baseDir: "C:/tmp/.local-agent", appVersion: "9.9.9"}
	p := newTestPlugin(t, host)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	info := p.Info()
	if info.ID == "" || info.Version == "" || info.HostAPIVersion == "" {
		t.Errorf("Info 身份字段不完整: %+v", info)
	}
	if info.State != string(StateRunning) {
		t.Errorf("Info.State = %q, want running", info.State)
	}
	if info.StartedAt == "" {
		t.Error("启动后 Info.StartedAt 不应为空")
	}
	if !strings.Contains(info.String(), "desktop-plugin") {
		t.Errorf("Info.String() 应含插件 id: %q", info.String())
	}

	// 返回的清单必须是副本，外部改动不得污染插件内部状态。
	m := p.Manifest()
	m.Capabilities[0] = "tampered"
	if p.manifest.Capabilities[0] == "tampered" {
		t.Error("Manifest() 应返回副本")
	}
}

func TestHandleNotifyResponseEmitsChange(t *testing.T) {
	host := &fakeHost{}
	p := newTestPlugin(t, host)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	host.onResponse(NotifyResponse{ID: "task:t1:tr1", ActionID: "snooze10", Text: "10 分钟后"})

	if len(host.emitted) != 2 {
		t.Fatalf("事件数 = %d, want 2", len(host.emitted))
	}
	e := host.emitted[1].payload.(Event)
	if e.Type != EventChanged {
		t.Errorf("事件类型 = %q, want %q", e.Type, EventChanged)
	}
}

func TestRunSubtypeMapping(t *testing.T) {
	cases := map[string]string{
		"started":   EventRunStarted,
		"finished":  EventRunFinished,
		"succeeded": EventRunFinished,
		"failed":    EventRunFinished,
		"timeout":   EventRunFinished,
		"whatever":  EventRunFinished,
	}
	for in, want := range cases {
		if got := runSubtype(in); got != want {
			t.Errorf("runSubtype(%q) = %q, want %q", in, got, want)
		}
	}
}

var errTestCategory = &testError{"注册表不可写"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
