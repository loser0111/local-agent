package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/snapshot"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
)

// ===== P3 第 3 步：子代理的执行（App 侧）=====
//
// 引擎侧已下沉 internal/agent/subagent.go：权限网关（agent.SubagentEnforcer）、
// 结论载荷（agent.SubagentResult）、派生的纯工具（agent.DelegatedPaths /
// agent.SubagentTitle / agent.BuildSubagentPrompt）与 spawn_agent 工具本体。
//
// 留在这里的只有"必须由 App 来做"的两件事：
//   - 常量：派生名额（挂在**运行**上，见下）与结论长度上限；状态取值（面板与结论共用一套）；
//   - runSubagent：建会话、写任务、登记进度、装配子视图、调同一个 runToolLoop、收尾写回。
//     这五步各自都要摸 App 的字段（sessionStore / diffService / toolManager / subagents /
//     visionVerdicts），且是"隔离"这件事的实现本身，不适合再往引擎里塞。

// toolSpawnAgent spawn_agent 的工具名（作为内置来源注册，见 internal/tool 的 tool.DefaultSources）
const toolSpawnAgent = tool.ToolSpawnAgent

const (
	// subagentMaxTotal 单次主运行最多派生几个子代理。
	// 上限是硬要求：没有它，一个跑飞的模型能把成本和时间都撑到不可控。
	subagentMaxTotal = 16
	// subagentSummaryMaxLen 结论长度上限——它就是唯一进入主上下文的东西，
	// 不设上限的话，一个话多的子代理会把主上下文又撑起来，这个功能的意义就没了。
	subagentSummaryMaxLen = 4096
)

// 子代理的状态取值。
// running / interrupted 只出现在概况（store.SubagentInfo）里；completed / failed / cancelled
// 同时是回给主会话的结论状态，两侧共用一套取值，避免"面板说完成、结论说失败"。
//
// 留在根包而不是随 SubagentResult 下沉：它是**面板与结论共用的词汇表**，两侧的写入方
// （runSubagent 收尾、ListSubagents 补中断态）都在根包，引擎侧只把它当普通字符串带着走。
const (
	subagentStatusRunning     = "running"
	subagentStatusCompleted   = "completed"
	subagentStatusFailed      = "failed"
	subagentStatusCancelled   = "cancelled"
	subagentStatusInterrupted = "interrupted"
)

// runSubagent 派生一个子代理并等它跑完，返回结论。
//
// 隔离是怎么来的：子代理**自建一个会话**（配置从父会话复制），因此天然有独立的 sessionID；
// 消息、diff 归因、文件改动记录、checkpoint ref 全按 sessionID 键控，
// 所以它不会污染父会话——不需要额外的归因键字段。
//
// 这一步是**同步**的：一次只跑一个子代理。并行需要循环并发执行同一批工具调用，
// 那会牵动权限询问、改动归因、事件顺序，是一次独立改动，不在这一步里做。
// 子代理的主要价值（上下文隔离）同步版本已经完整具备。
func (a *App) runSubagent(parentRun *agent.RunControl, parent *store.Session, dir string, model *store.Model,
	task string, maxTurns int) (*agent.SubagentResult, error) {

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
	if maxTurns <= 0 || maxTurns > agent.SubagentMaxTurns {
		maxTurns = agent.SubagentDefaultTurns
	}
	// 名额从**父运行**上扣：计划执行是按步骤重建工具视图的，计数挂在闭包里会逐步重置
	if !parentRun.ClaimSubagent(subagentMaxTotal) {
		return nil, fmt.Errorf("本次运行的派生名额已用完（上限 %d 个）。"+
			"请自己继续完成，或把剩下的事合并成一个更聚焦的子任务", subagentMaxTotal)
	}

	// 父会话本轮已经碰过的路径，用来判断哪些改动是父会话自己的（见 agent.DelegatedPaths）
	before := a.diffService.TurnTouchedSnapshot(parent.ID)

	// 子代理自建会话：配置从父会话复制，于是权限模式、工具与技能白名单都自然继承
	child, err := a.sessionStore.CreateSession(store.SessionConfig{
		Title:          agent.SubagentTitle(task),
		Project:        parent.Project,
		Model:          parent.Model,
		PermissionMode: parent.PermissionMode,
		ViewMode:       store.ViewModeSummary,
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
	if _, err := a.sessionStore.AppendMessage(child.ID, store.Message{
		Role:    store.RoleUser,
		Content: task,
	}); err != nil {
		return nil, fmt.Errorf("写入子代理任务失败: %w", err)
	}

	// 登记进 tracker：面板要能看到"谁在跑、跑到第几步"
	label := model.Name
	if label == "" {
		label = model.ModelID
	}
	live := &store.SubagentInfo{
		RunID:     child.ID,
		ParentID:  parent.ID,
		Title:     child.Title,
		Task:      task,
		Status:    subagentStatusRunning,
		Model:     label,
		StartedAt: time.Now().UnixMilli(),
	}
	a.subagents.Begin(live)
	a.emitSubagentEvent(*agent.CloneSubagentInfo(live))

	// 权限网关换成"不可交互"的：需要询问的操作一律降级为带理由的拒绝
	sub := agent.NewSubagentEnforcer(a.newPermissionEnforcer(child, dir))

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
		SystemPrompt: agent.BuildSubagentPrompt(dir),
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
		VisionUnsupported: a.visionVerdicts.Unsupported(child.Model, store.ModelCallID(model), model.URL),
		Compactor:         nil, // 子代理不做摘要压缩：多条运行同改一份会话摘要会互相打架
		Recorder:          &subagentRecorder{app: a, runID: child.ID},
	})

	out := &agent.SubagentResult{
		RunID:     child.ID,
		Status:    subagentStatusCompleted,
		Summary:   store.TruncateRunes(strings.TrimSpace(res.Reply), subagentSummaryMaxLen),
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
			out.Files = snapshot.DiffFilePaths(files)
			// 子代理的改动**不能算父会话的改动**：它整个跑动都落在父会话那次 spawn_agent
			// 调用的窗口里，而归因是按时间窗口扫全工作区做的（diff.DiffService.NoteActivity），
			// 不剔除的话父会话的 diff 面板会冒出不是它改的文件，回退父会话那一轮时还会
			// 连带把这些文件一起回退。登记后由工具循环在归因扫描之后剔除（见 chat.go）。
			parentRun.NoteExcludedPaths(parent.ID, agent.DelegatedPaths(before, out.Files))
		}
	}

	// 收尾：概况写回子代理会话（重启后仍能查看，回退它改的文件也要靠这个会话），
	// 同时从内存注册表摘掉——再留着会让面板里同时出现"运行中"和"已完成"两条。
	final := &store.SubagentInfo{
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
	a.subagents.Finish(child.ID)
	if err := a.sessionStore.SetSubagentInfo(child.ID, final); err != nil {
		// 写不回不影响本次结论（它已经回给模型了），只记一笔日志
		fmt.Printf("[subagent] 写入子代理概况失败: %v\n", err)
	}
	a.emitSubagentEvent(*agent.CloneSubagentInfo(final))
	return out, nil
}
