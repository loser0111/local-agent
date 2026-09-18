/**
 * 定时任务（desktop-plugin）类型定义
 *
 * 与 Go 侧 `plugin/` 包一一对应；字段的**语义边界**也照抄，因为它们本身就是需求：
 *  - `enabled` 是「用户想不想让它跑」，`state` 是「它跑到哪了」，两者严格分开；
 *  - `nextFireAt` 是派生值（每次由后端 NextAfter 重算，**不落盘**），
 *    所以它只出现在 TaskView 上，不在 TaskState 里；
 *  - 运行时状态只由插件单点驱动并持久化，界面一律以「重新拉取」为准，不做本地推断。
 */

/**
 * 任务类型。两种类型不可直接互转，只能复制转换。
 * @typedef {'reminder'|'exec'} TaskKind
 */

/**
 * 触发规则种类。
 * @typedef {'once'|'interval'|'recurring'|'cron'} TriggerKind
 */

/**
 * 触发规则：判别字段 kind + 类型化指针，同一时刻只有对应的那个指针非空
 * （与 Go 侧 plugin.Trigger 一致）。
 * @typedef {Object} TaskTrigger
 * @property {TriggerKind} kind
 * @property {{at: string}} [once] - RFC3339
 * @property {{everyMinutes: number, startAt?: string}} [interval] - startAt 是锚点，首次触发 = 锚点 + 间隔
 * @property {{period: 'daily'|'weekly'|'monthly', timeOfDay: string, weekdays?: number[], dayOfMonth?: number}} [recurring]
 * @property {{expr: string}} [cron] - 五段子集；没有预览不允许保存
 * @property {string} [until] - 到此为止（RFC3339）
 * @property {number} [maxFires] - 最多触发次数
 */

/**
 * 任务运行时状态。
 * @typedef {Object} TaskState
 * @property {string} [lastFiredAt]
 * @property {string} [lastResult] - succeeded|notified|skipped|missed|running|failed|timeout|aborted|interrupted
 * @property {string} [lastError] - 可读失败原因（不允许只显示「失败」）
 * @property {string} [lastRunID]
 * @property {number} [firedCount]
 * @property {number} [consecutiveFailures]
 * @property {{until: string, original: string, count: number}} [snooze] - 临时触发，不改原规则
 * @property {Array<{at: string, handled?: string}>} [missedPending]
 * @property {Object<string,string>} [notified] - 去重台账（随 tasks.json 落盘，重启后仍去重）
 * @property {boolean} [finished]
 */

/**
 * 任务视图（后端 plugin.TaskView）。
 * @typedef {Object} TaskView
 * @property {string} id
 * @property {string} title
 * @property {string} [note]
 * @property {TaskKind} kind
 * @property {TaskTrigger} trigger
 * @property {boolean} enabled
 * @property {{snoozeMinutes?: number}} [reminder]
 * @property {{prompt?: string, model?: string, workDir?: string, timeoutSeconds?: number}} [exec]
 * @property {{level?: string, inAppOnly?: boolean, titleTemplate?: string, bodyTemplate?: string}} [notify]
 * @property {TaskState} state
 * @property {string} createdAt
 * @property {string} updatedAt
 * @property {string} [nextFireAt] - 派生字段，不落盘
 * @property {string} [snoozeUntil]
 */

/**
 * 一次触发的历史记录（后端 plugin.RunRecord）。
 * @typedef {Object} TaskRunRecord
 * @property {string} runID
 * @property {string} taskID
 * @property {string} [taskTitle]
 * @property {TaskKind} kind
 * @property {string} source - scheduled|manual|snooze|catchup|retry
 * @property {string} triggeredAt
 * @property {string} [scheduledAt] - 计划时刻（与 triggeredAt 不同即可看出延迟）
 * @property {string} status
 * @property {number} [durationMS]
 * @property {string} [summary]
 * @property {string} [error]
 * @property {string} [outputPath]
 * @property {string|null} [runSessionID] - 指针：无执行会话时为 null，不内嵌 transcript
 * @property {string[]} [declined] - 无人值守下被拒绝的操作（含理由，必须可见）
 * @property {string} [notifyFallback] - 非空表示通知降级为应用内提醒及原因
 */

/**
 * 插件全局配置（后端 plugin.GlobalConfig）。
 * @typedef {Object} TaskGlobalConfig
 * @property {boolean} enabled - 插件总开关
 * @property {boolean} paused - 全局暂停（持久化）
 * @property {number} [defaultSnoozeMinutes]
 * @property {'catchup'|'skip'|'defer'} [missedPolicy]
 * @property {{enabled: boolean, start: string, end: string, weekdays?: number[]}} [dnd]
 * @property {number} [maxConcurrency] - 恒为 1（串行执行）
 * @property {boolean} notifyEnabled
 * @property {number} [rateLimitPerTaskPerMinute]
 * @property {number} [rateLimitGlobalPer5Minutes]
 * @property {boolean} [foregroundOnlyInApp]
 * @property {number} [keepHistoryDays] - <=0 表示不清理
 * @property {boolean} [deleteHistoryWithTask] - 默认 false：历史默认保留
 * @property {number} [taskSoftLimit] - 软上限，超出只提示不禁止
 */

/**
 * 桌面插件概况（后端 plugin.Info）。state=unavailable 表示插件未启用。
 * @typedef {Object} TaskPluginInfo
 * @property {string} id
 * @property {string} name
 * @property {string} version
 * @property {string} hostAPIVersion
 * @property {string} kind
 * @property {string} state - created|running|stopped|failed|unavailable
 * @property {string} dataDir
 * @property {string} appVersion
 * @property {string[]} [capabilities]
 * @property {string} [startedAt]
 */

/**
 * task:event 通道的事件信封（后端 plugin.Event）。
 * @typedef {Object} TaskEvent
 * @property {string} type
 * @property {string} [reason] - 变更原因/降级原因，便于向用户解释「为什么没弹」
 * @property {*} [payload]
 * @property {string} at
 */

/** 任务事件通道名（独立通道，不与 chat:event / diff:update 混用） */
export const TASK_EVENT_CHANNEL = 'task:event'

/** task:event 的子类型（与 Go 侧 plugin 常量保持一致） */
export const TASK_EVENT_TYPES = {
  PLUGIN_READY: 'task:plugin:ready',
  REVEAL: 'task:reveal',
  CHANGED: 'task:changed',
  FIRED: 'task:fired',
  RUN_STARTED: 'task:run:started',
  RUN_FINISHED: 'task:run:finished',
  MISSED: 'task:missed',
  NOTIFY_FALLBACK: 'task:notify:fallback',
  NOTIFY_IN_APP: 'task:notify:inapp',
}

/** 任务结果到中文文案的映射（界面只做翻译，不推断） */
export const TASK_RESULT_LABELS = {
  succeeded: '已完成',
  notified: '已提醒',
  skipped: '已跳过',
  missed: '已错过',
  running: '执行中',
  failed: '失败',
  timeout: '超时',
  aborted: '已中止',
  interrupted: '被中断',
}
