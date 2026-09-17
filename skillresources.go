package main

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ===== L3：技能自带资源的枚举与受限读取 =====
//
// 技能目录里的 references/ 与 assets/ 用 read_skill_file 读取；
// scripts/ 下的脚本交给 exec_shell 执行（它是命令，不是文本）。
//
// 与「让模型自己拼绝对路径调 read_file」相比，这里的价值是**边界**：
// 技能说明可以诱导模型读任何文件，而 read_skill_file 只能读到技能目录内的东西。

const (
	skillRefDir    = "references"
	skillAssetsDir = "assets"

	skillResourceMaxEntries = 100        // 单个技能最多枚举的条目数
	skillResourceMaxBytes   = 256 * 1024 // 单次读取上限
	skillResourceMaxDepth   = 3          // 相对技能目录的最大递归深度
)

// SkillResource 技能目录内的一个可读资源
type SkillResource struct {
	Path string `json:"path"` // 相对技能目录的路径（统一用 /），如 references/api.md
	Size int64  `json:"size"`
	Kind string `json:"kind"` // scripts | references | assets | other
}

// listSkillResources 枚举技能目录内的资源（不含 SKILL.md 自身）。
// 技能目录内容不可控，因此有条目数、深度与隐藏文件的限制，避免拖慢扫描。
func listSkillResources(dir string) []SkillResource {
	out := make([]SkillResource, 0, 8)
	rootDepth := pathDepth(dir)
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 单个条目读不了不影响整体枚举
		}
		if path == dir {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if pathDepth(path)-rootDepth >= skillResourceMaxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if len(out) >= skillResourceMaxEntries {
			return fs.SkipDir
		}
		if path == filepath.Join(dir, skillFileName) {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		var size int64
		if info, err := d.Info(); err == nil {
			size = info.Size()
		}
		out = append(out, SkillResource{
			Path: filepath.ToSlash(rel),
			Size: size,
			Kind: skillResourceKind(rel),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// pathDepth 路径层级（用于限制递归深度）
func pathDepth(p string) int {
	return strings.Count(filepath.Clean(p), string(os.PathSeparator))
}

// skillResourceKind 按顶层目录归类
func skillResourceKind(rel string) string {
	seg := filepath.ToSlash(rel)
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	switch seg {
	case skillScriptsDir:
		return "scripts"
	case skillRefDir:
		return "references"
	case skillAssetsDir:
		return "assets"
	default:
		return "other"
	}
}

// resolveSkillResource 把技能内的相对路径解析为绝对路径，并确保没有逃出技能目录。
// 三道闸：拒绝绝对路径、清理 .. 后做前缀校验、软链接解析后再校验一次。
func resolveSkillResource(skillDir, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("path 不能为空")
	}
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("path 含非法字符")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(filepath.ToSlash(rel), "/") {
		return "", fmt.Errorf("path 必须是相对于技能目录的路径")
	}

	root := filepath.Clean(skillDir)
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(filepath.ToSlash(rel))))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path 越出技能目录: %s", rel)
	}

	// 软链接可能指向目录外：解析真实路径后重新校验
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		realRoot, err := filepath.EvalSymlinks(root)
		if err == nil && resolved != realRoot && !strings.HasPrefix(resolved, realRoot+string(os.PathSeparator)) {
			return "", fmt.Errorf("path 经软链接越出技能目录: %s", rel)
		}
	}
	return target, nil
}

// readSkillResource 读取技能内的文本资源；超限截断，二进制拒绝
func readSkillResource(skillDir, rel string) (string, error) {
	abs, err := resolveSkillResource(skillDir, rel)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("资源不存在: %s", rel)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("path 指向目录而非文件: %s", rel)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("读取资源失败: %w", err)
	}

	truncated := false
	if len(data) > skillResourceMaxBytes {
		data = data[:skillResourceMaxBytes]
		truncated = true
	}
	if isBinaryData(data) {
		return "", fmt.Errorf("资源是二进制文件，无法按文本读取: %s", rel)
	}

	out := string(data)
	if truncated {
		out += fmt.Sprintf("\n\n[…已截断，仅显示前 %dKB]", skillResourceMaxBytes/1024)
	}
	return out, nil
}

// isBinaryData 前 8KB 含 NUL 字节即视为二进制
func isBinaryData(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}

// ===== 内置工具：read_skill_file（L3）=====

// ReadSkillFileTool 读取技能自带资源（L3）。
// 与 read_skill 的区别：read_skill 排除 disable-model-invocation 的技能，
// 这里不排除——用户显式调用某个技能后，正文里引用的资料仍要读得到。
type ReadSkillFileTool struct {
	*BaseTool
	store   *SkillStore
	allowed func(*SkillMeta) bool
}

// NewReadSkillFileTool 构造 read_skill_file 工具
func NewReadSkillFileTool(store *SkillStore, skillWhitelist []string) *ReadSkillFileTool {
	wl := skillWhitelistSet(skillWhitelist)
	t := &ReadSkillFileTool{
		BaseTool: &BaseTool{
			Name: "read_skill_file",
			Description: "读取某个技能自带的文件（如 references/ 下的文档、assets/ 下的模板）。" +
				"路径相对于该技能的目录，只能用本工具读取技能目录内的文件。" +
				"scripts/ 下的脚本是命令，请改用 exec_shell 执行而不是读取。",
			Parameters: map[string]*ToolArgDef{
				"id":   {Type: "string", Description: "技能 ID"},
				"path": {Type: "string", Description: "相对于技能目录的路径，如 references/api.md"},
			},
		},
		store: store,
	}
	t.allowed = func(m *SkillMeta) bool {
		if !m.Usable() {
			return false
		}
		if len(wl) > 0 {
			return wl[m.ID]
		}
		return true
	}
	return t
}

// HasAvailable 是否存在本会话可读取的技能（决定是否注册该工具）
func (t *ReadSkillFileTool) HasAvailable() bool {
	for _, m := range t.store.GetAll() {
		if t.allowed(m) {
			return true
		}
	}
	return false
}

// Execute 读取技能内资源
func (t *ReadSkillFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	rel, _ := args["path"].(string)
	if id == "" {
		return "", fmt.Errorf("id 参数是必需的")
	}
	if rel == "" {
		return "", fmt.Errorf("path 参数是必需的")
	}
	m, ok := t.store.Get(id)
	if !ok {
		return "", fmt.Errorf("技能不存在: %s", id)
	}
	if !t.allowed(m) {
		return "", fmt.Errorf("技能在本会话不可用: %s", id)
	}
	if m.HasScripts && strings.HasPrefix(filepath.ToSlash(rel), skillScriptsDir+"/") {
		return "", fmt.Errorf("scripts/ 下的脚本请用 exec_shell 执行，而不是当作文本读取")
	}
	content, err := readSkillResource(m.Dir, rel)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("# %s/%s\n\n%s", id, filepath.ToSlash(rel), content), nil
}

// skillWhitelistSet 把会话技能白名单转成集合（空=全部可用）
func skillWhitelistSet(whitelist []string) map[string]bool {
	if len(whitelist) == 0 {
		return nil
	}
	wl := make(map[string]bool, len(whitelist))
	for _, id := range whitelist {
		wl[id] = true
	}
	return wl
}
