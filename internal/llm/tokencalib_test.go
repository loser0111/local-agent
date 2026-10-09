package llm

import (
	"os"
	"path/filepath"
	"testing"
)

// ===== 估算校准系数（真实 usage / 字符估算）=====
//
// 只覆盖存储本身（读盘/落盘、EWMA、夹取、坏文件回落）。
// 「校准系数如何参与用量估算」的用例仍在根包 tokencalib_test.go——那要看得见
// contextStatOf / summarySavedTokens / FormatContextStatReport。

// calibNear 浮点比较。校准系数是经验值，不需要逐位相等。
func calibNear(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

// 没有观测 / 存储没初始化 / 模型名为空：一律 1.0，绝不能退化成 0
func TestCalibDefaultWhenUnknown(t *testing.T) {
	var nilStore *CalibStore
	if got := nilStore.Ratio("gpt-4o"); got != CalibDefault {
		t.Fatalf("nil 存储应回落 %v，实际 %v", CalibDefault, got)
	}
	s := NewCalibStore(filepath.Join(t.TempDir(), "token-calib.json"))
	if got := s.Ratio("gpt-4o"); got != CalibDefault {
		t.Fatalf("未观测过的模型应回落 %v，实际 %v", CalibDefault, got)
	}
	if got := s.Ratio(""); got != CalibDefault {
		t.Fatalf("空模型名应回落 %v，实际 %v", CalibDefault, got)
	}
}

// 首次观测直接采用，之后 EWMA 平滑；不同模型各算各的
func TestCalibObserveAndEWMA(t *testing.T) {
	s := NewCalibStore(filepath.Join(t.TempDir(), "token-calib.json"))
	s.Observe("m", 1000, 500) // 比值 2.0
	if got := s.Ratio("m"); !calibNear(got, 2.0) {
		t.Fatalf("首次观测应直接采用 2.0，实际 %v", got)
	}
	s.Observe("m", 1000, 1000) // 比值 1.0 → 0.3*1.0 + 0.7*2.0 = 1.7
	if got := s.Ratio("m"); !calibNear(got, 1.7) {
		t.Fatalf("EWMA 结果应为 1.7，实际 %v", got)
	}
	if got := s.Ratio("另一个模型"); got != CalibDefault {
		t.Fatalf("校准必须按模型分开记账，实际 %v", got)
	}
}

// 越界观测被夹住；小样本与非法输入不产生观测
func TestCalibClampAndReject(t *testing.T) {
	s := NewCalibStore(filepath.Join(t.TempDir(), "token-calib.json"))
	s.Observe("huge", 10_000_000, 200) // 比值 50000 → 夹到上限
	if got := s.Ratio("huge"); !calibNear(got, calibMax) {
		t.Fatalf("越界观测应夹到 %v，实际 %v", calibMax, got)
	}
	s.Observe("tiny", 50, calibMinSampleTokens-1) // 估算量级不足 → 忽略
	if got := s.Ratio("tiny"); got != CalibDefault {
		t.Fatalf("小样本应被忽略，实际 %v", got)
	}
	for _, c := range [][2]int{{0, 500}, {-1, 500}, {500, 0}, {500, -1}} {
		s.Observe("bad", c[0], c[1])
	}
	if got := s.Ratio("bad"); got != CalibDefault {
		t.Fatalf("非法输入不该产生观测，实际 %v", got)
	}
	s.Observe("", 1000, 500)
	if got := s.Ratio(""); got != CalibDefault {
		t.Fatalf("空模型名不该产生观测，实际 %v", got)
	}
}

// 落盘往返（模拟重启）
func TestCalibPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token-calib.json")
	NewCalibStore(path).Observe("m", 1000, 500)
	if got := NewCalibStore(path).Ratio("m"); !calibNear(got, 2.0) {
		t.Fatalf("重启后应读到 2.0，实际 %v", got)
	}
}

// 坏文件不能让用量估算变成不可控值
func TestCalibCorruptFileFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token-calib.json")
	if err := os.WriteFile(path, []byte("{{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := NewCalibStore(path).Ratio("m"); got != CalibDefault {
		t.Fatalf("坏文件应回落 %v，实际 %v", CalibDefault, got)
	}
}
