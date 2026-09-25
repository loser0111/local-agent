import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'
import { getSession, getAttachmentDataURL } from '@/api/session'

/**
 * 对话消息 Store —— 仅维护「当前会话」的消息
 * 切换会话时从后端加载历史消息；发送的消息通过 api/session 持久化
 */
export const useChatStore = defineStore('chat', () => {
  const messages = ref([])
  const isGenerating = ref(false)
  const loadingHistory = ref(false)
  // 当前已加载消息的会话 ID
  const loadedSessionId = ref(null)
  // 外部（如 DiffPane 的 Review code）请求填入输入框的提示词
  const pendingPrompt = ref(null)

  // 附件 data URL 缓存：key = `${sessionId}:${attachmentId}`。
  //
  // 图片是重数据（单张几百 KB 的 base64），而消息列表会因为流式输出、滚动、工具卡片
  // 状态变化而频繁重渲染。没有这层缓存，每渲染一次就向后端要一次图。
  // 用 shallowRef：我们只整体替换这个对象，不需要 Vue 深度追踪里面每个 URL。
  // 只增不减，切会话时整体清空——跨会话留着没有意义，还白占内存。
  const attachmentUrls = shallowRef({})

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

  /**
   * 加载指定会话的历史消息
   */
  async function loadMessages(sessionId) {
    if (!sessionId) {
      messages.value = []
      loadedSessionId.value = null
      attachmentUrls.value = {}
      return
    }
    if (loadedSessionId.value === sessionId) return

    // 切会话：附件缓存整体作废（键里虽然带会话 ID，但跨会话攒着只会白占内存）
    attachmentUrls.value = {}

    loadingHistory.value = true
    try {
      const session = await getSession(sessionId)
      // 过滤 role=tool 消息（工具结果已显示在 assistant 消息的 toolCalls 卡片中）
      messages.value = (session.messages || [])
        .filter((m) => m.role !== 'tool')
        .map((m) => ({ ...m, streaming: false }))
      loadedSessionId.value = sessionId
    } catch (e) {
      console.error('加载会话历史失败:', e)
      messages.value = []
      loadedSessionId.value = sessionId
    } finally {
      loadingHistory.value = false
    }
  }

  /**
   * 本地追加消息（不持久化），返回该消息对象引用
   */
  function addLocalMessage(message) {
    messages.value.push(message)
    return message
  }

  /**
   * 更新本地消息
   */
  function updateLocalMessage(messageId, patch) {
    const msg = messages.value.find((m) => m.id === messageId)
    if (msg) Object.assign(msg, patch)
  }

  function appendStreamContent(messageId, chunk) {
    const msg = messages.value.find((m) => m.id === messageId)
    if (msg) msg.content += chunk
  }

  function addToolCall(messageId, toolCall) {
    const msg = messages.value.find((m) => m.id === messageId)
    if (msg) {
      if (!msg.toolCalls) msg.toolCalls = []
      msg.toolCalls.push(toolCall)
    }
  }

  function updateToolCall(messageId, toolCallId, patch) {
    const msg = messages.value.find((m) => m.id === messageId)
    if (msg && msg.toolCalls) {
      const tc = msg.toolCalls.find((t) => t.id === toolCallId)
      if (tc) Object.assign(tc, patch)
    }
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
    messages,
    isGenerating,
    loadingHistory,
    loadedSessionId,
    pendingPrompt,
    attachmentUrls,
    loadAttachmentUrl,
    attachmentUrl,
    loadMessages,
    addLocalMessage,
    updateLocalMessage,
    appendStreamContent,
    addToolCall,
    updateToolCall,
    requestPrompt,
    consumePrompt,
  }
})
