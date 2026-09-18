package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ===== 每轮 checkpoint：撤销用的完整快照 =====
//
// 与 diff.go 的 Snapshot 是**两种不同的快照**，同一时刻各取一份，不能合并：
//
//	Snapshot（stash create）  → 用于算 diff。**不含未跟踪文件**，
//	                            DiffScoped 会另外单独把未跟踪文件补进来。
//	Checkpoint（本文件）      → 用于回退。**含未跟踪文件**，并且打 ref 钉住。
//
// 为什么必须含未跟踪文件：一个"轮次之前就存在、但一直未跟踪、本轮被 agent 改了"的文件，
// DiffScoped 会把它报成 added。回退时若只看 Status 就会**删掉用户手写的文件且无法恢复**。
// 有了完整快照，才能区分「本轮真正新建」与「改了本来就存在的未跟踪文件」。
//
// 为什么必须打 ref：stash create / commit-tree 产出的都是**悬空 commit**，
// 没有任何引用，`git gc --prune=now` 会把它们回收（实测确认）。不打 ref 的表现是
// "今天能撤销、下周不能"这种间歇性故障。
//
// 已实机验证的四条（见 docs/p2-p3-impl-spec.md 第零节）：
//   1. stash create 不含未跟踪文件
//   2. GIT_INDEX_FILE 方案不污染真实索引
//   3. 不打 ref 的悬空 commit 会被 gc 回收
//   4. git restore --source 能恢复修改与删除
const (
	checkpointRefPrefix = "refs/local-agent/checkpoints"
	// checkpointKeepTurns 每会话最多保留的轮次快照数，更早的删 ref 让 git 自然回收
	checkpointKeepTurns = 20
)

// checkpointRef 某轮快照的 ref 名
func checkpointRef(sessionID string, turn int) string {
	return fmt.Sprintf("%s/%s/%d", checkpointRefPrefix, sessionID, turn)
}

// validRefSegment 会话 ID 会进 ref 名，先挡掉非法字符，
// 免得 git 报一句难懂的错误、或者在 refs/ 下造出奇怪的路径
func validRefSegment(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return s != "." && s != ".."
}

// Checkpoint 造一份**含未跟踪文件**的工作区快照，打 ref 钉住，返回 commit sha。
//
// 全程只写临时索引：真实索引与工作区一个字节都不动（已实测）。
// 失败时返回错误，由调用方决定降级——**绝不能让它阻断对话**。
func Checkpoint(dir, sessionID string, turn int) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("工作区目录为空")
	}
	if !validRefSegment(sessionID) {
		return "", fmt.Errorf("会话 ID 不能用作 ref 片段: %q", sessionID)
	}
	if turn <= 0 {
		return "", fmt.Errorf("轮次号必须为正: %d", turn)
	}

	// 临时索引放系统临时目录，**不能放进仓库**——否则会被 add -A 自己收进快照
	idxFile, err := os.CreateTemp("", "local-agent-idx-")
	if err != nil {
		return "", fmt.Errorf("创建临时索引失败: %w", err)
	}
	idxPath := idxFile.Name()
	_ = idxFile.Close()
	_ = os.Remove(idxPath) // git 需要一个不存在的路径，自己创建
	defer os.Remove(idxPath)

	env := []string{"GIT_INDEX_FILE=" + idxPath}
	if _, err := runGitEnv(dir, env, "add", "-A"); err != nil {
		return "", fmt.Errorf("暂存工作区失败: %w", err)
	}
	tree, err := runGitEnv(dir, env, "write-tree")
	if err != nil {
		return "", fmt.Errorf("写 tree 失败: %w", err)
	}
	tree = strings.TrimSpace(tree)
	if tree == "" {
		return "", fmt.Errorf("写 tree 返回空值")
	}

	commit, err := runGit(dir, "commit-tree", tree, "-p", "HEAD", "-m", "checkpoint")
	if err != nil {
		// 空仓库（没有 HEAD）没有父提交可用，去掉 -p 再来一次
		commit, err = runGit(dir, "commit-tree", tree, "-m", "checkpoint")
		if err != nil {
			return "", fmt.Errorf("建快照 commit 失败: %w", err)
		}
	}
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return "", fmt.Errorf("建快照 commit 返回空值")
	}

	if _, err := runGit(dir, "update-ref", checkpointRef(sessionID, turn), commit); err != nil {
		return "", fmt.Errorf("钉住快照失败: %w", err)
	}
	return commit, nil
}

// ResolveCheckpoint 解析某轮的快照 sha；ref 已被清理或失效时 ok=false
func ResolveCheckpoint(dir, sessionID string, turn int) (string, bool) {
	if dir == "" || !validRefSegment(sessionID) || turn <= 0 {
		return "", false
	}
	out, err := runGit(dir, "rev-parse", "--verify", "--quiet", checkpointRef(sessionID, turn)+"^{commit}")
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(out)
	return sha, sha != ""
}

// listSessionCheckpointTurns 列出某会话已钉住的轮次号（升序）
func listSessionCheckpointTurns(dir, sessionID string) []int {
	if dir == "" || !validRefSegment(sessionID) {
		return nil
	}
	prefix := checkpointRefPrefix + "/" + sessionID + "/"
	out, err := runGit(dir, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return nil
	}
	turns := make([]int, 0, 8)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(line, prefix))
		if err == nil && n > 0 {
			turns = append(turns, n)
		}
	}
	sort.Ints(turns)
	return turns
}

