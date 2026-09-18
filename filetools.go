package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ===== 工作区文件工具 =====
//
// 这组工具把「文件操作」从 exec_shell 里拆出来，意义有三：
//
//  1. 权限判定的主体从「命令字符串」变成「工具 + 路径」，不必再猜 `sed -i` 之类改的是哪个文件；
//  2. acceptEdits 模式终于有意义：项目内的文件编辑可以自动放行，命令仍逐条询问；
//  3. 写操作能记录 before/after，差异可以归因到具体是哪次工具调用改的（见 FileChangeLog）。
//
// 路径约定：相对路径相对于会话工作区目录（root）；root 为空时回退到进程工作目录。

// 工具名
//
// 这里只放「直出名录的成员」用到的名字；exec_shell 的实现虽在 tools.go，
// 但它的名字要参与 directToolOrder，因此一并定义在此，避免两处字面量各写各的。
const (
	toolExecShell = "exec_shell"
	toolReadFile  = "read_file"
	toolWriteFile = "write_file"
	toolEditFile  = "edit_file"
	toolGlob      = "glob"
	toolGrep      = "grep"
	toolListDir   = "list_dir"
)

// directToolOrder 直出给模型的工具顺序（固定顺序便于断言与提示缓存）。
// 其余工具（MCP / 自定义 CLI / API）仍经 tool_router 发现，避免 prompt 膨胀。
//
// 判据不是"它常不常用"，而是**系统提示词有没有点名它**：提示词里提到的工具必须直出，
// 否则模型在工具列表里找不到它的 schema，只能先花一轮去 tool_router 里 list/describe，
// 甚至照着名字瞎猜参数——两者不一致正是"模型报找不到某工具"这类问题的来源。
var directToolOrder = []string{
	toolReadFile, toolWriteFile, toolEditFile, toolGlob, toolGrep, toolListDir, toolAskUser,
	// exec_shell 直出：buildBasePrompt 明确点名了它（"exec_shell 留给构建、测试、git 等
	// 真正的命令"），却曾因不在本名录而退化成"要先经 tool_router 发现"。构建/测试/git 是
	// 高频操作，每次首用都可能多耗 1-2 轮；它只有一个 cmd 参数，直出的 prompt 代价极小。
	toolExecShell,
	// spawn_agent 直出而不是经路由器：系统提示词里点名了它（buildBasePrompt 明确告诉模型
	// "可以用 spawn_agent 派子代理"），若工具列表里没有它的 schema，模型只能先花一轮
	// 去 tool_router 里 list/describe，否则就是照着名字瞎猜参数。
	toolSpawnAgent,
}

// 各类上限：避免一次工具调用把上下文或内存撑爆
const (
	maxReadFileBytes  = 2 << 20 // 单文件读取上限 2MB
	defaultReadLimit  = 2000    // read_file 默认返回行数
	maxWriteFileBytes = 2 << 20 // 单文件写入上限 2MB
	maxGlobResults    = 200     // glob 结果上限
	maxGrepFiles      = 20000   // grep 扫描文件数上限
	maxGrepFileBytes  = 2 << 20 // grep 单文件扫描上限
	maxGrepResults    = 200     // grep 结果上限
	maxDirEntries     = 500     // list_dir 条目上限
	maxAttributedDiff = 64 << 10
)

// skipDirNames 遍历时跳过的目录（噪音大且几乎不会被有意搜索）
var skipDirNames = map[string]bool{
	".git": true, "node_modules": true, ".idea": true, ".vscode": true,
	"__pycache__": true, ".venv": true, "venv": true, "target": true,
}

// ===== 运行上下文 =====

// fileToolContext 文件类工具的运行上下文：基准目录 + 归因记录的目标会话
type fileToolContext struct {
	root      string         // 工作区目录；为空时用进程工作目录
	sessionID string         // 会话 ID（归因记录用）
	changes   *FileChangeLog // 可为 nil（不记录）
}

