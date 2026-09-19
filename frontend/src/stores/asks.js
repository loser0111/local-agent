import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { resolveAskUser, cancelAskUser, getPendingAsk } from '@/api/ask'

/**
 * 模型提问 Store（ask_user 工具）
 *
 * 后端在工具调用里阻塞等待，前端拿到 ask_user 事件后弹窗收集答复，
 * 再经 ResolveAskUser 回传。答复会作为工具结果回到模型手里，同一轮对话继续。
 */
export const useAskStore = defineStore('asks', () => {
  const pending = ref(null) // AskRequest：当前待作答的提问
  const answers = ref({}) // 下标 -> { selected: string[], text: string }
  const submitting = ref(false)

  const hasPending = computed(() => !!pending.value)

  function initAnswers(req) {
    const m = {}
    const qs = req?.questions || []
    qs.forEach((_, i) => {
      m[i] = { selected: [], text: '' }
    })
    return m
  }

  /** 后端推来一条提问 */
  function setPending(req) {
    if (!req || !req.id) return
    pending.value = req
    answers.value = initAnswers(req)
  }

  function clearPending() {
    pending.value = null
    answers.value = {}
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
    if (!pending.value || submitting.value) return false
    submitting.value = true
    try {
      await resolveAskUser({ id: pending.value.id, answers: buildItems() })
      clearPending()
      return true
    } catch (e) {
      // 迟到答复（已超时/已取消）是正常竞态：提示一下并关掉弹窗，不必重试
      alert(`提交答复失败：${e?.message || e}`)
      clearPending()
      return false
    } finally {
      submitting.value = false
    }
  }

  /** 跳过：告诉后端"用户未作答"，模型会自行决策并说明假设 */
  async function skip(sessionId) {
    if (!pending.value) return
    try {
      await cancelAskUser(sessionId || pending.value.sessionId)
    } catch (e) {
      console.warn('取消提问失败:', e)
    }
    clearPending()
  }

  /** 切换会话/打开面板时，若该会话还有挂起提问则重新弹窗 */
  async function loadPending(sessionId) {
    if (!sessionId) {
      clearPending()
      return
    }
    try {
      const p = await getPendingAsk(sessionId)
      if (p && p.id) setPending(p)
      else if (pending.value && pending.value.sessionId !== sessionId) clearPending()
    } catch (e) {
      console.warn('查询挂起提问失败:', e)
    }
  }

  return {
    pending,
    answers,
    submitting,
    hasPending,
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
