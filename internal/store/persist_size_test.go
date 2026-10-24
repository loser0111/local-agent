package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// ===== 工具结果落盘截断 =====

// 超限的 Result 被截断，且不越界。
func TestTruncateToolCallResults_TruncatesOversized(t *testing.T) {
	big := strings.Repeat("a", MaxPersistedToolResult*3)
	calls := []ToolCall{{ID: "c1", Name: "read_file", Result: big}}

	got := truncateToolCallResults(calls)

	if len(got[0].Result) >= len(big) {
		t.Fatalf("应被截断，实际长度 %d（原 %d）", len(got[0].Result), len(big))
	}
	if !strings.Contains(got[0].Result, "已截断") {
		t.Fatalf("截断后应含说明文案，实际: %q", got[0].Result[len(got[0].Result)-60:])
	}
	// 必须能报告原始长度，否则用户无从判断丢了多少
	if !strings.Contains(got[0].Result, "原文") {
		t.Fatal("截断说明应包含原始字节数")
	}
}

// 关键：不能修改入参。入参切片与内存中的执行记录共享底层数组，
// 原地改 Result 会连带污染 tool 消息内容（发给模型的那份）与 trace 上报。
func TestTruncateToolCallResults_DoesNotMutateInput(t *testing.T) {
	big := strings.Repeat("b", MaxPersistedToolResult*2)
	original := []ToolCall{{ID: "c1", Name: "read_file", Result: big}}

	_ = truncateToolCallResults(original)

	if len(original[0].Result) != len(big) {
		t.Fatalf("入参被修改：期望 %d 字节，实际 %d", len(big), len(original[0].Result))
	}
}

// 未超限时零分配（返回原切片），避免无谓拷贝。
func TestTruncateToolCallResults_NoCopyWhenSmall(t *testing.T) {
	calls := []ToolCall{
		{ID: "c1", Name: "grep", Result: "small"},
		{ID: "c2", Name: "glob", Result: "tiny"},
	}
	got := truncateToolCallResults(calls)
	if &got[0] != &calls[0] {
		t.Fatal("未超限时应返回原切片，不应拷贝")
	}
}

// 空切片与 nil 不应 panic。
func TestTruncateToolCallResults_Empty(t *testing.T) {
	if got := truncateToolCallResults(nil); got != nil {
		t.Fatalf("nil 应返回 nil，实际: %v", got)
	}
	if got := truncateToolCallResults([]ToolCall{}); len(got) != 0 {
		t.Fatalf("空切片应返回空，实际长度: %d", len(got))
	}
}

// 截断必须落在 rune 边界上——切坏多字节字符会产生非法 UTF-8 / 乱码。
func TestTruncateToolResult_UTF8Boundary(t *testing.T) {
	// 每字 3 字节，"中" 反复填充，使 MaxPersistedToolResult 恰好落在一个字符中间。
	filler := strings.Repeat("中", MaxPersistedToolResult/3+10)
	got := truncateToolResult(filler)

	if !utf8.ValidString(got) {
		t.Fatal("截断结果不是合法 UTF-8")
	}
	body := strings.SplitN(got, "\n\n[已截断", 2)[0]
	if !utf8.ValidString(body) {
		t.Fatal("正文部分不是合法 UTF-8")
	}
	if strings.ContainsRune(body, utf8.RuneError) {
		t.Fatal("正文包含替换字符，说明切在了字符中间")
	}
}

// ===== 原子写 =====

// 正常写入后能读回，且不残留临时文件。
func TestSaveSession_AtomicWrite_NoTempLeftover(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	ss := NewSessionStore(dir)
	sess, err := ss.CreateSession(SessionConfig{Project: "p", Model: "m", PermissionMode: "manual"})
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}
	if _, err := ss.AppendMessage(sess.ID, Message{Role: RoleUser, Content: "hi"}); err != nil {
		t.Fatalf("AppendMessage 失败: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取会话目录失败: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}

	got, err := ss.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession 失败: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "hi" {
		t.Fatalf("消息未正确落盘: %+v", got.Messages)
	}
}

// 落盘文件必须是合法 JSON（无缩进也要能解析）。
func TestSaveSession_ProducesValidJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	ss := NewSessionStore(dir)
	sess, err := ss.CreateSession(SessionConfig{Project: "p", Model: "m", PermissionMode: "manual"})
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, sess.ID+".json"))
	if err != nil {
		t.Fatalf("读取会话文件失败: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("会话文件不是合法 JSON: %v", err)
	}
	// 不应再有 MarshalIndent 产生的换行缩进
	if strings.Contains(string(raw), "\n  \"") {
		t.Fatal("会话文件仍带缩进，应改用 json.Marshal")
	}
}

// AppendMessage 落盘时截断 toolCalls[].result，但不影响 tool 消息 content。
func TestAppendMessage_TruncatesToolResultButKeepsToolContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	ss := NewSessionStore(dir)
	sess, err := ss.CreateSession(SessionConfig{Project: "p", Model: "m", PermissionMode: "manual"})
	if err != nil {
		t.Fatalf("CreateSession 失败: %v", err)
	}

	big := strings.Repeat("x", MaxPersistedToolResult*2)
	calls := []ToolCall{{ID: "c1", Name: "read_file", Status: "success", Result: big}}

	if _, err := ss.AppendMessage(sess.ID, Message{
		Role: RoleAssistant, ToolCalls: calls,
	}); err != nil {
		t.Fatalf("AppendMessage(assistant) 失败: %v", err)
	}
	// 同一份 big 仍要原样进 tool 消息——那是发给模型的，不能截
	if _, err := ss.AppendMessage(sess.ID, Message{
		Role: RoleTool, Content: big, ToolCallID: "c1",
	}); err != nil {
		t.Fatalf("AppendMessage(tool) 失败: %v", err)
	}

	// 入参不应被修改
	if len(calls[0].Result) != len(big) {
		t.Fatalf("入参 toolCalls 被修改：%d", len(calls[0].Result))
	}

	got, err := ss.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession 失败: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("应有 2 条消息，实际 %d", len(got.Messages))
	}
	if len(got.Messages[0].ToolCalls[0].Result) >= len(big) {
		t.Fatal("assistant.toolCalls[].result 应被截断")
	}
	if got.Messages[1].Content != big {
		t.Fatalf("tool 消息 content 不应被截断：期望 %d 字节，实际 %d",
			len(big), len(got.Messages[1].Content))
	}
}
