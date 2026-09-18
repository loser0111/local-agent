import {
  CreateSession,
  ListSessions,
  GetSession,
  DeleteSession,
  AppendMessage,
  AppendConversation,
  UpdateSession,
  Chat,
  StopChat,
  GetContextStat,
  GetLastLLMRequest,
  GetDiff,
  GetDiffTurns,
} from '@/../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '@/../wailsjs/runtime/runtime'
import { putMockPlan } from '@/api/plan'
import { DEFAULT_VIEW_MODE } from '@/types'

/**
 * 判断是否运行在 Wails 桌面环境
 */
function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

// ===== 浏览器开发模式 mock（localStorage 模拟后端会话文件存储） =====
const MOCK_KEY = 'local-agent:sessions'

function readMockSessions() {
  try {
    const raw = localStorage.getItem(MOCK_KEY)
    return raw ? JSON.parse(raw) : {}
  } catch {
    return {}
  }
}

function writeMockSessions(map) {
  localStorage.setItem(MOCK_KEY, JSON.stringify(map))
}

function genSessionId() {
  const d = new Date()
  const pad = (n) => String(n).padStart(2, '0')
  const ts =
    `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}` +
    `_${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`
  const rand = Math.random().toString(16).slice(2, 10).padStart(8, '0')
  return `${ts}_${rand}`
}

/**
 * 创建会话
 * @param {{title?:string, project?:string, model?:string, permissionMode?:string, viewMode?:string, environment?:string, enabledTools?:string[], enabledSkills?:string[]}} config
 * @returns {Promise<object>} 创建的完整会话
 */
export async function createSession(config = {}) {
  if (isWails()) {
    return await CreateSession(config)
  }
  const map = readMockSessions()
  const now = Date.now()
  const session = {
    id: genSessionId(),
    title: config.title || '新会话',
    project: config.project || '',
    model: config.model || '',
    permissionMode: config.permissionMode || 'manual',
    viewMode: config.viewMode || DEFAULT_VIEW_MODE,
    environment: config.environment || 'local',
    status: 'active',
    startAt: now,
    endAt: now,
    messages: [],
    conversations: [],
    enabledTools: config.enabledTools || [],
    enabledSkills: config.enabledSkills || [],
  }
  map[session.id] = session
  writeMockSessions(map)
  return session
}

/**
 * 获取会话列表（元数据，按最近活跃倒序）
 */
export async function listSessions() {
  if (isWails()) {
    return await ListSessions()
  }
  const map = readMockSessions()
  return Object.values(map)
    .map((s) => ({ ...s, messages: [], conversations: [] }))
    .sort((a, b) => b.endAt - a.endAt)
}

/**
 * 获取完整会话（含消息）
 */
export async function getSession(id) {
  if (isWails()) {
    return await GetSession(id)
  }
  const map = readMockSessions()
  if (!map[id]) throw new Error(`会话 "${id}" 不存在`)
  return JSON.parse(JSON.stringify(map[id]))
}

/**
 * 删除会话
 */
export async function deleteSession(id) {
  if (isWails()) {
    return await DeleteSession(id)
  }
  const map = readMockSessions()
  if (!map[id]) throw new Error(`会话 "${id}" 不存在`)
  delete map[id]
  writeMockSessions(map)
}

/**
 * 追加消息并持久化
 * @param {string} sessionId
 * @param {{role:string, content:string, toolCalls?:array}} message
 * @returns {Promise<object>} 服务端补全后的消息（含 id/createdAt）
 */
export async function appendMessage(sessionId, message) {
  if (isWails()) {
    return await AppendMessage(sessionId, message)
  }
  const map = readMockSessions()
  const session = map[sessionId]
  if (!session) throw new Error(`会话 "${sessionId}" 不存在`)

  const now = Date.now()
  const full = {
    id: `msg-${now}-${Math.random().toString(16).slice(2, 8)}`,
    role: message.role,
    content: message.content || '',
    toolCalls: message.toolCalls || undefined,
    createdAt: now,
  }
  session.messages.push(full)
  session.endAt = now
  session.status = 'active'
  // 首条用户消息作为标题
  if (message.role === 'user' && (!session.title || session.title === '新会话')) {
    const title = (message.content || '').trim().replace(/\n/g, ' ')
    session.title = title.length > 30 ? title.slice(0, 30) + '...' : title
  }
  writeMockSessions(map)
  return full
}

/**
 * 追加一轮对话记录
 */
export async function appendConversation(sessionId, conversation) {
  if (isWails()) {
    return await AppendConversation(sessionId, conversation)
  }
  const map = readMockSessions()
  const session = map[sessionId]
  if (!session) throw new Error(`会话 "${sessionId}" 不存在`)
  conversation.index = session.conversations.length + 1
  session.conversations.push(conversation)
  if (conversation.endTime > session.endAt) session.endAt = conversation.endTime
  writeMockSessions(map)
}

