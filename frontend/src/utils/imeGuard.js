/**
 * 输入法（IME）键盘事件守卫。
 *
 * **要解决的问题**：开着中文/日文输入法打字时，按回车是用来「确认候选词」的，
 * 那一次回车只该归输入法；要发送消息得再按一次。但聊天输入框把回车直接当了发送，
 * 于是拼音打了一半按回车选字 → 消息被发出去（内容还是半成品）。
 *
 * **为什么写这个文件而不是每个 handler 里各写一份**：
 * 守卫有两个条件，第二个很反直觉、绝不是一眼能想到的（见下），
 * 散落在各组件里迟早有人只抄了第一句。
 *
 * **两个条件**：
 * 1. `e.isComposing || e.keyCode === 229` —— 标准做法，Chromium 系（Windows 微软拼音、
 *    Chrome、Edge）组字期间的 keydown 就是这个样子，够用。
 * 2. **compositionend 之后的时间窗兜底** —— macOS 的 WebKit（Safari，以及 Wails 用的
 *    WKWebView）事件顺序和 Chromium 相反：**compositionend 先于最后一次 keydown**。
 *    等 keydown 跑到我们的 handler 里，`isComposing` 已经被复位成 false，
 *    上面那段守卫形同虚设。这正是「只有 Mac 上出问题」的根本原因。
 *    所以记一个 compositionend 的时间戳，紧跟其后的那次按键也不当用户意图。
 *
 * 窗口取 100ms：确认候选词那次回车的 keydown 紧跟在 compositionend 之后（毫秒级）；
 * 而人手连打两次回车最快也在 150ms 以上，不会误伤「打完字立刻回车发送」的快手。
 * 用时间戳比较而不是 setTimeout 置标志位——时钟漂移、标签页休眠都会让后者翻车。
 *
 * 同一套守卫对 Escape 同样成立（取消候选词 ≠ 关闭弹窗），所以导出的是通用的
 * `isImeKeyEvent(e)`，由调用方决定「被守卫拦下后是否要提前 return」。
 */

/** 视为「输入法余波」的时间窗（毫秒） */
const COMPOSITION_TAIL_MS = 100

/**
 * 最近一次 compositionend 的时间戳。
 *
 * 全局一份就够：同一时刻只可能有一个元素处于组字状态（焦点唯一），
 * 不必按元素分别记。
 */
let lastCompositionEnd = 0
let installed = false

function now() {
  return typeof performance !== 'undefined' && typeof performance.now === 'function'
    ? performance.now()
    : Date.now()
}

function onCompositionEnd() {
  lastCompositionEnd = now()
}

/**
 * 安装全局的 compositionend 监听。
 *
 * 用 window + capture：compositionend 可能在任意输入元素上触发，
 * 而且 capture 能确保它先于该元素自己的事件处理被记录。
 * 重复调用是安全的（幂等）。
 */
export function installImeGuard() {
  if (installed || typeof window === 'undefined') return
  installed = true
  window.addEventListener('compositionend', onCompositionEnd, true)
}

/**
 * 这个键盘事件是否「属于输入法」——组字进行中，或刚刚结束组字。
 *
 * 调用方拿到 true 就应当**直接忽略**这次按键（不发送、不提交、不关闭弹窗），
 * 并且**不要** `preventDefault()`：让输入法自己消费它，候选词才能正常上屏。
 *
 * @param {KeyboardEvent} e
 * @returns {boolean}
 */
export function isImeKeyEvent(e) {
  if (!e) return false

  // 条件 1：标准守卫。keyCode 229 = VK_PROCESS，被输入法吃掉按键时的老信号，
  // 至今仍有浏览器/输入法组合只在 keyCode 上体现。
  if (e.isComposing || e.keyCode === 229) return true

  // 条件 2：WebKit 时序兜底（见文件头说明）
  if (lastCompositionEnd > 0 && now() - lastCompositionEnd < COMPOSITION_TAIL_MS) return true

  return false
}
