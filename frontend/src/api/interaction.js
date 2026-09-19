import { EventsOn } from '@/../wailsjs/runtime/runtime'
import { GetPendingInteraction } from '@/../wailsjs/go/main/App'

/**
 * 用户交互事件通道（授权请求 / 模型提问）
 *
 * 这类请求的特点是：后端**阻塞**等待，无论从哪条链路发起（普通聊天、计划执行、
 * 以后新增的入口）都必须能弹出来。因此它们走独立的 `user:interaction` 事件，
 * 由应用根部订阅一次即可 —— 而不是挂在每次调用内注册的 `chat:event` 回调上
 * （Wails 的 EventsOff 会移除该事件的全部监听，按调用注册极易漏掉某条链路）。
 */
export const USER_INTERACTION_EVENT = 'user:interaction'

function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

/**
 * 订阅用户交互事件（**进程级常驻，刻意不提供取消订阅**）
 *
 * 为什么不返回取消函数：Wails 的 `EventsOff(eventName)` 是按**事件名**移除该事件的
 * 全部监听，而不是只移除回调自己那一个。因此只要有任何一处组件卸载时调用它，
 * 就会把别的组件（包括根组件）的监听一起干掉——表现为"去过一次设置页之后，
 * 弹窗就再也不出现"。所以这个通道的订阅只增不减：由 App.vue 在启动时订阅一次，
 * 组件卸载不做清理。
 *
 * @param {(ev: {type: string, permission?: object, ask?: object}) => void} cb
 */
export function onUserInteraction(cb) {
  if (!isWails()) return
  EventsOn(USER_INTERACTION_EVENT, cb)
}

/**
 * 查询该会话当前挂起的交互请求（后端正在等待应答的那一条）
 *
 * 作为事件通道的兜底：轮询它，只要后端在等人就能把弹窗补出来 ——
 * 即使事件因"前后端产物版本错配"或运行时时序问题没送达。
 * @param {string} sessionId
 * @returns {Promise<{kind:string, permission?:object, ask?:object}|null>}
 */
export async function fetchPendingInteraction(sessionId) {
  if (!isWails() || !sessionId) return null
  try {
    return (await GetPendingInteraction(sessionId)) || null
  } catch (e) {
    return null // 轮询失败静默处理，不打扰用户
  }
}
