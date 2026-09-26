<script setup>
import { ref, computed } from 'vue'
import {
  Bot,
  ChevronsLeft,
  ChevronsRight,
  ClipboardList,
  Eye,
  FileCode,
  FileDiff,
  ListTodo,
  PanelRight,
  Terminal,
} from 'lucide-vue-next'
import { useSessionStore } from '@/stores/session'
import { usePaneStore } from '@/stores/pane'
import { PANE_TITLES } from '@/types'
import ResizeDivider from '@/components/layout/ResizeDivider.vue'
import ChatPane from '@/panes/ChatPane.vue'
import DiffPane from '@/panes/DiffPane.vue'
import TerminalPane from '@/panes/TerminalPane.vue'
import FileEditorPane from '@/panes/FileEditorPane.vue'
import PlanPane from '@/panes/PlanPane.vue'
import TasksPane from '@/panes/TasksPane.vue'
import SubagentPane from '@/panes/SubagentPane.vue'
import PreviewPane from '@/panes/PreviewPane.vue'

const sessionStore = useSessionStore()
const paneStore = usePaneStore()

const sessionId = computed(() => sessionStore.currentSessionId)

const openPanes = computed(() => {
  if (!sessionId.value) return ['chat']
  return paneStore.getPanes(sessionId.value)
})

const activePane = computed(() => {
  if (!sessionId.value) return 'chat'
  return paneStore.getActivePane(sessionId.value)
})

const secondaryPanes = computed(() => openPanes.value.filter((p) => p !== 'chat'))
const hasSecondary = computed(() => secondaryPanes.value.length > 0)

// 右侧**实际**渲染哪个面板。
//
// 不能直接拿 activePane：它可能是 'chat'（DiffPane 的「Review code」会把焦点切回聊天），
// 那样右侧会再渲染一个 ChatPane——同一个聊天面板并排出现两次（改前就是这样）。
// 落在右侧面板集合之外时回落到第一个。
const secondaryActivePane = computed(() => {
  const list = secondaryPanes.value
  if (!list.length) return ''
  return list.includes(activePane.value) ? activePane.value : list[0]
})

// 右侧是否收起。收起后不是"隐藏"，而是变成一条窄条（见模板）：每个已打开的面板还是一个图标，
// 因此"有几个面板开着、是哪些"始终可见，也不会出现"面板被打开了却毫无痕迹"。
const collapsed = computed(() => (sessionId.value ? paneStore.isSecondaryCollapsed(sessionId.value) : false))

function toggleCollapse() {
  if (sessionId.value) paneStore.toggleSecondaryCollapsed(sessionId.value)
}

/** 从窄条上点图标：展开并切到该面板 */
function expandTo(pane) {
  paneStore.setActivePane(sessionId.value, pane)
  paneStore.setSecondaryCollapsed(sessionId.value, false)
}

function setActive(pane) {
  paneStore.setActivePane(sessionId.value, pane)
}

function closePane(pane) {
  paneStore.closePane(sessionId.value, pane)
}

// 面板 → 图标。窄条是收起态的**唯一**可见信息，所以每个面板都得有图标；
// 没登记的面板退回一个通用图标而不是留空（留空等于"这里什么都没有"）。
const PANE_ICONS = {
  diff: FileDiff,
  terminal: Terminal,
  'file-editor': FileCode,
  plan: ClipboardList,
  tasks: ListTodo,
  subagent: Bot,
  preview: Eye,
}

function paneIcon(pane) {
  return PANE_ICONS[pane] || PanelRight
}

// 中间栏宽度（可拖拽调整）
const CHAT_DEFAULT = '60%'
const CHAT_MIN = 200
const chatWidth = ref(CHAT_DEFAULT)

// 收起右侧时聊天区自己铺满：chatWidth（百分比或像素）此时已经没有意义，
// 继续用 `0 0 X%` 会在右边留下一条空白。
const chatStyle = computed(() =>
  collapsed.value ? { flex: '1 1 auto' } : { flex: '0 0 ' + chatWidth.value }
)

function onChatDrag(dx) {
  const container = document.querySelector('.pane-container')
  if (!container) return
  const total = container.clientWidth
  // 当前像素宽度
  const currentPx = chatWidth.value.endsWith('%')
    ? (parseFloat(chatWidth.value) / 100) * total
    : parseFloat(chatWidth.value)
  const newPx = Math.max(CHAT_MIN, currentPx + dx)
  chatWidth.value = newPx + 'px'
}

const paneComponents = {
  chat: ChatPane,
  diff: DiffPane,
  terminal: TerminalPane,
  'file-editor': FileEditorPane,
  plan: PlanPane,
  tasks: TasksPane,
  subagent: SubagentPane,
  preview: PreviewPane,
}
</script>

