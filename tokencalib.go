package main

import "wails-tmp/internal/llm"

// ===== 估算校准系数：根包侧的查询入口 =====
//
// 存储实现已下沉到 internal/llm/tokencalib.go（CalibStore：读盘/落盘、EWMA 观测、
// 区间夹取），连"为什么它是观测缓存而不是配置""为什么只在没有锚点时套用"
// "为什么每次观测都写一次盘"这些理由都随它走。
//
// 这里只剩按会话模型查询这一个适配器。

// calibFor 取某模型的估算校准系数，供 App 各处按会话模型查询。
//
// 存储未初始化（测试里直接构造 App，或 startup 尚未跑到）时按 1.0，
// 与 contextPrefs 同一条原则：任何缺失都不该让估算退化。
func (a *App) calibFor(model string) float64 {
	if a == nil || a.tokenCalib == nil {
		return llm.CalibDefault
	}
	return a.tokenCalib.Ratio(model)
}
