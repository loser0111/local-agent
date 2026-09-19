import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { listSubagents, getSubagentMessages, onSubagentEvent } from '@/api/session'

/**
 * 子代理 Store（spawn_agent 派生的代理）
 *
 * 子代理跑在**自己独立的会话**里：它的工具调用不走 chat:event（否则会混进主会话的
 * 聊天流，看起来像主会话自己在跑那些工具），而是走 subagent:event 这条独立通道。
 * 所以这里既订阅实时进度，又在打开面板时向后端拉一次全量列表——两者合并的规则是
 * "以 runId 为准做 upsert"：实时事件只带概况，拉取回来的那份是权威。
 *
 * 列表里的历史记录来自子代理的会话文件（后端已落盘），因此应用重启后依然在。
 */
export const useSubagentStore = defineStore('subagent', () => {
  const runs = ref([])
  const loadedSessionId = ref(null)
  const loading = ref(false)
  const expanded = ref({}) // runId -> 是否展开
  const messages = ref({}) // runId -> Message[]
  const messagesLoading = ref({}) // runId -> 是否正在拉消息

  // 全局只订阅一次：事件是应用级的，而面板可能反复开关
  let unsubscribed = null

  const runningCount = computed(() => runs.value.filter((r) => r.status === 'running').length)

  /** 加载某主会话的子代理列表（打开面板时调用；同时挂上实时进度订阅） */
  async function load(sessionId) {
    subscribe()
    if (!sessionId) {
      runs.value = []
      loadedSessionId.value = null
      return
    }
    loading.value = true
    try {
      runs.value = (await listSubagents(sessionId)) || []
      loadedSessionId.value = sessionId
    } catch (e) {
      console.error('加载子代理列表失败:', e)
    } finally {
      loading.value = false
    }
  }

  /**
   * 订阅实时进度。只订阅一次，且**不随面板关闭而退订**——
   * 退订后再打开会因为漏掉中间事件而显示过期进度，而这里一条轻量事件的成本可以忽略。
   */
  function subscribe() {
    if (unsubscribed) return
    unsubscribed = onSubagentEvent(applyEvent)
  }

  /** 处理一条实时概况（按 runId 合并） */
  function applyEvent(info) {
    if (!info || !info.runId) return
    // 事件是全局的：只接受当前面板所在会话的（后端可能同时在跑别的会话的子代理）
    if (loadedSessionId.value && info.parentId !== loadedSessionId.value) return
    const idx = runs.value.findIndex((r) => r.runId === info.runId)
    if (idx > -1) runs.value[idx] = { ...runs.value[idx], ...info }
    else runs.value.unshift(info)
  }

  /** 展开/收起某个子代理；展开时按需拉取它的消息流 */
  async function toggle(runId) {
    const next = !expanded.value[runId]
    expanded.value[runId] = next
    if (next && !messages.value[runId]) {
      await loadMessages(runId)
    }
  }

  /** 拉取某个子代理的完整消息流（点开才拉：中间过程可能有几百条） */
  async function loadMessages(runId) {
    if (!runId) return
    messagesLoading.value[runId] = true
    try {
      messages.value[runId] = (await getSubagentMessages(runId)) || []
    } catch (e) {
      messages.value[runId] = []
      console.error('加载子代理消息失败:', e)
    } finally {
      messagesLoading.value[runId] = false
    }
  }

  /** 丢弃某条消息缓存（回退之后它的文件列表会变） */
  function invalidate(runId) {
    if (messages.value[runId]) delete messages.value[runId]
  }

  function clear() {
    runs.value = []
    expanded.value = {}
    messages.value = {}
    loadedSessionId.value = null
  }

  return {
    runs,
    loading,
    expanded,
    messages,
    messagesLoading,
    loadedSessionId,
    runningCount,
    load,
    subscribe,
    applyEvent,
    toggle,
    loadMessages,
    invalidate,
    clear,
  }
})
