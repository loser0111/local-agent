import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getDiffTurns, listCheckpoints, undoDiffTurn } from '@/api/session'

/**
 * 差异视图 Store
 * 维护当前会话的各轮 diff（turn=0 为工作区累计差异），供 DiffPane 渲染
 */
export const useDiffStore = defineStore('diff', () => {
  const turns = ref([])
  const activeTurn = ref(0)
  const loading = ref(false)
  const loadedSessionId = ref(null)
  // 各轮的回退可用性（是否可回退、冲突文件、是否已回退过）
  const checkpoints = ref([])

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
      checkpoints.value = []
      loadedSessionId.value = null
      return 0
    }
    loading.value = true
    try {
      turns.value = (await getDiffTurns(sessionId)) || []
      loadedSessionId.value = sessionId
      const last = turns.value[turns.value.length - 1]
      activeTurn.value = last ? last.turn : 0
      await loadCheckpoints(sessionId)
      return files.value.length
    } catch (e) {
      console.error('加载 diff 失败:', e)
      turns.value = []
      return 0
    } finally {
      loading.value = false
    }
  }

  /** 拉取各轮的回退可用性（不可回退的轮次也会在列表里，带 reason） */
  async function loadCheckpoints(sessionId) {
    if (!sessionId) {
      checkpoints.value = []
      return
    }
    try {
      checkpoints.value = (await listCheckpoints(sessionId)) || []
    } catch {
      checkpoints.value = []
    }
  }

  /** 取某轮的回退信息；没有则返回 null */
  function checkpointOf(turn) {
    return checkpoints.value.find((c) => c.turn === turn) || null
  }

  /**
   * 回退某一轮。
   *
   * 成功后就地重新拉取：**不用 diff:update 事件**——回退后这一轮的 diff 常常变成空，
   * 而 applyUpdate 会把空 diff 直接丢掉（见下方 `if (!diff || !diff.length) return`），
   * 靠事件刷新会留下过期数据。
   */
  async function undo(sessionId, turn, force = false) {
    const keep = activeTurn.value
    const res = await undoDiffTurn(sessionId, turn, force)
    await load(sessionId)
    // 尽量留在原来那一轮；该轮已被清掉时 load 会落到最后一轮
    if (turns.value.some((t) => t.turn === keep)) activeTurn.value = keep
    return res
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
    checkpoints.value = []
    activeTurn.value = 0
    loadedSessionId.value = null
  }

  return {
    turns,
    activeTurn,
    loading,
    loadedSessionId,
    checkpoints,
    currentTurn,
    files,
    additions,
    deletions,
    load,
    loadCheckpoints,
    checkpointOf,
    undo,
    applyUpdate,
    setActiveTurn,
    clear,
  }
})
