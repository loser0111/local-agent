import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { PERMISSION_MODES } from '@/types'
import {
  DEFAULT_THEME,
  FONT_SIZE_DEFAULT,
  applyFontSize,
  applyTheme,
  normalizeFontSize,
  normalizeTheme,
  watchSystemTheme,
} from '@/utils/theme'

const STORAGE_KEY = 'local-agent:settings'

/**
 * 默认设置（新增字段时，旧数据缺省由此兜底）
 *
 * 注意：这里的字段必须真的有人消费，否则就是「假控件」。
 * 历史上 theme / language / fontSize / autoSave / autoArchive 曾经存在于设置页
 * 却不在这个对象里、也没有任何消费方，表现为「选了没反应」。
 * language / autoSave / autoArchive 已移除（无 i18n 与自动保存语义）；
 * theme / fontSize 已在下方的 watch 里真正生效。
 */
function defaultSettings() {
  return {
    permissionMode: PERMISSION_MODES[0].value,
    // 流式输出：逐字显示回复（SSE）；旧数据缺该字段时默认开启
    streamResponse: true,
    // 默认模型名称（新建会话时自动选中，空串表示不设默认）
    defaultModel: '',
    // 主题：dark | light | system
    theme: DEFAULT_THEME,
    // 界面基础字号（px），驱动 --font-size-base
    fontSize: FONT_SIZE_DEFAULT,
  }
}

/** 从 localStorage 恢复设置，缺字段显式兜底 */
function loadSettings() {
  const base = defaultSettings()
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) {
      const saved = JSON.parse(raw)
      const merged = { ...base, ...saved }
      // 脏数据（手改过 localStorage / 旧版本写入的非法值）归一化，避免界面卡在无效状态
      merged.theme = normalizeTheme(merged.theme)
      merged.fontSize = normalizeFontSize(merged.fontSize)
      return merged
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

  function setTheme(v) {
    settings.value.theme = normalizeTheme(v)
  }

  function setFontSize(v) {
    settings.value.fontSize = normalizeFontSize(v)
  }

  /** 把当前设置应用到 DOM（应用启动时在 mount 之前调用一次，避免白闪） */
  function applyAppearance() {
    applyTheme(settings.value.theme)
    applyFontSize(settings.value.fontSize)
    // 第二个参数是「系统主题变化后」的额外回调；DOM 的重新应用由 watchSystemTheme 内部完成
    watchSystemTheme(settings.value.theme, () => {})
  }

  // 主题与字号变化时立即生效（「跟随系统」还要重新挂一次媒体查询监听）
  watch(
    () => settings.value.theme,
    (theme) => {
      applyTheme(theme)
      watchSystemTheme(theme, () => {})
    }
  )

  watch(
    () => settings.value.fontSize,
    (size) => {
      applyFontSize(size)
    }
  )

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

  return {
    settings,
    setPermissionMode,
    setStreamResponse,
    setDefaultModel,
    setTheme,
    setFontSize,
    applyAppearance,
  }
})
