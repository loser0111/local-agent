import { defineStore } from 'pinia'
import { ref } from 'vue'

/**
 * 应用内提示（toast）与确认框
 *
 * 为什么不用原生 alert / confirm：它们是宿主 webview 提供的能力，各平台实现不一致
 * （WKWebView 上未实现时 confirm 返回假值、alert 直接吞掉），一旦不可用就表现为
 * 「点了没反应、也不报错」——删除按钮全部失效就是这一类症状。这里统一改成应用内渲染：
 * 行为可控、可测，也不依赖宿主。
 */
export const useUiStore = defineStore('ui', () => {
  const toasts = ref([])
  const confirmState = ref(null) // { title, message, confirmText, cancelText, danger, resolve }
  const promptState = ref(null) // { title, message, placeholder, value, confirmText, cancelText, resolve }
  let seq = 0

  /**
   * 推一条提示
   * @param {string} message
   * @param {'info'|'success'|'error'} type
   * @param {number} [duration] 毫秒；0 表示不自动消失
   */
  function notify(message, type = 'info', duration) {
    const text = String(message ?? '').trim()
    if (!text) return null
    seq += 1
    const id = seq
    // 错误多留一会儿，方便读完
    const ttl = duration ?? (type === 'error' ? 8000 : 4000)
    toasts.value.push({ id, message: text, type })
    if (ttl > 0) setTimeout(() => dismiss(id), ttl)
    return id
  }

  function dismiss(id) {
    const idx = toasts.value.findIndex((t) => t.id === id)
    if (idx > -1) toasts.value.splice(idx, 1)
  }

  function clearToasts() {
    toasts.value = []
  }

  /**
   * 应用内确认框（替代原生 confirm）
   * @returns {Promise<boolean>} 用户是否点了确认
   */
  function ask({
    title = '确认操作',
    message = '',
    confirmText = '确定',
    cancelText = '取消',
    danger = true,
  } = {}) {
    // 已有未应答的确认框时先按取消结掉，避免那个 Promise 永远挂着
    if (confirmState.value) {
      confirmState.value.resolve(false)
      confirmState.value = null
    }
    return new Promise((resolve) => {
      confirmState.value = { title, message, confirmText, cancelText, danger, resolve }
    })
  }

  /**
   * 应用内输入框（替代原生 prompt，后者在部分 webview 上静默返回 null）
   * @returns {Promise<string|null>} 取消返回 null
   */
  function askText({
    title = '请输入',
    message = '',
    placeholder = '',
    value = '',
    confirmText = '确定',
    cancelText = '取消',
  } = {}) {
    if (promptState.value) {
      promptState.value.resolve(null)
      promptState.value = null
    }
    return new Promise((resolve) => {
      promptState.value = { title, message, placeholder, value, confirmText, cancelText, resolve }
    })
  }

  function setPromptValue(v) {
    if (promptState.value) promptState.value.value = v
  }

  function answerText(ok) {
    const cur = promptState.value
    if (!cur) return
    promptState.value = null
    const v = String(cur.value ?? '').trim()
    cur.resolve(ok && v ? v : null)
  }

  function answer(ok) {
    const cur = confirmState.value
    if (!cur) return
    confirmState.value = null
    cur.resolve(!!ok)
  }

  return {
    toasts,
    confirmState,
    promptState,
    notify,
    dismiss,
    clearToasts,
    ask,
    answer,
    askText,
    setPromptValue,
    answerText,
  }
})
