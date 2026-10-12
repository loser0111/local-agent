import { GetSessionTodos } from '@/../wailsjs/go/app/App'

/**
 * 判断是否运行在 Wails 桌面环境
 */
function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.app && window.go.app.App
}

/**
 * 读取某会话的待办清单。
 *
 * 清单的**变更**由 `todo_update` 事件推送（后端在每次 todo 工具改动后推全量清单）；
 * 这个接口只用于「打开面板 / 切回会话」时补一次当前状态——事件可能发生在面板
 * 尚未打开的时候，光靠事件会让面板显示空清单。
 *
 * @param {string} sessionId
 * @returns {Promise<Array<{id:string,text:string,status:string,createdAt?:number,updatedAt?:number}>>}
 */
export async function getTodos(sessionId) {
  if (!sessionId) return []
  if (isWails()) {
    const items = await GetSessionTodos(sessionId)
    return Array.isArray(items) ? items : []
  }
  // 浏览器开发模式：没有后端，返回空清单（面板会显示空态）
  return []
}
