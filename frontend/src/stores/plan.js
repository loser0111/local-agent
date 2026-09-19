import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getPlan, savePlan, executePlan, cancelPlan, reopenPlan } from '@/api/plan'
import { useChatStore } from '@/stores/chat'

/**
 * 计划 Store —— 维护当前会话的最新计划与执行状态
 * 执行期间复用 chat store 的消息链路：为每个 running 步骤创建流式占位消息，
 * 工具卡片/流式分片/步骤用户消息全部进聊天流（聊天流即审计日志）
 */
export const usePlanStore = defineStore('plan', () => {
  const plan = ref(null) // 当前会话最新计划
  const executing = ref(false) // 计划执行中（与 chat.isGenerating 互斥置位）

  /**
   * 加载指定会话的最新计划（切换会话/打开面板时调用）
   */
  async function loadForSession(sessionId) {
    if (!sessionId) {
      plan.value = null
      return
    }
    try {
      plan.value = await getPlan(sessionId)
    } catch (e) {
      console.error('加载计划失败:', e)
      plan.value = null
    }
  }

  /**
   * plan_update 事件/返回值落库
   */
  function applyUpdate(p) {
    if (p) plan.value = p
  }

  /**
   * 保存审核编辑
   * @param {object} edited 编辑后的计划（title/steps）
   */
  async function save(edited) {
    const saved = await savePlan(edited)
    plan.value = saved
    return saved
  }

  /**
   * 批准并执行计划：逐步执行，过程渲染进聊天流
   * @param {string} planId
   * @param {boolean} useStream
   */
  async function execute(planId, useStream) {
    if (executing.value) return null
    const chatStore = useChatStore()
    executing.value = true
    chatStore.isGenerating = true

    let placeholder = null // 当前 running 步骤的流式占位消息

    const finalizePlaceholder = (step) => {
      if (!placeholder) return
      const patch = { streaming: false }
      if (!placeholder.content && step?.summary) patch.content = step.summary
      if (!placeholder.content && step?.error) patch.content = `步骤失败：${step.error}`
      chatStore.updateLocalMessage(placeholder.id, patch)
      placeholder = null
    }

    try {
      const result = await executePlan(planId, useStream, {
        onPlanUpdate: (p) => {
          plan.value = p
          const running = p?.steps?.find((s) => s.status === 'running')
          if (running) {
            // 新步骤开始：先补步骤用户消息（与后端持久化内容一致），再建流式占位
            if (!placeholder || placeholder.__stepIndex !== running.index) {
              finalizePlaceholder(null)
              const stepQuery = running.detail
                ? `【计划步骤 ${running.index + 1}/${p.steps.length}】${running.title}\n${running.detail}`
                : `【计划步骤 ${running.index + 1}/${p.steps.length}】${running.title}`
              chatStore.addLocalMessage({
                id: `plan-user-${running.index}-${Date.now()}`,
                role: 'user',
                content: stepQuery,
                createdAt: Date.now(),
                streaming: false,
              })
              placeholder = chatStore.addLocalMessage({
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
          if (placeholder) chatStore.appendStreamContent(placeholder.id, chunk)
        },
        onToolCallStart: (tc) => {
          if (placeholder) chatStore.addToolCall(placeholder.id, tc)
        },
        onToolCallEnd: (tc) => {
          if (placeholder) {
            chatStore.updateToolCall(placeholder.id, tc.id, {
              status: tc.status,
              duration: tc.duration,
              result: tc.result,
            })
          }
        },
      })

      finalizePlaceholder(null)

      if (result?.plan) plan.value = result.plan
      if (result?.reply) {
        chatStore.addLocalMessage({
          id: `plan-reply-${Date.now()}`,
          role: 'assistant',
          content: result.reply,
          createdAt: Date.now(),
          streaming: false,
        })
      }
      if (result?.error && !result.reply) {
        chatStore.addLocalMessage({
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
      chatStore.addLocalMessage({
        id: `plan-error-${Date.now()}`,
        role: 'assistant',
        content: `计划执行失败：${e?.message || e}`,
        createdAt: Date.now(),
        streaming: false,
      })
      return null
    } finally {
      executing.value = false
      chatStore.isGenerating = false
    }
  }

  /**
   * 把已结束的计划退回待审核（失败后「修改后重试」）：
   * 已完成步骤保留，失败/跳过步骤重置为待执行，随后即可编辑并再次批准执行。
   * @param {string} planId
   */
  async function reopen(planId) {
    const p = await reopenPlan(planId)
    plan.value = p
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

  function reset() {
    plan.value = null
  }

  return {
    plan,
    executing,
    loadForSession,
    applyUpdate,
    save,
    execute,
    reopen,
    cancel,
    reset,
  }
})
