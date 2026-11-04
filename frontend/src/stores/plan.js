import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  getPlan,
  savePlan,
  executePlan,
  resumePlan,
  retryPlan,
  cancelPlan,
  reopenPlan,
} from '@/api/plan'
import { useChatStore } from '@/stores/chat'
import { useSessionStore } from '@/stores/session'
import { useTodoStore } from '@/stores/todo'

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
  const todoStore = useTodoStore()

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
   *
   * **按 revision 单调接受**：事件是异步推送的，旧运行（如续跑前那次）的迟到事件
   * 可能在新状态之后到达。接受它会把"已完成"刷回"执行中"。
   * revision 缺省（旧后端/旧数据）当 0 处理——那时还没这层保护，只能按到达顺序。
   */
  function applyUpdate(sessionId, p) {
    if (!p) return
    const cur = planBySession.value[sessionId]
    const nextRev = Number(p.revision) || 0
    const curRev = Number(cur?.revision) || 0
    if (cur && cur.id === p.id && nextRev < curRev) return
    setPlan(sessionId, p)
  }

  /** 规划器判定"无需计划"：后端丢弃了占位记录，这里同步清空 */
  function clearPlan(sessionId) {
    setPlan(sessionId, null)
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
   * @param {Function} runner 实际调用（executePlan / resumePlan），两者过程完全一致
   */
  async function runPlanFlow(sessionId, planId, useStream, runner) {
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
      const result = await runner(sessionId, planId, useStream, {
        onPlanUpdate: (p) => {
          applyUpdate(sessionId, p)
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
        // 思考分片写进同一个占位消息的 thinking 字段（与 reply_delta 分开的另一个区域）
        onThinkingDelta: (chunk) => {
          if (placeholder) chatStore.appendStreamThinking(sessionId, placeholder.id, chunk)
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
        // 计划执行期间模型也可能用 todo 工具跟踪步骤：清单同样要落到对应会话的桶里
        onTodoUpdate: (items) => todoStore.setTodos(sessionId, items),
      })

      finalizePlaceholder(null)

      if (result?.plan) applyUpdate(sessionId, result.plan)
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

  /** 批准并执行（仅待审核的计划） */
  function execute(sessionId, planId, useStream) {
    return runPlanFlow(sessionId, planId, useStream, executePlan)
  }

  /**
   * 从断点续跑（失败/中断/取消过的计划）。
   *
   * 不需要先退回待审核：用户的诉求就是"接着跑完"，多一次"改计划再批准"
   * 只是在中间加一道没有信息量的手续。
   */
  function resume(sessionId, planId, useStream) {
    return runPlanFlow(sessionId, planId, useStream, resumePlan)
  }

  /**
   * 重试规划（规划失败后）。
   *
   * 不占 executing 标志：它是一次普通的模型调用，而"执行中"的语义是
   * "计划步骤正在逐步跑"——混用会让停止按钮在规划期也走计划取消那条路。
   */
  async function retry(sessionId, planId, useStream) {
    try {
      const result = await retryPlan(sessionId, planId, useStream, {
        onPlanUpdate: (p) => applyUpdate(sessionId, p),
      })
      if (result?.plan) applyUpdate(sessionId, result.plan)
      return result
    } catch (e) {
      console.error('重试规划失败:', e)
      return { error: e?.message || String(e) }
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
    clearPlan,
    save,
    execute,
    resume,
    retry,
    reopen,
    cancel,
    reset,
  }
})
