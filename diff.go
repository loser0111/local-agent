package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ===== Diff 数据契约（字段名与前端 DiffFile/DiffLine/DiffHunk/DiffTurn 对齐）=====

// DiffLine 单行差异。add 行 OldLineNo=0；del 行 NewLineNo=0。
type DiffLine struct {
	Type      string `json:"type"` // add | del | context
	OldLineNo int    `json:"oldLineNo"`
	NewLineNo int    `json:"newLineNo"`
	Content   string `json:"content"`
}

// DiffHunk 一个差异块，Header 形如 @@ -12,7 +14,9 @@
type DiffHunk struct {
	Header string     `json:"header"`
	Lines  []DiffLine `json:"lines"`
}

// DiffFile 单个文件的差异
type DiffFile struct {
	Path      string     `json:"path"`              // 相对项目根目录，正斜杠
	OldPath   string     `json:"oldPath,omitempty"` // 重命名时使用
	Status    string     `json:"status"`            // added | modified | deleted | renamed
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	Hunks     []DiffHunk `json:"hunks"`
}

// DiffTurn 一轮对话产生的差异；Turn=0 表示“累计”
type DiffTurn struct {
	Turn      int        `json:"turn"`
	Label     string     `json:"label"`
	Files     []DiffFile `json:"files"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	CreatedAt int64      `json:"createdAt"`
}

const (
	maxDiffFileBytes = 1 << 20 // 单文件超过 1MB 跳过
	maxDiffFiles     = 200     // 最多返回文件数
	gitTimeout       = 15 * time.Second
)

// ===== git 执行封装 =====
// 关键点：
//
//	-c core.quotepath=false  避免中文/非 ASCII 路径被转义成 \xxx，否则无法解析
//	--no-pager               防止进入分页
//	固定超时                 防止大仓库卡死对话
func runGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	full := append([]string{"-C", dir, "-c", "core.quotepath=false", "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s 失败: %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// ===== 会话级差异服务 =====
//
// 核心约束：diff 必须按**会话**隔离。git 的 diff 本身是仓库级的——它只按时间过滤，
// 不区分是谁改的；而未跟踪文件更严重，`ls-files --others` 会把仓库里所有未跟踪文件
// 都算进来。于是两个会话开在同一个项目里时，彼此都会看到对方的改动。
//
// 做法：给每个会话维护「触碰过的路径」集合——每次工具调用前后各扫一次工作区状态，
// 按文件签名（mtime+size）的增量归因给当前会话；算 diff 时用这些路径做 pathspec 过滤。
// 这样连 exec_shell 改的文件（sed -i / mv / rm）也能算进来，而不是只认文件工具的记录。

// sessionDiffState 一个会话的差异状态
type sessionDiffState struct {
	baseline string          // 会话基线 commit
	touched  map[string]bool // 本会话触碰过的路径（仓库相对路径）
	turn     map[string]bool // 本轮触碰过的路径（每轮开始清空）
}

// DiffService 负责会话级基线、路径归因与差异计算。
//
// 归因用的扫描基线是**全局**的（globalScan），不是每会话一份。原因：若每个会话各存
// 一份上次扫描，那么会话 A 空闲期间、会话 B 改了文件，A 下次工具调用就会把 B 的改动
// 算成自己的（A 的基线还停在 B 改动之前）。全局基线保证每个变化只会被「第一个看到它的
// 扫描」消费掉，再配合「只在执行窗口之后归因」，空闲会话就不会沾上别人的改动。
type DiffService struct {
	mu         sync.Mutex
	sessions   map[string]*sessionDiffState
	globalScan map[string]string // 上一次任意扫描的 path → 签名；nil 表示尚未扫描
}

func NewDiffService() *DiffService {
	return &DiffService{
		sessions:   make(map[string]*sessionDiffState),
		globalScan: map[string]string{},
	}
}

// IsRepo 判断目录是否为 git 工作区
func (s *DiffService) IsRepo(dir string) bool {
	if dir == "" {
		return false
	}
	out, err := runGit(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// state 取（或创建）会话状态；调用方需持锁
func (s *DiffService) state(sessionID string) *sessionDiffState {
	st, ok := s.sessions[sessionID]
	if !ok {
		st = &sessionDiffState{touched: map[string]bool{}, turn: map[string]bool{}}
		s.sessions[sessionID] = st
	}
	return st
}

// RestoreSession 用持久化的状态恢复会话（重启后仍能算会话级 diff）。
// 已有内存状态时不覆盖，避免把正在进行的会话冲掉。
func (s *DiffService) RestoreSession(sessionID, baseline string, touched []string) {
	if sessionID == "" || (baseline == "" && len(touched) == 0) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state(sessionID)
	if st.baseline == "" {
		st.baseline = baseline
	}
	if len(st.touched) == 0 {
		for _, p := range touched {
			if p = strings.TrimSpace(p); p != "" {
				st.touched[p] = true
			}
		}
	}
}

// SnapshotState 返回可持久化的会话 diff 状态（基线 + 触碰过的路径）
func (s *DiffService) SnapshotState(sessionID string) (string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[sessionID]
	if !ok {
		return "", nil
	}
	return st.baseline, sortedKeys(st.touched)
}

// Touched 返回本会话触碰过的路径（稳定排序）
func (s *DiffService) Touched(sessionID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	return sortedKeys(st.touched)
}

// BeginTurn 开始新一轮：清空本轮归因集合
func (s *DiffService) BeginTurn(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state(sessionID).turn = map[string]bool{}
}

// TurnTouched 取出本轮触碰过的路径并清空
func (s *DiffService) TurnTouched(sessionID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	out := sortedKeys(st.turn)
	st.turn = map[string]bool{}
	return out
}

// NoteActivity 扫描工作区并推进全局扫描基线。
//
// attribute=false：工具调用**之前**调用——只推进基线，不归因。
// attribute=true ：工具调用**之后**调用——把这次执行窗口内出现的改动归因给本会话。
//
// 为什么只在窗口之后归因：工具调用开始时先扫一次、结束后再扫一次，两者之间出现的改动
// 才「确定发生在这次调用期间」。若在起始扫描就归因，空闲会话会把别的会话（或用户自己）
// 期间做的改动记到自己账上。
//
// 已知取舍：
//   - 两个会话**同时在执行**时，某次改动可能被两边各自的窗口看到，从而被同时归因
//     （偏向多算，面板不会漏显示）。
//   - 用户手动编辑发生在某次工具调用窗口内时，也会算进那个会话。
func (s *DiffService) NoteActivity(sessionID, dir string, attribute bool) {
	if dir == "" || !s.IsRepo(dir) {
		return
	}
	current := scanWorkingTree(dir)
	if current == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 首次扫描只建立基线：否则会话开始前就存在的改动会被算成改的
	if s.globalScan == nil {
		s.globalScan = current
		return
	}
	changed := make([]string, 0, 8)
	for path, sig := range current {
		if prev, ok := s.globalScan[path]; !ok || prev != sig {
			changed = append(changed, path)
		}
	}
	s.globalScan = current

	if !attribute || sessionID == "" || len(changed) == 0 {
		return
	}
	st := s.state(sessionID)
	for _, path := range changed {
		st.touched[path] = true
		st.turn[path] = true
	}
}

// EnsureBaseline 懒初始化会话基线（已有基线则沿用，包括从会话文件恢复的）
func (s *DiffService) EnsureBaseline(sessionID, dir string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state(sessionID)
	if st.baseline != "" {
		return st.baseline
	}
	sha, _ := s.Snapshot(dir)
	st.baseline = sha
	return sha
}

// GetBaseline 只读取已有基线，不创建。面板打开时调用，避免把当前工作区快照为基线导致 diff 永远为空
func (s *DiffService) GetBaseline(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.sessions[sessionID]; ok {
		return st.baseline
	}
	return ""
}

// TurnSnapshot 每轮开始时取本轮基线
func (s *DiffService) TurnSnapshot(dir string) string {
	sha, _ := s.Snapshot(dir)
	return sha
}

// ForgetBaseline 会话删除时清理
func (s *DiffService) ForgetBaseline(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// DiffForSession 返回**本会话**触碰过的文件的累计差异。
// 未触碰过任何文件时返回空列表——这正是会话级隔离的含义。
func (s *DiffService) DiffForSession(sessionID, dir string) ([]DiffFile, error) {
	s.mu.Lock()
	base, paths := "", []string(nil)
	if st, ok := s.sessions[sessionID]; ok {
		base = st.baseline
		paths = sortedKeys(st.touched)
	}
	s.mu.Unlock()
	return s.DiffScoped(dir, base, paths)
}

// DiffScoped 只计算指定路径范围内的差异（含这些路径下的未跟踪文件）。
// paths 为空表示没有任何归属改动 → 返回空。
func (s *DiffService) DiffScoped(dir, baseline string, paths []string) ([]DiffFile, error) {
	if baseline == "" || len(paths) == 0 {
		return []DiffFile{}, nil
	}
	paths = capPaths(paths)

	args := []string{"diff", "--no-color", "--unified=3", "--no-ext-diff", baseline, "--"}
	args = append(args, paths...)
	out, err := runGit(dir, args...)
	if err != nil {
		return nil, err
	}
	files := ParseUnifiedDiff(out)

	// git diff 不含未跟踪文件，单独补齐；同样限定在触碰过的路径内
	utArgs := append([]string{"ls-files", "--others", "--exclude-standard", "-z", "--"}, paths...)
	if untracked, err := runGit(dir, utArgs...); err == nil {
		for _, rel := range strings.Split(untracked, "\x00") {
			rel = strings.TrimSpace(rel)
			if rel == "" {
				continue
			}
			if f, ok := buildAddedFile(dir, rel); ok {
				files = append(files, f)
			}
		}
	}

	if len(files) > maxDiffFiles {
		files = files[:maxDiffFiles]
	}
	if files == nil {
		files = []DiffFile{}
	}
	return files, nil
}

// scanWorkingTree 扫描工作区，返回「相对 HEAD 有改动或未跟踪」的 path → 签名。
//
// 签名用 mtime(ns)+size：与 git 判定文件是否变化的信号同类，比读全文便宜得多。
// 用 -unormal（目录折叠）而不是 -uall：后者在未忽略的大目录（如未 gitignore 的
// node_modules）下会把每次扫描变成几万条，代价过大；目录折叠后目录自身的 mtime
// 仍能反映「里面新增/删除了文件」。
func scanWorkingTree(dir string) map[string]string {
	out, err := runGit(dir, "status", "--porcelain", "-z", "--untracked-files=normal")
	if err != nil {
		return nil
	}
	paths := parsePorcelainPaths(out)
	result := make(map[string]string, len(paths))
	for _, rel := range paths {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		info, err := os.Stat(full)
		if err != nil {
			result[rel] = "missing" // 删除也是改动，用固定签名表示
			continue
		}
		result[rel] = fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
	}
	return result
}

// parsePorcelainPaths 解析 `git status --porcelain -z` 输出里的路径。
// -z 模式条目以 NUL 分隔；重命名/复制条目会额外带一个源路径字段，需要跳过。
func parsePorcelainPaths(out string) []string {
	fields := strings.Split(out, "\x00")
	paths := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		status := entry[:2]
		paths = append(paths, entry[3:])
		if status[0] == 'R' || status[0] == 'C' || status[1] == 'R' || status[1] == 'C' {
			i++ // 跳过紧随其后的源路径
		}
	}
	return paths
}

// capPaths 限制 pathspec 数量：避免命令行过长（Windows 尤其敏感），
// 与 maxDiffFiles 保持一致，超出部分不再进入 diff（面板本来也只展示前 maxDiffFiles 个）。
func capPaths(paths []string) []string {
	if len(paths) <= maxDiffFiles {
		return paths
	}
	out := make([]string, maxDiffFiles)
	copy(out, paths)
	return out
}

// sortedKeys 返回集合的稳定排序切片（命令参数与落盘都要求确定性）
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ===== 内部：基线快照 =====

// Snapshot 生成工作区快照 commit，不修改工作区与暂存区。
// 优先用 git stash create（有改动时返回悬空 commit）；无改动时回退 HEAD。
func (s *DiffService) Snapshot(dir string) (string, error) {
	if out, err := runGit(dir, "stash", "create"); err == nil {
		if sha := strings.TrimSpace(out); sha != "" {
			return sha, nil
		}
	}
	out, err := runGit(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// buildAddedFile 把未跟踪文本文件构造成“全部新增”
func buildAddedFile(dir, rel string) (DiffFile, bool) {
	full := filepath.Join(dir, filepath.FromSlash(rel))
	info, err := os.Stat(full)
	if err != nil || info.IsDir() || info.Size() > maxDiffFileBytes {
		return DiffFile{}, false
	}
	data, err := os.ReadFile(full)
	if err != nil || bytes.IndexByte(data, 0) >= 0 { // 含 NUL 视为二进制
		return DiffFile{}, false
	}

	// 归一化 CRLF，避免“整文件被标记为改动”的噪音
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	f := DiffFile{
		Path:   rel,
		Status: "added",
		Hunks:  []DiffHunk{{Header: fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines))}},
	}
	for i, l := range lines {
		f.Hunks[0].Lines = append(f.Hunks[0].Lines, DiffLine{Type: "add", NewLineNo: i + 1, Content: l})
		f.Additions++
	}
	return f, true
}

// ===== unified diff 解析 =====

func trimABPrefix(p string) string {
	p = strings.TrimPrefix(p, "a/")
	p = strings.TrimPrefix(p, "b/")
	return p
}

// parseHunkHeader 解析 @@ -12,7 +14,9 @@
func parseHunkHeader(header string) (oldStart, newStart int) {
	rest := strings.TrimPrefix(header, "@@ ")
	parts := strings.SplitN(rest, " ", 2)

	oldPart := strings.TrimPrefix(parts[0], "-")
	if i := strings.IndexByte(oldPart, 0x2c); i >= 0 {
		oldPart = oldPart[:i]
	}
	oldStart, _ = strconv.Atoi(oldPart)

	if len(parts) == 2 {
		newPart := strings.TrimPrefix(parts[1], "+")
		if i := strings.IndexByte(newPart, 0x20); i >= 0 {
			newPart = newPart[:i]
		}
		if i := strings.IndexByte(newPart, 0x2c); i >= 0 {
			newPart = newPart[:i]
		}
		newStart, _ = strconv.Atoi(newPart)
	}
	return
}

// ParseUnifiedDiff 解析 git diff 输出为 []DiffFile。
// 用 inHunk 状态机区分“文件头的 --- / +++”与“内容里恰好以 --- 开头的行”。
func ParseUnifiedDiff(out string) []DiffFile {
	files := []DiffFile{}
	var cur *DiffFile
	var hunk *DiffHunk
	var oldNo, newNo int
	inHunk := false

	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // 支持长行

	for sc.Scan() {
		line := sc.Text()

		if strings.HasPrefix(line, "diff --git ") {
			if cur != nil {
				files = append(files, *cur)
			}
			cur = &DiffFile{Status: "modified", Hunks: []DiffHunk{}}
			hunk, inHunk = nil, false
			continue
		}
		if cur == nil {
			continue
		}

		if !inHunk {
			switch {
			case strings.HasPrefix(line, "new file mode"):
				cur.Status = "added"
			case strings.HasPrefix(line, "deleted file mode"):
				cur.Status = "deleted"
			case strings.HasPrefix(line, "rename from "):
				cur.Status = "renamed"
				cur.OldPath = strings.TrimSpace(line[len("rename from "):])
			case strings.HasPrefix(line, "rename to "):
				cur.Path = strings.TrimSpace(line[len("rename to "):])
			case strings.HasPrefix(line, "--- "):
				p := strings.TrimSpace(line[4:])
				if p == "/dev/null" {
					if cur.Status == "modified" {
						cur.Status = "added"
					}
				} else {
					pp := trimABPrefix(p)
					cur.OldPath = pp
					// 删除文件后续是 "+++ /dev/null"，不会再设置 Path；
					// 重命名场景 Path 已由 "rename to" 提供，不能覆盖
					if cur.Path == "" {
						cur.Path = pp
					}
				}
			case strings.HasPrefix(line, "+++ "):
				if p := strings.TrimSpace(line[4:]); p != "/dev/null" {
					cur.Path = trimABPrefix(p)
				}
			case strings.HasPrefix(line, "@@"):
				os_, ns := parseHunkHeader(line)
				oldNo, newNo = os_-1, ns-1
				cur.Hunks = append(cur.Hunks, DiffHunk{Header: line, Lines: []DiffLine{}})
				hunk = &cur.Hunks[len(cur.Hunks)-1]
				inHunk = true
			}
			continue
		}

		// hunk 内部
		switch {
		case strings.HasPrefix(line, "@@"):
			os_, ns := parseHunkHeader(line)
			oldNo, newNo = os_-1, ns-1
			cur.Hunks = append(cur.Hunks, DiffHunk{Header: line, Lines: []DiffLine{}})
			hunk = &cur.Hunks[len(cur.Hunks)-1]
		case line == "":
			// 忽略文件尾空行
		case line[0] == 0x2b: // +
			newNo++
			hunk.Lines = append(hunk.Lines, DiffLine{Type: "add", NewLineNo: newNo, Content: line[1:]})
			cur.Additions++
		case line[0] == 0x2d: // -
			oldNo++
			hunk.Lines = append(hunk.Lines, DiffLine{Type: "del", OldLineNo: oldNo, Content: line[1:]})
			cur.Deletions++
		case line[0] == 0x20: // 空格
			oldNo++
			newNo++
			hunk.Lines = append(hunk.Lines, DiffLine{Type: "context", OldLineNo: oldNo, NewLineNo: newNo, Content: line[1:]})
		case line[0] == 0x5c: // 反斜杠：“No newline at end of file”
			// 忽略
		}
	}

	if cur != nil {
		files = append(files, *cur)
	}
	return files
}

// ===== 统计工具 =====

func sumAdd(files []DiffFile) int {
	n := 0
	for _, f := range files {
		n += f.Additions
	}
	return n
}

func sumDel(files []DiffFile) int {
	n := 0
	for _, f := range files {
		n += f.Deletions
	}
	return n
}
