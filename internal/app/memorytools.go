package app

import "wails-tmp/internal/tool"

// ===== 记忆工具（本包只剩工具名常量）=====
//
// 三个工具的实现已下沉 `internal/memory/memorytools.go`（构造函数 memory.NewSearchTool /
// NewSaveTool / NewForgetTool），它们只认 internal/memory.ToolContext，不认识 App。
//
// 留在这里的只有**工具名常量**：internal/tool/names.go 第 10 行说明了这份名录的约定——
// 名字是单一真源（那里定义），main 侧保留同名未导出常量作为别名，于是
// tools.go 的 case 标签、subagent.go 的排除名单与 3 个测试文件都能直接写 toolMemorySave，
// 而不必到处出现 `tool.` 前缀（读起来更像"本包的内置工具名"）。
//
// 常量刻意留在这里而不是并进 tools.go：本文件是"记忆工具"这个名字在根包里的落点，
// 顺着它就能找到下沉后的实现。

const (
	toolMemorySearch = tool.ToolMemorySearch
	toolMemorySave   = tool.ToolMemorySave
	toolMemoryForget = tool.ToolMemoryForget
)
