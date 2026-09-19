package plugin

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ===== tasks.json 的持久化 =====
//
// 做法与仓库既有 ToolStore 一致：内存副本 + RWMutex + 整文件 JSON。
// 但有两处**有意偏离**（附录 §8.6 的建议）：
//  1. 原子写：同目录临时文件 → Sync → rename。既有实现是 os.WriteFile 直接覆盖，
//     断电/崩溃时可能留下半截文件；任务定义是用户的资产，不能这么写。
//  2. 文件权限 0o600（既有是 0o644）：任务里可能含 prompt 与工作区路径。
//
// 另外沿用仓库既有惯例：首次迁移留一份 tasks.json.bak（已存在则不覆盖），
// 解析失败把坏文件改名 tasks.json.broken 后以空集启动（F8.3）。

// 当前数据文件版本。
const taskFileVersion = 1

// taskFileName 是任务数据文件名。
const taskFileName = "tasks.json"

// ErrCorrupted 表示数据文件损坏，已改名保留并以空集启动。
var ErrCorrupted = errors.New("tasks.json 已损坏")

// TaskStore 负责 tasks.json 的读写。
type TaskStore struct {
	path string

	mu     sync.RWMutex
	file   TaskFile
	dir    string
	loaded bool

	// selfSum / selfWritten 记录最近一次「自己写盘」的内容摘要。
	//
	// 为什么需要：App 自己的每一次 Save / mutate 都会改 tasks.json，从而触发
	// fsnotify 事件。靠这对字段把「自己写的」与「外部写的」区分开 —— 否则任务
	// 状态每变一次都会白跑一次重新加载（还可能引发无意义的前端广播）。
	// selfWritten 为 false 时摘要不可比较（尚未写过盘）。
	selfSum     [sha256.Size]byte
	selfWritten bool
}

// NewTaskStore 创建 store；baseDir 为数据目录（~/.local-agent）。
func NewTaskStore(baseDir string) *TaskStore {
	return &TaskStore{
		dir:  baseDir,
		path: filepath.Join(baseDir, taskFileName),
		file: TaskFile{Version: taskFileVersion, Settings: NewGlobalConfig()},
	}
}

// Path 返回数据文件路径。
func (s *TaskStore) Path() string { return s.path }

// FormatVersion 返回当前数据格式版本。
func (s *TaskStore) FormatVersion() int { return taskFileVersion }

// Load 读取数据文件。
//
// 返回 nil 表示正常；返回 ErrCorrupted 表示文件损坏但已恢复（调用方只记录，不阻断）；
// 其余错误是真正的 IO 失败。
func (s *TaskStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.file = TaskFile{Version: taskFileVersion, Settings: NewGlobalConfig()}
			s.loaded = true
			return nil
		}
		return fmt.Errorf("读取 %s 失败: %w", s.path, err)
	}

	var f TaskFile
	if err := json.Unmarshal(data, &f); err != nil {
		// 坏文件不删，改名保留 —— 用户的任务定义值得人工抢救一次。
		broken := s.brokenPath()
		if renameErr := os.Rename(s.path, broken); renameErr != nil {
			return fmt.Errorf("解析 %s 失败且无法备份: %w", s.path, err)
		}
		s.file = TaskFile{Version: taskFileVersion, Settings: NewGlobalConfig()}
		s.loaded = true
		return fmt.Errorf("%w（原文件已备份为 %s）: %v", ErrCorrupted, filepath.Base(broken), err)
	}

	if err := s.migrateLocked(&f, data); err != nil {
		return err
	}

	if f.Settings == nil {
		f.Settings = NewGlobalConfig()
	}
	f.Settings.applyDefaults()
	normalizeTasks(f.Tasks)
	s.file = f
	s.loaded = true
	return nil
}

