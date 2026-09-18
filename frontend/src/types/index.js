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
 * @typedef {'manual'|'acceptEdits'|'plan'|'auto'} PermissionMode
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
 * 技能内一个可读资源（L3）
 * @typedef {Object} SkillResource
 * @property {string} path 相对技能目录的路径，如 references/api.md
 * @property {number} size 字节数
 * @property {'scripts'|'references'|'assets'|'other'} kind 归类
 */

/**
 * 技能安装来源（手动创建的技能没有该字段）
 * @typedef {Object} SkillInstallInfo
 * @property {'folder'|'zip'|'git'|'manual'} sourceType
 * @property {string} [source] 原始路径或 git URL
 * @property {string} [ref] git 分支/标签
 * @property {string} [subdir] 仓库内子目录
 * @property {number} [installedAt] Unix 秒
 * @property {boolean} canUpdate git 来源可重新拉取
 */

/**
 * 技能元数据（L1；进入 system prompt 的只有 id 与 description）
 * @typedef {Object} SkillMeta
 * @property {string} id 技能目录名，唯一主键
 * @property {string} name frontmatter.name（缺失时为目录名）
 * @property {string} description frontmatter.description，模型据此判断是否触发
 * @property {string} dir 技能目录绝对路径
 * @property {boolean} enabled 全局启用开关
 * @property {boolean} builtin 内置不可删
 * @property {string} [version] frontmatter.version
 * @property {string} [license] frontmatter.license
 * @property {string} [author] frontmatter.author
 * @property {string[]} [allowedTools] frontmatter.allowed-tools
 * @property {boolean} disableModelInvocation true=模型不可自动触发，仅允许 /名称 显式调用
 * @property {boolean} userInvocable false=不允许 /名称 显式调用
 * @property {boolean} hasScripts 是否存在 scripts/ 目录
 * @property {SkillResource[]} [resources] 技能内可读资源（L3）
 * @property {string[]} [errors] 阻断性错误（界面标红、不参与路由）
 * @property {string[]} [warnings] 提示性告警（不影响使用）
 * @property {SkillInstallInfo} [install] 安装来源
 */

/**
 * 技能详情（含 SKILL.md 正文与完整 frontmatter，L2 按需加载）
 * @typedef {SkillMeta & {body: string, frontmatter?: Object}} SkillDetail
 */

/**
 * 保存技能的表单（编辑器提交）
 * @typedef {Object} SkillDraft
 * @property {string} name
 * @property {string} description
 * @property {string} [version]
 * @property {string} [license]
 * @property {string} [author]
 * @property {string[]} [allowedTools]
 * @property {boolean} [disableModelInvocation]
 * @property {boolean} userInvocable
 * @property {string} body
 */

/**
 * 安装结果
 * @typedef {Object} SkillInstallResult
 * @property {string} id
 * @property {string} name
 * @property {string} dir
 * @property {'installed'|'updated'} action
 * @property {string[]} [warnings]
 */

/**
 * 一次真实发出的 LLM 请求快照（排障用）。
 * 注意与会话里存的消息不同：会话存原文，这份是压缩与工具结果预算**之后**实际交给模型的序列。
 * @typedef {Object} LLMRequestSnapshot
 * @property {string} sessionId
 * @property {string} [runId]
 * @property {number} turn
 * @property {string} model
 * @property {number} at Unix 毫秒
 * @property {string} systemPrompt
 * @property {Array<{role: string, content: string, tool_call_id?: string, tool_calls?: Array}>} messages
 * @property {number} summaryIndex 摘要说明消息的下标（-1 表示本次无摘要）
 * @property {string[]} toolNames 工具定义只留名字，不含 schema
 * @property {number} toolSchemaTokens 工具定义的估算开销
 * @property {number} estimatedTokens
 * @property {number} windowTokens
 * @property {boolean} compactedThisTurn 本轮是否刚做过摘要压缩
 * @property {number} coveredMsgs 摘要累计覆盖的消息条数
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
  { value: 'manual', label: 'Manual', desc: '写操作与命令都需确认（只读白名单除外）' },
  {
    value: 'acceptEdits',
    label: 'Accept edits',
    desc: '文件编辑类自动放行、命令仍需确认（待 write_file/edit_file 工具落地后生效）',
  },
  { value: 'plan', label: 'Plan', desc: '只读探索：非只读操作直接拒绝' },
  {
    value: 'auto',
    label: 'Auto',
    desc: '仅项目目录内的本地写操作自动放行；越界路径、网络、包管理、解释器、删除类仍会询问',
  },
]

/** 权限模式默认值（与 Go 侧 ModeManual 保持一致：无法识别时一律回退到最严格的模式） */
export const DEFAULT_PERMISSION_MODE = 'manual'

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
