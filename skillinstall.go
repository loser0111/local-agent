package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ===== 技能安装器 =====
//
// 支持两种来源（与「本地 agent」的定位匹配：不做在线市场）：
//
//	1. 本地文件夹 / zip：用户从别处拿到的技能包（含从 Claude Code、Codex 的
//	   skills 目录导出的一份），校验后复制进本项目的技能根目录。
//	2. Git 仓库：记录 URL/ref/子目录，之后可一键重新拉取更新。
//
// 三条不变式：
//   - 先校验后落盘：目标目录里出现半个技能比安装失败更糟，因此一律先解到
//     技能根目录下的临时目录，校验通过后整目录 rename（同盘内原子）。
//   - 冲突先拦：批量安装前先检查所有目标 ID，有任一冲突就整体拒绝，不做半截安装。
//   - 路径不许逃逸：zip 条目、软链接、git 子目录都要落在允许范围内。
const (
	skillArchiveMaxEntries = 2000              // 单个压缩包最多条目数
	skillArchiveMaxBytes   = 64 << 20          // 解压后总大小上限
	skillCopyMaxFiles      = 2000              // 单个技能最多复制文件数
	skillCopyMaxBytes      = 64 << 20          // 单个技能复制总大小上限
	skillDefaultGitTimeout = 120 * time.Second
)

// SkillInstallResult 单个技能的安装结果
type SkillInstallResult struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Dir      string   `json:"dir"`
	Action   string   `json:"action"`             // installed | updated
	Warnings []string `json:"warnings,omitempty"`
}

// SkillInstaller 技能安装器（持有 SkillStore 以确定落点与刷新）
type SkillInstaller struct {
	store *SkillStore
}

// NewSkillInstaller 创建安装器
func NewSkillInstaller(store *SkillStore) *SkillInstaller {
	return &SkillInstaller{store: store}
}

// InstallFromFolder 从本地文件夹安装。
// 目标文件夹自身可以是技能目录（含 SKILL.md），也可以是「一堆技能目录」的父目录。
func (i *SkillInstaller) InstallFromFolder(src string) ([]*SkillInstallResult, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, fmt.Errorf("未指定文件夹路径")
	}
	fi, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("文件夹不存在: %s", src)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("不是文件夹: %s", src)
	}
	info := &SkillInstallInfo{SourceType: "folder", Source: src, InstalledAt: time.Now().Unix()}
	return i.installFromDir(src, info, false)
}

// InstallFromZip 从 zip 压缩包安装（先解到临时目录，再按文件夹安装）
func (i *SkillInstaller) InstallFromZip(zipPath string) ([]*SkillInstallResult, error) {
	zipPath = strings.TrimSpace(zipPath)
	if zipPath == "" {
		return nil, fmt.Errorf("未指定压缩包路径")
	}
	tmp, err := os.MkdirTemp("", "local-agent-skill-zip-")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := extractSkillZip(zipPath, tmp); err != nil {
		return nil, err
	}
	info := &SkillInstallInfo{SourceType: "zip", Source: zipPath, InstalledAt: time.Now().Unix()}
	return i.installFromDir(tmp, info, false)
}

// InstallFromGit 从 Git 仓库安装（可指定分支/标签与仓库内子目录）
func (i *SkillInstaller) InstallFromGit(url, ref, subdir string) ([]*SkillInstallResult, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, fmt.Errorf("未指定 Git 仓库地址")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("未找到 git 命令，请先安装 git 再使用仓库安装")
	}

	tmp, err := os.MkdirTemp("", "local-agent-skill-git-")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmp)

	args := []string{"clone", "--depth", "1"}
	if strings.TrimSpace(ref) != "" {
		args = append(args, "--branch", strings.TrimSpace(ref))
	}
	args = append(args, url, tmp)

	ctx, cancel := context.WithTimeout(context.Background(), skillDefaultGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	hideConsoleWindow(cmd)                                  // Windows 上不弹控制台窗口（见该函数说明）
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0") // 需要交互认证时直接失败，不要挂住界面
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git clone 失败: %s", msg)
	}

	src := tmp
	sub := strings.TrimSpace(subdir)
	if sub != "" {
		resolved, err := resolveSubdir(tmp, sub)
		if err != nil {
			return nil, err
		}
		src = resolved
	}
	info := &SkillInstallInfo{
		SourceType:  "git",
		Source:      url,
		Ref:         strings.TrimSpace(ref),
		Subdir:      sub,
		InstalledAt: time.Now().Unix(),
		CanUpdate:   true,
	}
	// git 来源允许覆盖同源技能（即更新）
	return i.installFromDir(src, info, true)
}

