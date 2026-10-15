package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// ===== 权限网关：接口与装饰器 =====

// Enforcer 工具执行前的权限网关。判定需要用户确认时，实现内部负责把请求送到前端
// 并等待答复（可能阻塞数分钟），返回 error 表示拒绝执行（错误信息会回填给 LLM）。
type Enforcer interface {
	Enforce(ctx context.Context, tool ToolInterface, args map[string]interface{}) error
}

// AllowAllEnforcer 不做任何判定的网关。
// **仅供测试与非交互场景显式使用**：装配时绝不默认使用它，避免"忘了装配"变成静默放行。
type AllowAllEnforcer struct{}

// Enforce 永远放行
func (AllowAllEnforcer) Enforce(context.Context, ToolInterface, map[string]interface{}) error {
	return nil
}

// DenyAllEnforcer 拒绝一切的网关，作为装配缺失时的兜底（fail closed）
type DenyAllEnforcer struct{}

// Enforce 永远拒绝
func (DenyAllEnforcer) Enforce(context.Context, ToolInterface, map[string]interface{}) error {
	return fmt.Errorf("权限网关未装配，已拒绝执行（fail closed）")
}

// PreDecider 网关可选实现的「只判定、不等待」入口，用于**执行前预检**。
//
// 目前唯一的用途：一轮里有多个 tool_call 时，判断"这次调用会不会弹授权窗"。
// 需要弹窗的调用必须退回串行——前端一个会话只有**一个**挂起授权槽位
// （见 frontend/src/stores/permissions.js 的 pendingBySession），并发弹两个会互相覆盖，
// 前一个白等到超时。网关不实现本接口时视为"不会弹窗"（如子代理的自动拒绝网关、
// 测试用的放行网关），并发因此不被无谓地挡住。
type PreDecider interface {
	// WouldAsk 这次调用是否需要向用户询问。实现方只判定，不得产生审计之外的副作用。
	WouldAsk(t ToolInterface, args map[string]interface{}) bool
}

// GuardedTool 把工具包一层权限网关。
// 装饰发生在 registry 装配期，因此 tool_router 的分发路径与直调路径都会经过同一个网关，
// 不存在"某条路径忘了判定"的可能。
type GuardedTool struct {
	inner ToolInterface
	enf   Enforcer
}

// NewGuardedTool 用权限网关装饰一个工具
func NewGuardedTool(inner ToolInterface, enf Enforcer) ToolInterface {
	return &GuardedTool{inner: inner, enf: enf}
}

// Unwrap 若工具被权限网关装饰过，返回其内层工具；否则原样返回。
// 供装配断言与诊断使用（例如"装配结果确实被网关装饰"这类检查）。
func Unwrap(t ToolInterface) ToolInterface {
	if g, ok := t.(*GuardedTool); ok {
		return g.inner
	}
	return t
}

// Execute 先判定再执行
func (g *GuardedTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	if g.enf != nil {
		if err := g.enf.Enforce(ctx, g.inner, args); err != nil {
			return "", err
		}
	}
	return g.inner.Execute(ctx, args)
}

// GetName 透传工具名（tool_router 的 list/describe 依赖它）
func (g *GuardedTool) GetName() string { return g.inner.GetName() }

// GetDescription 透传描述
func (g *GuardedTool) GetDescription() string { return g.inner.GetDescription() }

// GetParameters 透传参数定义
func (g *GuardedTool) GetParameters() map[string]*ToolArgDef { return g.inner.GetParameters() }

// RequiredParams 透传必填参数名。
//
// ⚠️ 这不是可有可无的转发。装配期会给 registry 里每个工具包一层本装饰器，
// 而 schemaForTool 是对**装饰后**的对象做 `t.(RequiredParams)` 类型断言的——
// 少这一层转发，接口断言就会失败，required 退化成空，直出给模型的 schema 里
// 不再有任何 "required" 字段，模型只能从散文描述里猜哪个参数必填，
// 猜错就吃一个参数校验错误、白烧一轮。
func (g *GuardedTool) RequiredParams() []string {
	if rp, ok := g.inner.(RequiredParams); ok {
		return rp.RequiredParams()
	}
	return nil
}

// JSONSchema 透传工具自报的完整 schema。
//
// 同 RequiredParams：不转发的话 SchemaProvider 断言同样失败，MCP 工具从服务器
// 原样带回来的 inputSchema（enum / items / 嵌套 / required）会被压平成
// type + description，模型因此凑不出合法的枚举值。
// 返回 nil 表示"本工具没有自报 schema"，schemaForTool 会照旧走合成路径。
func (g *GuardedTool) JSONSchema() json.RawMessage {
	if sp, ok := g.inner.(SchemaProvider); ok {
		return sp.JSONSchema()
	}
	return nil
}

// WouldAsk 透传内层网关的预检能力（见 PreDecider）。
//
// 与 RequiredParams / JSONSchema 同一个理由必须转发：工具循环拿到的是**装饰后**的
// GuardedTool，不做这层转发，`t.(PreDecider)` 断言就会失败，预检退化成"从不询问"，
// 于是需要授权的调用会被错误地放进并行组——正是要避免的那种并发弹窗。
// 内层网关不实现 PreDecider 时返回 false（不弹窗）。
func (g *GuardedTool) WouldAsk(args map[string]interface{}) bool {
	if g.enf == nil {
		return false
	}
	if pd, ok := g.enf.(PreDecider); ok {
		return pd.WouldAsk(g.inner, args)
	}
	return false
}

// parallelSafeTools 可以与其他调用**并发执行**的内置只读工具。
//
// 判据（两条都要满足）：
//  1. 不写工作区——因此不需要工具循环那套"调用前后各扫一次工作区"的归因窗口
//     （见 diff.DiffService.NoteActivity），也不会与并发写操作互相踩；
//  2. 不产生需要串行化的副作用——**read_image 刻意不在其中**：它是只读的，
//     但它产出的图要经共用的 ImageCollector 取走，"执行一次、取一次"是串行假设
//     （见 media.ImageCollector 的说明）。把它留在串行组，代价可忽略（通常单次调用），
//     换来的是图片归因永远准确。
//
// 需要授权的调用即使工具本身只读（如用户配了 deny/ask 规则）也会退回串行——
// 由调用方经 PreDecider 判定，见 runToolLoop。
var parallelSafeTools = map[string]bool{
	ToolReadFile:  true,
	ToolGlob:      true,
	ToolGrep:      true,
	ToolListDir:   true,
	ToolWebSearch: true,
}

// ParallelSafe 该工具是否可以并发执行（工具维度，不含权限判定）。
func (g *GuardedTool) ParallelSafe() bool {
	return parallelSafeTools[g.inner.GetName()]
}

// ParallelSafeTool 由工具视图对外回答"这次调用能否并发"：
// 工具本身要属于并行安全集合，且本次调用**不需要**询问用户。
//
// 第二个条件必须在这里一并回答，而不是让调用方分别问：它依赖装饰器内层的网关，
// 调用方（工具循环）手上只有 SessionView，拿不到网关。
func (v *SessionView) ParallelSafeTool(name string, args map[string]interface{}) bool {
	if v == nil {
		return false
	}
	t, ok := v.nonMeta[name]
	if !ok {
		return false // 未找到 / tool_router：一律串行
	}
	g, ok := t.(*GuardedTool)
	if !ok || !g.ParallelSafe() {
		return false
	}
	return !g.WouldAsk(args)
}
