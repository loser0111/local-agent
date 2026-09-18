import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  fetchPluginInfo,
  fetchTasks,
  fetchTaskRuns,
  createTask,
  updateTask,
  deleteTask,
  setTaskEnabled,
  runTaskNow,
  snoozeTask,
  completeTask,
  fetchTaskSettings,
  updateTaskSettings,
  pauseAllTasks,
  previewTriggerTimes,
  fetchTaskCounts,
  onTaskEvent,
} from '@/api/task'

/**
 * 定时任务 Store
 *
 * 两条纪律：
 *  1. **状态由后端单点驱动**：收到 task:event 后重新拉取，界面绝不自己推断
 *     「下次触发」「是否完成」这类状态 —— 否则界面与真实调度必然漂移。
 *  2. 列表与详情分开持有：详情（历史）按需加载，避免列表页拖着一堆历史。
 */
export const useTaskStore = defineStore('task', () => {
  const info = ref(null)
  const tasks = ref([])
  const settings = ref(null)
  const counts = ref({ total: 0, limit: 200 })
  const runs = ref({})

  const loading = ref(false)
  const loaded = ref(false)
  const error = ref('')
  // 最近一次事件，供界面提示（例如「通知已降级为应用内提醒」）
  const lastEvent = ref(null)
  // 应用内提醒队列（F3.5：系统通知不可用/免打扰时的兜底展示）
  const inAppNotices = ref([])

  let unsubscribe = null

  /** 插件是否可用 */
  const available = computed(() => !!info.value && info.value.state !== 'unavailable')

  /** 启用中的任务数 */
  const enabledCount = computed(() => tasks.value.filter((t) => t.enabled).length)

  /** 已暂停（全局） */
  const paused = computed(() => !!settings.value?.paused)

  async function load(force = false) {
    if (loaded.value && !force) return
    loading.value = true
    error.value = ''
    try {
      info.value = await fetchPluginInfo()
      if (!available.value) {
        tasks.value = []
        return
      }
      tasks.value = await fetchTasks()
      settings.value = await fetchTaskSettings()
      const [total, limit] = await fetchTaskCounts()
      counts.value = { total, limit }
      loaded.value = true
    } catch (e) {
      error.value = describeError(e)
      console.error('加载定时任务失败:', e)
    } finally {
      loading.value = false
    }
  }

  /** 只刷新列表与计数（收到变更事件后调用） */
  async function refresh() {
    if (!available.value) return
    try {
      tasks.value = await fetchTasks()
      const [total, limit] = await fetchTaskCounts()
      counts.value = { total, limit }
    } catch (e) {
      console.error('刷新定时任务失败:', e)
    }
  }

  /** 订阅后端事件。返回取消函数，交给组件 onUnmounted 调用。 */
  function subscribe() {
    if (unsubscribe) return unsubscribe
    unsubscribe = onTaskEvent((ev) => {
      lastEvent.value = ev
      if (!ev || !ev.type) return
      switch (ev.type) {
        case 'task:changed':
        case 'task:fired':
        case 'task:missed':
        case 'task:run:finished':
          refresh()
          break
        case 'task:notify:inapp':
          // 应用内提醒：加入队列由界面展示
          inAppNotices.value = [...inAppNotices.value, { ...ev.payload, at: ev.at }]
          break
        case 'task:notify:fallback':
          refresh()
          break
        case 'task:reveal':
          // 通知被点击：由界面负责切换面板并定位任务
          break
        default:
          break
      }
    })
    return unsubscribe
  }

  function unsubscribeEvents() {
    if (unsubscribe) {
      unsubscribe()
      unsubscribe = null
    }
  }

  function dismissNotice(index) {
    inAppNotices.value = inAppNotices.value.filter((_, i) => i !== index)
  }

  async function create(input) {
    return withError(async () => {
      const task = await createTask(input)
      await refresh()
      return task
    })
  }

  async function update(id, input) {
    return withError(async () => {
      const task = await updateTask(id, input)
      await refresh()
      return task
    })
  }

  async function remove(id, deleteHistory = false) {
    return withError(async () => {
      await deleteTask(id, deleteHistory)
      await refresh()
    })
  }

  async function toggle(id, enabled) {
    return withError(async () => {
      await setTaskEnabled(id, enabled)
      await refresh()
    })
  }

  async function runNow(id) {
    return withError(async () => {
      const rec = await runTaskNow(id)
      await refresh()
      return rec
    })
  }

  async function snooze(id, minutes = 0) {
    return withError(async () => {
      await snoozeTask(id, minutes)
      await refresh()
    })
  }

  async function complete(id) {
    return withError(async () => {
      await completeTask(id)
      await refresh()
    })
  }

  async function loadRuns(taskID, limit = 50) {
    return withError(async () => {
      runs.value = { ...runs.value, [taskID]: await fetchTaskRuns(taskID, limit) }
      return runs.value[taskID]
    })
  }

  async function preview(trigger, n = 3) {
    return withError(async () => await previewTriggerTimes(trigger, n))
  }

  async function saveSettings(cfg) {
    return withError(async () => {
      await updateTaskSettings(cfg)
      settings.value = await fetchTaskSettings()
    })
  }

  async function setPaused(value) {
    return withError(async () => {
      await pauseAllTasks(value)
      settings.value = await fetchTaskSettings()
      await refresh()
    })
  }

  /**
   * 统一错误出口：把后端错误提示原样上抛给调用方做 toast，
   * 同时记录下来，避免「点了没反应」。
   */
  async function withError(fn) {
    error.value = ''
    try {
      return await fn()
    } catch (e) {
      const msg = describeError(e)
      error.value = msg
      throw new Error(msg)
    }
  }

  return {
    info,
    tasks,
    settings,
    counts,
    runs,
    loading,
    loaded,
    error,
    lastEvent,
    inAppNotices,
    available,
    enabledCount,
    paused,
    load,
    refresh,
    subscribe,
    unsubscribeEvents,
    dismissNotice,
    create,
    update,
    remove,
    toggle,
    runNow,
    snooze,
    complete,
    loadRuns,
    preview,
    saveSettings,
    setPaused,
  }
})

function describeError(e) {
  if (!e) return '未知错误'
  if (typeof e === 'string') return e
  return e.message || String(e)
}
