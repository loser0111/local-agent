import { defineStore } from 'pinia'
import { ref } from 'vue'

/**
 * 设置 Store
 */
export const useSettingStore = defineStore('setting', () => {
  const settings = ref({
    theme: 'dark',
    language: 'zh-CN',
    fontSize: 14,
    autoSave: true,
    autoArchive: false,
    defaultModel: 'Claude Sonnet 4.5',
    apiKey: '',
    apiEndpoint: '',
    defaultPermissionMode: 'manual',
    viewMode: 'normal',
  })

  function updateSetting(key, value) {
    settings.value[key] = value
  }

  function updateSettings(patch) {
    Object.assign(settings.value, patch)
  }

  return {
    settings,
    updateSetting,
    updateSettings,
  }
})
