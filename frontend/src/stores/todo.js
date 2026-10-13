import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getTodos } from '@/api/todo'
import { useSessionStore } from '@/stores/session'

/**
 * 待办清单 Store —— 每个会话各占一个桶
 *
 * 与 plan store 同构：`todo_update` 是后端推来的事件，它属于**产生它的那条会话**。
 * 只存一份的话，A 会话改清单会让正在看的 B 会话面板跟着变（多会话并行时的经典串台）。
 */
export const useTodoStore = defineStore('todo', () => {
  const sessionStore = useSessionStore()

  // sessionId -> TodoItem[]
  const todosBySession = ref({})

  const currentId = computed(() => sessionStore.currentSessionId)
  const todos = computed(() => todosBySession.value[currentId.value] || [])

  // 已完成数：面板标题上"3/7"这类进度用
  const doneCount = computed(() => todos.value.filter((t) => t.status === 'done').length)

  /** 取某会话的清单（不传则当前会话） */
  function todosOf(sessionId = currentId.value) {
    return todosBySession.value[sessionId] || []
  }

  /**
   * 整表替换某会话的清单。
   *
   * 事件推的就是全量清单，所以这里不做增量合并——清单很短，全量替换省掉了
   * 「删除/重排时与服务端状态漂移」这一类问题。
   */
  function setTodos(sessionId, items) {
    if (!sessionId) return
    todosBySession.value = { ...todosBySession.value, [sessionId]: Array.isArray(items) ? items : [] }
  }

  /** 主动拉一次（面板打开 / 切回会话时调用，补上事件没覆盖到的场景） */
  async function loadForSession(sessionId) {
    if (!sessionId) return
    try {
      setTodos(sessionId, await getTodos(sessionId))
    } catch (e) {
      console.error('[todo] 读取待办清单失败:', e)
    }
  }

  /** 丢弃某会话的清单（会话被删除时调用，避免桶里留孤儿数据） */
  function drop(sessionId) {
    if (!sessionId) return
    const next = { ...todosBySession.value }
    delete next[sessionId]
    todosBySession.value = next
  }

  return {
    todosBySession,
    todos,
    doneCount,
    todosOf,
    setTodos,
    loadForSession,
    drop,
  }
})