// ReloadIfChanged 从磁盘重新加载，仅在「文件确实变了、且能完整解析」时替换内存；
// 返回是否真的替换了内存。
//
// 与 Load 的分工（两者读同一个文件，但风险完全不同）：
//   - Load 是**启动路径**，解析失败敢把坏文件改名成 .broken 并以空集启动；
//   - 本方法是**外部变更路径**，写入方（脚本 / 编辑器）可能正处在非原子写的中间态，
//     半截 JSON 绝不能触发「归档 + 清空」—— 那等于把用户的任务当场删掉。
//     所以解析失败时保持内存与磁盘原样不动，把决定权交回调用方：通常只是记一条
//     日志，等下一次事件把完整内容送进来。
//
// 自写识别：磁盘内容与最近一次自己写盘的摘要一致时返回 false。没有这一条，
// App 自己的每一次 mutate 都会绕回来触发一次「外部变更」。
func (s *TaskStore) ReloadIfChanged() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件被外部删了：不擅自清空内存（清不清由调用方定），当作没变化。
			return false, nil
		}
		return false, fmt.Errorf("读取 %s 失败: %w", s.path, err)
	}

	sum := sha256.Sum256(data)
	if s.selfWritten && sum == s.selfSum {
		// 磁盘内容就是自己刚写的：不是外部变更。
		return false, nil
	}

	var f TaskFile
	if err := json.Unmarshal(data, &f); err != nil {
		return false, fmt.Errorf("外部写入的内容暂时无法解析，本次变更已忽略（内存与磁盘均未改动）: %w", err)
	}
	if f.Version > taskFileVersion {
		// 与 migrateLocked 同一态度：更新的数据不猜、不改写。这里也不留 .bak。
		return false, fmt.Errorf("外部文件版本 %d 高于本程序支持的 %d，已忽略以免丢数据", f.Version, taskFileVersion)
	}
	f.Version = taskFileVersion

	if f.Settings == nil {
		f.Settings = NewGlobalConfig()
	}
	f.Settings.applyDefaults()
	normalizeTasks(f.Tasks)

	s.file = f
	s.loaded = true
	// 内存与磁盘此刻一致：同步刷新摘要，免得下一次事件被误判成外部变更。
	s.selfSum = sum
	s.selfWritten = true
	return true, nil
}

// migrateLocked 处理版本迁移；首迁前留 .bak 备份（已存在则不覆盖）。
func (s *TaskStore) migrateLocked(f *TaskFile, raw []byte) error {
	if f.Version >= taskFileVersion {
		if f.Version > taskFileVersion {
			// 降级场景：数据是更新的版本写的。不猜、不改写，只提示。
			return fmt.Errorf("数据文件版本 %d 高于本程序支持的 %d，为避免丢数据已停止加载", f.Version, taskFileVersion)
		}
		return nil
	}

	bak := s.path + ".bak"
	if _, err := os.Stat(bak); os.IsNotExist(err) {
		if writeErr := writeFileAtomic(bak, raw, 0o600); writeErr != nil {
			// 备份失败不阻断启动，但要留下痕迹（用户资产优先可回退）。
			fmt.Printf("[desktop-plugin] 迁移前备份失败: %v\n", writeErr)
		}
	}

	// v0 → v1：早期文件没有 version 字段，字段结构一致，只需补版本号。
	f.Version = taskFileVersion
	return nil
}

// brokenPath 生成 .broken 备份路径；同名已存在时追加时间戳，避免覆盖上次的现场。
func (s *TaskStore) brokenPath() string {
	base := s.path + ".broken"
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return base
	}
	return fmt.Sprintf("%s.%s", base, time.Now().Format("20060102-150405"))
}

// normalizeTasks 修复从磁盘读入数据里那些「不该持久化」或不合法的状态。
//
// 崩溃恢复（F8.2）：残留的 running 说明上次进程是被硬杀的，
// 现在没有任何人在跑它，必须落到 interrupted，否则会永远显示「运行中」。
func normalizeTasks(tasks []*Task) {
	for _, t := range tasks {
		if t == nil {
			continue
		}
		if t.State.LastResult == ResultRunning {
			t.State.LastResult = ResultInterrupted
			if t.State.LastError == "" {
				t.State.LastError = "应用在任务执行中被关闭"
			}
		}
		// 通知台账只用于去重，留最近 50 条足够；无限增长会撑大文件。
		if len(t.State.Notified) > 50 {
			keys := make([]string, 0, len(t.State.Notified))
			for k := range t.State.Notified {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			trimmed := make(map[string]string, 50)
			for _, k := range keys[len(keys)-50:] {
				trimmed[k] = t.State.Notified[k]
			}
			t.State.Notified = trimmed
		}
		if len(t.State.MissedPending) > maxMissedPending {
			t.State.MissedPending = t.State.MissedPending[len(t.State.MissedPending)-maxMissedPending:]
		}
	}
}

// Save 原子写入数据文件。
func (s *TaskStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *TaskStore) saveLocked() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	s.file.Version = taskFileVersion
	if s.file.Settings == nil {
		s.file.Settings = NewGlobalConfig()
	}
	if s.file.Tasks == nil {
		s.file.Tasks = []*Task{}
	}
	data, err := json.MarshalIndent(s.file, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化任务数据失败: %w", err)
	}
	payload := append(data, '\n')
	if err := writeFileAtomic(s.path, payload, 0o600); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", s.path, err)
	}
	// 记下本次写盘内容的摘要：这是后面识别「自己写的事件」的唯一依据。
	s.selfSum = sha256.Sum256(payload)
	s.selfWritten = true
	return nil
}

