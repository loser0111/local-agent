import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getDiffTurns, listCheckpoints, undoDiffTurn } from '@/api/session'
import { useSessionStore } from '@/stores/session'

/**
 * 差异视图 Store —— 每个会话各占一个桶
 *
 * 分桶的理由与 chat store 相同（见 stores/chat.js 顶部）：`diff:update` 是后端在
 * 一轮对话结束时推来的，它属于**那次运行所属的会话**，而不是"用户此刻正在看的那个"。
 * 改造前 applyUpdate 直接写进唯一的一份 turns，于是 A 的改动会显示在 B 的差异面板里，
 * 还会凭空多出一个"第 N 轮"。
 *
 * 对外的 turns / checkpoints / activeTurn 都是当前会话那一份的 computed，
 * DiffPane 基本不用改。
 */
export const useDiffStore = defineStore('diff', () => {
  const sessionStore = useSessionStore()
  const currentId = computed(() => sessionStore.currentSessionId)

  // 以下四张表全部按 sessionId 分桶
  const turnsBySession = ref({})
  const checkpointsBySession = ref({})
  const activeTurnBySession = ref({})
  const loadingBySession = ref({})
  const loadedIds = ref({})

  // ===== 当前会话视图 =====

  const turns = computed(() => turnsBySession.value[currentId.value] || [])
  const checkpoints = computed(() => checkpointsBySession.value[currentId.value] || [])
  const loading = computed(() => !!loadingBySession.value[currentId.value])
  const loadedSessionId = computed(() => (loadedIds.value[currentId.value] ? currentId.value : null))
  const activeTurn = computed(() => {
    const bucket = activeTurnBySession.value[currentId.value]
    return bucket === undefined ? 0 : bucket
  })

  const currentTurn = computed(
    () => turns.value.find((t) => t.turn === activeTurn.value) || turns.value[0] || null
  )
  const files = computed(() => currentTurn.value?.files || [])
  const additions = computed(() => currentTurn.value?.additions || 0)
  const deletions = computed(() => currentTurn.value?.deletions || 0)

  /** 某会话的某一轮 */
  function turnOf(sessionId, turn) {
    return (turnsBySession.value[sessionId] || []).find((t) => t.turn === turn) || null
  }

  /**
   * 加载会话全部轮次差异，返回当前轮文件数
   */
  async function load(sessionId) {
    if (!sessionId) return 0
    loadingBySession.value = { ...loadingBySession.value, [sessionId]: true }
    try {
      const list = (await getDiffTurns(sessionId)) || []
      turnsBySession.value = { ...turnsBySession.value, [sessionId]: list }
      loadedIds.value = { ...loadedIds.value, [sessionId]: true }
      const last = list[list.length - 1]
      activeTurnBySession.value = {
        ...activeTurnBySession.value,
        [sessionId]: last ? last.turn : 0,
      }
      await loadCheckpoints(sessionId)
      return (list.find((t) => t.turn === activeTurnBySession.value[sessionId]) || list[0])?.files
        ?.length || 0
    } catch (e) {
      console.error('加载 diff 失败:', e)
      turnsBySession.value = { ...turnsBySession.value, [sessionId]: [] }
      return 0
    } finally {
      const next = { ...loadingBySession.value }
      delete next[sessionId]
      loadingBySession.value = next
    }
  }

  /** 拉取某会话各轮的回退可用性（不可回退的轮次也会在列表里，带 reason） */
  async function loadCheckpoints(sessionId) {
    if (!sessionId) return
    try {
      const list = (await listCheckpoints(sessionId)) || []
      checkpointsBySession.value = { ...checkpointsBySession.value, [sessionId]: list }
    } catch {
      checkpointsBySession.value = { ...checkpointsBySession.value, [sessionId]: [] }
    }
  }

  /** 取某会话某轮的回退信息；没有则返回 null */
  function checkpointOf(turn, sessionId = currentId.value) {
    return (checkpointsBySession.value[sessionId] || []).find((c) => c.turn === turn) || null
  }

  /**
   * 回退某一轮。
   *
   * 成功后就地重新拉取：**不用 diff:update 事件**——回退后这一轮的 diff 常常变成空，
   * 而 applyUpdate 会把空 diff 直接丢掉（见下方 `if (!diff || !diff.length) return`），
   * 靠事件刷新会留下过期数据。
   */
  async function undo(sessionId, turn, force = false) {
    const keep = activeTurnBySession.value[sessionId]
    const res = await undoDiffTurn(sessionId, turn, force)
    await load(sessionId)
    // 尽量留在原来那一轮；该轮已被清掉时 load 会落到最后一轮
    if ((turnsBySession.value[sessionId] || []).some((t) => t.turn === keep)) {
      activeTurnBySession.value = { ...activeTurnBySession.value, [sessionId]: keep }
    }
    return res
  }

  /**
   * 处理 diff:update 事件（一轮对话结束时推送）
   *
   * 归属取自事件本身：没有 sessionId 就丢弃（不做"当成当前会话"的推断——
   * 那正是改造前把 A 的改动显示进 B 的根因）。
   */
  function applyUpdate(payload) {
    const { diff, turn, sessionId } = payload || {}
    if (!sessionId) {
      console.warn('[diff] 收到没有 sessionId 的 diff:update，已丢弃', payload)
      return
    }
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
    const list = (turnsBySession.value[sessionId] || []).slice()
    const idx = list.findIndex((t) => t.turn === turn)
    if (idx > -1) list[idx] = item
    else list.push(item)
    turnsBySession.value = { ...turnsBySession.value, [sessionId]: list }
    activeTurnBySession.value = { ...activeTurnBySession.value, [sessionId]: turn }
  }

  /** 切到某会话的某一轮（默认作用于当前会话） */
  function setActiveTurn(t, sessionId = currentId.value) {
    if (!sessionId) return
    activeTurnBySession.value = { ...activeTurnBySession.value, [sessionId]: t }
  }

  /** 清空某会话的差异视图（删会话时调用；不传则清当前会话） */
  function clear(sessionId = currentId.value) {
    if (!sessionId) return
    const nextTurns = { ...turnsBySession.value }
    const nextCk = { ...checkpointsBySession.value }
    const nextActive = { ...activeTurnBySession.value }
    const nextLoaded = { ...loadedIds.value }
    delete nextTurns[sessionId]
    delete nextCk[sessionId]
    delete nextActive[sessionId]
    delete nextLoaded[sessionId]
    turnsBySession.value = nextTurns
    checkpointsBySession.value = nextCk
    activeTurnBySession.value = nextActive
    loadedIds.value = nextLoaded
  }

  return {
    turnsBySession,
    checkpointsBySession,
    activeTurnBySession,
    turns,
    checkpoints,
    loading,
    loadedSessionId,
    activeTurn,
    currentTurn,
    files,
    additions,
    deletions,
    turnOf,
    load,
    loadCheckpoints,
    checkpointOf,
    undo,
    applyUpdate,
    setActiveTurn,
    clear,
  }
})
