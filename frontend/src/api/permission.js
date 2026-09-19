import {
  GetPermissionState,
  GetPermissionAudit,
  ResolvePermission,
  CancelPermissionWait,
  AddPermissionRule,
  RemovePermissionRule,
  ClearPermissionGrants,
  SetSessionPermissionMode,
} from '@/../wailsjs/go/main/App'
import { DEFAULT_PERMISSION_MODE } from '@/types'

/**
 * 权限管理 API 层。
 * Wails 环境下调用后端 bound 方法；浏览器开发模式下用 localStorage 模拟，
 * 保证界面可以在没有后端的情况下被调通。
 */

function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

const MOCK_MODE_KEY = 'local-agent:permission-mode'
const MOCK_RULES_KEY = 'local-agent:permission-rules'

function readMockRules() {
  try {
    const raw = localStorage.getItem(MOCK_RULES_KEY)
    return raw ? JSON.parse(raw) : []
  } catch {
    return []
  }
}

function writeMockRules(rules) {
  localStorage.setItem(MOCK_RULES_KEY, JSON.stringify(rules))
}

function mockMode() {
  return localStorage.getItem(MOCK_MODE_KEY) || DEFAULT_PERMISSION_MODE
}

/**
 * 获取会话的权限现状（模式、生效规则、来源、会话授权、提示）
 * @param {string} sessionId
 */
export async function fetchPermissionState(sessionId) {
  if (isWails()) {
    return await GetPermissionState(sessionId)
  }
  return {
    sessionId,
    mode: mockMode(),
    projectDir: '（浏览器 mock 模式：无真实工作目录）',
    rules: readMockRules(),
    sources: [
      {
        scope: 'user',
        path: '~/.local-agent/permissions.json',
        exist: false,
        mode: '',
        deny: 0,
        ask: 0,
        allow: 0,
      },
    ],
    grants: [],
    warnings: ['浏览器开发模式：权限判定由后端执行，此处仅为界面演示'],
  }
}

/**
 * 获取会话的判定审计记录
 * @param {string} sessionId
 */
export async function fetchPermissionAudit(sessionId) {
  if (isWails()) {
    return await GetPermissionAudit(sessionId)
  }
  return []
}

/**
 * 应答一次授权请求
 * @param {{id:string, decision:string, scope?:string, rule?:string, layer?:string}} answer
 */
export async function resolvePermission({ id, decision, scope = 'once', rule = '', layer = '' }) {
  if (isWails()) {
    return await ResolvePermission(id, decision, scope, rule, layer)
  }
  return undefined
}

/**
 * 取消会话内挂起的授权等待（「停止」按钮）
 * @param {string} sessionId
 */
export async function cancelPermissionWait(sessionId) {
  if (isWails()) {
    return await CancelPermissionWait(sessionId)
  }
  return undefined
}

/**
 * 新增一条规则
 * @param {string} sessionId
 * @param {string} scope user / project / local
 * @param {string} bucket deny / ask / allow
 * @param {string} rule 规则文本，如 exec_shell(git status)
 */
export async function addRule(sessionId, scope, bucket, rule) {
  if (isWails()) {
    return await AddPermissionRule(sessionId, scope, bucket, rule)
  }
  const rules = readMockRules()
  rules.push({ rule, bucket, source: `${scope}（mock）` })
  writeMockRules(rules)
}

/**
 * 移除一条规则
 * @param {string} sessionId
 * @param {string} scope user / project / local
 * @param {string} bucket deny / ask / allow
 * @param {string} rule 规则文本
 */
export async function removeRule(sessionId, scope, bucket, rule) {
  if (isWails()) {
    return await RemovePermissionRule(sessionId, scope, bucket, rule)
  }
  writeMockRules(readMockRules().filter((r) => !(r.rule === rule && r.bucket === bucket)))
}

/**
 * 清空会话授权（"忘掉本会话的放行记录"）
 * @param {string} sessionId
 */
export async function clearGrants(sessionId) {
  if (isWails()) {
    return await ClearPermissionGrants(sessionId)
  }
  return undefined
}

/**
 * 设置会话权限模式（后端校验后落盘）
 * @param {string} sessionId
 * @param {string} mode
 */
export async function setPermissionMode(sessionId, mode) {
  if (isWails()) {
    return await SetSessionPermissionMode(sessionId, mode)
  }
  localStorage.setItem(MOCK_MODE_KEY, mode)
  return { id: sessionId, permissionMode: mode }
}
