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

/**
 * 权限管理 Store
 *
 * - pending：当前等待用户应答的授权请求（后端阻塞等待，超时/取消按拒绝处理）
 * - state：会话的权限现状（模式 / 生效规则 / 来源 / 会话授权 / 提示）
 * - audit：判定审计记录
 */
export const usePermissionStore = defineStore('permissions', () => {
  const pending = ref(null)
  const state = ref(null)
  const audit = ref([])
  const loading = ref(false)
  const error = ref('')
  const answering = ref(false)

  const mode = computed(() => state.value?.mode || DEFAULT_PERMISSION_MODE)
  const rules = computed(() => state.value?.rules || [])
  const sources = computed(() => state.value?.sources || [])
  const grants = computed(() => state.value?.grants || [])
  const warnings = computed(() => state.value?.warnings || [])
  const hasPending = computed(() => !!pending.value)

  /** 后端推来一条授权请求（阻塞等待中的那一条） */
  function setPending(req) {
    if (!req || !req.id) return
    pending.value = req
  }

  function clearPending() {
    pending.value = null
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
   * 应答授权请求
   * @param {{decision:'allow'|'deny', scope?:'once'|'session'|'rule', rule?:string, layer?:string}} payload
   */
  async function answer({ decision, scope = 'once', rule = '', layer = '' }) {
    const req = pending.value
    if (!req) return
    answering.value = true
    error.value = ''
    try {
      await resolvePermission({ id: req.id, decision, scope, rule, layer })
      clearPending()
    } catch (e) {
      // 应答失败多半是请求已超时/被取消（后端报"不存在"）：此时弹窗已无意义，
      // 一并关掉，只留错误提示——否则弹窗会永久悬挂。
      clearPending()
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
      clearPending()
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
    pending.value = null
    state.value = null
    audit.value = []
    error.value = ''
  }

  return {
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