func (c fileToolContext) baseDir() string {
	if strings.TrimSpace(c.root) != "" {
		return c.root
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// resolve 把参数里的路径解析成绝对路径。空路径报错；`~` 展开为家目录。
func (c fileToolContext) resolve(raw string) (string, error) {
	p := strings.Trim(strings.TrimSpace(raw), `"'`)
	if p == "" {
		return "", fmt.Errorf("path 参数不能为空")
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法定位家目录: %w", err)
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.baseDir(), p)
	}
	return filepath.Clean(p), nil
}

// rel 返回相对工作区的展示路径（在工作区外则原样返回绝对路径）
func (c fileToolContext) rel(path string) string {
	base := c.baseDir()
	if r, err := filepath.Rel(base, path); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(path)
}

// ===== 归因：文件改动记录 =====

// FileChange 一次工具对文件的改动
type FileChange struct {
	Time    int64  `json:"time"`
	Tool    string `json:"tool"`
	Path    string `json:"path"` // 绝对路径
	Rel     string `json:"rel"`  // 相对工作区的展示路径
	Action  string `json:"action"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Diff    string `json:"diff,omitempty"` // 统一 diff 文本（已截断）
}

// ToolFileChange 提供给界面的归因结果（带解析后的差异，复用差异面板的结构）
type ToolFileChange struct {
	Time    int64      `json:"time"`
	Tool    string     `json:"tool"`
	Path    string     `json:"path"`
	Rel     string     `json:"rel"`
	Action  string     `json:"action"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Files   []DiffFile `json:"files,omitempty"`
}

// FileChangeLog 会话级文件改动记录（内存；进程退出即失效）
type FileChangeLog struct {
	mu      sync.Mutex
	limit   int
	changes map[string][]FileChange
}

// NewFileChangeLog 创建改动记录；limit<=0 时取默认 500
func NewFileChangeLog(limit int) *FileChangeLog {
	if limit <= 0 {
		limit = 500
	}
	return &FileChangeLog{limit: limit, changes: map[string][]FileChange{}}
}

// Record 追加一条改动
func (l *FileChangeLog) Record(sessionID string, c FileChange) {
	if l == nil {
		return
	}
	if c.Time == 0 {
		c.Time = time.Now().UnixMilli()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.changes == nil {
		l.changes = map[string][]FileChange{}
	}
	list := append(l.changes[sessionID], c)
	if len(list) > l.limit {
		list = list[len(list)-l.limit:]
	}
	l.changes[sessionID] = list
}

// Mark 记录当前位置，配合 Since 取出「某次工具调用期间」的改动
func (l *FileChangeLog) Mark(sessionID string) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.changes[sessionID])
}

// Since 取出 mark 之后的改动
func (l *FileChangeLog) Since(sessionID string, mark int) []FileChange {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.changes[sessionID]
	if mark < 0 || mark > len(list) {
		mark = 0
	}
	out := make([]FileChange, len(list)-mark)
	copy(out, list[mark:])
	return out
}

// List 返回某会话的全部改动
func (l *FileChangeLog) List(sessionID string) []FileChange {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	src := l.changes[sessionID]
	out := make([]FileChange, len(src))
	copy(out, src)
	return out
}

// Clear 清空某会话的改动记录
func (l *FileChangeLog) Clear(sessionID string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.changes, sessionID)
}

// record 记录一次写操作（before 为空表示新建）
func (c fileToolContext) record(tool, path, before, after string) {
	if c.changes == nil {
		return
	}
	action := "update"
	if before == "" {
		action = "create"
	}
	diff := unifiedDiffText(before, after)
	added, removed := diffCounts(diff)
	c.changes.Record(c.sessionID, FileChange{
		Tool:    tool,
		Path:    path,
		Rel:     c.rel(path),
		Action:  action,
		Added:   added,
		Removed: removed,
		Diff:    diff,
	})
}

// ===== 差异文本（复用差异面板的解析） =====

