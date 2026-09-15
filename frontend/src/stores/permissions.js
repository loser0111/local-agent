import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { respondPermission } from '@/api/permission'

/**
 * 授权询问 Store
 *
 * 后端在执行工具前会阻塞等待用户作答，因此这里维护一个「待答队列」：
 * 后端一次只会阻塞在一个工具调用上，但保留队列可以稳妥应对并发工具调用
 * （LLM 单轮返回多个 tool_calls 时）。
 */
export const usePermissionsStore = defineStore('permissions', () => {
  const queue = ref([])
  const submitting = ref(false)

  /** 队首请求 = 当前需要用户作答的那一个 */
  const current = computed(() => queue.value[0] || null)
  const visible = computed(() => queue.value.length > 0)

  /** 入队（按 requestId 去重，避免重复事件把同一个请求push两次） */
  function enqueue(req) {
    if (!req || !req.requestId) return
    if (queue.value.some((r) => r.requestId === req.requestId)) return
    queue.value.push(req)
  }

  /**
   * 作答并出队
   * @param {'allow'|'deny'} decision
   * @param {'once'|'session'|'always'} scope
   * @param {string} [rule] 「永久允许」时写入的规则文本
   */
  async function respond(decision, scope, rule = '') {
    const req = queue.value[0]
    if (!req) return
    submitting.value = true
    try {
      await respondPermission(req.requestId, decision, scope, rule)
    } catch (e) {
      // 后端可能已超时或取消，此时作答会失败 —— 记录但不阻断 UI
      console.error('提交授权结果失败:', e)
    } finally {
      submitting.value = false
      queue.value.shift()
    }
  }

  /** 清空队列（切换会话/取消对话时调用） */
  function clear() {
    queue.value = []
  }

  return { queue, current, visible, submitting, enqueue, respond, clear }
})