/**
 * 更新会话元数据
 * @param {string} id
 * @param {{title?:string, model?:string, permissionMode?:string, viewMode?:string, status?:string, project?:string}} patch
 */
export async function updateSession(id, patch) {
  if (isWails()) {
    return await UpdateSession(id, patch)
  }
  const map = readMockSessions()
  const session = map[id]
  if (!session) throw new Error(`会话 "${id}" 不存在`)
  Object.keys(patch).forEach((k) => {
    if (patch[k] !== undefined && patch[k] !== null) session[k] = patch[k]
  })
  session.endAt = Date.now()
  writeMockSessions(map)
  return JSON.parse(JSON.stringify(session))
}

/**
 * 发送消息给 AI 并获取回复（真正的 LLM 调用）
 * 在 Wails 环境下，通过事件接收工具调用中间状态与流式文本分片，最终返回完整回复
 * 后端会持久化所有中间消息（assistant+tool_calls, tool结果, 最终回复）
 * @param {string} sessionId
 * @param {string} query
 * @param {{stream?:boolean, plan?:boolean, onReplyDelta?:Function, onToolCallStart?:Function, onToolCallEnd?:Function, onPlanUpdate?:Function, }} callbacks
 * @returns {Promise<{reply:string, toolCalls?:array, messages?:array, plan?:object, error?:string}>}
 */
export async function chat(
  sessionId,
  query,
  {
    stream = true,
    plan = false,
    onReplyDelta,
    onToolCallStart,
    onToolCallEnd,
    onPlanUpdate,
    onCancelled,
  } = {}
) {
  if (isWails()) {
    // 监听工具调用中间状态、流式分片、计划状态与授权请求事件
    const eventHandler = (eventData) => {
      if (!eventData) return
      switch (eventData.type) {
        case 'reply_delta':
          onReplyDelta?.(eventData.reply)
          break
        case 'tool_call_start':
          onToolCallStart?.(eventData.toolCall)
          break
        case 'tool_call_end':
          onToolCallEnd?.(eventData.toolCall)
          break
        case 'plan_update':
          onPlanUpdate?.(eventData.plan)
          break
        // 被用户停止（软取消或硬取消）：立即通知调用方收尾流式气泡，
        // 否则它会一直停在"正在输入"的状态
        case 'cancelled':
          onCancelled?.(eventData.error)
          break
        // 授权请求 / 模型提问走独立的 user:interaction 通道（由 App.vue 统一订阅），
        // 不在这里分发 —— 否则计划执行等入口会漏（详见 api/interaction.js 的说明）
      }
    }
    EventsOn('chat:event', eventHandler)

    try {
      const result = await Chat(sessionId, query, !!stream, !!plan)
      return result
    } finally {
      EventsOff('chat:event')
    }
  }

  // ===== 浏览器开发模式 mock =====

  // 计划模式：返回模拟计划（3 步骤，待审核状态）
  if (plan) {
    await new Promise((r) => setTimeout(r, 800))
    const now = Date.now()
    const mockPlan = {
      id: `plan_${now}_m0ck`,
      sessionId,
      title: `「${(query || '').slice(0, 12)}…」执行计划`,
      status: 'awaiting_approval',
      steps: [
        { index: 0, title: '梳理现有结构与依赖', detail: '阅读相关文件并总结现状', status: 'pending' },
        { index: 1, title: '实施主要变更', detail: '', status: 'pending' },
        { index: 2, title: '验证并汇总结果', detail: '', status: 'pending' },
      ],
      createdAt: now,
      updatedAt: now,
    }
    putMockPlan(mockPlan) // 落库，供 savePlan/executePlan mock 读写
    onPlanUpdate?.(mockPlan)
    return { plan: mockPlan }
  }

  // 模拟工具调用 + AI 回复（返回 messages 模拟后端持久化的消息）
  const tcId = `tc-${Date.now()}`
  const toolCall = {
    id: tcId,
    name: 'exec_shell',
    args: { cmd: 'ls' },
    status: 'success',
    duration: 0.5,
    result: 'app.go\nmain.go\nfrontend/\ntools.go\nchat.go',
  }

  if (onToolCallStart) {
    onToolCallStart({ ...toolCall, status: 'running' })
  }
  await new Promise((r) => setTimeout(r, 500))
  if (onToolCallEnd) {
    onToolCallEnd(toolCall)
  }

  const reply = `这是浏览器开发模式的模拟回复。\n\n你说的是："${query}"\n\n在 Wails 桌面环境中，这里会返回真正的 LLM 回复。`

  // 模拟 SSE：把回复切成小分片逐段回调
  if (stream && onReplyDelta) {
    const chunks = reply.match(/[\s\S]{1,6}/g) || [reply]
    for (const c of chunks) {
      onReplyDelta(c)
      await new Promise((r) => setTimeout(r, 30))
    }
  }

  const now = Date.now()

  return {
    reply,
    toolCalls: [toolCall],
    messages: [
      // assistant 消息（含 tool_calls 记录）
      {
        id: `msg-${now}-1`,
        role: 'assistant',
        content: '',
        toolCalls: [toolCall],
        createdAt: now,
      },
      // tool 结果消息（role=tool，前端会过滤掉）
      {
        id: `msg-${now}-2`,
        role: 'tool',
        content: toolCall.result,
        toolCallId: tcId,
        createdAt: now,
      },
      // 最终 assistant 回复
      {
        id: `msg-${now}-3`,
        role: 'assistant',
        content: reply,
        createdAt: now + 1,
      },
    ],
  }
}