// Update 重新拉取某个 git 来源技能。失败时原有目录保持不变。
func (i *SkillInstaller) Update(id string) (*SkillInstallResult, error) {
	m, ok := i.store.Get(id)
	if !ok {
		return nil, fmt.Errorf("技能不存在: %s", id)
	}
	if m.Install == nil || m.Install.SourceType != "git" || strings.TrimSpace(m.Install.Source) == "" {
		return nil, fmt.Errorf("技能 %s 不是从 Git 仓库安装的，无法更新", id)
	}
	results, err := i.InstallFromGit(m.Install.Source, m.Install.Ref, m.Install.Subdir)
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, fmt.Errorf("仓库中已不再包含技能 %s，未做任何改动", id)
}

// ===== 核心：从目录安装 =====

// installFromDir 把 srcDir 下的技能装进技能根目录。
// replace 为 true 时允许覆盖「同为可更新来源」的既有技能（git 更新语义）。
func (i *SkillInstaller) installFromDir(srcDir string, info *SkillInstallInfo, replace bool) ([]*SkillInstallResult, error) {
	roots, err := findSkillRoots(srcDir)
	if err != nil {
		return nil, err
	}

	// 第一遍：全部解析校验 + 冲突检查（不做任何写操作，避免半截安装）
	type plan struct {
		id       string
		srcDir   string
		name     string
		warnings []string
		existing *SkillMeta
	}
	plans := make([]*plan, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		id := filepath.Base(filepath.Clean(root))
		if !validSkillID(id) {
			return nil, fmt.Errorf("技能目录名 %q 不是合法 ID（只能用字母、数字、下划线、短横线）", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("来源中存在同名技能: %s", id)
		}
		seen[id] = true

		fm, body, err := readSkillDocument(root)
		if err != nil {
			return nil, fmt.Errorf("技能 %s 无法解析: %w", id, err)
		}
		errs, warns := ValidateSkillDocument(id, fm, body)
		if len(errs) > 0 {
			return nil, fmt.Errorf("技能 %s 校验未通过: %s", id, strings.Join(errs, "；"))
		}

		p := &plan{id: id, srcDir: root, name: id, warnings: warns}
		if fm != nil && fm.Name != "" {
			p.name = fm.Name
		}
		if existing, ok := i.store.Get(id); ok {
			p.existing = existing
			if !replace || existing.Install == nil || existing.Install.SourceType != "git" {
				return nil, fmt.Errorf("技能 %s 已存在（%s），请先卸载或改用其他 ID", id, existing.Dir)
			}
		}
		plans = append(plans, p)
	}

	// 第二遍：逐个复制落盘
	results := make([]*SkillInstallResult, 0, len(plans))
	for _, p := range plans {
		action := "installed"
		if p.existing != nil {
			action = "updated"
		}
		dir, err := i.placeSkillDir(p.srcDir, p.id, p.existing)
		if err != nil {
			return results, err
		}
		result := &SkillInstallResult{ID: p.id, Name: p.name, Dir: dir, Action: action, Warnings: p.warnings}
		results = append(results, result)
	}

	// 第三遍：刷新 + 记录安装来源
	i.store.Refresh()
	for _, r := range results {
		meta := info
		if meta != nil {
			cp := *meta
			// fork 出来的副本按技能独立记时间，便于界面展示「何时安装的」
			cp.InstalledAt = time.Now().Unix()
			meta = &cp
		}
		if err := i.store.SetInstall(r.ID, meta); err != nil {
			return results, err
		}
	}
	return results, nil
}

// placeSkillDir 把来源技能目录复制到技能根目录下的临时目录，校验后整目录替换到位
func (i *SkillInstaller) placeSkillDir(srcDir, id string, existing *SkillMeta) (string, error) {
	root := i.store.Dir()
	staging, err := os.MkdirTemp(root, ".staging-"+id+"-")
	if err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}
	// 失败路径统一清理：临时目录里放的是半成品，绝不能留在技能根目录下
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staging)
		}
	}()

	if err := copySkillTree(srcDir, staging); err != nil {
		return "", err
	}

	target := filepath.Join(root, id)
	if existing != nil {
		// 更新：旧目录先挪到临时备份，换上新目录后再删备份；
		// 中途失败则把备份恢复回去，保证旧版本不丢。
		backup := filepath.Join(root, ".backup-"+id)
		_ = os.RemoveAll(backup)
		if err := os.Rename(target, backup); err != nil {
			return "", fmt.Errorf("备份旧版本失败: %w", err)
		}
		if err := os.Rename(staging, target); err != nil {
			_ = os.Rename(backup, target) // 回滚
			return "", fmt.Errorf("替换新版本失败: %w", err)
		}
		committed = true
		_ = os.RemoveAll(backup)
		return target, nil
	}

	if err := os.Rename(staging, target); err != nil {
		return "", fmt.Errorf("落盘失败: %w", err)
	}
	committed = true
	return target, nil
}

