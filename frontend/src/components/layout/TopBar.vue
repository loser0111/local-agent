<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useSessionStore } from '@/stores/session'
import { useSettingStore } from '@/stores/setting'
import { VIEW_MODES } from '@/types'
import { fetchModelNames } from '@/api/model'

const sessionStore = useSessionStore()
const settingStore = useSettingStore()
const router = useRouter()

const emit = defineEmits(['toggle-sidebar'])

const currentSession = computed(() => sessionStore.currentSession)

// 从后端加载模型名称列表
const modelNames = ref([])

async function loadModelNames() {
  try {
    const names = await fetchModelNames()
    modelNames.value = names
    // 如果当前会话没有模型且有可用模型，默认选第一个并持久化
    if (currentSession.value && !currentSession.value.model && names.length > 0) {
      await sessionStore.patchSession(currentSession.value.id, { model: names[0] })
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

function changeViewMode(mode) {
  settingStore.updateSetting('viewMode', mode)
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
      </div>
    </div>

    <div class="topbar-right">
      <div class="view-mode-group">
        <button
          v-for="m in VIEW_MODES"
          :key="m.value"
          class="view-btn"
          :class="{ active: settingStore.settings.viewMode === m.value }"
          @click="changeViewMode(m.value)"
        >
          {{ m.label }}
        </button>
      </div>
      <button class="icon-btn" title="使用量">
        <svg width="18" height="18" viewBox="0 0 18 18" fill="none">
          <circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.5" />
          <path d="M9 5V9L12 11" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
        </svg>
      </button>
      <button class="icon-btn" title="设置" @click="goSettings">
        <svg width="18" height="18" viewBox="0 0 18 18" fill="none">
          <circle cx="9" cy="9" r="2.5" stroke="currentColor" stroke-width="1.5" />
          <path
            d="M9 1.5v2M9 14.5v2M1.5 9h2M14.5 9h2M3.7 3.7l1.4 1.4M12.9 12.9l1.4 1.4M3.7 14.3l1.4-1.4M12.9 5.1l1.4-1.4"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
          />
        </svg>
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

  &:not(.active):hover {
    color: $color-text-primary;
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
