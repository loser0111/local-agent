package permission

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ===== 权限网关：把判定引擎与询问回路接到工具执行上 =====

// ToolView 判定所需的最小工具视图（"工具叫什么名字"）。
//
// 为什么不直接用 internal/tool 的 ToolInterface：那个包已经 import 本包
// （工具内部要看权限规则），本包再 import 回去就成环。判定真正用到的只有"名字"这一件事，
// 而 tool.ToolInterface 的方法集天然是它的超集，调用点无需任何转换。
// 自报主体的工具（命令模板类）通过实现 SubjectProvider 补充更多信息，见 subjectFor。
type ToolView interface {
	GetName() string
}

// Responder 询问回路对"界面"的依赖，由装配方（根包的 *App）实现。
//
// 无头运行（自动化测试、无人值守）没有可应答的界面：此时 Available 为 false，
// 网关不进入阻塞等待，直接按拒绝处理——既符合 fail closed，也避免白等一个超时周期。
type Responder interface {
	// Available 当前是否有可应答的界面
	Available() bool
	// Emit 把授权请求推给前端
	Emit(req Request)
	// AddAllowRule 把用户选择的规则写入配置层（scope=rule 时）
	AddAllowRule(sessionID, layer, rule string) error
}

// Enforcer 把引擎与询问回路接到工具执行上。每次 runToolLoop 构造一个实例，
// 绑定了会话、工作目录、模式，以及**那一刻**的规则快照（改配置后下一条消息生效）。
type Enforcer struct {
	SessionID  string
	ProjectDir string
	Mode       Mode
	Rules      *RuleSet

	// Grants 会话级授权（用户答"本会话放行"时逐段落账）；Audit 判定审计
	Grants *GrantStore
	Audit  *AuditLog

	// Broker 询问回路；UI 是它的对端。两者都缺失（或 UI 无界面）时，需要询问的操作一律拒绝。
	Broker *Broker
	UI     Responder
}

// Decide 判定一次工具调用。
// Authorize 只判定、不等待：把「需要询问」也原样返回给调用方，由它决定怎么办。
//
// Enforce = Decide + 审计 + 分派。把它拆出来的原因是子代理：它没有 UI 通道，
// 走 Enforce 的 ask 分支只会干等 5 分钟——而且表现为"子代理偶尔卡住"，间歇且难复现。
// 子代理用 Decide 拿到结论后，把 ask 降级成带理由的拒绝（见根包 subagent.go）。
//
// 刻意**不在这里审计**：调用方拿到结论后自己决定怎么记——
// 子代理要把"被挡下"记成另一种 stage，不能与用户的真实拒绝混在一起。
//
// 返回的 error 表示**这次调用本身有问题**（如命令为空），不是权限结论；
// 有权限结论时 error 为 nil，结论在 Verdict 里。
func (e *Enforcer) Decide(t ToolView, args map[string]interface{}) (Subject, Verdict, error) {
	subject := e.subjectFor(t, args)

	// 命令为空属于**调用错误**（常见于模型把参数写成 command 而不是 cmd），
	// 直接报错让模型改正即可——不该让用户为一条根本不存在的命令等授权弹窗。
	// 注意：只有"空"走这条；"有内容但解析不可信"仍然询问（fail closed），别混为一谈。
	if subject.Kind == SubjectCommand && strings.TrimSpace(subject.Raw) == "" {
		return subject, Verdict{Decision: DecisionDeny, Stage: StageInvalid, Reason: "命令为空（参数缺失）"},
			fmt.Errorf("命令为空：请检查是否把参数名写成了 cmd")
	}

	verdict := Authorize(AuthorizeInput{
		Subject:    subject,
		Mode:       e.Mode,
		Rules:      e.Rules,
		Grants:     e.grants(),
		ProjectDir: e.ProjectDir,
	})
	return subject, verdict, nil
}

// Enforce 判定并执行：allow 放行、deny 报错、需要询问时阻塞等用户答复。
func (e *Enforcer) Enforce(ctx context.Context, t ToolView, args map[string]interface{}) error {
	subject, verdict, callErr := e.Decide(t, args)
	e.RecordVerdict(subject, verdict)
	if callErr != nil {
		return callErr
	}
	switch verdict.Decision {
	case DecisionAllow:
		return nil
	case DecisionDeny:
		return fmt.Errorf("权限拒绝（%s）：%s", verdict.Stage, verdict.Reason)
	default:
		return e.askAndApply(ctx, subject, verdict)
	}
}

