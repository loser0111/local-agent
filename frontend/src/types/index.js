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
 * 差异块
 * @typedef {Object} DiffHunk
 * @property {string} header 形如 @@ -12,7 +14,9 @@
 * @property {DiffLine[]} lines
 */

/**
 * 差异文件
 * @typedef {Object} DiffFile
 * @property {string} path 相对项目根目录，正斜杠
 * @property {string} [oldPath] 重命名时使用
 * @property {'added'|'modified'|'deleted'|'renamed'} status
 * @property {number} additions
 * @property {number} deletions
 * @property {DiffHunk[]} hunks
 */

/**
 * 差异行
 * @typedef {Object} DiffLine
 * @property {'add'|'del'|'context'} type
 * @property {number} oldLineNo add 行为 0
 * @property {number} newLineNo del 行为 0
 * @property {string} content
 */

/**
 * 轮次差异（turn=0 表示累计）
 * @typedef {Object} DiffTurn
 * @property {number} turn
 * @property {string} label
 * @property {DiffFile[]} files
 * @property {number} additions
 * @property {number} deletions
 * @property {number} createdAt
 */

/**
 * 技能元数据（L1，常驻 system prompt 的部分）
 * @typedef {Object} SkillMeta
 * @property {string} id 技能目录名，唯一主键
 * @property {string} name frontmatter.name
 * @property {string} description frontmatter.description
 * @property {string} dir 技能目录绝对路径
 * @property {boolean} enabled 全局启用开关
 * @property {boolean} alwaysInject true=正文直接注入 system prompt
 * @property {boolean} builtin 内置不可删
 * @property {boolean} hasScripts 是否存在 scripts/ 目录
 * @property {string} error 解析/校验错误
 */

/**
 * 技能详情（含 SKILL.md 正文，L2 按需加载）
 * @typedef {SkillMeta & {body: string}} SkillDetail
 */

/**
 * 计划步骤
 * @typedef {Object} PlanStep
 * @property {number} index
 * @property {string} title
 * @property {string} [detail]
 * @property {'pending'|'running'|'done'|'failed'|'skipped'} status
 * @property {string} [summary] 执行结果摘要（done 后）
 * @property {string} [error] 失败原因
 * @property {number} [startedAt]
 * @property {number} [finishedAt]
 */

/**
 * 计划
 * @typedef {Object} Plan
 * @property {string} id
 * @property {string} sessionId
 * @property {string} title
 * @property {'awaiting_approval'|'running'|'completed'|'failed'|'cancelled'} status
 * @property {PlanStep[]} steps
 * @property {number} createdAt
 * @property {number} updatedAt
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
  { value: 'verbose', label: 'Verbose', desc: '完整展示工具调用过程，参数与输出默认展开' },
  { value: 'normal', label: 'Normal', desc: '执行中展开、完成后折叠，可手动展开（默认）' },
  { value: 'summary', label: 'Summary', desc: '仅保留一行摘要，隐藏工具细节；工具报错时强制展开' },
]

/** 视图模式默认值（新建会话与旧数据兜底，与 Go 侧 DefaultViewMode 保持一致） */
export const DEFAULT_VIEW_MODE = 'normal'

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
