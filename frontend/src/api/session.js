import {
  CreateSession,
  ListSessions,
  GetSession,
  DeleteSession,
  AppendMessage,
  AppendConversation,
  UpdateSession,
  Chat,
} from '@/../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '@/../wailsjs/runtime/runtime'

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
 * @param {{title?:string, project?:string, model?:string, permissionMode?:string, environment?:string}} config
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
    environment: config.environment || 'local',
    status: 'active',
    startAt: now,
    endAt: now,
    messages: [],
    conversations: [],
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
 * @param {{title?:string, model?:string, permissionMode?:string, status?:string, project?:string}} patch
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
 * 在 Wails 环境下，通过事件接收工具调用中间状态，最终返回完整回复
 * 后端会持久化所有中间消息（assistant+tool_calls, tool结果, 最终回复）
 * @param {string} sessionId
 * @param {string} query
 * @param {{onToolCallStart?:Function, onToolCallEnd?:Function}} callbacks
 * @returns {Promise<{reply:string, toolCalls?:array, messages?:array, error?:string}>}
 */
export async function chat(sessionId, query, { onToolCallStart, onToolCallEnd } = {}) {
  if (isWails()) {
    // 监听工具调用中间状态事件
    const eventHandler = (eventData) => {
      if (!eventData) return
      switch (eventData.type) {
        case 'tool_call_start':
          onToolCallStart?.(eventData.toolCall)
          break
        case 'tool_call_end':
          onToolCallEnd?.(eventData.toolCall)
          break
      }
    }
    EventsOn('chat:event', eventHandler)

    try {
      const result = await Chat(sessionId, query)
      return result
    } finally {
      EventsOff('chat:event')
    }
  }

  // ===== 浏览器开发模式 mock =====
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
