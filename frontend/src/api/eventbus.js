import { EventsOn } from '@/../wailsjs/runtime/runtime'

/**
 * 事件总线：**进程级只订阅一次**，再按会话把事件路由给对应的消费者。
 *
 * 为什么必须这么做（而不是像原来那样"每次调用 EventsOn / finally EventsOff"）：
 * Wails 的 `EventsOff(name)` 移除的是该事件名下的**全部**监听，不是"我这个回调"。
 * 原来每个 `chat()` 调用各自注册一份 `chat:event`，于是：
 *   - A 会话在跑、B 会话也发一条 → B 的调用结束时把 A 的监听一起摘了；
 *   - 反过来 A 先结束，B 就再也收不到任何事件，工具卡片永远停在"运行中"。
 * 多会话并行放开之后这不是边角情况，而是必然发生。同一个坑在 `user:interaction`
 * 那一轮已经踩过一次并留下了注释，这里把 `chat:event` 也收敛成同一种形态。
 *
 * 归属由后端盖章（ChatEvent.sessionId / runId），前端**不做推断**：
 * 事件没有 sessionId 就丢弃。猜错的代价（把别人的消息写进这个会话）远大于丢一条事件。
 */

// sessionId -> { handlers, closed }
const chatSubs = new Map()

// 每个会话已见过的最大 runId：[毫秒, 序号]。
//
// 为什么需要它：Go 侧 emit 是同步的，但事件过 Wails 桥到 JS 是异步的。
// 上一轮运行的最后一两条事件，可能落在"这一轮已经订阅好"之后才到达，
// 于是被贴到新一轮的运行上（表现为回复末尾突然多出一段上一轮的尾巴）。
// runId 是单调递增的（`run_<毫秒>_<序号>`，序号是进程级自增），所以可以直接比较。
//
// **必须按会话分别记**：runId 的序号是全局自增的，两个会话并行时
// "B 的 run 序号比 A 大"完全正常，用一个全局最大值会把 A 的事件全判成陈旧。
const latestRunBySession = new Map()

let started = false

function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

/** 解析 `run_<毫秒>_<序号>`；解析不出来返回 null */
function parseRunID(id) {
  const m = /^run_(\d+)_(\d+)$/.exec(String(id || ''))
  if (!m) return null
  return [Number(m[1]), Number(m[2])]
}

/** a 是否比 b 旧（无法比较时一律返回 false：宁可多应用一条，也不要静默丢掉真事件） */
function isOlderRun(a, b) {
  if (!a || !b) return false
  return a[0] < b[0] || (a[0] === b[0] && a[1] < b[1])
}

/** 记录并判断一条事件是否陈旧 */
function isStale(sessionId, runId) {
  const parsed = parseRunID(runId)
  if (!parsed) return false
  const seen = latestRunBySession.get(sessionId)
  if (isOlderRun(parsed, seen)) return true
  if (!seen || isOlderRun(seen, parsed)) latestRunBySession.set(sessionId, parsed)
  return false
}

// 只提示一次：版本错配（后端旧、前端新）时每个事件都缺 sessionId，
// 逐条打日志会把控制台刷满、把真正有用的那几行冲掉。
let warnedMissingSession = false

function dispatchChat(ev) {
  if (!ev || !ev.sessionId) {
    if (!warnedMissingSession) {
      warnedMissingSession = true
      console.warn(
        '[事件总线] 收到没有 sessionId 的 chat:event，已丢弃。' +
          '这通常意味着前端产物与 Go 二进制不是同一次构建的（后端没盖章）。',
        ev
      )
    }
    return
  }
  if (isStale(ev.sessionId, ev.runId)) return
  const sub = chatSubs.get(ev.sessionId)
  if (!sub) return // 这个会话没有在等事件（例如后台会话的运行已结束）
  const h = sub.handlers
  switch (ev.type) {
    case 'reply_delta':
      h.onReplyDelta?.(ev.reply, ev)
      break
    case 'tool_call_start':
      h.onToolCallStart?.(ev.toolCall, ev)
      break
    case 'tool_call_end':
      h.onToolCallEnd?.(ev.toolCall, ev)
      break
    case 'plan_update':
      h.onPlanUpdate?.(ev.plan, ev)
      break
    // 自动摘要压缩刚生效：用量会立刻下降。这是设计内行为，但用户只看到数字掉了一半，
    // 不解释一句就会以为对话被截断了。
    case 'context_compacted':
      h.onContextCompacted?.(ev)
      break
    // 本轮 token 用量已产生：明细弹窗开着时据此实时刷新。
    // 与 context_compacted 分开：那个是"上下文剩多少"，这个是"已经花了多少"，
    // 两者跟着同一轮请求产生，但消费方不同（指示器 vs 用量明细弹窗）。
    case 'usage':
      h.onUsage?.(ev.usage, ev)
      break
    // 被用户停止（软取消或硬取消）：立即通知调用方收尾流式气泡，
    // 否则它会一直停在"正在输入"的状态
    case 'cancelled':
      h.onCancelled?.(ev.error, ev)
      break
    // 授权请求 / 模型提问走独立的 user:interaction 通道（由 App.vue 统一订阅），
    // 不在这里分发 —— 否则计划执行等入口会漏（详见 api/interaction.js 的说明）
    default:
      break
  }
}

/** 幂等启动：三条通道都只在这里 EventsOn 一次，之后永不解绑 */
function ensureStarted() {
  if (started || !isWails()) return
  started = true
  EventsOn('chat:event', dispatchChat)
}

/**
 * 订阅某个会话的运行事件。
 *
 * 一个会话同一时刻只会有一条运行（后端 beginExclusive 守着这条线），
 * 所以按 sessionId 路由是无歧义的。返回退订函数，**只退自己这一份**。
 *
 * @param {string} sessionId
 * @param {object} handlers 见 dispatchChat 的 switch：onReplyDelta / onToolCallStart /
 *   onToolCallEnd / onPlanUpdate / onContextCompacted / onUsage / onCancelled
 * @returns {() => void}
 */
export function subscribeChat(sessionId, handlers = {}) {
  ensureStarted()
  if (!sessionId) return () => {}
  chatSubs.set(sessionId, { handlers })
  return () => {
    // 只清掉自己那一份：万一退订前已被新的订阅顶替（同会话紧接着又发了一条），
    // 不要误删新的那份
    const cur = chatSubs.get(sessionId)
    if (cur && cur.handlers === handlers) chatSubs.delete(sessionId)
  }
}

/**
 * 判断某条 chat:event 是否属于某个会话（供不按会话订阅的通道做过滤，例如 diff）。
 * 没有 sessionId 的事件返回 false —— 同 dispatchChat 的理由：不做推断。
 */
export function belongsTo(ev, sessionId) {
  return !!(ev && ev.sessionId && ev.sessionId === sessionId)
}

/** 测试与排障用：当前有哪些会话挂着聊天订阅 */
export function activeChatSubscriptions() {
  return [...chatSubs.keys()]
}

/** 测试用：清空内部状态（进程级常驻，生产代码不该调用） */
export function __resetEventBusForTest() {
  chatSubs.clear()
  latestRunBySession.clear()
  warnedMissingSession = false
  started = false
}

// 供测试直接调用内部判定（不经过 Wails）
export const __internals = { parseRunID, isOlderRun, isStale, dispatchChat }
