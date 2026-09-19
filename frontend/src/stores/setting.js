import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { PERMISSION_MODES } from '@/types'

const STORAGE_KEY = 'local-agent:settings'

/** 默认设置（新增字段时，旧数据缺省由此兜底） */
function defaultSettings() {
  return {
    permissionMode: PERMISSION_MODES[0].value,
    // 流式输出：逐字显示回复（SSE）；旧数据缺该字段时默认开启
    streamResponse: true,
    // 默认模型名称（新建会话时自动选中，空串表示不设默认）
    defaultModel: '',
  }
}

/** 从 localStorage 恢复设置，缺字段显式兜底 */
function loadSettings() {
  const base = defaultSettings()
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) {
      const saved = JSON.parse(raw)
      return { ...base, ...saved }
    }
  } catch (e) {
    console.warn('读取本地设置失败，使用默认值:', e)
  }
  return base
}

export const useSettingStore = defineStore('setting', () => {
  const settings = ref(loadSettings())

  function setPermissionMode(mode) {
    if (PERMISSION_MODES.some((m) => m.value === mode)) {
      settings.value.permissionMode = mode
    }
  }

  function setStreamResponse(v) {
    settings.value.streamResponse = !!v
  }

  function setDefaultModel(v) {
    settings.value.defaultModel = v || ''
  }

  // 持久化（深监听整个 settings）
  watch(
    settings,
    (val) => {
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(val))
      } catch (e) {
        console.warn('保存设置失败:', e)
      }
    },
    { deep: true }
  )

  return { settings, setPermissionMode, setStreamResponse, setDefaultModel }
})
