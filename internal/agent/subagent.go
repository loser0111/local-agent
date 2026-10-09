// 本文件是子代理（spawn_agent）的引擎侧：不可交互的权限网关、结论载荷与派生工具。
//
// 与根包的边界：根包那边只剩 `runSubagent`（一次派生的**装配与编排**：建会话、写任务、
// 登记进度、调工具循环、收尾写回）与几个只有它才知道的常量。这里放的是与 App 无关的部分：
//
//   - SubagentEnforcer：把父网关的 ask 降级为带理由的拒绝（见下）；
//   - SubagentResult / DelegatedPaths / SubagentTitle / BuildSubagentPrompt：纯数据与纯文本；
//   - SpawnAgentTool：spawn_agent 工具本体（构造函数只吃一个 spawner 回调，
//     与 ask_user 同一套做法——它需要的是"派生的回路"，不是根包的装配上下文）。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"wails-tmp/internal/permission"
	"wails-tmp/internal/store"
	"wails-tmp/internal/tool"
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
type SubagentEnforcer struct {
	parent *permission.Enforcer

	mu       sync.Mutex
	declined []string
}

// NewSubagentEnforcer 用父会话的权限网关造一个"不可交互"的网关。
// parent 为 nil 时一切按 fail closed 处理（见 Enforce）。
func NewSubagentEnforcer(parent *permission.Enforcer) *SubagentEnforcer {
	return &SubagentEnforcer{parent: parent}
}

// Enforce 实现 Enforcer：只判定不等待。
//
// 走的是父网关的 Decide（不是 Enforce）——这是本文件最容易做错的地方：
// 若图省事直接调父的 Enforce，ask 分支照样会阻塞 5 分钟，而且因为它是间歇性的、
// 只在"子代理恰好碰到需要授权的操作"时出现，很难复现。测试用短超时的 broker 守这一点。
func (e *SubagentEnforcer) Enforce(ctx context.Context, t tool.ToolInterface, args map[string]interface{}) error {
	if e == nil || e.parent == nil {
		// 没有父网关时 fail closed：宁可拒绝，也不能静默放行
		return fmt.Errorf("子代理未装配权限网关，已拒绝执行")
	}

	subject, verdict, callErr := e.parent.Decide(t, args)
	e.parent.RecordVerdict(subject, verdict)
	if callErr != nil {
		// 调用错误（如命令为空）直接回给模型改，它不是权限结论
		return callErr
	}

	switch verdict.Decision {
	case permission.DecisionAllow:
		return nil
	case permission.DecisionDeny:
		return fmt.Errorf("权限拒绝（%s）：%s", verdict.Stage, verdict.Reason)
	default:
		// 需要询问 → 降级为拒绝。换一个 stage，让审计能区分
		// "用户拒绝"与"子代理被系统挡下"——不要把系统的自动行为记成用户的决定。
		declined := permission.Verdict{
			Decision: permission.DecisionDeny,
			Stage:    permission.StageSubagentDeclined,
			Reason:   "该操作需要用户授权，子代理无法代为确认",
		}
		e.parent.RecordVerdict(subject, declined)
		e.record(subject)
		return fmt.Errorf("该操作需要用户授权，子代理无法代为确认。请改用不需要授权的做法，"+
			"或在结论里说明需要用户批准：%s", subject.Summary())
	}
}

// Declined 被挡下的操作（"工具: 主体摘要"），随子代理结果回到主会话展示
func (e *SubagentEnforcer) Declined() []string {
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
func (e *SubagentEnforcer) record(s permission.Subject) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.declined = append(e.declined, fmt.Sprintf("%s: %s", s.Tool, s.Summary()))
}

// ===== 结论载荷与派生工具（P3 第 3 步的引擎侧）=====

const (
	// SubagentDefaultTurns 没指定 max_turns 时的轮次上限
	SubagentDefaultTurns = 20
	// SubagentMaxTurns 轮次硬上限
	SubagentMaxTurns = 30
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

// DelegatedPaths 子代理改过、而父会话在派生之前没碰过的路径。
//
// 只有这些路径要从父会话的归因里剔除：父会话自己先改过、子代理又改了同一个文件时，
// 那份改动仍应算父会话的（否则父会话自己的改动会从 diff 面板里凭空消失）。
// 代价是两个会话同时改同一文件时，父会话的 diff 显示的是子代理写入后的内容——
// 并发写冲突本来就只做事后检测，这里沿用同一条取舍。
func DelegatedPaths(before, childPaths []string) []string {
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

// SubagentTitle 子代理会话的标题：任务前 24 个字符
func SubagentTitle(task string) string {
	return "子代理：" + store.TruncateRunes(strings.ReplaceAll(task, "\n", " "), 24)
}

// BuildSubagentPrompt 子代理的系统提示词。
//
// 任务本身**不写在这里**，而是作为会话的第一条 user 消息落库（见根包 runSubagent）——
// 同一件事写在两处早晚会不一致，而"这条会话被派去做什么"必须是可查的。
// 这里只讲它不知道的事：没有对话对象、没有同僚可求助、看不到主会话的历史。
func BuildSubagentPrompt(dir string) string {
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

// SpawnAgentTool 把一件事交给子代理去做，只收回结论
type SpawnAgentTool struct {
	*tool.BaseTool
	spawner  func(ctx context.Context, task string, maxTurns int) (*SubagentResult, error)
	required []string
}

// NewSpawnAgentTool 构造 spawn_agent 工具。
//
// 构造函数只吃 spawner 这一个回调（与 NewAskUserTool 同一套做法）：根包的装配点因此
// 只剩一行，也不必把装配期上下文（buildContext）交给引擎。参数表由引擎自己用
// tool.FileToolParams 造——internal/tool 本来就是 internal/agent 可依赖的。
func NewSpawnAgentTool(spawner func(ctx context.Context, task string, maxTurns int) (*SubagentResult, error)) *SpawnAgentTool {
	params, required := tool.FileToolParams([]tool.FileArg{
		{Name: "task", Type: "string", Description: "要子代理做的事（它看不到你的对话历史，越具体越好）", Required: true},
		{Name: "max_turns", Type: "number", Description: fmt.Sprintf("可选：子代理最多跑几轮（默认 %d，上限 %d）", SubagentDefaultTurns, SubagentMaxTurns)},
	})
	return &SpawnAgentTool{
		BaseTool: &tool.BaseTool{
			Name: tool.ToolSpawnAgent,
			Description: "派生一个子代理去独立完成一项任务，并等它返回结论。" +
				"子代理有**自己独立的上下文**，它的中间过程不会占用你的上下文——你只收到一份结论。" +
				"适合：把一个模块摸清楚、把一处改动写完并自测、独立调研一个问题。" +
				"不适合：一两步就能做完的事（派生的开销比直接做还大）、必须与用户来回确认的事。" +
				"注意：子代理无法向用户提问，需要授权的操作会被它拒绝并写进结论；它也不能再派生子代理。",
			Parameters: params,
		},
		spawner:  spawner,
		required: required,
	}
}

// RequiredParams 声明必填参数
func (t *SpawnAgentTool) RequiredParams() []string { return t.required }

// Execute 派生子代理并等它跑完，把结果序列化后作为工具结果返回
func (t *SpawnAgentTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
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
