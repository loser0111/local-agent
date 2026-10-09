package agent

import (
	"testing"
	"time"
)

// ===== 取消控制的单元级语义（随包迁入）=====
//
// 这三条断言直接作用于 `markSoft` / `markHard`（含"首次触发返回 true、重复调用返回 false"
// 这种幂等语义），而它们是未导出方法。与其为测试把它们导出、污染包 API，不如把用例搬进来
// ——与 permission 域对 `permission_internal_test.go` 的处理一致。
// 注册表层面的语义（按会话/按计划查、end 注销、BeginExclusive 互斥）用的是导出 API，留在根包。

// 软取消：只置信号，不碰 ctx
func TestRunControlSoftCancel(t *testing.T) {
	reg := &RunRegistry{}
	run := reg.Begin("s1", "")

	if run.SoftRequested() {
		t.Fatal("刚创建的运行不应处于已取消状态")
	}
	if !run.markSoft() {
		t.Fatal("首次软取消应返回 true")
	}
	if !run.SoftRequested() {
		t.Fatal("软取消后 SoftRequested 应为 true")
	}
	if run.Kind() != CancelKindSoft {
		t.Fatalf("取消种类应为 soft，实际 %q", run.Kind())
	}
	// 软取消不该 cancel ctx：在途请求与工具要能跑完
	if run.Ctx().Err() != nil {
		t.Fatalf("软取消不应 cancel ctx，实际 %v", run.Ctx().Err())
	}
	if run.markSoft() {
		t.Fatal("重复软取消应返回 false（幂等）")
	}
}

// 硬取消：先置软信号，再 cancel ctx
func TestRunControlHardCancel(t *testing.T) {
	reg := &RunRegistry{}
	run := reg.Begin("s1", "")

	if !run.markHard() {
		t.Fatal("首次硬取消应返回 true")
	}
	if !run.SoftRequested() {
		t.Fatal("硬取消必须同时置软信号——循环要靠它走正常收尾路径")
	}
	if run.Kind() != CancelKindHard {
		t.Fatalf("取消种类应为 hard，实际 %q", run.Kind())
	}
	select {
	case <-run.Ctx().Done():
		// 期望：ctx 已取消
	case <-time.After(time.Second):
		t.Fatal("硬取消应 cancel 运行 ctx")
	}
	if run.markHard() {
		t.Fatal("重复硬取消应返回 false（幂等）")
	}
}

// 先软后硬：升级路径必须成立（前端"再点一次"依赖它）
func TestRunControlSoftThenHard(t *testing.T) {
	reg := &RunRegistry{}
	run := reg.Begin("s1", "")

	if !run.markSoft() {
		t.Fatal("软取消应成功")
	}
	if run.Kind() != CancelKindSoft {
		t.Fatalf("应为 soft，实际 %q", run.Kind())
	}
	if !run.markHard() {
		t.Fatal("软取消之后升级为硬取消应返回 true")
	}
	if run.Kind() != CancelKindHard {
		t.Fatalf("应升级为 hard，实际 %q", run.Kind())
	}
	select {
	case <-run.Ctx().Done():
	case <-time.After(time.Second):
		t.Fatal("升级后应 cancel ctx")
	}
}
