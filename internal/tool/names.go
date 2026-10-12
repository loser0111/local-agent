package tool

// 内置工具名（工具名录的单一真源）。
//
// 这些名字同时被三处使用，必须只有一处定义：
//   - defaultSources()（本包）按它们生成内置来源；
//   - main 侧 newBuiltinTool 按 src.Name 分派实现；
//   - main 侧 directToolOrder 决定谁直出给模型。
//
// main 侧保留同名（未导出）常量作为别名，见 filetools.go / ask.go / memorytools.go / subagent.go。
const (
	ToolExecShell = "exec_shell"
	ToolReadFile  = "read_file"
	ToolWriteFile = "write_file"
	ToolEditFile  = "edit_file"
	ToolGlob      = "glob"
	ToolGrep      = "grep"
	ToolListDir   = "list_dir"
	ToolReadImage = "read_image"

	ToolAskUser      = "ask_user"
	ToolSpawnAgent   = "spawn_agent"
	ToolMemorySearch = "memory_search"
	ToolMemorySave   = "memory_save"
	ToolMemoryForget = "memory_forget"
	// ToolTodo 主会话的待办清单：模型用它在长任务里自跟踪多步进度。
	// 与 plan（规划-审批-执行的重流程）刻意分开——todo 是轻量、可随时改的草稿纸，
	// 不涉及人工审批，也不落 git。
	ToolTodo = "todo"

	// ToolWebSearch 联网搜索：内置、直出。做成内置而不是 cli/http 来源的原因见
	// websearchtool.go 顶部说明（模板替换不转义 + 响应不裁剪两个坑）。
	ToolWebSearch = "web_search"
)

// DefaultReadLimit read_file 默认返回行数（defaultSources 的参数说明与实现共用）
const DefaultReadLimit = 2000

// DirectToolOrder 直出给模型的工具顺序（固定顺序便于断言与提示缓存）。
// 其余工具（MCP / 自定义 CLI / API）仍经 tool_router 发现，避免 prompt 膨胀。
//
// 判据不是"它常不常用"，而是**系统提示词有没有点名它**：提示词里提到的工具必须直出，
// 否则模型在工具列表里找不到它的 schema，只能先花一轮去 tool_router 里 list/describe，
// 甚至照着名字瞎猜参数——两者不一致正是"模型报找不到某工具"这类问题的来源。
var DirectToolOrder = []string{
	ToolReadFile, ToolWriteFile, ToolEditFile, ToolGlob, ToolGrep, ToolListDir, ToolAskUser,
	// read_image 直出：buildBasePrompt 点名了它（"图片要看内容就调 read_image"）。
	// 它是文件六件套里"读"的那一支，参数形状与 read_file 一致，直出的 prompt 代价极小。
	ToolReadImage,
	// exec_shell 直出：buildBasePrompt 明确点名了它（"exec_shell 留给构建、测试、git 等
	// 真正的命令"），却曾因不在本名录而退化成"要先经 tool_router 发现"。构建/测试/git 是
	// 高频操作，每次首用都可能多耗 1-2 轮；它只有一个 cmd 参数，直出的 prompt 代价极小。
	ToolExecShell,
	// spawn_agent 直出而不是经路由器：系统提示词里点名了它（buildBasePrompt 明确告诉模型
	// "可以用 spawn_agent 派子代理"），若工具列表里没有它的 schema，模型只能先花一轮
	// 去 tool_router 里 list/describe，否则就是照着名字瞎猜参数。
	ToolSpawnAgent,
	// 记忆工具直出：buildBasePrompt 点名 memory_search / memory_save / memory_forget。
	ToolMemorySearch, ToolMemorySave, ToolMemoryForget,
	// todo 直出：buildBasePrompt 点名了它（长任务里"先列出清单、做完一项勾一项"）。
	// 它只有一个很短的参数表，直出的 prompt 代价极小，而经路由器发现要多耗一轮——
	// 而"该建清单时模型却先花一轮去 list/describe"恰恰会削弱这个工具的意义。
	ToolTodo,
	// web_search 直出：buildBasePrompt 点名了它（"需要最新/实时信息时用 web_search"）。
	// 它的判据与其它直出工具一致——提示词提到的工具必须在工具列表里有 schema，
	// 否则模型只能先花一轮去 tool_router 里 list/describe，甚至照着名字瞎猜参数。
	// 参数表很短（query / max_results / site），直出的 prompt 代价很小；
	// 而"想联网却先花一轮发现工具"会明显削弱这个工具的用处。
	ToolWebSearch,
}
