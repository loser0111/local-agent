import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getPlan, savePlan, executePlan, cancelPlan, reopenPlan } from '@/api/plan'
import { useChatStore } from '@/stores/chat'
import { useSessionStore } from '@/stores/session'

/**
 * 计划 Store —— 每个会话各占一个桶
 *
 * 为什么要分桶：`plan_update` 是后端推来的事件，它属于**产生这条计划的那个会话**。
 * 改造前只有一份 `plan` / `executing`，于是 A 会话产出或推进计划时，
 * 用户正在看的 B 会话的计划面板会显示 A 的计划；A 执行计划期间 B 也会显示"执行中"。
 * 执行期间的消息链路（流式气泡、工具卡片）同样按会话桶写入（见 stores/chat.js）。
 */
export const usePlanStore = defineStore('plan', () => {
  const sessionStore = useSessionStore()

  // sessionId -> Plan / boolean
  const planBySession = ref({})
  const executingBySession = ref({})

  const currentId = computed(() => sessionStore.currentSessionId)
  const plan = computed(() => planBySession.value[currentId.value] || null)
  const executing = computed(() => !!executingBySession.value[currentId.value])

  /** 取某会话的计划（不传则当前会话） */
  function planOf(sessionId = currentId.value) {
    return planBySession.value[sessionId] || null
  }

  /** 是否某会话正在执行计划 */
  function isExecutingIn(sessionId) {
    return !!executingBySession.value[sessionId]
  }

  /** 所有正在执行计划的会话（会话列表展示用） */
  const executingSessionIds = computed(() => Object.keys(executingBySession.value))

  function setPlan(sessionId, p) {
    if (!sessionId) return
    if (!p) {
      const next = { ...planBySession.value }
      delete next[sessionId]
      planBySession.value = next
      return
    }
    planBySession.value = { ...planBySession.value, [sessionId]: p }
  }

  function setExecuting(sessionId, v) {
    if (!sessionId) return
    if (!v) {
      const next = { ...executingBySession.value }
      delete next[sessionId]
      executingBySession.value = next
      return
    }
    executingBySession.value = { ...executingBySession.value, [sessionId]: true }
  }

  /**
   * 加载指定会话的最新计划（切换会话/打开面板时调用）
   */
  async function loadForSession(sessionId) {
    if (!sessionId) return
    try {
      setPlan(sessionId, await getPlan(sessionId))
    } catch (e) {
      console.error('加载计划失败:', e)
      setPlan(sessionId, null)
    }
  }

  /**
   * plan_update 事件/返回值落库。
   *
   * sessionId 必传：这条计划属于哪个会话由事件（或调用方持有的计划对象）给出，
   * 不能拿"当前显示的会话"顶替——那正是 A 的计划显示在 B 上的原因。
   */
  function applyUpdate(sessionId, p) {
    if (p) setPlan(sessionId, p)
  }

  /**
   * 保存审核编辑
   * @param {object} edited 编辑后的计划（title/steps）
   */
  async function save(edited) {
    const saved = await savePlan(edited)
    setPlan(saved?.sessionId || edited?.sessionId || currentId.value, saved)
    return saved
  }

  /**
   * 批准并执行计划：逐步执行，过程渲染进聊天流
   * @param {string} sessionId 计划所属会话
   * @param {string} planId
   * @param {boolean} useStream
   */
  async function execute(sessionId, planId, useStream) {
    if (!sessionId || executingBySession.value[sessionId]) return null
    const chatStore = useChatStore()
    setExecuting(sessionId, true)
    chatStore.markRunStarted(sessionId)

    let placeholder = null // 当前 running 步骤的流式占位消息

    const finalizePlaceholder = (step) => {
      if (!placeholder) return
      const patch = { streaming: false }
      if (!placeholder.content && step?.summary) patch.content = step.summary
      if (!placeholder.content && step?.error) patch.content = `步骤失败：${step.error}`
      chatStore.updateLocalMessage(sessionId, placeholder.id, patch)
      placeholder = null
    }

    try {
      const result = await executePlan(sessionId, planId, useStream, {
        onPlanUpdate: (p) => {
          setPlan(sessionId, p)
          const running = p?.steps?.find((s) => s.status === 'running')
          if (running) {
            // 新步骤开始：先补步骤用户消息（与后端持久化内容一致），再建流式占位
            if (!placeholder || placeholder.__stepIndex !== running.index) {
              finalizePlaceholder(null)
              const stepQuery = running.detail
                ? `【计划步骤 ${running.index + 1}/${p.steps.length}】${running.title}\n${running.detail}`
                : `【计划步骤 ${running.index + 1}/${p.steps.length}】${running.title}`
              chatStore.addLocalMessage(sessionId, {
                id: `plan-user-${running.index}-${Date.now()}`,
                role: 'user',
                content: stepQuery,
                createdAt: Date.now(),
                streaming: false,
              })
              placeholder = chatStore.addLocalMessage(sessionId, {
                id: `plan-step-${running.index}-${Date.now()}`,
                role: 'assistant',
                content: '',
                toolCalls: [],
                createdAt: Date.now(),
                streaming: true,
              })
              placeholder.__stepIndex = running.index
            }
          } else if (placeholder) {
            // 当前步骤已结束（done/failed）
            const step = (p?.steps || []).find(
              (s) => s.index === placeholder.__stepIndex && (s.status === 'done' || s.status === 'failed')
            )
            finalizePlaceholder(step)
          }
        },
        onReplyDelta: (chunk) => {
          if (placeholder) chatStore.appendStreamContent(sessionId, placeholder.id, chunk)
        },
        onToolCallStart: (tc) => {
          if (placeholder) chatStore.addToolCall(sessionId, placeholder.id, tc)
        },
        onToolCallEnd: (tc) => {
          if (placeholder) {
            chatStore.updateToolCall(sessionId, placeholder.id, tc.id, {
              status: tc.status,
              duration: tc.duration,
              result: tc.result,
            })
          }
        },
      })

      finalizePlaceholder(null)

      if (result?.plan) setPlan(sessionId, result.plan)
      if (result?.reply) {
        chatStore.addLocalMessage(sessionId, {
          id: `plan-reply-${Date.now()}`,
          role: 'assistant',
          content: result.reply,
          createdAt: Date.now(),
          streaming: false,
        })
      }
      if (result?.error && !result.reply) {
        chatStore.addLocalMessage(sessionId, {
          id: `plan-error-${Date.now()}`,
          role: 'assistant',
          content: `计划执行失败：${result.error}`,
          createdAt: Date.now(),
          streaming: false,
        })
      }
      return result
    } catch (e) {
      console.error('计划执行失败:', e)
      finalizePlaceholder(null)
      chatStore.addLocalMessage(sessionId, {
        id: `plan-error-${Date.now()}`,
        role: 'assistant',
        content: `计划执行失败：${e?.message || e}`,
        createdAt: Date.now(),
        streaming: false,
      })
      return null
    } finally {
      setExecuting(sessionId, false)
      chatStore.markRunEnded(sessionId)
    }
  }

  /**
   * 把已结束的计划退回待审核（失败后「修改后重试」）：
   * 已完成步骤保留，失败/跳过步骤重置为待执行，随后即可编辑并再次批准执行。
   * @param {string} planId
   */
  async function reopen(planId) {
    const p = await reopenPlan(planId)
    setPlan(p?.sessionId || currentId.value, p)
    return p
  }

  /**
   * 取消执行中的计划
   */
  async function cancel(planId) {
    try {
      await cancelPlan(planId)
    } catch (e) {
      console.warn('取消计划失败:', e)
    }
  }

  function reset(sessionId = currentId.value) {
    setPlan(sessionId, null)
  }

  return {
    planBySession,
    executingBySession,
    plan,
    executing,
    executingSessionIds,
    planOf,
    isExecutingIn,
    setPlan,
    loadForSession,
    applyUpdate,
    save,
    execute,
    reopen,
    cancel,
    reset,
  }
})
