import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getDiffTurns } from '@/api/session'

/**
 * 差异视图 Store
 * 维护当前会话的各轮 diff（turn=0 为工作区累计差异），供 DiffPane 渲染
 */
export const useDiffStore = defineStore('diff', () => {
  const turns = ref([])
  const activeTurn = ref(0)
  const loading = ref(false)
  const loadedSessionId = ref(null)

  const currentTurn = computed(
    () => turns.value.find((t) => t.turn === activeTurn.value) || turns.value[0] || null
  )
  const files = computed(() => currentTurn.value?.files || [])
  const additions = computed(() => currentTurn.value?.additions || 0)
  const deletions = computed(() => currentTurn.value?.deletions || 0)

  /**
   * 加载会话全部轮次差异，返回当前轮文件数
   */
  async function load(sessionId) {
    if (!sessionId) {
      turns.value = []
      loadedSessionId.value = null
      return 0
    }
    loading.value = true
    try {
      turns.value = (await getDiffTurns(sessionId)) || []
      loadedSessionId.value = sessionId
      const last = turns.value[turns.value.length - 1]
      activeTurn.value = last ? last.turn : 0
      return files.value.length
    } catch (e) {
      console.error('加载 diff 失败:', e)
      turns.value = []
      return 0
    } finally {
      loading.value = false
    }
  }

  /**
   * 处理 diff:update 事件（一轮对话结束后推送）
   */
  function applyUpdate(payload) {
    const { diff, turn } = payload || {}
    if (!diff || !diff.length) return
    const add = diff.reduce((s, f) => s + (f.additions || 0), 0)
    const del = diff.reduce((s, f) => s + (f.deletions || 0), 0)
    const item = {
      turn,
      label: turn === 0 ? '累计' : `第 ${turn} 轮`,
      files: diff,
      additions: add,
      deletions: del,
      createdAt: Date.now(),
    }
    const idx = turns.value.findIndex((t) => t.turn === turn)
    if (idx > -1) turns.value[idx] = item
    else turns.value.push(item)
    activeTurn.value = turn
  }

  function setActiveTurn(t) {
    activeTurn.value = t
  }

  function clear() {
    turns.value = []
    activeTurn.value = 0
    loadedSessionId.value = null
  }

  return {
    turns,
    activeTurn,
    loading,
    loadedSessionId,
    currentTurn,
    files,
    additions,
    deletions,
    load,
    applyUpdate,
    setActiveTurn,
    clear,
  }
})
