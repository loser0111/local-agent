package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ===== 触发历史与执行输出 =====
//
// 落盘布局（附录 §8.6）：
//
//	~/.local-agent/task-runs/<taskId>/<runId>.json
//
// 为什么独立目录、而不是塞进 tasks.json：
//  1. tasks.json 是「定义 + 状态」，会被频繁整体重写；历史是追加型数据，
//     混在一起会让每次触发都重写全量数据，文件迅速膨胀且风险放大。
//  2. 完整执行输出可能很大，绝不能进配置文件的体积预算。

// TriggerSource 描述一次触发是怎么来的。
const (
	TriggerSourceScheduled = "scheduled"
	TriggerSourceManual    = "manual"
	TriggerSourceSnooze    = "snooze"
	TriggerSourceCatchUp   = "catchup"
	TriggerSourceRetry     = "retry"
)

// RunRecord 是「一次触发」的完整记录。
type RunRecord struct {
	RunID     string `json:"runID"`
	TaskID    string `json:"taskID"`
	TaskTitle string `json:"taskTitle,omitempty"`
	Kind      Kind   `json:"kind"`

	// Source 见 TriggerSource* 常量。
	Source      string `json:"source"`
	TriggeredAt string `json:"triggeredAt"`
	// ScheduledAt 是规则原本约定的时刻（与 TriggeredAt 不同即可看出延迟）。
	ScheduledAt string `json:"scheduledAt,omitempty"`

	Status     string `json:"status"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	DurationMS int64  `json:"durationMS,omitempty"`

	// Summary 是给人与通知看的摘要（结论前置）。
	Summary string `json:"summary,omitempty"`
	// Error 必须是可读原因，不允许只写「失败」（F6.5）。
	Error string `json:"error,omitempty"`
	// OutputPath 是完整输出的落盘位置。
	OutputPath string `json:"outputPath,omitempty"`
	// RunSessionID 是**指针**：没有执行会话时写 null，也不内嵌 transcript。
	RunSessionID *string `json:"runSessionID,omitempty"`
	// Declined 是无人值守下被拒绝的操作（含理由），必须可见。
	Declined []string `json:"declined,omitempty"`
	// NotifiedAt 记录通知实际送达/降级的时刻。
	NotifiedAt string `json:"notifiedAt,omitempty"`
	// NotifyFallback 非空表示通知降级为应用内提醒，值是降级原因。
	NotifyFallback string `json:"notifyFallback,omitempty"`
	// RetryOf 指向上一次失败的 runID（重试产生）。
	RetryOf string `json:"retryOf,omitempty"`
}

// History 管理 task-runs 目录。
type History struct {
	dir string
}

// NewHistory 创建历史管理器；baseDir 为数据目录。
func NewHistory(baseDir string) *History {
	return &History{dir: filepath.Join(baseDir, "task-runs")}
}

// Dir 返回历史根目录。
func (h *History) Dir() string { return h.dir }

// Append 写入一条记录，返回落盘路径。原子写，权限 0o600。
func (h *History) Append(rec RunRecord) (string, error) {
	if strings.TrimSpace(rec.TaskID) == "" || strings.TrimSpace(rec.RunID) == "" {
		return "", fmt.Errorf("历史记录缺少 taskID 或 runID")
	}
	dir := filepath.Join(h.dir, sanitizeSegment(rec.TaskID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建历史目录失败: %w", err)
	}
	if rec.TriggeredAt == "" {
		rec.TriggeredAt = time.Now().Local().Format(time.RFC3339)
	}
	path := filepath.Join(dir, sanitizeSegment(rec.RunID)+".json")
	if err := writeJSONAtomic(path, rec, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ListByTask 返回某任务的历史，按触发时间倒序（最新在前）。
func (h *History) ListByTask(taskID string, limit int) ([]RunRecord, error) {
	dir := filepath.Join(h.dir, sanitizeSegment(taskID))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取历史目录失败: %w", err)
	}
	out := make([]RunRecord, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var rec RunRecord
		if err := readJSON(filepath.Join(dir, e.Name()), &rec); err != nil {
			// 单条坏记录不该让整页历史打不开：跳过并继续。
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TriggeredAt > out[j].TriggeredAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// RecoverRunning 把残留的 running 记录改为 interrupted（崩溃恢复，F8.2）。
//
// 返回被修复的记录数：大于 0 说明上次进程是被硬杀的。
func (h *History) RecoverRunning() (int, error) {
	var taskDirs []string
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			taskDirs = append(taskDirs, filepath.Join(h.dir, e.Name()))
		}
	}
	fixed := 0
	for _, dir := range taskDirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, f.Name())
			var rec RunRecord
			if err := readJSON(path, &rec); err != nil {
				continue
			}
			if rec.Status != ResultRunning {
				continue
			}
			rec.Status = ResultInterrupted
			if rec.Error == "" {
				rec.Error = "应用在任务执行中被关闭"
			}
			rec.FinishedAt = time.Now().Local().Format(time.RFC3339)
			if err := writeJSONAtomic(path, rec, 0o600); err == nil {
				fixed++
			}
		}
	}
	return fixed, nil
}

// Prune 清理超过保留天数的历史（F6.4）；keepDays <= 0 表示不清理。
func (h *History) Prune(keepDays int) (int, error) {
	if keepDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	removed := 0
	taskDirs, err := os.ReadDir(h.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	for _, td := range taskDirs {
		if !td.IsDir() {
			continue
		}
		dir := filepath.Join(h.dir, td.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		empty := true
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			info, err := f.Info()
			if err != nil {
				empty = false
				continue
			}
			if info.ModTime().Before(cutoff) {
				if os.Remove(filepath.Join(dir, f.Name())) == nil {
					removed++
				}
				continue
			}
			empty = false
		}
		if empty {
			_ = os.Remove(dir)
		}
	}
	return removed, nil
}

// DeleteByTask 删除某任务的全部历史（仅在用户明确要求时调用，F1.3）。
func (h *History) DeleteByTask(taskID string) error {
	dir := filepath.Join(h.dir, sanitizeSegment(taskID))
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除历史失败: %w", err)
	}
	return nil
}

// sanitizeSegment 把 ID 收敛成安全的单层路径片段。
//
// 信任边界：ID 可能来自被手改的 tasks.json，绝不允许拼出 "../" 逃出数据目录。
func sanitizeSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." {
		out = "unknown"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func writeJSONAtomic(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), perm)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
