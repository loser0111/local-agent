import {
  GetSessionPlan,
  SavePlan,
  ExecutePlan,
  CancelPlan,
  ListPlans,
} from '@/../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '@/../wailsjs/runtime/runtime'

/**
 * 判断是否运行在 Wails 桌面环境
 */
function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

// ===== 浏览器开发模式 mock（localStorage 模拟计划存储） =====
const MOCK_PREFIX = 'local-agent:plan:'

function readMockPlan(sessionId) {
  try {
    const raw = localStorage.getItem(MOCK_PREFIX + sessionId)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

function writeMockPlan(sessionId, plan) {
  localStorage.setItem(MOCK_PREFIX + sessionId, JSON.stringify(plan))
}

/**
 * 浏览器 mock：写入计划存储（session.js mock 计划生成时共用同一 key 约定）
 * @param {object} plan
 */
export function putMockPlan(plan) {
  writeMockPlan(plan.sessionId, plan)
}

function findMockPlanById(planId) {
  for (let i = 0; i < localStorage.length; i++) {
    const key = localStorage.key(i)
    if (key && key.startsWith(MOCK_PREFIX)) {
      try {
        const p = JSON.parse(localStorage.getItem(key))
        if (p && p.id === planId) return { key, plan: p }
      } catch {
        /* 跳过损坏数据 */
      }
    }
  }
  return null
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const clone = (o) => JSON.parse(JSON.stringify(o))

// mock 执行取消标志（planId -> true）
const mockCancelFlags = new Set()

/**
 * 获取会话当前（最新）计划
 * @param {string} sessionId
 * @returns {Promise<import('@/types').Plan|null>}
 */
export async function getPlan(sessionId) {
  if (isWails()) {
    return await GetSessionPlan(sessionId)
  }
  return readMockPlan(sessionId)
}

/**
 * 获取会话全部历史计划（按创建时间倒序）
 * @param {string} sessionId
 * @returns {Promise<import('@/types').Plan[]>}
 */
export async function listPlans(sessionId) {
  if (isWails()) {
    return await ListPlans(sessionId)
  }
  const p = readMockPlan(sessionId)
  return p ? [p] : []
}

/**
 * 保存计划编辑（仅 awaiting_approval 状态可改）
 * @param {import('@/types').Plan} plan
 * @returns {Promise<import('@/types').Plan>}
 */
export async function savePlan(plan) {
  if (isWails()) {
    await SavePlan(plan)
    return plan
  }
  const stored = readMockPlan(plan.sessionId)
  if (!stored || stored.id !== plan.id) throw new Error('计划不存在')
  if (stored.status !== 'awaiting_approval') throw new Error('计划当前状态不可编辑')
  stored.title = plan.title
  stored.steps = clone(plan.steps).map((s, i) => ({
    ...s,
    index: i,
    title: (s.title || '').trim(),
    status: s.status || 'pending',
  }))
  stored.updatedAt = Date.now()
  writeMockPlan(plan.sessionId, stored)
  return clone(stored)
}

/**
 * 逐步执行计划（长耗时；过程经 onPlanUpdate/onReplyDelta/onToolCall* 回调）
 * @param {string} planId
 * @param {boolean} useStream
 * @param {{onPlanUpdate?:Function, onReplyDelta?:Function, onToolCallStart?:Function, onToolCallEnd?:Function}} handlers
 * @returns {Promise<{plan?:import('@/types').Plan, reply?:string, error?:string}>}
 */
export async function executePlan(planId, useStream, handlers = {}) {
  if (isWails()) {
    // 监听计划状态与工具调用/流式分片事件（后端执行期间持续推送）
    const eventHandler = (eventData) => {
      if (!eventData) return
      switch (eventData.type) {
        case 'plan_update':
          handlers.onPlanUpdate?.(eventData.plan)
          break
        case 'reply_delta':
          handlers.onReplyDelta?.(eventData.reply)
          break
        case 'tool_call_start':
          handlers.onToolCallStart?.(eventData.toolCall)
          break
        case 'tool_call_end':
          handlers.onToolCallEnd?.(eventData.toolCall)
          break
      }
    }
    EventsOn('chat:event', eventHandler)
    try {
      return await ExecutePlan(planId, !!useStream)
    } finally {
      EventsOff('chat:event')
    }
  }

  // ===== 浏览器 mock：逐步状态机模拟 =====
  const found = findMockPlanById(planId)
  if (!found) throw new Error('计划不存在')
  const plan = found.plan
  mockCancelFlags.delete(planId)

  plan.status = 'running'
  handlers.onPlanUpdate?.(clone(plan))

  for (const step of plan.steps) {
    if (step.status === 'done') continue
    if (mockCancelFlags.has(planId)) {
      return finishMockCancel(plan, step.index, handlers)
    }
    step.status = 'running'
    step.startedAt = Date.now()
    handlers.onPlanUpdate?.(clone(plan))
    await sleep(1200)
    if (mockCancelFlags.has(planId)) {
      step.status = 'failed'
      step.error = '执行取消'
      step.finishedAt = Date.now()
      return finishMockCancel(plan, step.index + 1, handlers)
    }
    step.status = 'done'
    step.summary = `「${step.title}」已完成（浏览器模拟执行结果）`
    step.finishedAt = Date.now()
    handlers.onPlanUpdate?.(clone(plan))
  }

  plan.status = 'completed'
  writeMockPlan(plan.sessionId, plan)
  handlers.onPlanUpdate?.(clone(plan))
  return { plan: clone(plan), reply: '计划执行完成' }
}

function finishMockCancel(plan, fromIndex, handlers) {
  plan.status = 'cancelled'
  for (const st of plan.steps) {
    if (st.index >= fromIndex && st.status === 'pending') st.status = 'skipped'
  }
  writeMockPlan(plan.sessionId, plan)
  mockCancelFlags.delete(plan.id)
  handlers.onPlanUpdate?.(clone(plan))
  return { plan: clone(plan) }
}

/**
 * 取消执行中的计划（协作式：当前步骤跑完后停止）
 * @param {string} planId
 * @returns {Promise<void>}
 */
export async function cancelPlan(planId) {
  if (isWails()) {
    await CancelPlan(planId)
    return
  }
  mockCancelFlags.add(planId)
}
