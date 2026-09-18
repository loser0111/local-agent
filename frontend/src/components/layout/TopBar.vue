<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Moon, Settings, Sun } from 'lucide-vue-next'
import { useSessionStore } from '@/stores/session'
import { useSettingStore } from '@/stores/setting'
import { PERMISSION_MODES, VIEW_MODES } from '@/types'
import { fetchModelNames } from '@/api/model'
import { THEME_OPTIONS, resolveTheme } from '@/utils/theme'

const sessionStore = useSessionStore()
const settingStore = useSettingStore()
const router = useRouter()

const emit = defineEmits(['toggle-sidebar'])

const currentSession = computed(() => sessionStore.currentSession)

// ===== 主题快捷切换 =====
// 这个位置原本是一个「使用量」按钮，但它没有绑定任何点击事件（点了没反应），已替换为
// 可用的主题切换。三选一（深色 / 浅色 / 跟随系统）在「设置 - 通用设置」里。
const resolvedTheme = ref(resolveTheme(settingStore.settings.theme))

watch(
  () => settingStore.settings.theme,
  (t) => {
    resolvedTheme.value = resolveTheme(t)
  }
)

const themeTitle = computed(() => {
  const cur = THEME_OPTIONS.find((o) => o.value === settingStore.settings.theme)
  const next = resolvedTheme.value === 'light' ? '深色' : '浅色'
  return `主题：${cur?.label || '深色'}（点击切换为${next}）`
})

function toggleTheme() {
  settingStore.setTheme(resolvedTheme.value === 'light' ? 'dark' : 'light')
}

// 从后端加载模型名称列表
const modelNames = ref([])

async function loadModelNames() {
  try {
    const names = await fetchModelNames()
    modelNames.value = names
    // 如果当前会话没有模型，优先使用设置的默认模型，否则选第一个可用模型
    if (currentSession.value && !currentSession.value.model && names.length > 0) {
      const defaultModel = settingStore.settings.defaultModel
      const target = (defaultModel && names.includes(defaultModel)) ? defaultModel : names[0]
      await sessionStore.patchSession(currentSession.value.id, { model: target })
    }
  } catch (e) {
    console.error('加载模型列表失败:', e)
  }
}

onMounted(loadModelNames)

// 当会话切换时，确保模型列表是最新的
watch(() => sessionStore.currentSessionId, loadModelNames)

async function changeModel(e) {
  if (currentSession.value) {
    await sessionStore.patchSession(currentSession.value.id, { model: e.target.value })
  }
}

async function changePermissionMode(e) {
  if (currentSession.value) {
    await sessionStore.patchSession(currentSession.value.id, {
      permissionMode: e.target.value,
    })
  }
}

async function changeViewMode(mode) {
  if (currentSession.value) {
    await sessionStore.patchSession(currentSession.value.id, { viewMode: mode })
  }
}

function goSettings() {
  router.push('/settings')
}
</script>

<template>
  <div class="topbar">
    <div class="topbar-left">
      <button class="icon-btn" title="切换侧边栏" @click="$emit('toggle-sidebar')">
        <svg width="18" height="18" viewBox="0 0 18 18" fill="none">
          <path d="M2 4H16M2 9H16M2 14H16" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
        </svg>
      </button>
      <span class="app-name">local-agent</span>

      <div v-if="currentSession" class="session-info">
        <span class="sep">/</span>
        <select class="select-sm" :value="currentSession.model" @change="changeModel">
          <option v-if="modelNames.length === 0" value="">暂无模型</option>
          <option v-for="m in modelNames" :key="m" :value="m">{{ m }}</option>
        </select>
        <select
          class="select-sm"
          :value="currentSession.permissionMode"
          @change="changePermissionMode"
        >
          <option v-for="p in PERMISSION_MODES" :key="p.value" :value="p.value">
            {{ p.label }}
          </option>
        </select>
      </div>
    </div>

    <div class="topbar-right">
      <div class="view-mode-group">
        <button
          v-for="m in VIEW_MODES"
          :key="m.value"
          class="view-btn"
          :class="{ active: currentSession?.viewMode === m.value }"
          :disabled="!currentSession"
          :title="m.desc"
          @click="changeViewMode(m.value)"
        >
          {{ m.label }}
        </button>
      </div>
      <button class="icon-btn" :title="themeTitle" @click="toggleTheme">
        <Sun v-if="resolvedTheme === 'light'" :size="18" />
        <Moon v-else :size="18" />
      </button>
      <button class="icon-btn" title="设置" @click="goSettings">
        <Settings :size="18" />
      </button>
    </div>
  </div>
</template>

<style scoped lang="scss">
.topbar {
  height: $topbar-height;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 $space-md;
  background-color: $color-bg-secondary;
  border-bottom: 1px solid $color-border;
  flex-shrink: 0;
  z-index: 10;
}

.topbar-left,
.topbar-right {
  display: flex;
  align-items: center;
  gap: $space-sm;
}

.app-name {
  font-size: $font-size-md;
  font-weight: $font-weight-bold;
  color: $color-primary;
}

.sep {
  color: $color-text-muted;
  margin: 0 $space-xs;
}

.session-info {
  display: flex;
  align-items: center;
  gap: $space-sm;
}

.select-sm {
  padding: $space-xs $space-sm;
  font-size: $font-size-xs;
  background-color: $color-bg-tertiary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  color: $color-text-primary;
  cursor: pointer;
  outline: none;

  &:hover {
    border-color: $color-border-light;
  }
}

.view-mode-group {
  display: flex;
  background-color: $color-bg-tertiary;
  border-radius: $radius-sm;
  padding: 2px;
}

.view-btn {
  padding: $space-xs $space-sm;
  font-size: $font-size-xs;
  border-radius: $radius-sm;
  color: $color-text-secondary;
  transition: all $transition-fast;

  &.active {
    background-color: $color-primary;
    color: #fff;
  }

  &:not(.active):not(:disabled):hover {
    color: $color-text-primary;
  }

  // 无会话时不可切换视图模式（视图模式是会话级配置）
  &:disabled {
    cursor: default;
    opacity: 0.45;
  }
}

.icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: $radius-sm;
  color: $color-text-secondary;
  transition: all $transition-fast;

  &:hover {
    background-color: $color-bg-tertiary;
    color: $color-text-primary;
  }
}
</style>