// subjectFor 构造判定主体。实现了 SubjectProvider 的工具（命令模板类）自报主体，
// 其余按"工具名 + 参数摘要"处理。
func (e *Enforcer) subjectFor(t ToolView, args map[string]interface{}) Subject {
	if t == nil {
		return Subject{}
	}
	if sp, ok := t.(SubjectProvider); ok {
		return sp.PermissionSubject(args)
	}
	return NewToolSubject(t.GetName(), args)
}

// RecordVerdict 记录一条判定。
// 导出是为了让根包的"子代理降级"路径也能记一条自己的 stage（见 subagent.go）：
// 系统的自动挡下与用户的真实拒绝必须能在审计里区分开。
func (e *Enforcer) RecordVerdict(s Subject, v Verdict) {
	if e == nil || e.Audit == nil {
		return
	}
	e.Audit.Append(AuditEntry{
		SessionID: e.SessionID,
		Tool:      s.Tool,
		Subject:   s.Summary(),
		Decision:  v.Decision.String(),
		Stage:     v.Stage,
		Reason:    v.Reason,
		Rule:      FormatRule(v.Rule),
		Source:    v.Rule.Source,
	})
}

// grants 会话级授权快照（未装配存储时视为没有授权）
func (e *Enforcer) grants() []Grant {
	if e == nil || e.Grants == nil {
		return nil
	}
	return e.Grants.List(e.SessionID)
}

// askAndApply 进入询问回路：发事件 → 等答复 → 按答复放行或拒绝
func (e *Enforcer) askAndApply(ctx context.Context, subject Subject, verdict Verdict) error {
	if e.Broker == nil || e.UI == nil || !e.UI.Available() {
		e.recordDeny(subject, "no-responder", "当前没有可应答的界面")
		return fmt.Errorf("权限未获授权：当前没有可应答的界面（%s）", verdict.Reason)
	}
	id, ch := e.Broker.Register(e.SessionID)
	req := Request{
		ID:        id,
		SessionID: e.SessionID,
		Tool:      subject.Tool,
		Subject:   subject.Summary(),
		Units:     subject.Units,
		Stage:     verdict.Stage,
		Reason:    verdict.Reason,
		CreatedAt: time.Now().UnixMilli(),
	}
	e.Broker.SetRequest(id, req)
	e.UI.Emit(req)

	ans, err := e.Broker.Wait(id, ch, ctx)
	if err != nil {
		// 超时 / 取消 / 无人应答：一律按拒绝处理（fail closed）
		e.recordDeny(subject, "ask-unanswered", err.Error())
		return fmt.Errorf("权限未获授权：%v", err)
	}

	if !AnswerAllows(ans) {
		e.recordDeny(subject, "ask-denied", "用户拒绝了本次操作")
		return fmt.Errorf("用户拒绝了本次操作：%s", subject.Summary())
	}

	// 放行，并按用户选择的记忆范围落账
	switch ans.Scope {
	case "session":
		// 逐段精确记忆：复合命令的每一段各记一条，保证放宽方向仍是"全部覆盖"
		if e.Grants != nil {
			for _, u := range subject.Units {
				if strings.TrimSpace(u) == "" {
					continue
				}
				e.Grants.Add(e.SessionID, Grant{Tool: subject.Tool, Spec: u})
			}
		}
	case "rule":
		if strings.TrimSpace(ans.Rule) == "" {
			return fmt.Errorf("已放行但规则为空，未写入配置文件（请重试并填写规则）")
		}
		layer := ans.Layer
		if layer == "" {
			layer = PermissionScopeLocal
		}
		if err := e.UI.AddAllowRule(e.SessionID, layer, ans.Rule); err != nil {
			return fmt.Errorf("已放行但规则写入失败：%w", err)
		}
	}
	return nil
}

// recordDeny 记一条"未获授权"的审计（超时 / 无界面 / 用户拒绝各有一种 stage）
func (e *Enforcer) recordDeny(s Subject, stage, reason string) {
	e.RecordVerdict(s, Verdict{Decision: DecisionDeny, Stage: stage, Reason: reason})
}
