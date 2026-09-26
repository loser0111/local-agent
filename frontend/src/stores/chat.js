import { defineStore } from 'pinia'
import { ref, shallowRef, computed } from 'vue'
import { getSession, getAttachmentDataURL } from '@/api/session'
import { useSessionStore } from '@/stores/session'

/**
 * 对话消息 Store —— 每个会话各占一个桶，对外仍以"当前会话"的形状暴露
 *
 * 为什么要分桶：一次运行（`Chat` 调用）从发起到结束可能跨很久，期间用户完全可以切到别的会话，
 * 而事件是后端主动推来的。改造前只有一份 `messages` / `isGenerating`，写法上
 * "写当前会话"和"写这条运行所属的会话"看起来一样，于是：
 *   - A 跑完的回复被追加进 B 的消息列表（A 的结果写到了"当前会话"）；
 *   - B 的发送按钮因为 A 在跑而禁用；
 *   - 切到 B 点"停止"，停的是 B（后端未命中），界面却复位了"生成中"。
 *
 * 所以这里的规矩是：**所有写操作都必须显式传 sessionId**，不提供隐式的"写当前会话"。
 * 对外的 `messages` / `isGenerating` 等只是当前会话那一份的 computed，组件与模板不用改。
 */
export const useChatStore = defineStore('chat', () => {
  const sessionStore = useSessionStore()

  const currentId = computed(() => sessionStore.currentSessionId)

  // sessionId -> Message[]
  const messagesBySession = ref({})
  // sessionId -> 是否已从后端载入过。切回已载入的会话直接用内存里那份——
  // 后台会话正在流式的占位消息就在里面，重新拉会把它冲掉（那段文字会凭空消失）。
  const loadedIds = ref({})
  const loadingBySession = ref({})
  // sessionId -> { startedAt, runId }：该会话是否有正在跑的运行
  const generatingBySession = ref({})
  // sessionId -> ContextStat：上下文用量跟着会话走（改造前它挂在 ChatPane 上，
  // 于是后台会话跑完回传的用量会显示在当前会话的指示器上）
  const contextBySession = ref({})

  // 附件 data URL 缓存：key = `${sessionId}:${attachmentId}`。
  //
  // 图片是重数据（单张几百 KB 的 base64），而消息列表会因为流式输出、滚动、工具卡片
  // 状态变化而频繁重渲染。没有这层缓存，每渲染一次就向后端要一次图。
  // 用 shallowRef：我们只整体替换这个对象，不需要 Vue 深度追踪里面每个 URL。
  const attachmentUrls = shallowRef({})

  // 外部（如 DiffPane 的 Review code）请求填入输入框的提示词
  const pendingPrompt = ref(null)

  /** 某个会话的消息列表（永远返回数组，避免调用方到处判空） */
  function messagesOf(sessionId) {
    if (!sessionId) return []
    return messagesBySession.value[sessionId] || []
  }

  /** 某个会话是否已载入过历史 */
  function isLoaded(sessionId) {
    return !!loadedIds.value[sessionId]
  }

  /** 某个会话是否有正在跑的运行 */
  function isGeneratingIn(sessionId) {
    return !!generatingBySession.value[sessionId]
  }

  // ===== 对外的"当前会话"视图 =====

  /** 当前会话的消息（模板与组件照旧用这个名字） */
  const messages = computed(() => messagesOf(currentId.value))

  /** 当前会话是否在生成 */
  const isGenerating = computed(() => isGeneratingIn(currentId.value))

  /** 当前会话是否正在载入历史 */
  const loadingHistory = computed(() => !!loadingBySession.value[currentId.value])

  /** 当前会话的上下文用量 */
  const contextStat = computed(() => contextBySession.value[currentId.value] || null)

  /** 所有正在跑的会话 ID（会话列表据此显示"运行中"） */
  const runningSessionIds = computed(() => Object.keys(generatingBySession.value))

  // 兼容旧读法（原实现里它是当前会话的已载入标记）
  const loadedSessionId = computed(() => (isLoaded(currentId.value) ? currentId.value : null))

  // ===== 运行状态 =====

  /**
   * 标记某会话开始一次运行。
   * @param {string} sessionId
   * @param {string} runId 后端给的事件归属标识（可空：旧后端没有这个字段）
   */
  function markRunStarted(sessionId, runId = '') {
    if (!sessionId) return
    generatingBySession.value = {
      ...generatingBySession.value,
      [sessionId]: { startedAt: Date.now(), runId },
    }
  }

  /** 标记某会话的运行结束（用户停止、出错、正常结束都走这里） */
  function markRunEnded(sessionId) {
    if (!sessionId || !generatingBySession.value[sessionId]) return
    const next = { ...generatingBySession.value }
    delete next[sessionId]
    generatingBySession.value = next
  }

  /**
   * 用后端的活跃运行列表恢复"哪几条在跑"（页面重载后调用）。
   *
   * 刻意**只补状态、不造订阅**：这些运行的流式内容已经流出去了，补不回来；
   * 能补的是"它还在跑、可以停"这个事实。前端重载后不该把它们显示成空闲。
   */
  function hydrateRunning(sessionIds) {
    const list = Array.isArray(sessionIds) ? sessionIds : []
    const next = { ...generatingBySession.value }
    for (const id of list) {
      if (!id || next[id]) continue
      next[id] = { startedAt: 0, runId: '', restored: true }
    }
    // 反过来：后端说没在跑、而本地以为在跑的，也要清掉（例如后端已收尾但我们漏了那一条事件）
    for (const id of Object.keys(next)) {
      if (!list.includes(id)) delete next[id]
    }
    generatingBySession.value = next
  }

  // ===== 上下文用量 =====

  function setContextStat(sessionId, st) {
    if (!sessionId || !st) return
    contextBySession.value = { ...contextBySession.value, [sessionId]: st }
  }

  function contextStatOf(sessionId) {
    return contextBySession.value[sessionId] || null
  }

  // ===== 附件 =====

  /**
   * 取某个附件的 data URL（带缓存）。取不到时缓存空串并返回空串。
   * @param {string} sessionId
   * @param {string} attachmentId
   * @returns {Promise<string>}
   */
  async function loadAttachmentUrl(sessionId, attachmentId) {
    const key = `${sessionId}:${attachmentId}`
    const cached = attachmentUrls.value[key]
    if (cached !== undefined) return cached
    const url = await getAttachmentDataURL(sessionId, attachmentId)
    attachmentUrls.value = { ...attachmentUrls.value, [key]: url }
    return url
  }

  /**
   * 同步读缓存（已加载过才有值）。组件渲染时先看它，避免闪一下空占位。
   */
  function attachmentUrl(sessionId, attachmentId) {
    return attachmentUrls.value[`${sessionId}:${attachmentId}`] || ''
  }

  // ===== 消息装载与写入 =====

  /**
   * 载入指定会话的历史消息（已载入过则直接复用内存里那份，不重复拉取）
   *
   * 为什么不每次都重新拉：后台会话可能正在流式输出，内存里那份带着流式占位消息与
   * 已累积的文本；重新拉会把它换成一个"后端持久化的快照"，正在流的内容会缺一段。
   */
  async function loadMessages(sessionId) {
    if (!sessionId) return
    if (loadedIds.value[sessionId]) return

    loadingBySession.value = { ...loadingBySession.value, [sessionId]: true }
    try {
      const session = await getSession(sessionId)
      // 过滤 role=tool 消息（工具结果已显示在 assistant 消息的 toolCalls 卡片中）
      const list = (session.messages || [])
        .filter((m) => m.role !== 'tool')
        .map((m) => ({ ...m, streaming: false }))
      messagesBySession.value = { ...messagesBySession.value, [sessionId]: list }
      loadedIds.value = { ...loadedIds.value, [sessionId]: true }
    } catch (e) {
      console.error('加载会话历史失败:', e)
      messagesBySession.value = { ...messagesBySession.value, [sessionId]: [] }
      // 失败也标记为已载入：否则每次切回来都要重试一遍、每次都闪一下加载中
      loadedIds.value = { ...loadedIds.value, [sessionId]: true }
    } finally {
      const next = { ...loadingBySession.value }
      delete next[sessionId]
      loadingBySession.value = next
    }
  }

  /** 丢弃某会话的内存副本（下次切回会重新拉） */
  function invalidate(sessionId) {
    if (!sessionId) return
    const nextMsgs = { ...messagesBySession.value }
    delete nextMsgs[sessionId]
    messagesBySession.value = nextMsgs
    const nextLoaded = { ...loadedIds.value }
    delete nextLoaded[sessionId]
    loadedIds.value = nextLoaded
    const nextCtx = { ...contextBySession.value }
    delete nextCtx[sessionId]
    contextBySession.value = nextCtx
    dropAttachmentUrls(sessionId)
    markRunEnded(sessionId)
  }

  /**
   * 丢掉某会话的附件 URL 缓存。
   *
   * 附件缓存按 `${sessionId}:${attachmentId}` 键控、只增不减，所以删会话时必须显式清——
   * 单张图是几百 KB 的 base64 字符串，攒着不放会一直占内存。
   */
  function dropAttachmentUrls(sessionId) {
    const prefix = `${sessionId}:`
    const next = {}
    for (const [k, v] of Object.entries(attachmentUrls.value)) {
      if (!k.startsWith(prefix)) next[k] = v
    }
    attachmentUrls.value = next
  }

  /**
   * 本地追加消息（不持久化），返回该消息对象引用
   * @param {string} sessionId 归属会话（必传）
   */
  function addLocalMessage(sessionId, message) {
    if (!sessionId) return null
    const list = messagesOf(sessionId).slice()
    list.push(message)
    messagesBySession.value = { ...messagesBySession.value, [sessionId]: list }
    return message
  }

  /** 更新某会话里的某条消息 */
  function updateLocalMessage(sessionId, messageId, patch) {
    const msg = messagesOf(sessionId).find((m) => m.id === messageId)
    if (msg) Object.assign(msg, patch)
  }

  /** 把流式分片追加到某会话的某条消息上 */
  function appendStreamContent(sessionId, messageId, chunk) {
    const msg = messagesOf(sessionId).find((m) => m.id === messageId)
    if (msg) msg.content += chunk
  }

  /** 往某会话的某条消息上挂一个工具调用卡片 */
  function addToolCall(sessionId, messageId, toolCall) {
    const msg = messagesOf(sessionId).find((m) => m.id === messageId)
    if (msg) {
      if (!msg.toolCalls) msg.toolCalls = []
      msg.toolCalls.push(toolCall)
    }
  }

  /** 更新某会话某条消息里的工具调用卡片 */
  function updateToolCall(sessionId, messageId, toolCallId, patch) {
    const msg = messagesOf(sessionId).find((m) => m.id === messageId)
    if (msg && msg.toolCalls) {
      const tc = msg.toolCalls.find((t) => t.id === toolCallId)
      if (tc) Object.assign(tc, patch)
    }
  }

  /**
   * 用后端持久化的消息替换某会话的流式占位。
   *
   * 改造前这一步操作的是"当前会话"，于是后台会话跑完时，它的回复会被追加到
   * 用户正在看的那个会话里。现在显式传 sessionId，写的一定是它自己的桶。
   *
   * @returns {boolean} 是否找到了占位消息（找不到说明用户期间刷新过，调用方按需兜底）
   */
  function replacePlaceholder(sessionId, placeholderId, persisted) {
    const list = messagesOf(sessionId).slice()
    const idx = list.findIndex((m) => m.id === placeholderId)
    if (idx === -1) return false
    list.splice(idx, 1, ...(persisted || []).map((m) => ({ ...m, streaming: false })))
    messagesBySession.value = { ...messagesBySession.value, [sessionId]: list }
    return true
  }

  /** 请求把一段提示词填入聊天输入框（如 Review code） */
  function requestPrompt(text) {
    pendingPrompt.value = text
  }

  /** 消费并清空待填提示词 */
  function consumePrompt() {
    const t = pendingPrompt.value
    pendingPrompt.value = null
    return t
  }

  return {
    // 状态
    messagesBySession,
    loadedIds,
    loadingBySession,
    generatingBySession,
    contextBySession,
    attachmentUrls,
    pendingPrompt,
    // 当前会话视图
    messages,
    isGenerating,
    loadingHistory,
    contextStat,
    runningSessionIds,
    loadedSessionId,
    // 查询
    messagesOf,
    isLoaded,
    isGeneratingIn,
    contextStatOf,
    // 运行状态
    markRunStarted,
    markRunEnded,
    hydrateRunning,
    setContextStat,
    // 附件
    loadAttachmentUrl,
    attachmentUrl,
    // 消息
    loadMessages,
    invalidate,
    addLocalMessage,
    updateLocalMessage,
    appendStreamContent,
    addToolCall,
    updateToolCall,
    replacePlaceholder,
    requestPrompt,
    consumePrompt,
  }
})
