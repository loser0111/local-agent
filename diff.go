package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// ===== 差异服务 =====

// DiffService 负责会话级基线管理与 diff 计算
type DiffService struct {
	mu       sync.Mutex
	baseline map[string]string // sessionID -> 会话基线 commit sha
}

func NewDiffService() *DiffService {
	return &DiffService{baseline: make(map[string]string)}
}

// IsRepo 判断目录是否为 git 工作区
func (s *DiffService) IsRepo(dir string) bool {
	if dir == "" {
		return false
	}
	out, err := runGit(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

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

// EnsureBaseline 懒初始化会话基线
func (s *DiffService) EnsureBaseline(sessionID, dir string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sha, ok := s.baseline[sessionID]; ok {
		return sha
	}
	sha, _ := s.Snapshot(dir)
	s.baseline[sessionID] = sha
	return sha
}

// TurnSnapshot 每轮开始时取本轮基线
func (s *DiffService) TurnSnapshot(dir string) string {
	sha, _ := s.Snapshot(dir)
	return sha
}

// ForgetBaseline 会话删除时清理（可选）
func (s *DiffService) ForgetBaseline(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.baseline, sessionID)
}

// Diff 计算 dir 相对 baseline 的差异（含未跟踪文件）
func (s *DiffService) Diff(dir, baseline string) ([]DiffFile, error) {
	args := []string{"diff", "--no-color", "--unified=3", "--no-ext-diff"}
	if baseline != "" {
		args = append(args, baseline)
	}
	args = append(args, "--")

	out, err := runGit(dir, args...)
	if err != nil {
		return nil, err
	}
	files := ParseUnifiedDiff(out)

	// git diff 不含未跟踪文件，单独补齐
	if untracked, err := runGit(dir, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
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
