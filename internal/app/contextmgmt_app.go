package app

import (
	"context"
	"fmt"

	"wails-tmp/internal/agent"
	"wails-tmp/internal/store"
)

// ===== 上下文管理的 App 侧 =====
//
// 引擎（估算、切点、摘要素材与编排、用量统计与报告）已下沉到 internal/agent：
// 见 internal/agent/contextmgmt.go。这里只留必须碰 App 的三件事：
// 从存储里取会话与模型、读用户配置的保留条数、读观测到的校准系数。
//
// 构建请求的消息序列（buildRunMessages）留在 chat.go —— 它要与 buildLLMMessages
// 共用同一套协议组装（附件/图片），两者是一体的。

// compactSessionContext 按会话 ID 做一次摘要式压缩。
// ctx 用运行 ctx：压缩要额外发一次 LLM 请求，硬取消同样要能断掉它。
func (a *App) compactSessionContext(ctx context.Context, sessionID string) (*agent.CompactOutcome, error) {
	session, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("加载会话失败: %w", err)
	}
	model, err := a.modelStore.GetModelForCall(session.Model)
	if err != nil {
		return nil, fmt.Errorf("获取模型配置失败: %w", err)
	}
	return a.compactSession(ctx, session, &model)
}

// compactSession 对已加载的会话做一次压缩，把三样只有 App 知道的东西注入引擎。
func (a *App) compactSession(ctx context.Context, session *store.Session, model *store.Model) (*agent.CompactOutcome, error) {
	return agent.CompactSession(ctx, session, model, agent.CompactDeps{
		KeepRecent:  a.keepRecentMsgs(),
		Calib:       a.calibFor(session.Model),
		SaveSummary: a.sessionStore.SetContextSummary,
	})
}

// keepRecentMsgs 压缩时要保留的最近消息条数（读可配置项）。
//
// 存储未初始化时（测试里直接构造 App，或 startup 尚未跑到）回落到内置默认值——
// 压缩策略不能因为一个 nil 指针就变成"留 0 条"，那会把整段对话压没。
func (a *App) keepRecentMsgs() int {
	if a == nil || a.contextPrefs == nil {
		return store.KeepRecentMsgsDefault
	}
	return a.contextPrefs.Get().KeepRecentMsgs
}
