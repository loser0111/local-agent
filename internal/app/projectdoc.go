package app

import (
	"os"
	"path/filepath"
	"strings"

	"wails-tmp/internal/store"
)

// projectInstructionFiles 会话工作区里会被自动注入系统提示词的指令文件，按优先级排列。
//
// 只认**第一个存在的**：多个同时存在时取优先级最高的那个，而不是把几份可能互相冲突的
// 约定都塞进去——那样模型要在两份矛盾的要求之间自行取舍，结果不可预期。
// 顺序按"跨工具通用 → 工具专属"排：AGENTS.md 是约定俗成的通用名，其余是各家的旧名字。
var projectInstructionFiles = []string{
	"AGENTS.md",
	"CLAUDE.md",
	".cursorrules",
}

// projectInstructionMaxRunes 注入的指令正文上限（按字符，多字节安全）。
//
// 这段文字会出现在**每一轮请求**的系统提示词里（system 恒为第 0 条），不设上限的话
// 一个超大文件会长期挤占上下文窗口。超限按字符截断并加省略号。
const projectInstructionMaxRunes = 8000

// loadProjectInstruction 读取会话工作区根目录的项目指令文件，返回可直接追加到系统提示词的段落。
//
// 找不到任何指令文件时返回空串（调用方据此不追加任何内容）。
// 读取失败（权限、路径不存在等）一律按"没有指令"处理，不阻断对话——它只是锦上添花。
//
// dir 是会话绑定的工作区目录（见 resolveProjectDir）；为空串时直接返回空串。
func loadProjectInstruction(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	for _, name := range projectInstructionFiles {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		body := strings.TrimSpace(string(data))
		if body == "" {
			continue
		}
		body = store.TruncateRunes(body, projectInstructionMaxRunes)
		return "\n\n## 项目指令（" + name + "）\n" +
			"以下内容来自工作区根目录的 " + name + "，是本仓库对 agent 的约定，" +
			"在本次会话中优先遵守；若它与上面的通用偏好冲突，以它为准：\n\n" + body
	}
	return ""
}