// unifiedDiffText 用 `git diff --no-index` 生成统一 diff 文本，风格与差异面板一致。
// 注意不能直接用 runGit：有差异时 git 退出码为 1，runGit 会把输出丢掉。
func unifiedDiffText(before, after string) string {
	dir, err := os.MkdirTemp("", "la-filediff-")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(dir)

	oldPath := filepath.Join(dir, "before")
	newPath := filepath.Join(dir, "after")
	if err := os.WriteFile(oldPath, []byte(before), 0o600); err != nil {
		return ""
	}
	if err := os.WriteFile(newPath, []byte(after), 0o600); err != nil {
		return ""
	}

	out, err := runGitDiff(dir, "diff", "--no-index", "--unified=3", "--", oldPath, newPath)
	if err != nil {
		return ""
	}
	// 把临时绝对路径换成 before / after，便于阅读
	out = strings.ReplaceAll(out, oldPath, "before")
	out = strings.ReplaceAll(out, newPath, "after")
	if len(out) > maxAttributedDiff {
		out = out[:maxAttributedDiff] + "\n…（差异过长已截断）"
	}
	return out
}

// runGitDiff 执行 git 命令：即使退出码非 0，只要 stdout 有内容也照常返回
func runGitDiff(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	full := append([]string{"-C", dir, "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	hideConsoleWindow(cmd) // Windows 上不弹控制台窗口（见该函数说明）
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil && stdout.Len() == 0 {
		return "", fmt.Errorf("git %s 失败: %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// diffCounts 从统一 diff 文本里统计增删行数（解析失败时返回 0,0）
func diffCounts(diff string) (int, int) {
	if strings.TrimSpace(diff) == "" {
		return 0, 0
	}
	files := ParseUnifiedDiff(diff)
	if len(files) == 0 {
		return 0, 0
	}
	return sumAdd(files), sumDel(files)
}

// ===== 公共读取工具 =====

// looksBinary 粗判二进制：前 8KB 含 NUL，或控制字符占比过高。
// 刻意不用 utf8.Valid 判断——8KB 截断可能正好切断一个多字节字符，会把文本误判成二进制。
func looksBinary(data []byte) bool {
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	if len(head) == 0 {
		return false
	}
	ctrl := 0
	for _, b := range head {
		if b < 0x09 || (b > 0x0d && b < 0x20) {
			ctrl++
		}
	}
	return ctrl*100/len(head) > 10
}

// readFileSafe 读取文件并做大小/类型检查
func readFileSafe(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("文件不存在: %s", path)
		}
		return "", fmt.Errorf("读取文件信息失败: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s 是目录，请用 list_dir", path)
	}
	if info.Size() > maxReadFileBytes {
		return "", fmt.Errorf("文件过大（%d 字节，上限 %d），请用 grep 定位或分片读取", info.Size(), maxReadFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取失败: %w", err)
	}
	if looksBinary(data) {
		return "", fmt.Errorf("疑似二进制文件，未返回内容: %s", path)
	}
	return string(data), nil
}

// splitLines 按行切分（保留末行无换行的情形）
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1] // 结尾换行不算一行
	}
	return lines
}

// ===== read_file =====

type readFileTool struct {
	*BaseTool
	ctx      fileToolContext
	required []string
}

func newReadFileTool(bc buildContext) *readFileTool {
	params, required := fileToolParams([]fileArg{
		{Name: "path", Type: "string", Description: "要读取的文件路径（相对工作区目录或绝对路径）", Required: true},
		{Name: "offset", Type: "number", Description: "起始行号（从 1 开始，可选）"},
		{Name: "limit", Type: "number", Description: fmt.Sprintf("最多读取行数（默认 %d）", defaultReadLimit)},
	})
	return &readFileTool{
		BaseTool: &BaseTool{
			Name:        toolReadFile,
			Description: "读取工作区内某个文本文件的内容，返回带行号的文本。大文件可用 offset/limit 分片读取；查找内容请优先用 grep。",
			Parameters:  params,
		},
		ctx:      bc.fileCtx,
		required: required,
	}
}

func (t *readFileTool) RequiredParams() []string { return t.required }

// PermissionSubject 路径类主体（读）
func (t *readFileTool) PermissionSubject(args map[string]interface{}) Subject {
	return pathSubjectFor(t.GetName(), SubjectActionRead, t.ctx, args)
}

func (t *readFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	raw, _ := args["path"].(string)
	path, err := t.ctx.resolve(raw)
	if err != nil {
		return "", err
	}
	content, err := readFileSafe(path)
	if err != nil {
		return "", err
	}

	lines := splitLines(content)
	total := len(lines)

	offset := intArg(args, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	limit := intArg(args, "limit", defaultReadLimit)
	if limit <= 0 {
		limit = defaultReadLimit
	}
	if offset > total {
		return fmt.Sprintf("%s 共 %d 行，起始行 %d 超出范围", t.ctx.rel(path), total, offset), nil
	}

	end := offset - 1 + limit
	if end > total {
		end = total
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s（共 %d 行，显示 %d-%d）\n", t.ctx.rel(path), total, offset, end)
	for i := offset - 1; i < end; i++ {
		fmt.Fprintf(&sb, "%d\t%s\n", i+1, lines[i])
	}
	if end < total {
		fmt.Fprintf(&sb, "…（还有 %d 行未显示，可用 offset=%d 继续）\n", total-end, end+1)
	}
	return sb.String(), nil
}

// ===== write_file =====

type writeFileTool struct {
	*BaseTool
	ctx      fileToolContext
	required []string
}

func newWriteFileTool(bc buildContext) *writeFileTool {
	params, required := fileToolParams([]fileArg{
		{Name: "path", Type: "string", Description: "要写入的文件路径（相对工作区目录或绝对路径）", Required: true},
		{Name: "content", Type: "string", Description: "完整文件内容（会覆盖原有内容）", Required: true},
	})
	return &writeFileTool{
		BaseTool: &BaseTool{
			Name:        toolWriteFile,
			Description: "写入（新建或整体覆盖）一个文本文件。局部修改请用 edit_file，避免覆盖他人改动。",
			Parameters:  params,
		},
		ctx:      bc.fileCtx,
		required: required,
	}
}

func (t *writeFileTool) RequiredParams() []string { return t.required }

func (t *writeFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	raw, _ := args["path"].(string)
	path, err := t.ctx.resolve(raw)
	if err != nil {
		return "", err
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content 参数是必需的")
	}
	if len(content) > maxWriteFileBytes {
		return "", fmt.Errorf("内容过大（%d 字节，上限 %d）", len(content), maxWriteFileBytes)
	}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return "", fmt.Errorf("%s 是目录", path)
	}

	before := ""
	if data, err := os.ReadFile(path); err == nil {
		before = string(data)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	t.ctx.record(toolWriteFile, path, before, content)

	verdict := "已新建"
	if before != "" {
		verdict = "已覆盖"
	}
	return fmt.Sprintf("%s %s（%d 行，%d 字节）", verdict, t.ctx.rel(path), len(splitLines(content)), len(content)), nil
}

// PermissionSubject 路径类主体（写）
func (t *writeFileTool) PermissionSubject(args map[string]interface{}) Subject {
	return pathSubjectFor(t.GetName(), SubjectActionWrite, t.ctx, args)
}

// ===== edit_file =====

type editFileTool struct {
	*BaseTool
	ctx      fileToolContext
	required []string
}

func newEditFileTool(bc buildContext) *editFileTool {
	params, required := fileToolParams([]fileArg{
		{Name: "path", Type: "string", Description: "要修改的文件路径", Required: true},
		{Name: "old_string", Type: "string", Description: "被替换的原文（需与文件内容完全一致，含缩进）", Required: true},
		{Name: "new_string", Type: "string", Description: "替换后的新文本", Required: true},
		{Name: "replace_all", Type: "boolean", Description: "为 true 时替换所有匹配处，默认 false（要求唯一匹配）"},
	})
	return &editFileTool{
		BaseTool: &BaseTool{
			Name: toolEditFile,
			Description: "对文件做精确字符串替换。old_string 必须在文件中唯一出现（否则用 replace_all 或补充上下文）；" +
				"不确定文件内容时先用 read_file 查看。",
			Parameters: params,
		},
		ctx:      bc.fileCtx,
		required: required,
	}
}

func (t *editFileTool) RequiredParams() []string { return t.required }

func (t *editFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	raw, _ := args["path"].(string)
	path, err := t.ctx.resolve(raw)
	if err != nil {
		return "", err
	}
	oldStr, ok := args["old_string"].(string)
	if !ok {
		return "", fmt.Errorf("old_string 参数是必需的")
	}
	newStr, ok2 := args["new_string"].(string)
	if !ok2 {
		return "", fmt.Errorf("new_string 参数是必需的")
	}
	if oldStr == "" {
		return "", fmt.Errorf("old_string 不能为空")
	}
	if oldStr == newStr {
		return "", fmt.Errorf("old_string 与 new_string 相同，无需修改")
	}
	replaceAll := boolArg(args, "replace_all")

	before, err := readFileSafe(path)
	if err != nil {
		return "", err
	}

	count := strings.Count(before, oldStr)
	if count == 0 {
		return "", fmt.Errorf("在 %s 中未找到 old_string（注意缩进与空行需完全一致；建议先 read_file 确认原文）", t.ctx.rel(path))
	}
	if count > 1 && !replaceAll {
		return "", fmt.Errorf("old_string 在 %s 中出现 %d 次，不唯一；请补充上下文使其唯一，或设置 replace_all=true", t.ctx.rel(path), count)
	}

	after := strings.Replace(before, oldStr, newStr, -1)
	if !replaceAll {
		after = strings.Replace(before, oldStr, newStr, 1)
	}
	if len(after) > maxWriteFileBytes {
		return "", fmt.Errorf("修改后内容过大（%d 字节，上限 %d）", len(after), maxWriteFileBytes)
	}
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	t.ctx.record(toolEditFile, path, before, after)

	return fmt.Sprintf("已更新 %s（替换 %d 处）", t.ctx.rel(path), count), nil
}

// PermissionSubject 路径类主体（写）
func (t *editFileTool) PermissionSubject(args map[string]interface{}) Subject {
	return pathSubjectFor(t.GetName(), SubjectActionWrite, t.ctx, args)
}

// ===== glob =====

type globTool struct {
	*BaseTool
	ctx      fileToolContext
	required []string
}

func newGlobTool(bc buildContext) *globTool {
	params, required := fileToolParams([]fileArg{
		{Name: "pattern", Type: "string", Description: "文件匹配模式，如 *.go、src/**/*.ts、**/*_test.go", Required: true},
		{Name: "path", Type: "string", Description: "搜索起点目录（默认工作区根目录）"},
	})
	return &globTool{
		BaseTool: &BaseTool{
			Name:        toolGlob,
			Description: "按 glob 模式查找文件（支持 ** 跨目录），按修改时间倒序返回。查找文件内容请用 grep。",
			Parameters:  params,
		},
		ctx:      bc.fileCtx,
		required: required,
	}
}

func (t *globTool) RequiredParams() []string { return t.required }

// PermissionSubject 路径类主体（读）
func (t *globTool) PermissionSubject(args map[string]interface{}) Subject {
	return pathSubjectFor(t.GetName(), SubjectActionRead, t.ctx, args)
}

func (t *globTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if pattern == "" {
		return "", fmt.Errorf("pattern 参数是必需的")
	}
	base := t.ctx.baseDir()
	if raw, _ := args["path"].(string); strings.TrimSpace(raw) != "" {
		p, err := t.ctx.resolve(raw)
		if err != nil {
			return "", err
		}
		base = p
	}

	re, err := globToRegexp(pattern)
	if err != nil {
		return "", err
	}

	type hit struct {
		rel     string
		modTime int64
	}
	hits := make([]hit, 0, 64)
	walkSkipDirs(base, func(path string, info os.FileInfo) bool {
		r, err := filepath.Rel(base, path)
		if err != nil {
			return true
		}
		r = filepath.ToSlash(r)
		if re.MatchString(r) {
			hits = append(hits, hit{rel: t.ctx.rel(path), modTime: info.ModTime().UnixMilli()})
		}
		return len(hits) < maxGlobResults*4
	})

	sort.Slice(hits, func(i, j int) bool { return hits[i].modTime > hits[j].modTime })
	truncated := false
	if len(hits) > maxGlobResults {
		hits = hits[:maxGlobResults]
		truncated = true
	}
	if len(hits) == 0 {
		return fmt.Sprintf("没有匹配 %s 的文件（搜索起点：%s）", pattern, t.ctx.rel(base)), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "匹配 %s 的文件（%d 个）:\n", pattern, len(hits))
	for _, h := range hits {
		sb.WriteString(h.rel)
		sb.WriteString("\n")
	}
	if truncated {
		fmt.Fprintf(&sb, "…（结果过多，仅返回前 %d 个）\n", maxGlobResults)
	}
	return sb.String(), nil
}

// ===== grep =====

type grepTool struct {
	*BaseTool
	ctx      fileToolContext
	required []string
}

func newGrepTool(bc buildContext) *grepTool {
	params, required := fileToolParams([]fileArg{
		{Name: "pattern", Type: "string", Description: "正则表达式", Required: true},
		{Name: "path", Type: "string", Description: "搜索起点目录或文件（默认工作区根目录）"},
		{Name: "glob", Type: "string", Description: "只搜索匹配该模式的文件，如 *.go"},
		{Name: "output_mode", Type: "string", Description: "files_with_matches（默认）/ content / count"},
		{Name: "case_insensitive", Type: "boolean", Description: "忽略大小写"},
		{Name: "head_limit", Type: "number", Description: fmt.Sprintf("结果上限（默认 %d）", maxGrepResults)},
	})
	return &grepTool{
		BaseTool: &BaseTool{
			Name:        toolGrep,
			Description: "在工作区内按正则搜索文件内容。直接搜内容比用 exec_shell 跑 grep 更安全（只读、不解释 shell 语法）。",
			Parameters:  params,
		},
		ctx:      bc.fileCtx,
		required: required,
	}
}

func (t *grepTool) RequiredParams() []string { return t.required }

// PermissionSubject 路径类主体（读）
func (t *grepTool) PermissionSubject(args map[string]interface{}) Subject {
	return pathSubjectFor(t.GetName(), SubjectActionRead, t.ctx, args)
}

func (t *grepTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	if strings.TrimSpace(pattern) == "" {
		return "", fmt.Errorf("pattern 参数是必需的")
	}
	mode, _ := args["output_mode"].(string)
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "files_with_matches"
	}
	switch mode {
	case "files_with_matches", "content", "count":
	default:
		return "", fmt.Errorf("output_mode 只能是 files_with_matches / content / count，收到 %q", mode)
	}
	limit := intArg(args, "head_limit", maxGrepResults)
	if limit <= 0 {
		limit = maxGrepResults
	}

	expr := pattern
	if boolArg(args, "case_insensitive") {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", fmt.Errorf("正则表达式无效: %v", err)
	}

	var nameFilter *regexp.Regexp
	if g, _ := args["glob"].(string); strings.TrimSpace(g) != "" {
		nameFilter, err = globToRegexp(strings.TrimSpace(g))
		if err != nil {
			return "", err
		}
	}

	base := t.ctx.baseDir()
	if raw, _ := args["path"].(string); strings.TrimSpace(raw) != "" {
		p, err := t.ctx.resolve(raw)
		if err != nil {
			return "", err
		}
		base = p
	}

	type matchLine struct {
		rel  string
		line int
		text string
	}
	var (
		fileHits  []string
		countHits []string
		content   []matchLine
		scanned   int
	)
	handleFile := func(path string) {
		if nameFilter != nil {
			if r, err := filepath.Rel(base, path); err != nil || !nameFilter.MatchString(filepath.ToSlash(r)) {
				return
			}
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxGrepFileBytes {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil || looksBinary(data) {
			return
		}
		scanned++
		rel := t.ctx.rel(path)
		n := 0
		for i, line := range splitLines(string(data)) {
			if !re.MatchString(line) {
				continue
			}
			n++
			if mode == "content" && len(content) < limit {
				content = append(content, matchLine{rel: rel, line: i + 1, text: strings.TrimRight(line, "\r")})
			}
			if mode == "content" && len(content) >= limit {
				break
			}
		}
		if n > 0 {
			fileHits = append(fileHits, rel)
			countHits = append(countHits, fmt.Sprintf("%s: %d", rel, n))
		}
	}

	if st, err := os.Stat(base); err == nil && !st.IsDir() {
		handleFile(base)
	} else {
		walkSkipDirs(base, func(path string, info os.FileInfo) bool {
			if info.IsDir() {
				return true
			}
			handleFile(path)
			return scanned < maxGrepFiles && len(fileHits) < limit*4
		})
	}

	switch mode {
	case "content":
		if len(content) == 0 {
			return fmt.Sprintf("没有匹配 %q 的内容（起始：%s）", pattern, t.ctx.rel(base)), nil
		}
		var sb strings.Builder
		for _, m := range content {
			fmt.Fprintf(&sb, "%s:%d:%s\n", m.rel, m.line, m.text)
		}
		if len(content) >= limit {
			fmt.Fprintf(&sb, "…（结果达到上限 %d）\n", limit)
		}
		return sb.String(), nil
	case "count":
		if len(countHits) == 0 {
			return fmt.Sprintf("没有匹配 %q 的内容", pattern), nil
		}
		return strings.Join(countHits, "\n") + "\n", nil
	default:
		if len(fileHits) == 0 {
			return fmt.Sprintf("没有匹配 %q 的文件（起始：%s）", pattern, t.ctx.rel(base)), nil
		}
		return fmt.Sprintf("匹配 %q 的文件（%d 个）:\n%s", pattern, len(fileHits), strings.Join(fileHits, "\n")+"\n"), nil
	}
}

// ===== list_dir =====

type listDirTool struct {
	*BaseTool
	ctx      fileToolContext
	required []string
}

func newListDirTool(bc buildContext) *listDirTool {
	params, required := fileToolParams([]fileArg{
		{Name: "path", Type: "string", Description: "要列出的目录（默认工作区根目录）"},
	})
	return &listDirTool{
		BaseTool: &BaseTool{
			Name:        toolListDir,
			Description: "列出目录内容（目录在前、文件在后，含大小）。比 ls 更适合被模型直接使用。",
			Parameters:  params,
		},
		ctx:      bc.fileCtx,
		required: required,
	}
}

func (t *listDirTool) RequiredParams() []string { return t.required }

// PermissionSubject 路径类主体（读）
func (t *listDirTool) PermissionSubject(args map[string]interface{}) Subject {
	return pathSubjectFor(t.GetName(), SubjectActionRead, t.ctx, args)
}

func (t *listDirTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path := t.ctx.baseDir()
	if raw, _ := args["path"].(string); strings.TrimSpace(raw) != "" {
		p, err := t.ctx.resolve(raw)
		if err != nil {
			return "", err
		}
		path = p
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("目录不存在: %s", t.ctx.rel(path))
		}
		return "", fmt.Errorf("读取目录失败: %w", err)
	}

	type item struct {
		name  string
		isDir bool
		size  int64
	}
	dirs := make([]item, 0, len(entries))
	files := make([]item, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && skipDirNames[e.Name()] {
			continue
		}
		it := item{name: e.Name(), isDir: e.IsDir()}
		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				it.size = info.Size()
			}
		}
		if e.IsDir() {
			dirs = append(dirs, it)
		} else {
			files = append(files, it)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].name < dirs[j].name })
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s/（%d 个目录，%d 个文件）\n", t.ctx.rel(path), len(dirs), len(files))
	shown := 0
	for _, group := range [][]item{dirs, files} {
		for _, it := range group {
			if shown >= maxDirEntries {
				fmt.Fprintf(&sb, "…（达到上限 %d 条）\n", maxDirEntries)
				return sb.String(), nil
			}
			if it.isDir {
				fmt.Fprintf(&sb, "%s/\n", it.name)
			} else {
				fmt.Fprintf(&sb, "%s\t%d\n", it.name, it.size)
			}
			shown++
		}
	}
	return sb.String(), nil
}

