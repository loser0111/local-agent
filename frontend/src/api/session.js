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
  GetContextPrefs,
  SetContextKeepRecentMsgs,
  GetLastLLMRequest,
  ListCheckpoints,
  UndoDiffTurn,
  GetDiff,
  GetDiffTurns,
  ListSubagents,
  GetSubagentMessages,
  ListRunningSessions,
  SaveAttachment,
  GetAttachmentDataURL,
  FetchImageURL,
} from '@/../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '@/../wailsjs/runtime/runtime'
import { subscribeChat } from '@/api/eventbus'
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
    // 附件引用要跟着消息一起落库：少了它，界面上那张图会在刷新后消失
    attachments: message.attachments?.length ? message.attachments : undefined,
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
    onContextCompacted,
  } = {}
) {
  if (isWails()) {
    // 按会话订阅运行事件（工具调用中间状态、流式分片、计划状态、压缩提示、取消）。
    //
    // 这里**不再**自己 EventsOn / finally EventsOff：Wails 的 EventsOff 会摘掉该事件名下的
    // 全部监听，A 会话跑完会把 B 会话那次的监听一起摘掉（多会话并行时必然发生）。
    // 现在统一由 api/eventbus.js 做进程级单订阅 + 按 sessionId 路由，详见那里的说明。
    const off = subscribeChat(sessionId, {
      onReplyDelta,
      onToolCallStart,
      onToolCallEnd,
      onPlanUpdate,
      onContextCompacted,
      onCancelled,
    })

    try {
      const result = await Chat(sessionId, query, !!stream, !!plan)
      return result
    } finally {
      off()
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

// ===== 附件（图片）=====

// 浏览器开发模式的附件表：`${sessionId}:${id}` → data URL。
// 真实环境里图是落盘的（~/.local-agent/attachments/<会话ID>/），这里只放内存够用。
const mockAttachments = new Map()

/**
 * 保存一张图片附件，返回可写进消息的附件引用。
 *
 * 流程是"先存附件、再带引用落消息"：消息落库时引用已经完整，
 * 因此不会出现"消息里挂着一张读不到的图"这种中间态。
 *
 * 后端会做入站规整（校验格式与大小、按最长边 1568px 等比缩小、必要时重编码），
 * 所以返回的 bytes/width/height 可能与你传进来的不一致——界面应以后端返回的为准。
 *
 * @param {string} sessionId
 * @param {string} name 原始文件名（只用于展示）
 * @param {string} payload base64 字符串或 data URL
 * @param {string} [source] user（用户贴的）/ tool（模型读的）
 * @returns {Promise<import('@/types').Attachment>}
 */
export async function saveAttachment(sessionId, name, payload, source = 'user') {
  if (isWails()) {
    return await SaveAttachment(sessionId, name, payload, source)
  }
  const mediaType = /^data:([^;,]+)/.exec(payload)?.[1] || 'image/png'
  const base64 = payload.includes(',') ? payload.slice(payload.indexOf(',') + 1) : payload
  const id = `mock${Math.random().toString(16).slice(2, 10)}`
  const dataUrl = payload.startsWith('data:') ? payload : `data:${mediaType};base64,${base64}`
  mockAttachments.set(`${sessionId}:${id}`, dataUrl)
  return {
    id,
    kind: 'image',
    name,
    mediaType,
    bytes: Math.round((base64.length * 3) / 4),
    source,
    changed: false,
    createdAt: Date.now(),
  }
}

/**
 * 取附件的数据 URL（历史消息里的图片展示用）。
 *
 * 取不到时返回空串而不是抛错：附件目录被清理过是真实存在的情况，
 * 界面据此显示"图片已丢失"，比一个破图占位诚实得多。
 *
 * @param {string} sessionId
 * @param {string} id 附件 ID（只能给 ID，后端不接受路径——那等于把读任意文件的能力开给前端）
 * @returns {Promise<string>} 空串表示取不到
 */
export async function getAttachmentDataURL(sessionId, id) {
  if (!sessionId || !id) return ''
  if (!isWails()) {
    return mockAttachments.get(`${sessionId}:${id}`) || ''
  }
  try {
    return (await GetAttachmentDataURL(sessionId, id)) || ''
  } catch {
    return ''
  }
}

/**
 * 从图片链接抓取一张图，按与"用户贴图"完全相同的方式存成会话附件。
 *
 * 只应由**用户明确点按**触发（界面上的「链接」入口）：这会从本机发起一次出站请求。
 * 后端限定 http/https、重定向最多 3 跳且逐跳校验、整体超时、响应体限长，
 * 并要求内容真能嗅探成受支持的图片。模型无法触发它——它不是工具。
 *
 * @param {string} sessionId
 * @param {string} url
 * @returns {Promise<import('@/types').Attachment>}
 */
export async function fetchImageURL(sessionId, url) {
  if (isWails()) {
    return await FetchImageURL(sessionId, url)
  }
  throw new Error('浏览器开发模式不支持从链接抓取图片')
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
 * @param {Function} cb 回调，参数为 { diff, turn, sessionId, runId }
 *   —— sessionId 是归属：调用方必须把它写进对应会话的桶里，而不是"当前显示的那一份"。
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
 * 列出当前有活跃运行的会话 ID（可能同时有多条）。
 *
 * 页面重载（wails dev 热更新 / 手动刷新）后，前端内存里的"哪条在跑"全丢了，
 * 而后端的运行还在跑。用它把状态补回来——否则界面会把它们显示成空闲，
 * 用户再点发送只会收到后端的互斥拒绝，看不出为什么。
 *
 * 只能补"在跑"这个事实，补不回已经流出去的那段文字（那只能靠事件流）。
 *
 * @returns {Promise<string[]>}
 */
export async function listRunningSessions() {
  if (!isWails()) return []
  try {
    return (await ListRunningSessions()) || []
  } catch (e) {
    console.warn('查询运行中的会话失败:', e)
    return []
  }
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
/**
 * 查询上下文压缩偏好（压缩时保留最近多少条原文）。
 *
 * 它是**后端**配置（~/.local-agent/context.json），不是前端 localStorage：
 * 压缩发生在后端，前端再存一份只会两边不一致。
 *
 * @returns {Promise<{keepRecentMsgs:number}|null>} 非 Wails 环境返回 null
 */
export async function getContextPrefs() {
  if (!isWails()) return null
  try {
    return await GetContextPrefs()
  } catch {
    return null
  }
}

/**
 * 更新"压缩时保留最近多少条原文"。
 * 合法区间由后端校验（4–500），越界会抛错而不是被静默改掉。
 *
 * @param {number} keepRecentMsgs
 * @returns {Promise<{keepRecentMsgs:number}>} 更新后的完整偏好
 */
export async function setContextKeepRecentMsgs(keepRecentMsgs) {
  if (!isWails()) return { keepRecentMsgs }
  return await SetContextKeepRecentMsgs(keepRecentMsgs)
}

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

/**
 * 查询会话各轮的回退可用性与冲突情况。
 * @param {string} sessionId
 * @returns {Promise<Array<import('@/types').CheckpointInfo>>} 不可回退的轮次也在列表里（带 reason）
 */
export async function listCheckpoints(sessionId) {
  if (!isWails()) return []
  try {
    return (await ListCheckpoints(sessionId)) || []
  } catch {
    return []
  }
}

/**
 * 回退某一轮：把该轮改过的文件恢复到轮次开始时的状态。
 *
 * 有冲突（文件在该轮之后又被改过）且 force=false 时后端会拒绝并抛出带冲突清单的错误，
 * 由调用方确认后带 force 重试。
 *
 * @param {string} sessionId
 * @param {number} turn
 * @param {boolean} [force]
 * @returns {Promise<import('@/types').UndoResult>}
 */
export async function undoDiffTurn(sessionId, turn, force = false) {
  if (!isWails()) throw new Error('mock 模式不支持回退')
  return await UndoDiffTurn(sessionId, turn, !!force)
}

// ===== 子代理（P3）=====

/**
 * 列出某主会话派生过的子代理：正在跑的 + 历史上跑完的。
 *
 * 注意入参是**主会话 ID**，返回项里的 runId 才是子代理自己的会话 ID
 * （看它的消息、回退它改的文件都要用 runId）。
 *
 * @param {string} sessionId 主会话 ID
 * @returns {Promise<Array<import('@/types').SubagentInfo>>}
 */
export async function listSubagents(sessionId) {
  if (!isWails()) return []
  try {
    return (await ListSubagents(sessionId)) || []
  } catch {
    return [] // 只是展示用，取不到就不显示，不打扰用户
  }
}

/**
 * 取某个子代理的完整消息流（点开某一条时才拉）。
 * @param {string} runId 子代理会话 ID
 * @returns {Promise<Array>} Message[]
 */
export async function getSubagentMessages(runId) {
  if (!isWails()) return []
  return (await GetSubagentMessages(runId)) || []
}

/**
 * 订阅子代理进度事件（工具调用开始/结束、跑完）。
 *
 * 与 chat:event 分开走一条通道：子代理的工具调用**不该**混进主会话的聊天流，
 * 否则看起来像主会话自己在跑那些工具。
 *
 * @param {Function} cb 回调，参数为 SubagentInfo
 * @returns {Function} 取消订阅函数
 */
export function onSubagentEvent(cb) {
  if (!isWails()) return () => {}
  EventsOn('subagent:event', cb)
  return () => EventsOff('subagent:event')
}
