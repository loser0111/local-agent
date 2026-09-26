import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  fetchPermissionState,
  fetchPermissionAudit,
  resolvePermission,
  cancelPermissionWait,
  addRule as apiAddRule,
  removeRule as apiRemoveRule,
  clearGrants as apiClearGrants,
  setPermissionMode as apiSetMode,
} from '@/api/permission'
import { DEFAULT_PERMISSION_MODE } from '@/types'
import { useSessionStore } from '@/stores/session'

/**
 * 权限管理 Store
 *
 * - pending：**当前显示会话**等待用户应答的授权请求（后端阻塞等待，超时/取消按拒绝处理）
 * - state：会话的权限现状（模式 / 生效规则 / 来源 / 会话授权 / 提示）
 * - audit：判定审计记录
 *
 * pending 为什么按会话分桶：后端是**阻塞**等应答的，而多会话并行下完全可能
 * "A 在等授权、用户正在看 B"。改造前只有一个 pending 槽位，于是 A 的授权框会弹在 B 上，
 * 应答后 B 里那张工具卡片被标成"等待授权"（因为标记逻辑扫的是当前会话的消息）。
 * 现在的规矩是：弹窗只弹**当前会话**那条；后台会话的请求在会话列表中出标记。
 */
export const usePermissionStore = defineStore('permissions', () => {
  const sessionStore = useSessionStore()

  // sessionId -> PermissionAskRequest
  const pendingBySession = ref({})
  const state = ref(null)
  const audit = ref([])
  const loading = ref(false)
  const error = ref('')
  const answering = ref(false)

  const currentId = computed(() => sessionStore.currentSessionId)
  const pending = computed(() => pendingBySession.value[currentId.value] || null)
  const hasPending = computed(() => !!pending.value)
  /** 所有有挂起授权请求的会话（会话列表打标记用） */
  const pendingSessionIds = computed(() => Object.keys(pendingBySession.value))

  const mode = computed(() => state.value?.mode || DEFAULT_PERMISSION_MODE)
  const rules = computed(() => state.value?.rules || [])
  const sources = computed(() => state.value?.sources || [])
  const grants = computed(() => state.value?.grants || [])
  const warnings = computed(() => state.value?.warnings || [])

  /** 后端推来一条授权请求（阻塞等待中的那一条），按它的会话归档 */
  function setPending(req) {
    if (!req || !req.id || !req.sessionId) return
    pendingBySession.value = { ...pendingBySession.value, [req.sessionId]: req }
  }

  /** 清掉某会话的挂起请求（不传则当前会话） */
  function clearPending(sessionId = currentId.value) {
    if (!sessionId) return
    const next = { ...pendingBySession.value }
    delete next[sessionId]
    pendingBySession.value = next
  }

  /** 载入会话权限现状 */
  async function load(sessionId) {
    if (!sessionId) return
    loading.value = true
    error.value = ''
    try {
      state.value = await fetchPermissionState(sessionId)
    } catch (e) {
      error.value = `加载权限配置失败：${e.message || e}`
    } finally {
      loading.value = false
    }
  }

  /** 载入审计记录 */
  async function loadAudit(sessionId) {
    if (!sessionId) return
    try {
      audit.value = (await fetchPermissionAudit(sessionId)) || []
    } catch (e) {
      console.warn('加载权限审计失败:', e)
      audit.value = []
    }
  }

  /**
   * 应答授权请求。
   *
   * 请求 ID 在全后端唯一，因此应答天然落到正确的那个会话上——
   * 但清 pending 时要用**请求自带**的会话，不能用"当前显示会话"（用户可能在弹窗弹出后切走）。
   * @param {{decision:'allow'|'deny', scope?:'once'|'session'|'rule', rule?:string, layer?:string}} payload
   */
  async function answer({ decision, scope = 'once', rule = '', layer = '' }) {
    const req = pending.value
    if (!req) return
    answering.value = true
    error.value = ''
    try {
      await resolvePermission({ id: req.id, decision, scope, rule, layer })
      clearPending(req.sessionId)
    } catch (e) {
      error.value = `应答失败：${e.message || e}`
    } finally {
      answering.value = false
    }
  }

  /** 取消当前挂起的授权等待（等价于拒绝） */
  async function cancel(sessionId) {
    try {
      await cancelPermissionWait(sessionId)
    } catch (e) {
      // 没有挂起请求时后端会报错，这里不算异常
      console.warn('取消授权等待:', e.message || e)
    } finally {
      clearPending(sessionId)
    }
  }

  async function addRule(sessionId, scope, bucket, rule) {
    await apiAddRule(sessionId, scope, bucket, rule)
    await load(sessionId)
  }

  async function removeRule(sessionId, scope, bucket, rule) {
    await apiRemoveRule(sessionId, scope, bucket, rule)
    await load(sessionId)
  }

  async function clearGrants(sessionId) {
    await apiClearGrants(sessionId)
    await load(sessionId)
  }

  /** 设置会话权限模式；成功后就地更新状态，避免整页刷新 */
  async function setMode(sessionId, next) {
    await apiSetMode(sessionId, next)
    if (state.value) state.value.mode = next
  }

  function reset() {
    pendingBySession.value = {}
    state.value = null
    audit.value = []
    error.value = ''
  }

  return {
    pendingBySession,
    pending,
    state,
    audit,
    loading,
    error,
    answering,
    mode,
    rules,
    sources,
    grants,
    warnings,
    hasPending,
    pendingSessionIds,
    setPending,
    clearPending,
    load,
    loadAudit,
    answer,
    cancel,
    addRule,
    removeRule,
    clearGrants,
    setMode,
    reset,
  }
})