// snapshot 返回内部数据的只读快照（深拷贝），调用方不得直接改。
func (s *TaskStore) snapshot() ([]*Task, *GlobalConfig, map[string]json.RawMessage) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tasks := make([]*Task, 0, len(s.file.Tasks))
	for _, t := range s.file.Tasks {
		tasks = append(tasks, t.clone())
	}
	settings := NewGlobalConfig()
	if s.file.Settings != nil {
		cp := *s.file.Settings
		if s.file.Settings.DND != nil {
			d := *s.file.Settings.DND
			d.Weekdays = append([]int(nil), s.file.Settings.DND.Weekdays...)
			cp.DND = &d
		}
		settings = &cp
	}
	var unknown map[string]json.RawMessage
	if len(s.file.unknown) > 0 {
		unknown = make(map[string]json.RawMessage, len(s.file.unknown))
		for k, v := range s.file.unknown {
			unknown[k] = v
		}
	}
	return tasks, settings, unknown
}

// mutate 在写锁内改数据并落盘：把「读-改-写」合成一个原子操作，
// 避免并发调用出现丢更新。
func (s *TaskStore) mutate(fn func(*TaskFile) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file.Settings == nil {
		s.file.Settings = NewGlobalConfig()
	}
	if err := fn(&s.file); err != nil {
		return err
	}
	return s.saveLocked()
}

// get 在写锁内按 id 找任务（返回内部指针，仅供 mutate 内部使用）。
func (f *TaskFile) get(id string) *Task {
	for _, t := range f.Tasks {
		if t != nil && t.ID == id {
			return t
		}
	}
	return nil
}

func (f *TaskFile) remove(id string) bool {
	out := f.Tasks[:0]
	removed := false
	for _, t := range f.Tasks {
		if t != nil && t.ID == id {
			removed = true
			continue
		}
		out = append(out, t)
	}
	f.Tasks = out
	return removed
}

// countTasks 统计任务数（用于软上限提示）。
func (f *TaskFile) countTasks() int { return len(f.Tasks) }

// writeFileAtomic 原子写：同目录临时文件 → Sync → rename。
//
// 同目录是硬要求：跨卷 rename 会失败，而且不保证原子性。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 任何一条提前返回的路径都要清掉临时文件，否则数据目录会被残片堆满。
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = "" // rename 成功后不需要再清理
	return nil
}

// TaskFile 的自定义序列化：保留本版本不认识的顶层字段（前向兼容）。

func (f *TaskFile) UnmarshalJSON(data []byte) error {
	type plain TaskFile
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*f = TaskFile(p)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		switch k {
		case "version", "settings", "tasks":
			continue
		}
		if f.unknown == nil {
			f.unknown = make(map[string]json.RawMessage)
		}
		f.unknown[k] = v
	}
	return nil
}

func (f TaskFile) MarshalJSON() ([]byte, error) {
	type plain TaskFile
	base, err := json.Marshal(plain(f))
	if err != nil {
		return nil, err
	}
	if len(f.unknown) == 0 {
		return base, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, v := range f.unknown {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

// EnsureTasksDir 确保数据目录存在（供历史目录使用）。
func EnsureTasksDir(baseDir string) error {
	if strings.TrimSpace(baseDir) == "" {
		return fmt.Errorf("数据目录为空")
	}
	return os.MkdirAll(baseDir, 0o700)
}
