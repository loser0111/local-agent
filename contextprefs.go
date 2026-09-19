package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ===== 上下文管理的可配置项 =====
//
// 为什么单独一个文件，而不是塞进模型配置：这两个参数是**全局策略**，与具体模型无关。
// 同一个模型今天想留 12 条原文、明天想留 30 条，不该逼用户去改模型条目。
//
// 存放位置：<userDir>/context.json（userDir = ~/.local-agent）。
// **文件不存在 = 全部取内置默认值**：这让"从未配置过"和"配置文件被人手改坏"
// 走同一条安全路径，不会因为一个解析错误把压缩策略变成不可控值。

// ContextPrefs 上下文管理偏好
type ContextPrefs struct {
	// KeepRecentMsgs 压缩时强制保留的最近消息条数（这段原文不进摘要）。
	//
	// 它就是原先的常量 contextKeepRecentMsgs，现在可配。留空/越界一律回落到默认，
	// 所以零值是一个"没配过"的合法表示，不需要额外的 *int。
	KeepRecentMsgs int `json:"keepRecentMsgs"`
}

const (
	// contextKeepRecentMin 下限。留 1 条等于让切点贴近队尾，几乎压不动东西；
	// 留 4 条是最激进的合理值（约两轮对话的原文）。
	contextKeepRecentMin = 4
	// contextKeepRecentMax 上限。比这更大基本等于关掉压缩，多半是手滑多敲了几个 0。
	contextKeepRecentMax = 500
)

// ContextPrefsStore context.json 的读写（读写都加锁，内存为唯一真源）
type ContextPrefsStore struct {
	path  string
	mu    sync.Mutex
	prefs ContextPrefs
}

// NewContextPrefsStore 建存储并尝试读盘。读失败不报错——见文件头的失败方向说明。
func NewContextPrefsStore(path string) *ContextPrefsStore {
	s := &ContextPrefsStore{path: path}
	s.load()
	return s
}

// load 读盘；任何失败都保持零值（= 用默认）
func (s *ContextPrefsStore) load() {
	if s == nil || s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var p ContextPrefs
	if err := json.Unmarshal(b, &p); err != nil {
		fmt.Printf("[context] 解析 %s 失败（改用默认压缩策略）: %v\n", s.path, err)
		return
	}
	s.prefs = p
}

// Get 取当前偏好。返回值一定已归一化，调用方不必再校验。
func (s *ContextPrefsStore) Get() ContextPrefs {
	if s == nil {
		return ContextPrefs{KeepRecentMsgs: contextKeepRecentMsgs}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.prefs
	p.KeepRecentMsgs = normalizeKeepRecent(p.KeepRecentMsgs)
	return p
}

// SetKeepRecentMsgs 写入并落盘。越界在这里就被拒掉，而不是存下去再靠 Get 纠正——
// 否则用户改完看不到反馈，还以为自己设的值生效了。
func (s *ContextPrefsStore) SetKeepRecentMsgs(n int) error {
	if s == nil {
		return fmt.Errorf("上下文配置存储未初始化")
	}
	if n < contextKeepRecentMin || n > contextKeepRecentMax {
		return fmt.Errorf("保留条数需在 %d–%d 之间", contextKeepRecentMin, contextKeepRecentMax)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefs.KeepRecentMsgs = n
	b, err := json.MarshalIndent(s.prefs, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(s.path, b, 0o644)
}

// normalizeKeepRecent 越界与零值统一回落到内置默认
func normalizeKeepRecent(n int) int {
	if n < contextKeepRecentMin || n > contextKeepRecentMax {
		return contextKeepRecentMsgs
	}
	return n
}
