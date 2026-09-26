package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ===== 子代理的权限网关（P3 第 2 步）=====
//
// 主会话的权限网关在"需要询问"时会**阻塞等待用户**（默认 5 分钟）。子代理没有 UI 通道，
// 走那条路只会干等——而且表现为"子代理偶尔卡 5 分钟才失败"，间歇且难复现，是最难查的一类 bug。
//
// 所以这里把 ask **降级成带理由的拒绝**，而不是简单放行或静默失败：
//
//   - allow → 照常放行（父网关允许的，子代理也允许）
//   - deny  → 照常拒绝，理由如实回给模型（内置拒绝名单、用户 deny 规则、plan 模式）
//   - ask   → 降级为拒绝，但错误信息里说清"这需要用户授权，请在结论里说明"，
//     同时记进 Declined，随子代理结果回到主会话
//
// 第三条是这块设计的关键：既保住了不阻塞，又不让子代理的活动变成黑箱——
// 用户最终能看到它想做什么、被什么挡住了，而不是只看到一个"没做"的结论。
type subagentEnforcer struct {
	parent *permissionEnforcer

	mu       sync.Mutex
	declined []string
}

// Enforce 实现 Enforcer：只判定不等待。
//
// 走的是父网关的 Decide（不是 Enforce）——这是本文件最容易做错的地方：
// 若图省事直接调父的 Enforce，ask 分支照样会阻塞 5 分钟，而且因为它是间歇性的、
// 只在"子代理恰好碰到需要授权的操作"时出现，很难复现。测试用短超时的 broker 守这一点。
func (e *subagentEnforcer) Enforce(ctx context.Context, tool ToolInterface, args map[string]interface{}) error {
	if e == nil || e.parent == nil {
		// 没有父网关时 fail closed：宁可拒绝，也不能静默放行
		return fmt.Errorf("子代理未装配权限网关，已拒绝执行")
	}

	subject, verdict, callErr := e.parent.Decide(tool, args)
	e.parent.audit(subject, verdict)
	if callErr != nil {
		// 调用错误（如命令为空）直接回给模型改，它不是权限结论
		return callErr
	}

	switch verdict.Decision {
	case DecisionAllow:
		return nil
	case DecisionDeny:
		return fmt.Errorf("权限拒绝（%s）：%s", verdict.Stage, verdict.Reason)
	default:
		// 需要询问 → 降级为拒绝。换一个 stage，让审计能区分
		// "用户拒绝"与"子代理被系统挡下"——不要把系统的自动行为记成用户的决定。
		declined := Verdict{
			Decision: DecisionDeny,
			Stage:    StageSubagentDeclined,
			Reason:   "该操作需要用户授权，子代理无法代为确认",
		}
		e.parent.audit(subject, declined)
		e.record(subject)
		return fmt.Errorf("该操作需要用户授权，子代理无法代为确认。请改用不需要授权的做法，"+
			"或在结论里说明需要用户批准：%s", subject.Summary())
	}
}

// Declined 被挡下的操作（"工具: 主体摘要"），随子代理结果回到主会话展示
func (e *subagentEnforcer) Declined() []string {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.declined))
	copy(out, e.declined)
	return out
}

// record 记下一次被挡下的操作
func (e *subagentEnforcer) record(s Subject) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.declined = append(e.declined, fmt.Sprintf("%s: %s", s.Tool, s.Summary()))
}

// ===== P3 第 3 步：spawn_agent 工具与子代理执行 =====

// toolSpawnAgent spawn_agent 的工具名（作为内置来源注册，见 toolstore.go 的 defaultSources）
const toolSpawnAgent = "spawn_agent"

const (
	subagentDefaultTurns = 20 // 没指定 max_turns 时的轮次上限
	subagentMaxTurns     = 30 // 轮次硬上限
	// subagentMaxTotal 单次主运行最多派生几个子代理。
	// 上限是硬要求：没有它，一个跑飞的模型能把成本和时间都撑到不可控。
	subagentMaxTotal = 16
	// subagentSummaryMaxLen 结论长度上限——它就是唯一进入主上下文的东西，
	// 不设上限的话，一个话多的子代理会把主上下文又撑起来，这个功能的意义就没了。
	subagentSummaryMaxLen = 4096
)

// 子代理的状态取值。
// running / interrupted 只出现在概况（SubagentInfo）里；completed / failed / cancelled
// 同时是回给主会话的结论状态，两侧共用一套取值，避免"面板说完成、结论说失败"。
const (
	subagentStatusRunning     = "running"
	subagentStatusCompleted   = "completed"
	subagentStatusFailed      = "failed"
	subagentStatusCancelled   = "cancelled"
	subagentStatusInterrupted = "interrupted"
)

