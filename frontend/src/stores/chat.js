import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getSession } from '@/api/session'

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

  /**
   * 加载指定会话的历史消息
   */
  async function loadMessages(sessionId) {
    if (!sessionId) {
      messages.value = []
      loadedSessionId.value = null
      return
    }
    if (loadedSessionId.value === sessionId) return

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

  return {
    messages,
    isGenerating,
    loadingHistory,
    loadedSessionId,
    loadMessages,
    addLocalMessage,
    updateLocalMessage,
    appendStreamContent,
    addToolCall,
    updateToolCall,
  }
})