<template>
  <div class="pane-container">
    <!-- 左侧：对话面板（常驻） -->
    <div class="pane chat-pane" :style="chatStyle">
      <component :is="paneComponents.chat" />
    </div>

    <template v-if="hasSecondary">
      <!-- 拖拽分隔条：收起时不需要（拖拽本身也会被隐藏掉） -->
      <ResizeDivider v-if="!collapsed" @drag="onChatDrag" />

      <!-- 收起态：窄条 + 图标。点图标 = 展开并切到该面板 -->
      <div v-if="collapsed" class="pane-strip">
        <button
          class="strip-btn strip-expand"
          title="展开右侧面板（也可以点下面的图标直达某个面板）"
          @click="toggleCollapse"
        >
          <ChevronsLeft :size="15" />
        </button>
        <button
          v-for="p in secondaryPanes"
          :key="p"
          class="strip-btn"
          :class="{ active: secondaryActivePane === p }"
          :title="`展开并切到「${PANE_TITLES[p] || p}」`"
          @click="expandTo(p)"
        >
          <component :is="paneIcon(p)" :size="16" />
        </button>
      </div>

      <!-- 展开态：Tab 切换 + 内容 -->
      <div v-else class="pane secondary-pane">
        <div class="pane-tabs">
          <div
            v-for="p in secondaryPanes"
            :key="p"
            class="pane-tab"
            :class="{ active: secondaryActivePane === p }"
            @click="setActive(p)"
          >
            <span>{{ PANE_TITLES[p] }}</span>
            <button class="tab-close" @click.stop="closePane(p)">
              <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
                <path d="M1 1L9 9M9 1L1 9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
              </svg>
            </button>
          </div>
          <!-- 收起：只收起右侧这块，不关闭任何面板（面板入口变成窄条上的图标） -->
          <button class="tab-collapse" title="收起右侧面板（面板不会被关闭）" @click="toggleCollapse">
            <ChevronsRight :size="14" />
          </button>
        </div>
        <div class="secondary-content">
          <component :is="paneComponents[secondaryActivePane]" />
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped lang="scss">
.pane-container {
  display: flex;
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}

.pane {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.chat-pane {
  border-right: 1px solid $color-border;
}

.secondary-pane {
  flex: 1;
  min-width: 200px;
  background-color: $color-bg-primary;
}

.pane-tabs {
  display: flex;
  height: $pane-header-height;
  background-color: $color-bg-secondary;
  border-bottom: 1px solid $color-border;
  padding: 0 $space-xs;
  flex-shrink: 0;
}

.pane-tab {
  display: flex;
  align-items: center;
  gap: $space-xs;
  padding: 0 $space-md;
  font-size: $font-size-sm;
  color: $color-text-secondary;
  cursor: pointer;
  border-bottom: 2px solid transparent;
  transition: all $transition-fast;

  &:hover {
    color: $color-text-primary;
  }

  &.active {
    color: $color-primary;
    border-bottom-color: $color-primary;
  }
}

.tab-close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border-radius: $radius-sm;
  color: $color-text-muted;
  opacity: 0;
  transition: all $transition-fast;

  .pane-tab:hover & {
    opacity: 1;
  }

  &:hover {
    background-color: $color-bg-tertiary;
    color: $color-text-primary;
  }
}

// 收起按钮：靠右。margin-left:auto 让它在 tab 少的时候贴右边缘、tab 多的时候也不被挤掉
.tab-collapse {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  margin-left: auto;
  flex-shrink: 0;
  align-self: center;
  border-radius: $radius-sm;
  color: $color-text-secondary;
  transition: all $transition-fast;

  &:hover {
    background-color: $color-bg-tertiary;
    color: $color-text-primary;
  }
}

// 收起态：窄条。宽度只够放图标，与左侧会话栏的收起形态同一个思路
.pane-strip {
  width: 38px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  padding-bottom: $space-xs;
  background-color: $color-bg-secondary;
}

.strip-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: $radius-sm;
  color: $color-text-secondary;
  transition: all $transition-fast;

  &:hover {
    background-color: $color-bg-tertiary;
    color: $color-text-primary;
  }

  &.active {
    color: $color-primary;
    background-color: $color-bg-tertiary;
  }
}

// 展开按钮做成与面板标题栏等高的一行：视觉上与右侧面板的 tab 栏对齐，
// 一眼能看出"这条窄条就是刚才那块面板"
.strip-expand {
  width: 100%;
  height: $pane-header-height;
  border-radius: 0;
  margin-bottom: $space-xs;

  &:hover {
    background-color: $color-bg-tertiary;
  }
}

.secondary-content {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
</style>
