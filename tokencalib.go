package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
)

// ===== 估算校准系数（真实 usage / 字符估算）=====
//
// 背景：estimateTokens 是纯字符估算（中日韩字符 ×1、其余 4 字符 ×1），而真实 token 数
// 完全取决于各家模型的分词器。同一个汉字在不同分词器下可以是 0.6 个 token
// （DeepSeek/Qwen 这类做过中文优化的），也可以是 1.5~2 个（byte-level BPE 且词表里
// 中文覆盖一般的）。**没有任何一个写死的系数能同时覆盖它们**，所以这里不写系数，而是观测它：
//
//	每次真实模型调用都会回传 usage.prompt_tokens。那一刻，"同一段消息序列的真实 token 数"
//	和"它的字符估算数"都在手上，两者的比值就是这个模型、这个分词器当前的经验系数。
//
// 于是本文件存的是**观测缓存**，不是配置：它随模型自动适配，也会随对话内容构成
// （中英比例）缓慢漂移，用 EWMA 平滑掉单次噪声。别把它改成写死的常数。
//
// 只在**没有锚点**时套用：有锚点时用量已经是模型回传的真值，再乘一次等于对真值做二次修正
// （见 contextStatOf）。

const (
	// calibDefault 没有观测时用的系数：1.0 = 退化成原样的纯字符估算。
	calibDefault = 1.0
	// calibMin/calibMax 合法区间。观测值越界说明这次样本本身有问题（比如请求被上游改写、
	// usage 记的不是这个量），夹住而不是丢弃：丢弃会让坏样本永远卡在系统里，
	// 夹住至少不会把估算推到荒谬的数量级。
	calibMin = 0.5
	calibMax = 4.0
	// calibAlpha EWMA 权重。取 0.3 的理由：一次观测只覆盖一段特定内容构成
	// （可能整段都是英文代码），信它 30%、其余信历史，能在几次调用内收敛，
	// 又不会被单次异常样本带飞。
	calibAlpha = 0.3
	// calibMinSampleTokens 低于这个估算量级的样本直接忽略：几百 token 的序列里，
	// 系统提示与工具定义的固定开销占比过大，几 token 的偏差就能把比值推成 2 倍。
	calibMinSampleTokens = 200
)

// TokenCalibStore token-calib.json 的读写（读写都加锁，内存为唯一真源）。
//
// 与 ContextPrefsStore 保持同一条失败方向：文件不存在或解析失败一律回落默认值，
// 绝不让一个坏文件把用量估算变成不可控值。
type TokenCalibStore struct {
	path   string
	mu     sync.Mutex
	ratios map[string]float64 // model -> ratio
}

// tokenCalibFile 落盘的 JSON 形状。用一层 "ratios" 包住而不是把 map 直接放顶层：
// 将来要加版本号、观测次数之类的字段时，不必破坏已有文件的解析。
type tokenCalibFile struct {
	Ratios map[string]float64 `json:"ratios"`
}

// NewTokenCalibStore 建存储并尝试读盘。
func NewTokenCalibStore(path string) *TokenCalibStore {
	s := &TokenCalibStore{path: path, ratios: map[string]float64{}}
	s.load()
	return s
}

// load 读盘；任何失败都保持空表（= 全部用默认系数 1.0）
func (s *TokenCalibStore) load() {
	if s == nil || s.path == "" {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return // 文件不存在 = 还没观测过，用默认系数
	}
	var f tokenCalibFile
	if err := json.Unmarshal(b, &f); err != nil {
		fmt.Printf("[context] 解析 %s 失败（估算校准系数改回 1.0）: %v\n", s.path, err)
		return
	}
	if s.ratios == nil {
		s.ratios = map[string]float64{}
	}
	for model, r := range f.Ratios {
		s.ratios[model] = clampCalib(r)
	}
}

// Ratio 取某模型的校准系数。任何异常（存储为 nil、模型名为空、从未观测过）都返回
// calibDefault，绝不退化成 0——那会把用量显示成 0，用户会以为上下文是空的。
func (s *TokenCalibStore) Ratio(model string) float64 {
	if s == nil || model == "" {
		return calibDefault
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.ratios[model]
	if !ok {
		return calibDefault
	}
	return clampCalib(r)
}

// Observe 用一次真实用量回传做观测：realTokens 个真实 token 对应 estimatedTokens 个
// 字符估算 token。估计侧必须传**未缩放**的值，否则观测会被现有系数拉向 1.0、自我抹平。
func (s *TokenCalibStore) Observe(model string, realTokens, estimatedTokens int) {
	if s == nil || model == "" || realTokens <= 0 || estimatedTokens <= 0 {
		return
	}
	if estimatedTokens < calibMinSampleTokens {
		return // 样本太小，比值噪声会主导 EWMA
	}
	r := clampCalib(float64(realTokens) / float64(estimatedTokens))

	s.mu.Lock()
	if old, ok := s.ratios[model]; ok {
		r = clampCalib(calibAlpha*r + (1-calibAlpha)*old)
	}
	s.ratios[model] = r
	snapshot := make(map[string]float64, len(s.ratios))
	for k, v := range s.ratios {
		snapshot[k] = v
	}
	s.mu.Unlock()

	// 拷一份快照再落盘：写文件不该握着锁，否则一次慢 IO 会挡住正在跑的对话。
	s.persist(snapshot)
}

// persist 落盘。失败只打日志、不向上抛：校准系数是锦上添花的东西，绝不能因为它
// 把一次对话搞挂；下次调用会再观测一次。
//
// 每次观测都写一次盘（小文件、几百字节），为的是"观测即持久"——攒着批量写会在崩溃时
// 丢掉最近的观测，而那段观测恰恰最贴近当前的内容构成。
func (s *TokenCalibStore) persist(ratios map[string]float64) {
	if s == nil || s.path == "" {
		return
	}
	b, err := json.MarshalIndent(tokenCalibFile{Ratios: ratios}, "", "  ")
	if err != nil {
		fmt.Printf("[context] 序列化校准系数失败: %v\n", err)
		return
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Printf("[context] 创建校准系数目录失败: %v\n", err)
			return
		}
	}
	if err := os.WriteFile(s.path, b, 0o644); err != nil {
		fmt.Printf("[context] 写入 %s 失败: %v\n", s.path, err)
	}
}

// clampCalib 把系数收进合法区间；NaN/Inf 一并挡掉——它们会让后面所有比较与格式化
// 的结果变得不可预测，而它们真的可能出现（除零之外的浮点异常路径）。
func clampCalib(r float64) float64 {
	if math.IsNaN(r) || math.IsInf(r, 0) || r <= 0 {
		return calibDefault
	}
	if r < calibMin {
		return calibMin
	}
	if r > calibMax {
		return calibMax
	}
	return r
}

// calibFor 取某模型的估算校准系数，供 App 各处按会话模型查询。
//
// 存储未初始化（测试里直接构造 App，或 startup 尚未跑到）时按 1.0，
// 与 contextPrefs 同一条原则：任何缺失都不该让估算退化。
func (a *App) calibFor(model string) float64 {
	if a == nil || a.tokenCalib == nil {
		return calibDefault
	}
	return a.tokenCalib.Ratio(model)
}
