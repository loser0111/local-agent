import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { resolveAskUser, cancelAskUser, getPendingAsk } from '@/api/ask'
import { useSessionStore } from '@/stores/session'

/**
 * 模型提问 Store（ask_user 工具）
 *
 * 后端在工具调用里阻塞等待，前端拿到 ask_user 事件后弹窗收集答复，
 * 再经 ResolveAskUser 回传。答复会作为工具结果回到模型手里，同一轮对话继续。
 *
 * 挂起请求与答题草稿都按会话分桶：
 *   - 挂起请求：两个会话可能同时在等人（后端各等各的），不能互相顶掉；
 *     弹窗只弹**当前会话**那条，后台会话的在会话列表出标记。
 *   - 答题草稿：切走再切回来时，用户已经勾了一半的选项不该丢，
 *     也不该跑到另一个会话的弹窗里去。
 */
export const useAskStore = defineStore('asks', () => {
  const sessionStore = useSessionStore()

  // sessionId -> AskRequest
  const pendingBySession = ref({})
  // sessionId -> { [qIndex]: { selected: string[], text: string } }
  const answersBySession = ref({})
  const submitting = ref(false)

  const currentId = computed(() => sessionStore.currentSessionId)
  const pending = computed(() => pendingBySession.value[currentId.value] || null)
  const answers = computed(() => answersBySession.value[currentId.value] || {})
  const hasPending = computed(() => !!pending.value)
  /** 所有有挂起提问的会话（会话列表打标记用） */
  const pendingSessionIds = computed(() => Object.keys(pendingBySession.value))

  const hasPendingIn = (sessionId) => !!pendingBySession.value[sessionId]

  function initAnswers(req) {
    const m = {}
    const qs = req?.questions || []
    qs.forEach((_, i) => {
      m[i] = { selected: [], text: '' }
    })
    return m
  }

  /** 后端推来一条提问，按它的会话归档（草稿一并初始化） */
  function setPending(req) {
    if (!req || !req.id || !req.sessionId) return
    pendingBySession.value = { ...pendingBySession.value, [req.sessionId]: req }
    // 同一个会话重复推同一条（事件 + 轮询兜底）时不要清掉用户已填的草稿
    if (!answersBySession.value[req.sessionId]) {
      answersBySession.value = {
        ...answersBySession.value,
        [req.sessionId]: initAnswers(req),
      }
    }
  }

  /** 清掉某会话的挂起提问（不传则当前会话） */
  function clearPending(sessionId = currentId.value) {
    if (!sessionId) return
    const nextP = { ...pendingBySession.value }
    const nextA = { ...answersBySession.value }
    delete nextP[sessionId]
    delete nextA[sessionId]
    pendingBySession.value = nextP
    answersBySession.value = nextA
  }

  /** 勾选/取消一个选项（单选时替换，多选时增删） */
  function toggleOption(qIndex, label, multiSelect) {
    const cur = answers.value[qIndex]
    if (!cur) return
    if (multiSelect) {
      const i = cur.selected.indexOf(label)
      if (i >= 0) cur.selected.splice(i, 1)
      else cur.selected.push(label)
    } else {
      cur.selected = [label]
    }
  }

  function setText(qIndex, text) {
    const cur = answers.value[qIndex]
    if (cur) cur.text = text
  }

  /** 该问题是否已有答复（选项或文字任一即可） */
  function isAnswered(qIndex) {
    const cur = answers.value[qIndex]
    return !!cur && (cur.selected.length > 0 || (cur.text || '').trim() !== '')
  }

  function buildItems() {
    const qs = pending.value?.questions || []
    return qs.map((q, i) => {
      const cur = answers.value[i] || { selected: [], text: '' }
      return {
        questionId: q.id || '',
        selected: [...cur.selected],
        text: (cur.text || '').trim(),
      }
    })
  }

  /** 提交答复 */
  async function submit() {
    const req = pending.value
    if (!req || submitting.value) return false
    submitting.value = true
    try {
      await resolveAskUser({ id: req.id, answers: buildItems() })
      clearPending(req.sessionId)
      return true
    } catch (e) {
      // 迟到答复（已超时/已取消）是正常竞态：提示一下并关掉弹窗，不必重试
      alert(`提交答复失败：${e?.message || e}`)
      clearPending(req.sessionId)
      return false
    } finally {
      submitting.value = false
    }
  }

  /** 跳过：告诉后端"用户未作答"，模型会自行决策并说明假设 */
  async function skip(sessionId) {
    const req = pendingBySession.value[sessionId] || pending.value
    if (!req) return
    try {
      await cancelAskUser(sessionId || req.sessionId)
    } catch (e) {
      console.warn('取消提问失败:', e)
    }
    clearPending(sessionId || req.sessionId)
  }

  /**
   * 该会话是否还有挂起的提问（切换会话/打开面板时调用，作为事件通道的兜底）。
   *
   * 只查传入的那条会话：轮询是"当前会话的弹窗补出来"的兜底手段，
   * 后台会话靠事件通道（它推给所有会话，不依赖焦点）。
   */
  async function loadPending(sessionId) {
    if (!sessionId) return
    try {
      const p = await getPendingAsk(sessionId)
      if (p && p.id) {
        setPending({ ...p, sessionId: p.sessionId || sessionId })
      } else if (pendingBySession.value[sessionId]) {
        clearPending(sessionId)
      }
    } catch (e) {
      console.warn('查询挂起提问失败:', e)
    }
  }

  return {
    pendingBySession,
    answersBySession,
    pending,
    answers,
    submitting,
    hasPending,
    pendingSessionIds,
    hasPendingIn,
    setPending,
    clearPending,
    toggleOption,
    setText,
    isAnswered,
    submit,
    skip,
    loadPending,
  }
})
