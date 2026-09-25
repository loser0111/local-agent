package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===== 估算校准系数（真实 usage / 字符估算）=====

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
	var nilStore *TokenCalibStore
	if got := nilStore.Ratio("gpt-4o"); got != calibDefault {
		t.Fatalf("nil 存储应回落 %v，实际 %v", calibDefault, got)
	}
	s := NewTokenCalibStore(filepath.Join(t.TempDir(), "token-calib.json"))
	if got := s.Ratio("gpt-4o"); got != calibDefault {
		t.Fatalf("未观测过的模型应回落 %v，实际 %v", calibDefault, got)
	}
	if got := s.Ratio(""); got != calibDefault {
		t.Fatalf("空模型名应回落 %v，实际 %v", calibDefault, got)
	}
	if got := (&App{}).calibFor("gpt-4o"); got != calibDefault {
		t.Fatalf("存储未初始化时应回落 %v，实际 %v", calibDefault, got)
	}
	var nilApp *App
	if got := nilApp.calibFor("gpt-4o"); got != calibDefault {
		t.Fatalf("nil App 应回落 %v，实际 %v", calibDefault, got)
	}
}

// 首次观测直接采用，之后 EWMA 平滑；不同模型各算各的
func TestCalibObserveAndEWMA(t *testing.T) {
	s := NewTokenCalibStore(filepath.Join(t.TempDir(), "token-calib.json"))
	s.Observe("m", 1000, 500) // 比值 2.0
	if got := s.Ratio("m"); !calibNear(got, 2.0) {
		t.Fatalf("首次观测应直接采用 2.0，实际 %v", got)
	}
	s.Observe("m", 1000, 1000) // 比值 1.0 → 0.3*1.0 + 0.7*2.0 = 1.7
	if got := s.Ratio("m"); !calibNear(got, 1.7) {
		t.Fatalf("EWMA 结果应为 1.7，实际 %v", got)
	}
	if got := s.Ratio("另一个模型"); got != calibDefault {
		t.Fatalf("校准必须按模型分开记账，实际 %v", got)
	}
}

// 越界观测被夹住；小样本与非法输入不产生观测
func TestCalibClampAndReject(t *testing.T) {
	s := NewTokenCalibStore(filepath.Join(t.TempDir(), "token-calib.json"))
	s.Observe("huge", 10_000_000, 200) // 比值 50000 → 夹到上限
	if got := s.Ratio("huge"); !calibNear(got, calibMax) {
		t.Fatalf("越界观测应夹到 %v，实际 %v", calibMax, got)
	}
	s.Observe("tiny", 50, calibMinSampleTokens-1) // 估算量级不足 → 忽略
	if got := s.Ratio("tiny"); got != calibDefault {
		t.Fatalf("小样本应被忽略，实际 %v", got)
	}
	for _, c := range [][2]int{{0, 500}, {-1, 500}, {500, 0}, {500, -1}} {
		s.Observe("bad", c[0], c[1])
	}
	if got := s.Ratio("bad"); got != calibDefault {
		t.Fatalf("非法输入不该产生观测，实际 %v", got)
	}
	s.Observe("", 1000, 500)
	if got := s.Ratio(""); got != calibDefault {
		t.Fatalf("空模型名不该产生观测，实际 %v", got)
	}
}

// 落盘往返（模拟重启）
func TestCalibPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token-calib.json")
	NewTokenCalibStore(path).Observe("m", 1000, 500)
	if got := NewTokenCalibStore(path).Ratio("m"); !calibNear(got, 2.0) {
		t.Fatalf("重启后应读到 2.0，实际 %v", got)
	}
}

// 坏文件不能让用量估算变成不可控值
func TestCalibCorruptFileFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token-calib.json")
	if err := os.WriteFile(path, []byte("{{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := NewTokenCalibStore(path).Ratio("m"); got != calibDefault {
		t.Fatalf("坏文件应回落 %v，实际 %v", calibDefault, got)
	}
}

// 无锚点时套系数；有锚点时锚点是真值，绝不再乘一次
func TestContextStatCalibOnlyWithoutAnchor(t *testing.T) {
	sess := &Session{Messages: []Message{{Role: RoleUser, Content: strings.Repeat("内容", 50)}}}
	msgs := buildRunMessages(sess, "", false, nil)

	base := contextStatOf(sess, msgs, 1_000_000, nil, 0, 1.0)
	doubled := contextStatOf(sess, msgs, 1_000_000, nil, 0, 2.0)
	if doubled.UsedTokens != base.UsedTokens*2 {
		t.Fatalf("无锚点时 2.0 系数应给出两倍用量：%d vs %d", doubled.UsedTokens, base.UsedTokens)
	}
	if !calibNear(doubled.CalibRatio, 2.0) {
		t.Fatalf("统计里应带上所用系数，实际 %v", doubled.CalibRatio)
	}

	anchor := &tokenAnchor{InputTokens: 1234, MsgCount: len(msgs)}
	a1 := contextStatOf(sess, msgs, 1_000_000, anchor, 0, 1.0)
	a2 := contextStatOf(sess, msgs, 1_000_000, anchor, 0, 3.0)
	if a1.UsedTokens != a2.UsedTokens {
		t.Fatalf("有锚点时系数不该参与计算：%d vs %d", a1.UsedTokens, a2.UsedTokens)
	}
	if !a1.HasAnchor {
		t.Fatal("有锚点时应标记 HasAnchor")
	}

	// extra（工具定义开销）也是字符估算，同样要缩放
	e1 := contextStatOf(sess, msgs, 1_000_000, nil, 400, 1.0)
	e2 := contextStatOf(sess, msgs, 1_000_000, nil, 400, 2.0)
	if e2.UsedTokens != e1.UsedTokens*2 {
		t.Fatalf("工具定义开销应一并缩放：%d vs %d", e2.UsedTokens, e1.UsedTokens)
	}
}

// saved 也随系数缩放：两侧同系数不会互相抵消
func TestSavedTokensScalesWithCalib(t *testing.T) {
	sess := &Session{
		Messages:           []Message{{Role: RoleUser, Content: strings.Repeat("内容", 100)}},
		ContextSummary:     "摘要正文",
		ContextCoveredUpTo: 1,
	}
	one := summarySavedTokens(sess, 1.0)
	if one <= 0 {
		t.Fatalf("压缩态应算出正数，实际 %d", one)
	}
	if two := summarySavedTokens(sess, 2.0); two != one*2 {
		t.Fatalf("2.0 系数下应为两倍：%d vs %d", two, one)
	}
}

// 报告只在系数不等于 1 时打印那一行，避免常态噪声
func TestReportShowsCalibOnlyWhenNotOne(t *testing.T) {
	st := &ContextStat{UsedTokens: 100, WindowTokens: 1000, CalibRatio: 1.0}
	if rep := FormatContextStatReport(st, 12); strings.Contains(rep, "估算系数") {
		t.Fatalf("系数为 1 时不该打印该行：\n%s", rep)
	}
	st.CalibRatio = 1.23
	if rep := FormatContextStatReport(st, 12); !strings.Contains(rep, "×1.23") {
		t.Fatalf("应打印校准系数：\n%s", rep)
	}
}