// DropCheckpoint 删掉某轮的快照 ref（sha 本身由 git 自行回收）
func DropCheckpoint(dir, sessionID string, turn int) error {
	if dir == "" || !validRefSegment(sessionID) || turn <= 0 {
		return nil
	}
	_, err := runGit(dir, "update-ref", "-d", checkpointRef(sessionID, turn))
	return err
}

// DropSessionCheckpoints 会话删除时清掉它名下所有快照 ref。
// 这是本项目唯一会写入用户仓库 refs/ 的地方，必须在会话生命周期结束时清理干净。
func DropSessionCheckpoints(dir, sessionID string) error {
	for _, turn := range listSessionCheckpointTurns(dir, sessionID) {
		_ = DropCheckpoint(dir, sessionID, turn)
	}
	return nil
}

// PruneCheckpoints 只保留最近 keep 轮，更早的删 ref
func PruneCheckpoints(dir, sessionID string, keep int) {
	if keep <= 0 {
		keep = checkpointKeepTurns
	}
	turns := listSessionCheckpointTurns(dir, sessionID)
	if len(turns) <= keep {
		return
	}
	for _, turn := range turns[:len(turns)-keep] {
		_ = DropCheckpoint(dir, sessionID, turn)
	}
}

// ===== 快照内容查询 =====

// blobHashAt 取快照里某路径的 blob 哈希；该路径不在快照里时返回空串。
// 用 rev-parse 一条命令既完成存在性判断也拿到哈希——空 blob 的哈希是
// e69de29...（非空串），所以"空串"可靠地表示"不存在"。
func blobHashAt(dir, sha, rel string) string {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if dir == "" || sha == "" || rel == "" {
		return ""
	}
	// 前缀 "-" 会被 git 当选项；":" 会被当 rev 语法
	if strings.HasPrefix(rel, "-") || strings.HasPrefix(rel, ":") {
		return ""
	}
	out, err := runGit(dir, "rev-parse", "--verify", "--quiet", sha+":"+rel)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// pathInCheckpoint 该路径在快照里是否存在
func pathInCheckpoint(dir, sha, rel string) bool {
	return blobHashAt(dir, sha, rel) != ""
}

// hashWorktreeFile 取工作区文件当前内容的 blob 哈希；文件不存在或不可读时返回空串
func hashWorktreeFile(dir, rel string) string {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if dir == "" || rel == "" || strings.HasPrefix(rel, "-") {
		return ""
	}
	out, err := runGit(dir, "hash-object", "--", rel)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// endStateOf 记录一组路径在**当前**工作区的内容状态（用于回退前的冲突检查）。
// 值语义：blob 哈希；空串表示"此时该文件不存在"（例如本轮把它删了）。
func endStateOf(dir string, files []DiffFile) map[string]string {
	if dir == "" || len(files) == 0 {
		return nil
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		if f.Path == "" {
			continue
		}
		out[f.Path] = hashWorktreeFile(dir, f.Path)
	}
	return out
}

// ===== 回退原语 =====

// restoreFromCheckpoint 把某个路径恢复到快照中的状态。
// 用 git restore --source 而不是 git checkout <sha> -- <path>：
// 后者会连索引一起改，动到用户的暂存区。
func restoreFromCheckpoint(dir, sha, rel string) error {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if dir == "" || sha == "" || rel == "" {
		return fmt.Errorf("恢复参数不完整")
	}
	if _, err := runGit(dir, "restore", "--source="+sha, "--", rel); err != nil {
		return fmt.Errorf("恢复 %s 失败: %w", rel, err)
	}
	return nil
}

// removeWorktreeFile 删除工作区文件，并清掉因此变空的目录（最多向上两层）。
// 只删明确指定的那一个文件，不做任何批量清理——回退不该波及无关文件。
func removeWorktreeFile(dir, rel string) error {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if dir == "" || rel == "" {
		return fmt.Errorf("删除参数不完整")
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.Remove(full); err != nil {
		if os.IsNotExist(err) {
			return nil // 已经不在了，视为成功
		}
		return fmt.Errorf("删除 %s 失败: %w", rel, err)
	}
	// 向上清理空目录，遇到非空或到仓库根就停
	cur := filepath.Dir(full)
	root := filepath.Clean(dir)
	for i := 0; i < 2 && cur != root && strings.HasPrefix(cur, root+string(os.PathSeparator)); i++ {
		if err := os.Remove(cur); err != nil {
			break // 非空或权限不足都停手，不影响主流程
		}
		cur = filepath.Dir(cur)
	}
	return nil
}

// runGitEnv 与 runGit 相同，但可附加环境变量（checkpoint 需要 GIT_INDEX_FILE）。
// runGit 委托到它，全项目只有这一份 git 调用封装。
//
// 关于时间戳：checkpoint 自身不记时间——时间由 DiffTurn.CreatedAt 负责，
// 这里不留冗余字段，免得两处时间不一致。
func runGitEnv(dir string, extraEnv []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	full := append([]string{"-C", dir, "-c", "core.quotepath=false", "--no-pager"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s 失败: %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}