package app

import (
	"path/filepath"
	"strings"
	"testing"

	"wails-tmp/internal/store"
)

// 工作区里有 AGENTS.md 时，它的正文应进入系统提示词。
func TestProjectInstructionInjected(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), "# 项目约定\n跑测试用 make test\n")

	got := loadProjectInstruction(dir)
	if !strings.Contains(got, "项目指令（AGENTS.md）") {
		t.Fatalf("应带上文件名作为标题: %q", got)
	}
	if !strings.Contains(got, "跑测试用 make test") {
		t.Fatalf("应包含正文: %q", got)
	}
}

// 优先级：多个指令文件同时存在时只取第一个（AGENTS.md > CLAUDE.md > .cursorrules），
// 避免把几份可能互相冲突的约定都塞给模型。
func TestProjectInstructionPriority(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), "agents 约定")
	writeTestFile(t, filepath.Join(dir, "CLAUDE.md"), "claude 约定")

	got := loadProjectInstruction(dir)
	if !strings.Contains(got, "agents 约定") {
		t.Fatalf("应取优先级最高的 AGENTS.md: %q", got)
	}
	if strings.Contains(got, "claude 约定") {
		t.Fatalf("低优先级文件不应同时注入: %q", got)
	}
}

// 没有任何指令文件时返回空串（调用方据此不追加内容）。
func TestProjectInstructionAbsent(t *testing.T) {
	if got := loadProjectInstruction(t.TempDir()); got != "" {
		t.Fatalf("无指令文件应返回空串，实际 %q", got)
	}
	// 空工作区目录同样安全
	if got := loadProjectInstruction(""); got != "" {
		t.Fatalf("空目录应返回空串，实际 %q", got)
	}
}

// 超大指令文件按字符截断（多字节安全），不会把整段塞进上下文。
func TestProjectInstructionTruncated(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), strings.Repeat("字", projectInstructionMaxRunes+500))

	got := loadProjectInstruction(dir)
	if !strings.Contains(got, "…") {
		t.Fatal("超长正文应被截断并带省略号")
	}
	if len([]rune(got)) > projectInstructionMaxRunes+200 {
		t.Fatalf("截断后仍过长: %d 字符", len([]rune(got)))
	}
}

// 端到端：buildBasePrompt 会把项目指令拼进系统提示词。
func TestBuildBasePromptIncludesProjectInstruction(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "AGENTS.md"), "本项目用 pnpm，不要用 npm")

	app := &App{}
	prompt := app.buildBasePrompt(&store.Session{}, dir)
	if !strings.Contains(prompt, "本项目用 pnpm，不要用 npm") {
		t.Fatalf("系统提示词应包含项目指令: %q", prompt)
	}
}
