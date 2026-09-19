import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as sessionApi from '@/api/session'

/**
 * 会话管理 Store —— 对接后端会话域（JSON 文件持久化）
 */
export const useSessionStore = defineStore('session', () => {
  // 会话元数据列表（不含消息）
  const sessions = ref([])
  const currentSessionId = ref(null)
  const loaded = ref(false)

  const currentSession = computed(
    () => sessions.value.find((s) => s.id === currentSessionId.value) || null
  )

  /**
   * 应用启动时加载会话列表；若没有任何会话则自动创建一个
   */
  async function init() {
    await refreshSessions()
    if (sessions.value.length > 0) {
      currentSessionId.value = sessions.value[0].id
    } else {
      await createSession({ title: '新会话' })
    }
    loaded.value = true
  }

  async function refreshSessions() {
    sessions.value = await sessionApi.listSessions()
  }

  async function createSession(config) {
    const session = await sessionApi.createSession(config)
    sessions.value.unshift(session)
    currentSessionId.value = session.id
    return session
  }

  /**
   * 切换会话（消息加载由 chatStore 监听 currentSessionId 完成）
   */
  function switchSession(id) {
    if (currentSessionId.value === id) return
    currentSessionId.value = id
  }

  async function deleteSession(id) {
    await sessionApi.deleteSession(id)
    const idx = sessions.value.findIndex((s) => s.id === id)
    if (idx > -1) sessions.value.splice(idx, 1)
    if (currentSessionId.value === id) {
      currentSessionId.value = sessions.value[0]?.id || null
    }
    // 删完了自动新建一个，保证始终有可用会话
    if (sessions.value.length === 0) {
      await createSession({ title: '新会话' })
    }
  }

  /**
   * 更新会话元数据并同步列表项
   */
  async function patchSession(id, patch) {
    const updated = await sessionApi.updateSession(id, patch)
    const idx = sessions.value.findIndex((s) => s.id === id)
    if (idx > -1) {
      // 保留列表项引用，仅合并元数据（列表项本身不含消息）
      sessions.value[idx] = {
        ...sessions.value[idx],
        title: updated.title,
        model: updated.model,
        permissionMode: updated.permissionMode,
        viewMode: updated.viewMode,
        status: updated.status,
        project: updated.project,
        endAt: updated.endAt,
      }
    }
    return updated
  }

  /**
   * 本地更新列表项的活跃时间（消息追加后调用，避免频繁请求列表）
   */
  function touchSession(id, patch = {}) {
    const s = sessions.value.find((item) => item.id === id)
    if (s) Object.assign(s, patch, { endAt: Date.now() })
  }

  return {
    sessions,
    currentSessionId,
    currentSession,
    loaded,
    init,
    refreshSessions,
    createSession,
    switchSession,
    deleteSession,
    patchSession,
    touchSession,
  }
})
