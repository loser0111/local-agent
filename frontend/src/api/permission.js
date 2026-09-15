import {
  ResolvePermission,
  GetPermissionConfig,
  SetPermissionMode,
  SetDefaultPermissionMode,
  AddPermissionRule,
  RemovePermissionRule,
  RevokePermissionGrant,
  GetPermissionAudit,
  GetPermissionGrants,
  ClearPermissionGrants,
} from '@/../wailsjs/go/main/App'

/**
 * 判断是否运行在 Wails 桌面环境
 */
function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

// ===== 浏览器 dev mock（localStorage 持久化）=====
//
// 与后端 settings.go 的三层结构对齐：mock 只模拟「用户全局层」，
// 足以验证设置页的增删改与展示，不模拟跨层合并。

const MOCK_KEY = 'local-agent:permission-settings'

export const PERMISSION_MODES = [
  { value: 'default', label: 'Default', desc: '未匹配规则的写操作与命令需人工确认' },
  { value: 'acceptEdits', label: 'Accept edits', desc: '自动批准文件系统类命令，其余仍需确认' },
  { value: 'plan', label: 'Plan', desc: '只读模式：禁止修改与执行' },
  { value: 'bypassPermissions', label: 'Bypass', desc: '自动批准绝大多数操作，deny 规则仍然生效' },
]

export const PERMISSION_BUCKETS = [
  { key: 'allow', label: '允许', desc: '命中即放行' },
  { key: 'ask', label: '询问', desc: '命中即弹窗确认（优先于允许）' },
  { key: 'deny', label: '拒绝', desc: '命中即拒绝，任何模式与授权都无法覆盖' },
]

function defaultMockConfig() {
  return {
    mode: 'default',
    defaultMode: 'default',
    allow: [],
    ask: [],
    deny: [],
    builtinDeny: [
      'exec_shell(rm -rf /)',
      'exec_shell(rm -rf /*)',
      'exec_shell(mkfs:*)',
      'exec_shell(shutdown:*)',
      'exec_shell(reboot:*)',
    ].map(toMockRule),
    builtinAsk: ['.env', '.ssh/', 'id_rsa', '.aws/credentials'].map((p) =>
      toMockRule(`exec_shell(${p})`)
    ),
    sources: ['(浏览器 mock 模式，未读取真实配置文件)'],
    errors: [],
    projectDir: '',
    globalPath: '~/.local-agent/settings.json',
    askTimeoutMs: 300000,
  }
}

/** 把规则文本转成后端 Rule 结构的等价形态，供界面统一渲染 */
function toMockRule(raw, source = 'user') {
  const m = /^([A-Za-z0-9_*-]+)\((.*)\)$/.exec(raw)
  if (!m) {
    return { raw, tool: raw, kind: '', spec: '', isPrefix: false, source }
  }
  const [, tool, inner] = m
  if (inner.startsWith('domain:')) {
    return { raw, tool, kind: 'domain', spec: inner.slice(7), isPrefix: false, source }
  }
  if (inner.endsWith(':*')) {
    return { raw, tool, kind: 'command', spec: inner.slice(0, -2), isPrefix: true, source }
  }
  return { raw, tool, kind: 'command', spec: inner, isPrefix: false, source }
}

function getMockConfig() {
  try {
    const raw = localStorage.getItem(MOCK_KEY)
    if (!raw) {
      const d = defaultMockConfig()
      localStorage.setItem(MOCK_KEY, JSON.stringify(d))
      return d
    }
    return { ...defaultMockConfig(), ...JSON.parse(raw) }
  } catch {
    return defaultMockConfig()
  }
}

function saveMockConfig(cfg) {
  localStorage.setItem(MOCK_KEY, JSON.stringify(cfg))
}

/**
 * 获取权限配置全貌
 * @param {string} sessionId
 * @returns {Promise<object>}
 */
export async function fetchPermissionConfig(sessionId) {
  if (isWails()) {
    return await GetPermissionConfig(sessionId || '')
  }
  return getMockConfig()
}

/**
 * 切换会话权限模式（不落盘）
 */
export async function changePermissionMode(sessionId, mode) {
  if (isWails()) {
    return await SetPermissionMode(sessionId, mode)
  }
  const cfg = getMockConfig()
  cfg.mode = mode
  saveMockConfig(cfg)
}

/**
 * 修改默认权限模式并落盘
 * @param {string} source 目标层：user | project | local
 */
export async function changeDefaultMode(sessionId, mode, source = 'user') {
  if (isWails()) {
    return await SetDefaultPermissionMode(sessionId, mode, source)
  }
  const cfg = getMockConfig()
  cfg.defaultMode = mode
  cfg.mode = mode
  saveMockConfig(cfg)
}

/**
 * 新增规则
 * @param {string} bucket allow | ask | deny
 * @param {string} rule 规则文本，如 exec_shell(git:*)
 * @param {string} source 目标层：user | project | local
 */
export async function addRule(sessionId, bucket, rule, source = 'local') {
  if (isWails()) {
    return await AddPermissionRule(sessionId, bucket, rule, source)
  }
  const cfg = getMockConfig()
  if (source === 'builtin') throw new Error('内置规则不可修改')
  const list = cfg[bucket] || []
  if (!list.some((r) => (typeof r === 'string' ? r : r.raw) === rule)) {
    list.push(toMockRule(rule, source))
  }
  cfg[bucket] = list
  saveMockConfig(cfg)
}

/**
 * 删除规则
 */
export async function removeRule(sessionId, bucket, rule, source = 'local') {
  if (isWails()) {
    return await RemovePermissionRule(sessionId, bucket, rule, source)
  }
  const cfg = getMockConfig()
  if (source === 'builtin') throw new Error('内置规则不可删除')
  cfg[bucket] = (cfg[bucket] || []).filter((r) => (r.raw || r) !== rule)
  saveMockConfig(cfg)
}

/**
 * 对一次授权询问作答
 * @param {string} requestId
 * @param {'allow'|'deny'} decision
 * @param {'once'|'session'|'always'} scope
 * @param {string} rule 用户可选的编辑后规则文本
 */
export async function respondPermission(requestId, decision, scope, rule = '') {
  if (isWails()) {
    return await ResolvePermission(requestId, decision, scope, rule)
  }
  // mock 模式没有后端在等待，直接返回；弹窗行为由 store 驱动
  return null
}

/**
 * 逐条撤销会话授权。
 * 「永久」级别的授权会连带删除配置文件中对应的允许规则，否则规则会把它带回来。
 */
export async function revokeGrant(sessionId, grant) {
  if (isWails()) {
    return await RevokePermissionGrant(
      sessionId,
      grant.toolName || '',
      grant.spec || '',
      !!grant.isPrefix,
      grant.kind || ''
    )
  }
  // mock 模式没有后端授权存储，直接返回
  return null
}

/**
 * 获取权限决策审计
 */
export async function fetchAudit(sessionId) {
  if (isWails()) {
    return await GetPermissionAudit(sessionId)
  }
  return []
}

/**
 * 获取当前会话的授权列表
 */
export async function fetchGrants(sessionId) {
  if (isWails()) {
    return await GetPermissionGrants(sessionId)
  }
  return []
}

/**
 * 清空当前会话的授权
 */
export async function clearGrants(sessionId) {
  if (isWails()) {
    return await ClearPermissionGrants(sessionId)
  }
}

export { toMockRule }