// findSkillRoots 在 srcDir 下定位技能目录。
// srcDir 自身有 SKILL.md 就是单个技能；否则扫描它的直接子目录（一层）。
func findSkillRoots(srcDir string) ([]string, error) {
	if isFile(filepath.Join(srcDir, skillFileName)) {
		return []string{srcDir}, nil
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}
	roots := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// 打包常见噪音：隐藏目录、macOS 资源叉、版本控制目录
		if strings.HasPrefix(name, ".") || name == "__MACOSX" {
			continue
		}
		sub := filepath.Join(srcDir, name)
		if isFile(filepath.Join(sub, skillFileName)) {
			roots = append(roots, sub)
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("未找到 %s：来源目录里既没有技能，也没有含 %s 的子目录", skillFileName, skillFileName)
	}
	return roots, nil
}

// copySkillTree 递归复制技能目录。
// 跳过 .git 与隐藏文件；软链接一律跳过（不跟随，避免复制出目录外的东西）。
func copySkillTree(src, dst string) error {
	files := 0
	var total int64
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil // 跳过软链接
		}
		if !info.Mode().IsRegular() {
			return nil // 跳过设备文件/管道等
		}
		files++
		total += info.Size()
		if files > skillCopyMaxFiles {
			return fmt.Errorf("技能文件数超过 %d，已中止", skillCopyMaxFiles)
		}
		if total > skillCopyMaxBytes {
			return fmt.Errorf("技能体积超过 %dMB，已中止", skillCopyMaxBytes>>20)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

// copyFile 复制单个文件（保留可执行位：脚本要靠它才能被 exec_shell 跑起来）
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// extractSkillZip 解压技能包，逐条目做路径与体积校验
func extractSkillZip(zipPath, dst string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开压缩包失败: %w", err)
	}
	defer r.Close()
	if len(r.File) > skillArchiveMaxEntries {
		return fmt.Errorf("压缩包条目过多（%d > %d）", len(r.File), skillArchiveMaxEntries)
	}

	cleanDst := filepath.Clean(dst)
	var total int64
	for _, f := range r.File {
		name := filepath.ToSlash(f.Name)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "__MACOSX/") {
			continue // macOS 压缩产生的资源叉
		}
		// 软链接条目可能是逃逸的捷径，直接拒绝而不是解出来
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("压缩包含软链接条目，已拒绝: %s", f.Name)
		}
		if strings.HasPrefix(name, "/") || filepath.IsAbs(f.Name) || hasDotDotSegment(name) {
			return fmt.Errorf("压缩包含非法路径: %s", f.Name)
		}
		target := filepath.Join(cleanDst, filepath.FromSlash(name))
		if target != cleanDst && !strings.HasPrefix(target, cleanDst+string(os.PathSeparator)) {
			return fmt.Errorf("压缩包路径越出目标目录: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		total += int64(f.UncompressedSize64)
		if total > skillArchiveMaxBytes {
			return fmt.Errorf("压缩包解压后超过 %dMB，已中止", skillArchiveMaxBytes>>20)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

// writeZipEntry 写出单个 zip 条目
func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	perm := f.Mode().Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// hasDotDotSegment 路径里是否存在 ".." 段（仅靠前缀判断会漏掉 a/../../b 这类写法）
func hasDotDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// resolveSubdir 把仓库内子目录解析成绝对路径，并确保它就在仓库内
func resolveSubdir(repoDir, sub string) (string, error) {
	norm := filepath.ToSlash(sub)
	if filepath.IsAbs(sub) || strings.HasPrefix(norm, "/") || hasDotDotSegment(norm) {
		return "", fmt.Errorf("仓库内子目录必须是相对路径且不含 ..: %s", sub)
	}
	root := filepath.Clean(repoDir)
	target := filepath.Clean(filepath.Join(root, filepath.FromSlash(norm)))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("仓库内子目录越出仓库范围: %s", sub)
	}
	fi, err := os.Stat(target)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("仓库内不存在子目录: %s", sub)
	}
	return target, nil
}

// isFile 路径是否存在且是普通文件
func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
