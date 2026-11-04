package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeRawPlanFile 按"另一个进程留下的计划文件"的方式直接写盘，
// 绕开 PlanStore（它的 Save 会改动 UpdatedAt/Revision，构造不出旧数据的样子）。
func writeRawPlanFile(t *testing.T, dir string, p *Plan) {
	t.Helper()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("序列化计划失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, p.ID+".json"), data, 0o644); err != nil {
		t.Fatalf("写入计划文件失败: %v", err)
	}
}

func readRawPlanFile(t *testing.T, dir, id string) *Plan {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatalf("读取计划文件失败: %v", err)
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("解析计划文件失败: %v", err)
	}
	return &p
}

// TestPlanStoreRevisionMonotonic 每次落盘 Revision 严格递增，且重载后不倒退。
//
// 版本号是前端丢弃乱序事件的唯一依据：它一旦回退，界面就会接受一条更旧的
// plan_update，把"已完成"刷回"执行中"。
func TestPlanStoreRevisionMonotonic(t *testing.T) {
	dir := t.TempDir()
	s := NewPlanStore(dir)

	p := &Plan{
		ID: "plan_rev", SessionID: "s1", Title: "t", Status: PlanAwaitingApproval,
		Steps: []*PlanStep{{Index: 0, Title: "a", Status: StepPending}},
	}
	if err := s.Save(p); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	if p.Revision != 1 {
		t.Fatalf("首次落盘后 Revision 应为 1，实际: %d", p.Revision)
	}

	p.Steps[0].Status = StepRunning
	if err := s.Save(p); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	if p.Revision != 2 {
		t.Fatalf("第二次落盘后 Revision 应为 2，实际: %d", p.Revision)
	}

	// 重载：版本号必须跟着文件走（否则重启后前端会把新事件当旧事件丢掉）
	reloaded, err := NewPlanStore(dir).Get(p.ID)
	if err != nil {
		t.Fatalf("重载计划失败: %v", err)
	}
	if reloaded.Revision != 2 {
		t.Fatalf("重载后 Revision 应为 2，实际: %d", reloaded.Revision)
	}
}

// TestPlanStoreStartupRecoveryKeepsProgress 应用启动时 running 计划转为 interrupted，
// 已完成的步骤与摘要原样保留（这是"从断点续跑"能成立的前提）。
func TestPlanStoreStartupRecoveryKeepsProgress(t *testing.T) {
	dir := t.TempDir()
	// 模拟上一次进程退出时正在执行第 2 步
	writeRawPlanFile(t, dir, &Plan{
		ID: "plan_crash", SessionID: "s1", Title: "升级依赖", Status: PlanRunning,
		Query: "帮我升级依赖并跑通测试",
		Steps: []*PlanStep{
			{Index: 0, Title: "检查依赖", Status: StepDone, Summary: "共 24 个依赖"},
			{Index: 1, Title: "逐个升级", Status: StepRunning, Attempt: 1},
			{Index: 2, Title: "跑测试", Status: StepPending},
		},
	})

	s := NewPlanStore(dir)
	got, err := s.Get("plan_crash")
	if err != nil {
		t.Fatalf("加载计划失败: %v", err)
	}
	if got.Status != PlanInterrupted {
		t.Fatalf("running 计划应恢复为 %q，实际: %q", PlanInterrupted, got.Status)
	}
	if got.Query != "帮我升级依赖并跑通测试" {
		t.Fatalf("原始请求不应丢失，实际: %q", got.Query)
	}
	if got.Steps[0].Status != StepDone || got.Steps[0].Summary != "共 24 个依赖" {
		t.Fatalf("已完成步骤及其摘要必须保留，实际: %+v", got.Steps[0])
	}
	if got.Steps[1].Status != StepInterrupted || got.Steps[1].Error == "" {
		t.Fatalf("执行中的步骤应转为 %q 并写明原因，实际: %+v", StepInterrupted, got.Steps[1])
	}
	if got.Steps[2].Status != StepPending {
		t.Fatalf("未开始的步骤应保持 %q（可续跑），实际: %q", StepPending, got.Steps[2].Status)
	}

	// 恢复结果必须已经落盘：否则第二次重启又会把它读回 running
	onDisk := readRawPlanFile(t, dir, "plan_crash")
	if onDisk.Status != PlanInterrupted || onDisk.Steps[1].Status != StepInterrupted {
		t.Fatalf("恢复结果未落盘，磁盘上仍是: status=%q step=%q", onDisk.Status, onDisk.Steps[1].Status)
	}

	// 再启动一次：已是 interrupted，不应被改写（幂等）
	again, err := NewPlanStore(dir).Get("plan_crash")
	if err != nil {
		t.Fatalf("二次加载失败: %v", err)
	}
	if again.Status != PlanInterrupted || again.Steps[1].Status != StepInterrupted {
		t.Fatalf("二次加载不应改变状态，实际: status=%q step=%q", again.Status, again.Steps[1].Status)
	}
}

