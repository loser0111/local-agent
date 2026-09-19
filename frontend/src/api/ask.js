import { ResolveAskUser, CancelAskUser, GetPendingAsk } from '@/../wailsjs/go/main/App'

/**
 * 判断是否运行在 Wails 桌面环境
 */
function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

// 浏览器开发模式：把挂起提问存在 localStorage，便于 mock 联调
const MOCK_KEY = 'local-agent:pending-ask'

/**
 * 提交问答复（模型会拿到这份答复继续执行）
 * @param {{id:string, answers:Array<{questionId?:string, selected?:string[], text?:string}>}} answer
 * @returns {Promise<void>}
 */
export async function resolveAskUser(answer) {
  if (isWails()) {
    await ResolveAskUser(answer)
    return
  }
  localStorage.removeItem(MOCK_KEY)
}

/**
 * 取消该会话挂起的提问（用户点跳过 / 停止生成）
 * @param {string} sessionId
 * @returns {Promise<void>}
 */
export async function cancelAskUser(sessionId) {
  if (isWails()) {
    await CancelAskUser(sessionId)
  }
}

/**
 * 查询该会话是否有挂起的提问（切换会话后重新弹窗用）
 * @param {string} sessionId
 * @returns {Promise<object|null>}
 */
export async function getPendingAsk(sessionId) {
  if (isWails()) {
    return (await GetPendingAsk(sessionId)) || null
  }
  try {
    const raw = localStorage.getItem(MOCK_KEY)
    if (!raw) return null
    const p = JSON.parse(raw)
    return p.sessionId === sessionId ? p : null
  } catch {
    return null
  }
}
