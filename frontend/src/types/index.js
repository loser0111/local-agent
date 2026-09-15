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
 * 视图模式
 * @typedef {'verbose'|'normal'|'summary'} ViewMode
 */

/**
 * 权限模式
 * @typedef {'default'|'acceptEdits'|'plan'|'bypassPermissions'} PermissionMode
 */

/**
 * 权限规则
 * @typedef {Object} PermissionRule
 * @property {string} raw 规则原文，如 exec_shell(git:*)
 * @property {string} tool 工具名部分
 * @property {''|'command'|'domain'} kind 限定符类型，空串表示工具级
 * @property {string} spec 限定符值
 * @property {boolean} isPrefix 命令类是否前缀匹配
 * @property {'builtin'|'user'|'project'|'local'} source 规则来源层
 */

/**
 * 授权询问（后端 permission_request 事件载荷）
 * @typedef {Object} PermissionRequest
 * @property {string} requestId 作答时回传的关联 ID
 * @property {string} [sessionId]
 * @property {string} [toolCallId] 用于把工具卡片标为「等待授权」
 * @property {string} toolName 真实工具名（非 tool_router）
 * @property {string} [toolLabel] 界面显示名
 * @property {'read'|'write'|'network'|'process'} risk 风险级别
 * @property {string} [command] 命令类工具最终要执行的命令行
 * @property {string[]} [argv] 命令分解后的各段
 * @property {string} summary 一句话描述
 * @property {string} reason 为什么需要询问
 * @property {string} [matchedRule] 触发的 ask 规则（避免被误认为 bug）
 * @property {string} [ruleSource] 该规则的来源层
 * @property {string} [suggestRule] 建议写入的规则文本
 * @property {object} [raw] 工具原始参数
 */

/**
 * 工具调用状态
 * @typedef {'running'|'pending'|'success'|'denied'|'error'} ToolCallStatus
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

export const SESSION_STATUS = {
  ACTIVE: 'active',
  COMPLETED: 'completed',
  WAITING: 'waiting',
  ERROR: 'error',
  ARCHIVED: 'archived',
}

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