// ===== 辅助 =====

// fileArg 文件类工具的参数定义（含 required，直出给模型时生成 JSON Schema 要用）
type fileArg struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// fileToolParams 生成 GetParameters 的映射与必填列表
func fileToolParams(args []fileArg) (map[string]*ToolArgDef, []string) {
	params := make(map[string]*ToolArgDef, len(args))
	required := make([]string, 0, len(args))
	for _, a := range args {
		params[a.Name] = &ToolArgDef{Type: a.Type, Description: a.Description}
		if a.Required {
			required = append(required, a.Name)
		}
	}
	return params, required
}

// pathSubjectFor 构造路径类判定主体；路径解析失败时返回不可信主体（判定会落到询问）
func pathSubjectFor(tool, action string, ctx fileToolContext, args map[string]interface{}) Subject {
	raw, _ := args["path"].(string)
	if strings.TrimSpace(raw) == "" && (tool == toolGlob || tool == toolGrep || tool == toolListDir) {
		// 这三个工具的 path 可选：缺省即工作区根目录
		return newPathSubject(tool, action, ctx.baseDir())
	}
	path, err := ctx.resolve(raw)
	if err != nil {
		s := newPathSubject(tool, action, strings.TrimSpace(raw))
		s.Trusted = false
		return s
	}
	return newPathSubject(tool, action, path)
}

