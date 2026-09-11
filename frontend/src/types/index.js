/**
 * 会话状态
 * @typedef {'active'|'completed'|'waiting'|'error'|'archived'} SessionStatus
 */

/**
 * 会话
 * @typedef {Object} Session
 * @property {string} id
 * @property {string} title
 * @property {SessionStatus} status
 * @property {string} project
 * @property {number} lastActiveAt
 * @property {number} createdAt
 * @property {string} model
 * @property {string} permissionMode
 * @property {string[]} openPanes
 */

/**
 * 消息角色
 * @typedef {'user'|'assistant'|'system'} MessageRole
 */

/**
 * 工具调用状态
 * @typedef {'running'|'success'|'error'} ToolCallStatus
 */

/**
 * 工具调用
 * @typedef {Object} ToolCall
 * @property {string} id
 * @property {string} name
 * @property {object} args
 * @property {ToolCallStatus} status
 * @property {number} [duration]
 * @property {string} [result]
 */

/**
 * 消息
 * @typedef {Object} Message
 * @property {string} id
 * @property {MessageRole} role
 * @property {string} content
 * @property {ToolCall[]} [toolCalls]
 * @property {number} createdAt
 * @property {boolean} [streaming]
 */

/**
 * 面板类型
 * @typedef {'chat'|'diff'|'terminal'|'file-editor'|'plan'|'tasks'|'subagent'|'preview'} PaneType
 */

/**
 * 权限模式
 * @typedef {'manual'|'acceptEdits'|'plan'|'auto'|'bypassPermissions'} PermissionMode
 */

/**
 * 视图模式
 * @typedef {'verbose'|'normal'|'summary'} ViewMode
 */

/**
 * 差异文件
 * @typedef {Object} DiffFile
 * @property {string} path
 * @property {number} additions
 * @property {number} deletions
 * @property {DiffLine[]} lines
 */

/**
 * 差异行
 * @typedef {Object} DiffLine
 * @property {'add'|'del'|'context'} type
 * @property {number} oldLineNo
 * @property {number} newLineNo
 * @property {string} content
 */

export const SESSION_STATUS = {
  ACTIVE: 'active',
  COMPLETED: 'completed',
  WAITING: 'waiting',
  ERROR: 'error',
  ARCHIVED: 'archived',
}

export const PERMISSION_MODES = [
  { value: 'manual', label: 'Manual', desc: '编辑文件或运行命令前均需确认' },
  { value: 'acceptEdits', label: 'Accept edits', desc: '自动接受文件编辑，命令仍需确认' },
  { value: 'plan', label: 'Plan', desc: '只读探索并提出计划，不修改代码' },
  { value: 'auto', label: 'Auto', desc: '后台安全检查，减少权限提示' },
]

export const VIEW_MODES = [
  { value: 'verbose', label: 'Verbose' },
  { value: 'normal', label: 'Normal' },
  { value: 'summary', label: 'Summary' },
]

export const PANE_TITLES = {
  chat: 'Chat',
  diff: 'Diff',
  terminal: 'Terminal',
  'file-editor': 'File Editor',
  plan: 'Plan',
  tasks: 'Tasks',
  subagent: 'Subagents',
  preview: 'Preview',
}
