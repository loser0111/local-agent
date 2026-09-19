<script setup>
import { ref, computed } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useUiStore } from '@/stores/ui'
import { formatTime } from '@/utils/format'

const sessionStore = useSessionStore()
const ui = useUiStore()

const emit = defineEmits(['new-session'])

const filter = ref('all')
const collapsed = defineModel('collapsed', { default: false })

// 左侧栏宽度（由父组件控制，可拖拽调整）
const props = defineProps({
  width: { type: Number, default: 240 },
})
const SIDEBAR_COLLAPSED = 56
const sidebarStyle = computed(() => ({
  width: (collapsed.value ? SIDEBAR_COLLAPSED : props.width) + 'px',
}))

const statusOptions = [
  { value: 'all', label: '全部' },
  { value: 'active', label: '活跃' },
  { value: 'completed', label: '已完成' },
  { value: 'archived', label: '归档' },
]

const filteredSessions = computed(() => {
  if (filter.value === 'all') return sessionStore.sessions
  return sessionStore.sessions.filter((s) => s.status === filter.value)
})

function handleNewSession() {
  emit('new-session')
}

function handleSwitch(id) {
  sessionStore.switchSession(id)
}

async function handleDelete(e, id) {
  e.stopPropagation()
  // 用应用内确认框：原生 confirm 在部分 webview 上返回假值，会导致删除静默失效
  const ok = await ui.ask({
    title: '删除会话',
    message: '确定删除该会话吗？会话记录与消息将一并移除，且不可恢复。',
    confirmText: '删除',
  })
  if (!ok) return
  try {
    await sessionStore.deleteSession(id)
  } catch (err) {
    alert(`删除失败：${err.message || err}`)
  }
}
</script>

<template>
  <div class="session-sidebar" :class="{ collapsed }" :style="sidebarStyle">
    <div class="sidebar-header">
      <button v-if="!collapsed" class="btn btn-primary new-session-btn" @click="handleNewSession">
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
          <path d="M7 1V13M1 7H13" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
        </svg>
        新建会话
      </button>
      <button v-else class="icon-btn" title="新建会话" @click="handleNewSession">
        <svg width="16" height="16" viewBox="0 0 14 14" fill="none">
          <path d="M7 1V13M1 7H13" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
        </svg>
      </button>
    </div>

    <div v-if="!collapsed" class="sidebar-filter">
      <select v-model="filter" class="filter-select">
        <option v-for="o in statusOptions" :key="o.value" :value="o.value">
          {{ o.label }}
        </option>
      </select>
    </div>

    <div class="session-list scroll-container">
      <div
        v-for="s in filteredSessions"
        :key="s.id"
        class="session-item"
        :class="{ active: s.id === sessionStore.currentSessionId }"
        @click="handleSwitch(s.id)"
      >
        <span class="status-dot" :class="s.status" />
        <div v-if="!collapsed" class="session-content">
          <div class="session-title">{{ s.title }}</div>
          <div class="session-meta">
            <span>{{ s.status === 'active' ? '活跃' : s.status === 'completed' ? '已完成' : s.status === 'archived' ? '归档' : s.status }}</span>
            <span>·</span>
            <span>{{ formatTime(s.endAt || s.startAt) }}</span>
          </div>
        </div>
        <button
          v-if="!collapsed"
          class="delete-btn"
          title="删除会话"
          @click="(e) => handleDelete(e, s.id)"
        >
          <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
            <path d="M1 1L11 11M11 1L1 11" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
          </svg>
        </button>
      </div>

      <div v-if="filteredSessions.length === 0 && !collapsed" class="empty-tip">
        暂无会话
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.session-sidebar {
  background-color: $color-bg-secondary;
  border-right: 1px solid $color-border;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  overflow: hidden;

  &.collapsed {
    width: $sidebar-width-collapsed;
  }
}

.sidebar-header {
  padding: $space-md;
  flex-shrink: 0;
}

.new-session-btn {
  width: 100%;
  justify-content: center;
}

.sidebar-filter {
  padding: 0 $space-md $space-md;
  flex-shrink: 0;
}

.filter-select {
  width: 100%;
  padding: $space-xs $space-sm;
  font-size: $font-size-xs;
  background-color: $color-bg-tertiary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  color: $color-text-primary;
  cursor: pointer;
  outline: none;
}

.session-list {
  flex: 1;
  padding: 0 $space-sm;
}

.session-item {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-sm $space-md;
  margin-bottom: 2px;
  border-radius: $radius-sm;
  cursor: pointer;
  transition: background-color $transition-fast;

  &:hover {
    background-color: $color-bg-tertiary;

    .delete-btn {
      opacity: 1;
    }
  }

  &.active {
    background-color: rgb(var(--color-primary-rgb) / 0.15);
  }
}

.session-content {
  flex: 1;
  min-width: 0;
}

.session-title {
  font-size: $font-size-sm;
  color: $color-text-primary;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.session-meta {
  font-size: $font-size-xs;
  color: $color-text-muted;
  display: flex;
  gap: $space-xs;
  margin-top: 2px;
}

.delete-btn {
  opacity: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: $radius-sm;
  color: $color-text-muted;
  transition: all $transition-fast;

  &:hover {
    background-color: $color-error;
    color: #fff;
  }
}

.empty-tip {
  text-align: center;
  color: $color-text-muted;
  font-size: $font-size-xs;
  padding: $space-xl 0;
}

.icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 32px;
  border-radius: $radius-sm;
  color: $color-text-secondary;
  background-color: $color-bg-tertiary;
  transition: all $transition-fast;

  &:hover {
    color: $color-text-primary;
  }
}
</style>
