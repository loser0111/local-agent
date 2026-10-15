import { openExternalURL } from '@/api/session'

/**
 * 让 `v-html` 注入内容里的链接可点击。
 *
 * **要解决的问题**：聊天内容（模型输出）经 `renderMarkdown` 渲染后会带
 * `<a target="_blank">`，但 Wails 的 WebView **不处理 `target="_blank"`**——
 * 既不会唤起系统浏览器，也不会在应用内开新窗口，而是静默失败（点了没反应）。
 * 必须由 Go 侧调 `runtime.BrowserOpenURL`，所以要在点击时拦下来转一道。
 *
 * **为什么用事件委托而不是给每个 `<a>` 绑事件**：
 * 内容是 `v-html` 注入的，DOM 由 marked 生成、随每次渲染重建，
 * 逐个绑事件既拿不到那些节点，也会在重渲染后失效。
 * 委托到容器上，则无论内容怎么变都有效。
 *
 * @returns {(e: MouseEvent) => void} 直接绑到容器上的 `@click`
 */
export function useExternalLink() {
  return function onContentClick(e) {
    // closest 从被点中的节点往上找最近的链接——点 <code> 或 <strong> 也算点到链接
    const anchor = e.target?.closest?.('a[href]')
    if (!anchor) return

    const href = anchor.getAttribute('href') || ''
    // 页内锚点（#section）留在应用内滚动，不交给系统浏览器
    if (href.startsWith('#')) return

    // 阻止默认行为：WebView 对 target="_blank" 的处理是静默失败，
    // 不拦的话某些平台还可能让 WebView 自己尝试导航（把整个界面顶掉）。
    e.preventDefault()

    openExternalURL(href)
  }
}
