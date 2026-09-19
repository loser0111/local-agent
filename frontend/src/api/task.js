import {
  TaskPluginInfo,
  TaskPluginManifest,
  ListTasks,
  GetTask,
  CreateTask,
  UpdateTask,
  DeleteTask,
  SetTaskEnabled,
  RunTaskNow,
  SnoozeTask,
  CompleteTask,
  ListTaskRuns,
  PreviewTaskTriggerTimes,
  GetTaskSettings,
  UpdateTaskSettings,
  PauseAllTasks,
  TaskCounts,
} from '@/../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '@/../wailsjs/runtime/runtime'

/**
 * 定时任务（桌面插件）API
 *
 * 约定与仓库其余 api 模块一致：从 wailsjs 导入绑定、带 isWails() 判断，
 * 浏览器开发模式下退回 localStorage mock，保证 `vite dev` 可以单独调界面。
 */

const EVENT_CHANNEL = 'task:event'
const MOCK_KEY = 'local-agent:tasks'

/** 是否运行在 Wails 桌面环境 */
function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

/** 读取 mock 数据（仅浏览器开发模式使用） */
function readMock() {
  try {
    const raw = localStorage.getItem(MOCK_KEY)
    if (!raw) return { settings: defaultSettings(), tasks: [], runs: {} }
    const parsed = JSON.parse(raw)
    return {
      settings: parsed.settings || defaultSettings(),
      tasks: parsed.tasks || [],
      runs: parsed.runs || {},
    }
  } catch {
    return { settings: defaultSettings(), tasks: [], runs: {} }
  }
}

function writeMock(data) {
  localStorage.setItem(MOCK_KEY, JSON.stringify(data))
}

function defaultSettings() {
  return {
    enabled: true,
    paused: false,
    defaultSnoozeMinutes: 10,
    missedPolicy: 'catchup',
    maxConcurrency: 1,
    notifyEnabled: true,
    rateLimitPerTaskPerMinute: 1,
    rateLimitGlobalPer5Minutes: 3,
    foregroundOnlyInApp: true,
    keepHistoryDays: 0,
    deleteHistoryWithTask: false,
    taskSoftLimit: 200,
    unhandledWindowMinutes: 120,
  }
}

/** 插件概况（含 state=unavailable 表示插件未启用） */
export async function fetchPluginInfo() {
  if (isWails()) return await TaskPluginInfo()
  return { id: 'desktop-plugin', name: 'Windows 桌面插件（定时任务提示）', version: '0.1.0', state: 'mock', dataDir: '(浏览器模式)' }
}

/** 插件清单 */
export async function fetchPluginManifest() {
  if (isWails()) return await TaskPluginManifest()
  return { id: 'desktop-plugin', kind: 'builtin', version: '0.1.0', hostAPIVersion: '1.0' }
}

/** 任务列表 */
export async function fetchTasks() {
  if (isWails()) return (await ListTasks()) || []
  return readMock().tasks
}

/** 单个任务 */
export async function fetchTask(id) {
  if (isWails()) return await GetTask(id)
  return readMock().tasks.find((t) => t.id === id) || null
}

/** 新建任务 */
export async function createTask(input) {
  if (isWails()) return await CreateTask(input)
  // 浏览器模式只做最小可用模拟：不执行校验（校验在后端，界面依赖其报错）
  const data = readMock()
  const task = { ...input, id: `mock_${Date.now()}`, enabled: input.enabled ?? true, state: {} }
  data.tasks.push(task)
  writeMock(data)
  return task
}

/** 编辑任务 */
export async function updateTask(id, input) {
  if (isWails()) return await UpdateTask(id, input)
  const data = readMock()
  const idx = data.tasks.findIndex((t) => t.id === id)
  if (idx >= 0) data.tasks[idx] = { ...data.tasks[idx], ...input }
  writeMock(data)
  return data.tasks[idx]
}

/** 删除任务（deleteHistory 默认 false：历史默认保留） */
export async function deleteTask(id, deleteHistory = false) {
  if (isWails()) return await DeleteTask(id, deleteHistory)
  const data = readMock()
  data.tasks = data.tasks.filter((t) => t.id !== id)
  if (deleteHistory) delete data.runs[id]
  writeMock(data)
}

/** 启用/停用 */
export async function setTaskEnabled(id, enabled) {
  if (isWails()) return await SetTaskEnabled(id, enabled)
  const data = readMock()
  const t = data.tasks.find((x) => x.id === id)
  if (t) t.enabled = enabled
  writeMock(data)
  return t
}

/** 立即执行一次 */
export async function runTaskNow(id) {
  if (isWails()) return await RunTaskNow(id)
  return { runID: `mock_${Date.now()}`, taskID: id, status: 'notified', source: 'manual' }
}

/** 稍后提醒 */
export async function snoozeTask(id, minutes = 0) {
  if (isWails()) return await SnoozeTask(id, minutes)
}

/** 标记完成 */
export async function completeTask(id) {
  if (isWails()) return await CompleteTask(id)
}

/** 触发历史 */
export async function fetchTaskRuns(taskID, limit = 50) {
  if (isWails()) return (await ListTaskRuns(taskID, limit)) || []
  return readMock().runs[taskID] || []
}

/** 未来 n 次触发时间预览（保存前必须能预览） */
export async function previewTriggerTimes(trigger, n = 3) {
  if (isWails()) return (await PreviewTaskTriggerTimes(trigger, n)) || []
  return []
}

/** 全局配置 */
export async function fetchTaskSettings() {
  if (isWails()) return (await GetTaskSettings()) || defaultSettings()
  return readMock().settings
}

/** 更新全局配置 */
export async function updateTaskSettings(cfg) {
  if (isWails()) return await UpdateTaskSettings(cfg)
  const data = readMock()
  data.settings = { ...defaultSettings(), ...cfg }
  writeMock(data)
}

/** 全局暂停/恢复 */
export async function pauseAllTasks(paused) {
  if (isWails()) return await PauseAllTasks(paused)
  const data = readMock()
  data.settings.paused = paused
  writeMock(data)
}

/** 任务数与软上限 */
export async function fetchTaskCounts() {
  if (isWails()) return await TaskCounts()
  return [readMock().tasks.length, 200]
}

/**
 * 订阅 task:event（独立通道，不与 chat:event / diff:update 混用）。
 *
 * 只负责把事件转交出去：状态一律以「重新拉取」为准，界面不做本地推断
 * —— 状态机由插件单点驱动。
 */
export function onTaskEvent(cb) {
  if (!isWails()) return () => {}
  EventsOn(EVENT_CHANNEL, cb)
  return () => EventsOff(EVENT_CHANNEL)
}