// intArg 读取数字参数（兼容 float64 / json.Number / string）
func intArg(args map[string]interface{}, key string, def int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return def
	}
}

// boolArg 读取布尔参数
func boolArg(args map[string]interface{}, key string) bool {
	v, ok := args[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// walkSkipDirs 遍历目录，跳过噪音目录；visit 返回 false 表示提前结束
func walkSkipDirs(root string, visit func(path string, info os.FileInfo) bool) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 单个条目出错不中断整体遍历
		}
		if info.IsDir() && path != root && skipDirNames[info.Name()] {
			return filepath.SkipDir
		}
		if !visit(path, info) {
			return filepath.SkipAll
		}
		return nil
	})
}

// globToRegexp 把 glob 模式编译成正则：
//
//	**  跨目录（可匹配零个或多个路径段）
//	*   段内任意字符（不含 /）
//	?   段内单个字符
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	pattern = strings.TrimPrefix(pattern, "./")
	if pattern == "" {
		return nil, fmt.Errorf("模式不能为空")
	}
	segs := strings.Split(pattern, "/")
	var b strings.Builder
	b.WriteString("^")
	for i, seg := range segs {
		last := i == len(segs)-1
		switch seg {
		case "**":
			if last {
				b.WriteString(".*")
			} else {
				b.WriteString("(?:[^/]+/)*")
			}
			continue
		default:
			b.WriteString(segmentRegexp(seg))
			if !last {
				b.WriteString("/")
			}
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("模式无效: %v", err)
	}
	return re, nil
}

// segmentRegexp 单段内的 glob 转义
func segmentRegexp(seg string) string {
	var b strings.Builder
	for _, r := range seg {
		switch r {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}
