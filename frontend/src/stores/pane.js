import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

/**
 * 面板布局 Store
 */
export const usePaneStore = defineStore('pane', () => {
  // sessionId -> PaneType[]
  const panesMap = ref({})
  const activePaneMap = ref({}) // sessionId -> 当前活动面板
  const layoutMap = ref({}) // sessionId -> 布局类型
  // sessionId -> 右侧面板是否收起（收起后是"窄条 + 图标"形态，见 PaneContainer）
  //
  // 跟随会话（与 panesMap 同一套键控）：每条会话各记各的。
  // 刻意**不做持久化**：panesMap 本身就不持久化，重启后打开的面板本来就会回到只剩 chat，
  // 单独把"收起"存下来会出现"没有面板可收、却记着已收起"的错位状态。
  const collapsedMap = ref({})

  function getPanes(sessionId) {
    if (!panesMap.value[sessionId]) {
      panesMap.value[sessionId] = ['chat']
    }
    return panesMap.value[sessionId]
  }

  function getActivePane(sessionId) {
    return activePaneMap.value[sessionId] || 'chat'
  }

  function isSecondaryCollapsed(sessionId) {
    return !!collapsedMap.value[sessionId]
  }

  function setSecondaryCollapsed(sessionId, collapsed) {
    if (!sessionId) return
    collapsedMap.value = { ...collapsedMap.value, [sessionId]: !!collapsed }
  }

  function toggleSecondaryCollapsed(sessionId) {
    setSecondaryCollapsed(sessionId, !isSecondaryCollapsed(sessionId))
  }

  /**
   * 打开面板 —— **用户显式动作**（点聊天工具栏的 Diff、点窄条上的图标等）
   *
   * 顺带把右侧展开：用户点了"看 Diff"，右侧却还收着会显得点了没反应。
   */
  function openPane(sessionId, paneType) {
    const panes = getPanes(sessionId)
    if (!panes.includes(paneType)) {
      panes.push(paneType)
    }
    activePaneMap.value[sessionId] = paneType
    setSecondaryCollapsed(sessionId, false)
  }

  /**
   * 后台自动打开面板（一轮跑完有文件改动、派生了子代理、生成了计划）
   *
   * 与 openPane 的唯一区别：**不改变用户的收起状态**。收起后这些面板会以图标出现在窄条上
   * ——"有新东西打开了"是看得见的，但不该因为一轮后台运行就把右侧强行铺开。
   */
  function openPaneBackground(sessionId, paneType) {
    const panes = getPanes(sessionId)
    if (!panes.includes(paneType)) {
      panes.push(paneType)
    }
    activePaneMap.value[sessionId] = paneType
  }

  function closePane(sessionId, paneType) {
    if (paneType === 'chat') return // chat 不可关闭
    const panes = getPanes(sessionId)
    const idx = panes.indexOf(paneType)
    if (idx > -1) panes.splice(idx, 1)
    if (getActivePane(sessionId) === paneType) {
      activePaneMap.value[sessionId] = panes[panes.length - 1] || 'chat'
    }
  }

  function setActivePane(sessionId, paneType) {
    activePaneMap.value[sessionId] = paneType
  }

  function getLayout(sessionId) {
    return layoutMap.value[sessionId] || 'split-right'
  }

  function setLayout(sessionId, layout) {
    layoutMap.value[sessionId] = layout
  }

  return {
    panesMap,
    collapsedMap,
    getPanes,
    getActivePane,
    isSecondaryCollapsed,
    setSecondaryCollapsed,
    toggleSecondaryCollapsed,
    openPane,
    openPaneBackground,
    closePane,
    setActivePane,
    getLayout,
    setLayout,
  }
})
