package main

import (
	"strings"
	"testing"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/llm"
	"wails-tmp/internal/store"
)

// ===== 估算校准系数：系数如何参与用量估算（根包侧）=====
//
// 存储本身（读盘/落盘、EWMA、夹取、坏文件回落）的用例已随实现搬到
// internal/llm/tokencalib_test.go。这里只测根包才看得见的两件事：
// App.calibFor 的兜底，以及**有锚点时不套系数**这条估算规则。

// calibNear 浮点比较。校准系数是经验值，不需要逐位相等。
func calibNear(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

// 存储未初始化（零值 App / startup 尚未跑到）时必须回落 1.0，不能 panic、不能变 0
func TestCalibForFallsBackWithoutStore(t *testing.T) {
	if got := (&App{}).calibFor("gpt-4o"); got != llm.CalibDefault {
		t.Fatalf("存储未初始化时应回落 %v，实际 %v", llm.CalibDefault, got)
	}
	var nilApp *App
	if got := nilApp.calibFor("gpt-4o"); got != llm.CalibDefault {
		t.Fatalf("nil App 应回落 %v，实际 %v", llm.CalibDefault, got)
	}
}

// 无锚点时套系数；有锚点时锚点是真值，绝不再乘一次
func TestContextStatCalibOnlyWithoutAnchor(t *testing.T) {
	sess := &store.Session{Messages: []store.Message{{Role: store.RoleUser, Content: strings.Repeat("内容", 50)}}}
	msgs := buildRunMessages(sess, "", false, nil)

	base := agent.ContextStatOf(sess, msgs, 1_000_000, nil, 0, 1.0)
	doubled := agent.ContextStatOf(sess, msgs, 1_000_000, nil, 0, 2.0)
	if doubled.UsedTokens != base.UsedTokens*2 {
		t.Fatalf("无锚点时 2.0 系数应给出两倍用量：%d vs %d", doubled.UsedTokens, base.UsedTokens)
	}
	if !calibNear(doubled.CalibRatio, 2.0) {
		t.Fatalf("统计里应带上所用系数，实际 %v", doubled.CalibRatio)
	}

	anchor := &agent.TokenAnchor{InputTokens: 1234, MsgCount: len(msgs)}
	a1 := agent.ContextStatOf(sess, msgs, 1_000_000, anchor, 0, 1.0)
	a2 := agent.ContextStatOf(sess, msgs, 1_000_000, anchor, 0, 3.0)
	if a1.UsedTokens != a2.UsedTokens {
		t.Fatalf("有锚点时系数不该参与计算：%d vs %d", a1.UsedTokens, a2.UsedTokens)
	}
	if !a1.HasAnchor {
		t.Fatal("有锚点时应标记 HasAnchor")
	}

	// extra（工具定义开销）也是字符估算，同样要缩放
	e1 := agent.ContextStatOf(sess, msgs, 1_000_000, nil, 400, 1.0)
	e2 := agent.ContextStatOf(sess, msgs, 1_000_000, nil, 400, 2.0)
	if e2.UsedTokens != e1.UsedTokens*2 {
		t.Fatalf("工具定义开销应一并缩放：%d vs %d", e2.UsedTokens, e1.UsedTokens)
	}
}

// saved 也随系数缩放：两侧同系数不会互相抵消
func TestSavedTokensScalesWithCalib(t *testing.T) {
	sess := &store.Session{
		Messages:           []store.Message{{Role: store.RoleUser, Content: strings.Repeat("内容", 100)}},
		ContextSummary:     "摘要正文",
		ContextCoveredUpTo: 1,
	}
	one := agent.SummarySavedTokens(sess, 1.0)
	if one <= 0 {
		t.Fatalf("压缩态应算出正数，实际 %d", one)
	}
	if two := agent.SummarySavedTokens(sess, 2.0); two != one*2 {
		t.Fatalf("2.0 系数下应为两倍：%d vs %d", two, one)
	}
}

// 报告只在系数不等于 1 时打印那一行，避免常态噪声
func TestReportShowsCalibOnlyWhenNotOne(t *testing.T) {
	st := &agent.ContextStat{UsedTokens: 100, WindowTokens: 1000, CalibRatio: 1.0}
	if rep := agent.FormatContextStatReport(st, 12); strings.Contains(rep, "估算系数") {
		t.Fatalf("系数为 1 时不该打印该行：\n%s", rep)
	}
	st.CalibRatio = 1.23
	if rep := agent.FormatContextStatReport(st, 12); !strings.Contains(rep, "×1.23") {
		t.Fatalf("应打印校准系数：\n%s", rep)
	}
}