// TestPlanStoreLegacyPlanFileCompat 旧版本写的计划文件（没有 query/error/revision/
// attempt/contextMark）必须能直接读，零值即"没有这些信息"。
func TestPlanStoreLegacyPlanFileCompat(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
  "id": "plan_old",
  "sessionId": "s1",
  "title": "旧计划",
  "status": "failed",
  "steps": [
    {"index": 0, "title": "第一步", "status": "done", "summary": "好了"}
  ],
  "createdAt": 1700000000000,
  "updatedAt": 1700000000000
}`
	if err := os.WriteFile(filepath.Join(dir, "plan_old.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("写入旧计划文件失败: %v", err)
	}

	s := NewPlanStore(dir)
	got, err := s.Get("plan_old")
	if err != nil {
		t.Fatalf("读取旧计划失败: %v", err)
	}
	if got.Revision != 0 || got.Query != "" || got.Error != "" {
		t.Fatalf("旧文件的缺失字段应为零值，实际: revision=%d query=%q error=%q",
			got.Revision, got.Query, got.Error)
	}
	if got.Steps[0].Attempt != 0 || got.Steps[0].ContextMark != 0 {
		t.Fatalf("旧步骤的缺失字段应为 0，实际: attempt=%d mark=%d",
			got.Steps[0].Attempt, got.Steps[0].ContextMark)
	}

	// 旧计划第一次被本版本写盘后，版本号从 1 开始（不会与旧数据冲突）
	got.Status = PlanAwaitingApproval
	if err := s.Save(got); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got.Revision != 1 {
		t.Fatalf("旧计划首次落盘后 Revision 应为 1，实际: %d", got.Revision)
	}
}

// TestPlanStoreReopenFromInterrupted 中断的计划也能退回待审核（用户可能想先改步骤）。
func TestPlanStoreReopenFromInterrupted(t *testing.T) {
	dir := t.TempDir()
	s := NewPlanStore(dir)
	p := &Plan{
		ID: "plan_int", SessionID: "s1", Title: "t", Status: PlanInterrupted,
		Steps: []*PlanStep{
			{Index: 0, Title: "done", Status: StepDone, Summary: "ok", FinishedAt: 111},
			{Index: 1, Title: "中断", Status: StepInterrupted, Error: "应用重启，执行中断", FinishedAt: 222},
			{Index: 2, Title: "没跑", Status: StepPending},
		},
	}
	if err := s.Save(p); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	reopened, err := s.Reopen(p.ID)
	if err != nil {
		t.Fatalf("interrupted 计划应可重开，实际报错: %v", err)
	}
	if reopened.Status != PlanAwaitingApproval {
		t.Fatalf("重开后应为 %q，实际: %q", PlanAwaitingApproval, reopened.Status)
	}
	if reopened.Steps[0].Status != StepDone || reopened.Steps[0].Summary != "ok" {
		t.Fatalf("已完成步骤必须保留，实际: %+v", reopened.Steps[0])
	}
	if reopened.Steps[1].Status != StepPending || reopened.Steps[1].Error != "" {
		t.Fatalf("中断步骤应重置为待执行并清空原因，实际: %+v", reopened.Steps[1])
	}
}
