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