// SubagentResult 子代理跑完后回给主会话的结果。
// Summary 是**唯一**进入主上下文的部分；中间过程留在子代理自己的会话文件里。
type SubagentResult struct {
	RunID     string   `json:"runId"`
	Status    string   `json:"status"`  // completed | failed | cancelled
	Summary   string   `json:"summary"` // 结论（已截断）
	Files     []string `json:"files"`   // 它改过的文件
	ToolCalls int      `json:"toolCalls"`
	Declined  []string `json:"declined,omitempty"` // 因需要授权而被挡下的操作
	Error     string   `json:"error,omitempty"`
}

// delegatedPaths 子代理改过、而父会话在派生之前没碰过的路径。
//
// 只有这些路径要从父会话的归因里剔除：父会话自己先改过、子代理又改了同一个文件时，
// 那份改动仍应算父会话的（否则父会话自己的改动会从 diff 面板里凭空消失）。
// 代价是两个会话同时改同一文件时，父会话的 diff 显示的是子代理写入后的内容——
// 并发写冲突本来就只做事后检测（见文档 2.6），这里沿用同一条取舍。
func delegatedPaths(before, childPaths []string) []string {
	if len(childPaths) == 0 {
		return nil
	}
	keep := make(map[string]bool, len(before))
	for _, p := range before {
		keep[p] = true
	}
	out := make([]string, 0, len(childPaths))
	for _, p := range childPaths {
		if p == "" || keep[p] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// runSubagent 派生一个子代理并等它跑完，返回结论。
//
// 隔离是怎么来的：子代理**自建一个会话**（配置从父会话复制），因此天然有独立的 sessionID；
// 消息、diff 归因、文件改动记录、checkpoint ref 全按 sessionID 键控，
// 所以它不会污染父会话——不需要额外的归因键字段。
//
// 这一步是**同步**的：一次只跑一个子代理。并行需要循环并发执行同一批工具调用，
// 那会牵动权限询问、改动归因、事件顺序，是一次独立改动，不在这一步里做。
// 子代理的主要价值（上下文隔离）同步版本已经完整具备。
func (a *App) runSubagent(parentRun *runControl, parent *Session, dir string, model *Model,
	task string, maxTurns int) (*SubagentResult, error) {

	task = strings.TrimSpace(task)
	if task == "" {
		return nil, fmt.Errorf("task 不能为空")
	}
	if parent == nil {
		return nil, fmt.Errorf("缺少父会话，无法派生代理")
	}
	if model == nil {
		return nil, fmt.Errorf("缺少模型配置，无法派生代理")
	}
	// parentRun 为 nil 时 claimSubagent 也会返回 false，但那样报出来的是"名额已用完"，
	// 与真实原因（这次运行没有取消控制块）不符，会把排查带偏。取消要级联也依赖它。
	if parentRun == nil {
		return nil, fmt.Errorf("当前运行不支持派生代理")
	}
	if maxTurns <= 0 || maxTurns > subagentMaxTurns {
		maxTurns = subagentDefaultTurns
	}
	// 名额从**父运行**上扣：计划执行是按步骤重建工具视图的，计数挂在闭包里会逐步重置
	if !parentRun.claimSubagent(subagentMaxTotal) {
		return nil, fmt.Errorf("本次运行的派生名额已用完（上限 %d 个）。"+
			"请自己继续完成，或把剩下的事合并成一个更聚焦的子任务", subagentMaxTotal)
	}

	// 父会话本轮已经碰过的路径，用来判断哪些改动是父会话自己的（见 delegatedPaths）
	before := a.diffService.TurnTouchedSnapshot(parent.ID)

	// 子代理自建会话：配置从父会话复制，于是权限模式、工具与技能白名单都自然继承
	child, err := a.sessionStore.CreateSession(SessionConfig{
		Title:          subagentTitle(task),
		Project:        parent.Project,
		Model:          parent.Model,
		PermissionMode: parent.PermissionMode,
		ViewMode:       ViewModeSummary,
		Environment:    parent.Environment,
		EnabledTools:   parent.EnabledTools,
		EnabledSkills:  parent.EnabledSkills,
		ParentID:       parent.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("创建子代理会话失败: %w", err)
	}

	// 任务必须作为**第一条 user 消息**落进子代理会话，不能只写在系统提示词里。
	// 两个理由：① runToolLoop 只加载会话里已有的消息，而子代理会话是新建的、一条消息都没有，
	// 于是第一次请求里只剩一条 system——多数网关（含 Anthropic）会直接以
	// "messages 不能为空"拒绝，功能根本跑不起来；② 面板与排障都要能看到它被派去做什么。
	if _, err := a.sessionStore.AppendMessage(child.ID, Message{
		Role:    RoleUser,
		Content: task,
	}); err != nil {
		return nil, fmt.Errorf("写入子代理任务失败: %w", err)
	}

	// 登记进 tracker：面板要能看到"谁在跑、跑到第几步"
	label := model.Name
	if label == "" {
		label = model.ModelID
	}
	live := &SubagentInfo{
		RunID:     child.ID,
		ParentID:  parent.ID,
		Title:     child.Title,
		Task:      task,
		Status:    subagentStatusRunning,
		Model:     label,
		StartedAt: time.Now().UnixMilli(),
	}
	a.subagents.begin(live)
	a.emitSubagentEvent(*cloneSubagentInfo(live))

	// 权限网关换成"不可交互"的：需要询问的操作一律降级为带理由的拒绝
	sub := &subagentEnforcer{parent: a.newPermissionEnforcer(child, dir)}

	// 工具视图：剔掉 ask_user（没有 UI 通道，留着只会浪费它一轮）；
	// 刻意**不设 SpawnAgent** —— 于是 spawn_agent 不注册，子代理无法再次派生（禁止嵌套）。
	view := a.toolManager.BuildView(context.Background(), BuildOptions{
		EnabledTools:  child.EnabledTools,
		EnabledSkills: child.EnabledSkills,
		ProjectDir:    dir,
		SessionID:     child.ID,
		Changes:       a.fileChanges,
		Attachments:   a.attachments,
		Enforcer:      sub,
		ExcludeTools:  []string{toolAskUser, toolMemorySave, toolMemoryForget},
		Memory:        a.memory,
		MemoryProject: memoryProjectSlug(child.Project),
		IgnoreMemory:  child.IgnoreMemory || !a.memoryEnabled(),
	})

	res := runToolLoop(&agentRun{
		RunID:        child.ID,
		SessionID:    child.ID,
		Dir:          dir,
		IsRepo:       dir != "" && a.diffService.IsRepo(dir),
		SystemPrompt: buildSubagentPrompt(dir),
		Model:        model,
		ToolView:     view,
		Stream:       false, // 子代理不流式：没人实时看它的输出
		Control:      parentRun,
		MaxTurns:     maxTurns,
		Store:        a.sessionStore,
		Diff:         a.diffService,
		Changes:      a.fileChanges,
		ReqLog:       a.reqLog,
		Attachments:  a.attachments,
		// 子代理用的是同一个模型，看图能力的结论也照传（它同样可能读到图）
		VisionUnsupported: a.visionVerdicts.Unsupported(child.Model, modelCallID(model), model.URL),
		Compactor:         nil, // 子代理不做摘要压缩：多条运行同改一份会话摘要会互相打架
		Recorder:          &subagentRecorder{app: a, runID: child.ID},
	})

	out := &SubagentResult{
		RunID:     child.ID,
		Status:    subagentStatusCompleted,
		Summary:   truncateRunes(strings.TrimSpace(res.Reply), subagentSummaryMaxLen),
		Declined:  sub.Declined(),
		ToolCalls: len(res.ToolCalls),
	}
	switch {
	case res.Cancelled:
		out.Status = subagentStatusCancelled
	case res.Error != "":
		out.Status = subagentStatusFailed
		out.Error = res.Error
	}
	if out.Summary == "" {
		out.Summary = "（子代理没有给出结论）"
	}
	if dir != "" {
		if files, err := a.diffService.DiffForSession(child.ID, dir); err == nil {
			out.Files = diffFilePaths(files)
			// 子代理的改动**不能算父会话的改动**：它整个跑动都落在父会话那次 spawn_agent
			// 调用的窗口里，而归因是按时间窗口扫全工作区做的（DiffService.NoteActivity），
			// 不剔除的话父会话的 diff 面板会冒出不是它改的文件，回退父会话那一轮时还会
			// 连带把这些文件一起回退。登记后由工具循环在归因扫描之后剔除（见 chat.go）。
			parentRun.noteExcludedPaths(parent.ID, delegatedPaths(before, out.Files))
		}
	}

	// 收尾：概况写回子代理会话（重启后仍能查看，回退它改的文件也要靠这个会话），
	// 同时从内存注册表摘掉——再留着会让面板里同时出现"运行中"和"已完成"两条。
	final := &SubagentInfo{
		RunID:     child.ID,
		ParentID:  parent.ID,
		Title:     child.Title,
		Task:      task,
		Status:    out.Status,
		Model:     label,
		Step:      len(res.ToolCalls),
		StartedAt: live.StartedAt,
		EndedAt:   time.Now().UnixMilli(),
		Summary:   out.Summary,
		Files:     out.Files,
		Declined:  out.Declined,
		Error:     out.Error,
	}
	a.subagents.finish(child.ID)
	if err := a.sessionStore.SetSubagentInfo(child.ID, final); err != nil {
		// 写不回不影响本次结论（它已经回给模型了），只记一笔日志
		fmt.Printf("[subagent] 写入子代理概况失败: %v\n", err)
	}
	a.emitSubagentEvent(*cloneSubagentInfo(final))
	return out, nil
}

// subagentTitle 子代理会话的标题：任务前 24 个字符
func subagentTitle(task string) string {
	return "子代理：" + truncateRunes(strings.ReplaceAll(task, "\n", " "), 24)
}

// buildSubagentPrompt 子代理的系统提示词。
//
// 任务本身**不写在这里**，而是作为会话的第一条 user 消息落库（见 runSubagent）——
// 同一件事写在两处早晚会不一致，而"这条会话被派去做什么"必须是可查的。
// 这里只讲它不知道的事：没有对话对象、没有同僚可求助、看不到主会话的历史。
func buildSubagentPrompt(dir string) string {
	var sb strings.Builder
	sb.WriteString("你是主会话派来的子代理，独立完成用户消息里那一项任务。\n\n## 工作方式\n")
	if dir != "" {
		sb.WriteString("- 工作区目录：" + dir + "（相对路径都相对于它）\n")
	}
	sb.WriteString("- 你有自己独立的上下文：主会话**看不到**你的中间过程，只会收到你最后这份结论。\n")
	sb.WriteString("- 你无法向用户提问。遇到需要用户授权、或必须由用户拍板的事，不要绕过去硬做，" +
		"写进结论的「待用户确认」一节即可。\n")
	sb.WriteString("- 允许改动文件，但只改完成任务所需的部分，不要顺手重构无关代码。\n\n")
	sb.WriteString("## 结论格式（直接输出，不要寒暄）\n")
	sb.WriteString("1. 做了什么：关键动作\n")
	sb.WriteString("2. 结论：查清或完成的事实，带上具体路径、函数名、命令等标识\n")
	sb.WriteString("3. 改动的文件：列出路径；没有就写「无」\n")
	sb.WriteString("4. 未完成 / 待用户确认：受阻原因与下一步建议\n")
	return sb.String()
}

// ===== spawn_agent 工具 =====

// spawnAgentTool 把一件事交给子代理去做，只收回结论
type spawnAgentTool struct {
	*BaseTool
	spawner  func(ctx context.Context, task string, maxTurns int) (*SubagentResult, error)
	required []string
}

// newSpawnAgentTool 构造 spawn_agent 工具（bc.spawner 为 nil 时调用方不会注册它）
func newSpawnAgentTool(bc buildContext) *spawnAgentTool {
	params, required := fileToolParams([]fileArg{
		{Name: "task", Type: "string", Description: "要子代理做的事（它看不到你的对话历史，越具体越好）", Required: true},
		{Name: "max_turns", Type: "number", Description: fmt.Sprintf("可选：子代理最多跑几轮（默认 %d，上限 %d）", subagentDefaultTurns, subagentMaxTurns)},
	})
	return &spawnAgentTool{
		BaseTool: &BaseTool{
			Name: toolSpawnAgent,
			Description: "派生一个子代理去独立完成一项任务，并等它返回结论。" +
				"子代理有**自己独立的上下文**，它的中间过程不会占用你的上下文——你只收到一份结论。" +
				"适合：把一个模块摸清楚、把一处改动写完并自测、独立调研一个问题。" +
				"不适合：一两步就能做完的事（派生的开销比直接做还大）、必须与用户来回确认的事。" +
				"注意：子代理无法向用户提问，需要授权的操作会被它拒绝并写进结论；它也不能再派生子代理。",
			Parameters: params,
		},
		spawner:  bc.spawner,
		required: required,
	}
}

// RequiredParams 声明必填参数
func (t *spawnAgentTool) RequiredParams() []string { return t.required }

// Execute 派生子代理并等它跑完，把结果序列化后作为工具结果返回
func (t *spawnAgentTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if t.spawner == nil {
		return "", fmt.Errorf("当前运行不支持派生代理")
	}
	task, _ := args["task"].(string)
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("task 参数是必需的")
	}
	maxTurns := 0
	switch v := args["max_turns"].(type) {
	case float64:
		maxTurns = int(v)
	case int:
		maxTurns = v
	}

	res, err := t.spawner(ctx, task, maxTurns)
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return "", fmt.Errorf("序列化子代理结果失败: %w", err)
	}
	return string(b), nil
}
