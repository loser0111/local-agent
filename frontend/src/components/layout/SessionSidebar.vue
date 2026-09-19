<script setup>
import { ref, computed, watch } from 'vue'
import { ChevronDown, ChevronRight, FolderOpen } from 'lucide-vue-next'
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

// ===== 按工作区分组 + 分组折叠 =====
//
// 同一个工作区（会话的 project 字段，即「项目文件夹」）里通常不止一个会话，
// 平铺列出既长、又看不出每条会话属于哪个目录。这里按工作区分组，每组单独折叠。
// 折叠状态持久化：否则每次启动都要重新折一遍，折叠也就白做了。
const COLLAPSED_KEY = 'local-agent:collapsed-workspaces'
const NO_WORKSPACE = '__none__' // 未设置工作区目录的会话（旧数据 / 浏览器 mock 模式）

/** 读取折叠状态（存的是被折叠的工作区路径数组） */
function readCollapsed() {
  try {
    const raw = localStorage.getItem(COLLAPSED_KEY)
    const keys = raw ? JSON.parse(raw) : []
    if (!Array.isArray(keys)) return {}
    return Object.fromEntries(keys.filter((k) => typeof k === 'string').map((k) => [k, true]))
  } catch {
    return {} // 存坏了就当没折过；不能因为读存储失败就让会话列表渲染不出来
  }
}

// key 为工作区路径（或 NO_WORKSPACE），value 为 true 表示已折叠
const collapsedGroups = ref(readCollapsed())

function workspaceKey(session) {
  const project = (session.project || '').trim()
  if (!project) return NO_WORKSPACE
  // 去掉末尾分隔符：同一个目录写成 "C:\work" 还是 "C:\work\" 不该分成两个组
  // （末尾回退到原值是给根目录 "/" 这类情况兜底，不能让 key 变成空串）
  return project.replace(/[\\/]+$/, '') || project
}

/** 组标题只显示路径末段目录名（完整路径太长，放 tooltip 里） */
function workspaceLabel(key) {
  if (key === NO_WORKSPACE) return '未指定工作区'
  const parts = key.split(/[\\/]/)
  return parts[parts.length - 1] || key
}

const filteredSessions = computed(() => {
  if (filter.value === 'all') return sessionStore.sessions
  return sessionStore.sessions.filter((s) => s.status === filter.value)
})

/**
 * 渲染用的分组列表。
 * - 侧边栏整体折叠时只有状态点，不需要分组标题，因此退化成无标题的单组；
 * - 组的顺序沿用会话顺序（后端按最近活跃倒序），即「最近动过的工作区排在前面」。
 */
const groups = computed(() => {
  if (collapsed.value) {
    return [{ key: null, label: '', hint: '', sessions: filteredSessions.value }]
  }
  const map = new Map()
  for (const s of filteredSessions.value) {
    const key = workspaceKey(s)
    let group = map.get(key)
    if (!group) {
      group = {
        key,
        label: workspaceLabel(key),
        hint: key === NO_WORKSPACE ? '这些会话没有设置工作区目录' : key,
        sessions: [],
      }
      map.set(key, group)
    }
    group.sessions.push(s)
  }
  return [...map.values()]
})

function isGroupCollapsed(key) {
  return !!collapsedGroups.value[key]
}

function toggleGroup(key) {
  const next = { ...collapsedGroups.value }
  if (next[key]) delete next[key]
  else next[key] = true
  collapsedGroups.value = next
  try {
    localStorage.setItem(COLLAPSED_KEY, JSON.stringify(Object.keys(next)))
  } catch {
    // 隐私模式等写不进去：只影响「记住折叠状态」，本次折叠照常生效
  }
}

// 当前会话若落在折叠的组里就自动展开它：首次加载（默认选中最近的会话）、
// 删除会话后回落到别的会话、新建会话都会走到这里——否则会「切了会话却看不见它在哪」。
// 注意用户手动折叠**当前会话所在**的组不会触发（此时 currentSessionId 没变），
// 所以手动折叠依然算数。
watch(
  () => sessionStore.currentSessionId,
  (id) => {
    if (!id) return
    const s = sessionStore.sessions.find((item) => item.id === id)
    if (s && isGroupCollapsed(workspaceKey(s))) toggleGroup(workspaceKey(s))
  }
)

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
      <template v-for="g in groups" :key="g.key || '__flat__'">
        <!-- 工作区分组头：点击整行折叠 / 展开 -->
        <button
          v-if="g.key"
          class="group-header"
          :title="g.hint"
          :aria-expanded="!isGroupCollapsed(g.key)"
          @click="toggleGroup(g.key)"
        >
          <ChevronDown v-if="!isGroupCollapsed(g.key)" :size="13" class="group-arrow" />
          <ChevronRight v-else :size="13" class="group-arrow" />
          <FolderOpen :size="13" class="group-icon" />
          <span class="group-name">{{ g.label }}</span>
          <span class="group-count" :title="`${g.sessions.length} 个会话`">{{ g.sessions.length }}</span>
        </button>

        <div v-show="!g.key || !isGroupCollapsed(g.key)" class="group-body" :class="{ grouped: !!g.key }">
          <div
            v-for="s in g.sessions"
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
        </div>
      </template>

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

.group-header {
  display: flex;
  align-items: center;
  gap: $space-xs;
  width: 100%;
  padding: $space-xs $space-sm;
  margin: $space-xs 0 2px;
  border-radius: $radius-sm;
  font-size: $font-size-xs;
  color: $color-text-secondary;
  text-align: left;
  transition: background-color $transition-fast;

  &:hover {
    background-color: $color-bg-tertiary;
    color: $color-text-primary;
  }
}

.group-arrow,
.group-icon {
  flex-shrink: 0;
  opacity: 0.8;
}

.group-name {
  flex: 1;
  min-width: 0;
  font-weight: $font-weight-medium;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.group-count {
  flex-shrink: 0;
  color: $color-text-muted;
  font-variant-numeric: tabular-nums;
}

.group-body.grouped {
  // 组内会话缩进一点，让层级一眼可辨
  padding-left: $space-sm;
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
