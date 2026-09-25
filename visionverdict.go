package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ===== 看图能力自检结论（落盘，按模型名记住）=====
//
// 为什么值得落盘：`/vision` 测出来的结论（这个端点能不能把图交给模型）在一次会话里
// 可能是"知道了"，但重开应用就忘了——而它的作用恰恰是**在用户贴图时提前说实话**：
// 端点看不到图时，模型会去 curl 下载链接、编造图片内容、让你把文件另存到工作区，
// 这三种行为都让用户更难排查（真机上都出现过）。
//
// 与 token-calib.json 同类：都是**观测缓存**，用户不需要也不应该手改它。

// VisionVerdict 某个模型的看图能力自检结论。
//
// 除了"过没过"，还要记下**当时测的是哪个端点**（ModelID + URL）：
// 结论是关于"这个端点"的，不是关于"这个名字"的。用户很可能换了端点却沿用同一个
// 配置名（比如把 glm-5.2-discount 的 URL 改指向一个视觉模型）——那时旧结论必须失效，
// 否则它会继续告诉模型"你看不到图"，在用户刚修好的时候说反话比不说更糟。
type VisionVerdict struct {
	Passed  bool   `json:"passed"`
	At      int64  `json:"at"`                // Unix 毫秒
	Answer  string `json:"answer,omitempty"`  // 探针的原话（便于日后回看它当时答了什么）
	ModelID string `json:"modelId,omitempty"` // 实际请求用的模型 ID
	URL     string `json:"url,omitempty"`     // 端点地址
}

// VisionVerdictStore 结论存储（~/.local-agent/vision-check.json）
type VisionVerdictStore struct {
	mu       sync.Mutex
	filePath string
	verdicts map[string]VisionVerdict
}

// NewVisionVerdictStore 加载结论；文件不存在或损坏时按"没有结论"处理
// （不要把损坏的文件当成"通过"——那是把不确定性当成放行）。
func NewVisionVerdictStore(filePath string) *VisionVerdictStore {
	s := &VisionVerdictStore{filePath: filePath, verdicts: map[string]VisionVerdict{}}
	data, err := os.ReadFile(filePath)
	if err != nil || len(data) == 0 {
		return s
	}
	var m map[string]VisionVerdict
	if err := json.Unmarshal(data, &m); err != nil {
		fmt.Printf("[VisionCheck] 解析结论文件失败（按无结论处理）: %v\n", err)
		return s
	}
	if m != nil {
		s.verdicts = m
	}
	return s
}

// Set 记录一次自检结论（modelKey 用会话里存的模型名，便于按会话模型查）
func (s *VisionVerdictStore) Set(modelKey, modelID, url string, passed bool, answer string) {
	if s == nil || strings.TrimSpace(modelKey) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verdicts == nil {
		s.verdicts = map[string]VisionVerdict{}
	}
	runes := []rune(strings.TrimSpace(answer))
	if len(runes) > 200 {
		answer = string(runes[:200]) + "…"
	}
	s.verdicts[modelKey] = VisionVerdict{
		Passed:  passed,
		At:      time.Now().UnixMilli(),
		Answer:  answer,
		ModelID: modelID,
		URL:     url,
	}
	if err := s.save(); err != nil {
		fmt.Printf("[VisionCheck] 保存结论失败: %v\n", err)
	}
}

// Get 取某个模型的结论；第二个返回值为 false 表示"没测过"。
// **没测过与"测过且未通过"必须区分**：前者不该影响任何行为，
// 后者要在请求里加一句实话（见 visionUnsupportedNote）。
//
// 端点变了（ModelID 或 URL 与记录不符）时按"没测过"处理：结论是关于端点的，
// 换了端点就该重新测，而不是拿旧结论去影响新端点的行为。
func (s *VisionVerdictStore) Get(modelKey, modelID, url string) (VisionVerdict, bool) {
	if s == nil || strings.TrimSpace(modelKey) == "" {
		return VisionVerdict{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.verdicts[modelKey]
	if !ok {
		return VisionVerdict{}, false
	}
	if v.ModelID != modelID || v.URL != url {
		return VisionVerdict{}, false
	}
	return v, true
}

// Unsupported 该模型是否**已被证明**看不到图（没测过、或端点已变时返回 false）
func (s *VisionVerdictStore) Unsupported(modelKey, modelID, url string) bool {
	v, ok := s.Get(modelKey, modelID, url)
	return ok && !v.Passed
}

func (s *VisionVerdictStore) save() error {
	if s == nil || strings.TrimSpace(s.filePath) == "" {
		return fmt.Errorf("结论存储未初始化")
	}
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	data, err := json.MarshalIndent(s.verdicts, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}
	return os.WriteFile(s.filePath, data, 0o644)
}

// visionUnsupportedNote 注入系统提示词的一句实话：**当前模型很可能看不到图**。
//
// 它不是"修复"——端点能力改不了——而是不让模型在收不到图时瞎猜。
// 真机上它为此去 curl 下载链接、编造过图片内容、还让用户把文件另存到工作区，
// 三种行为都让用户更难排查。把事实告诉它，它就会直接说"我收不到图像内容"。
const visionUnsupportedNote = "\n\n## 关于图片的重要提示\n" +
	"当前模型的看图能力自检**未通过**（用户可用 /vision 复测）：它很可能收不到图像内容，" +
	"只会收到一条「有图片但看不到」的说明。因此：\n" +
	"1. 收不到图像内容时**直接如实说明**——「我这边没有收到图像内容，当前模型可能不支持看图」，" +
	"并建议用户到「设置 → 模型」换一个支持视觉的端点（换完用 /vision 一测即知）。\n" +
	"2. 不要为了看图去下载链接、不要编造图片内容、也不要让用户把图片另存到工作区——" +
	"这些做法都绕不过「模型看不到图」这件事，只会让排查更难。\n" +
	"3. 如果图里主要是文字（报错、日志、看板），可以请用户把关键文字直接贴成文本，" +
	"那样你立刻就能分析。"