// ===== Diff 差异视图 =====

// 浏览器开发模式 mock（非 Wails 环境）
const MOCK_DIFF = [
  {
    path: 'app.go',
    status: 'modified',
    additions: 2,
    deletions: 1,
    hunks: [
      {
        header: '@@ -1,4 +1,5 @@',
        lines: [
          { type: 'context', oldLineNo: 1, newLineNo: 1, content: 'package main' },
          { type: 'del', oldLineNo: 2, newLineNo: 0, content: 'import "fmt"' },
          { type: 'add', oldLineNo: 0, newLineNo: 2, content: 'import (' },
          { type: 'add', oldLineNo: 0, newLineNo: 3, content: '\t"os"' },
          { type: 'context', oldLineNo: 3, newLineNo: 4, content: ')' },
        ],
      },
    ],
  },
  {
    path: 'frontend/src/main.js',
    status: 'added',
    additions: 3,
    deletions: 0,
    hunks: [
      {
        header: '@@ -0,0 +1,3 @@',
        lines: [
          { type: 'add', oldLineNo: 0, newLineNo: 1, content: "import { createApp } from 'vue'" },
          { type: 'add', oldLineNo: 0, newLineNo: 2, content: "import App from './App.vue'" },
          { type: 'add', oldLineNo: 0, newLineNo: 3, content: 'createApp(App).mount("#app")' },
        ],
      },
    ],
  },
]

/**
 * 获取会话工作区相对基线的累计差异
 * @param {string} sessionId
 * @returns {Promise<Array>} DiffFile[]
 */
export async function getDiff(sessionId) {
  if (isWails()) {
    return await GetDiff(sessionId)
  }
  return MOCK_DIFF
}

/**
 * 获取按轮次分组的差异（索引 0 为“累计”）
 * @param {string} sessionId
 * @returns {Promise<Array>} DiffTurn[]
 */
export async function getDiffTurns(sessionId) {
  if (isWails()) {
    return await GetDiffTurns(sessionId)
  }
  return [
    {
      turn: 0,
      label: '累计',
      files: MOCK_DIFF,
      additions: 5,
      deletions: 1,
      createdAt: Date.now(),
    },
  ]
}

/**
 * 订阅 diff 实时更新事件
 * @param {Function} cb 回调，参数为 { diff, turn }
 * @returns {Function} 取消订阅函数
 */
export function onDiffUpdate(cb) {
  if (!isWails()) return () => {}
  EventsOn('diff:update', cb)
  return () => EventsOff('diff:update')
}

/**
 * 停止某会话当前正在运行的生成（两级）。
 *
 * @param {string} sessionId
 * @param {boolean} hard false=协作式（当前 LLM 请求与工具跑完，下一轮不再开始）；
 *   true=硬取消（在途请求立即断开、正在跑的子进程被 kill、等待中的提问立即结束）
 * @returns {Promise<boolean>} 是否命中了一个在跑的运行。
 *   返回 false 时调用方**必须**复位按钮并结束本地"生成中"状态——
 *   否则会出现"点了停止却没反应"的假象（这正是改造前的表现）。
 */
export async function stopChat(sessionId, hard = false) {
  if (isWails()) return await StopChat(sessionId, !!hard)
  return false // 浏览器 mock 模式没有真实运行可停
}

/**
 * 查询某会话的上下文用量。
 *
 * 后端给的是**近似值**（不含系统提示与工具定义的精确开销），只用于界面显示比例，
 * 不参与任何判定——真正的阈值判断在后端用运行期的真实序列算。
 *
 * @param {string} sessionId
 * @returns {Promise<import('@/types').ContextStat|null>} 会话不存在时返回 null
 */
export async function getContextStat(sessionId) {
  if (!isWails()) return null
  try {
    return await GetContextStat(sessionId)
  } catch {
    return null // 只是显示用，取不到就不显示，不打扰用户
  }
}

/**
 * 查询本会话**最近一次真实发出**的请求快照（压缩与工具结果预算之后的那一份）。
 *
 * 与会话里存的消息不同：会话存的是原文，这里记的是实际交给模型的序列。
 * 快照只在内存里，应用重启后需要再发一条消息才会有。
 *
 * @param {string} sessionId
 * @returns {Promise<import('@/types').LLMRequestSnapshot|null>} 无记录时返回 null
 */
export async function getLastLLMRequest(sessionId) {
  if (!isWails()) return null
  try {
    return await GetLastLLMRequest(sessionId)
  } catch {
    return null
  }
}
