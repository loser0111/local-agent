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

  function getPanes(sessionId) {
    if (!panesMap.value[sessionId]) {
      panesMap.value[sessionId] = ['chat']
    }
    return panesMap.value[sessionId]
  }

  function getActivePane(sessionId) {
    return activePaneMap.value[sessionId] || 'chat'
  }

  function openPane(sessionId, paneType) {
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
    getPanes,
    getActivePane,
    openPane,
    closePane,
    setActivePane,
    getLayout,
    setLayout,
  }
})
