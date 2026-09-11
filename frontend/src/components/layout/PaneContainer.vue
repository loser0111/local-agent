<script setup>
import { ref, computed } from 'vue'
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

function setActive(pane) {
  paneStore.setActivePane(sessionId.value, pane)
}

function closePane(pane) {
  paneStore.closePane(sessionId.value, pane)
}

// 中间栏宽度（可拖拽调整）
const CHAT_DEFAULT = '60%'
const CHAT_MIN = 200
const chatWidth = ref(CHAT_DEFAULT)

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
    <div class="pane chat-pane" :style="{ flex: '0 0 ' + chatWidth }">
      <component :is="paneComponents.chat" />
    </div>

    <!-- 拖拽分隔条 -->
    <ResizeDivider v-if="secondaryPanes.length > 0" @drag="onChatDrag" />

    <!-- 右侧：其他面板（Tab 切换） -->
    <div v-if="secondaryPanes.length > 0" class="pane secondary-pane">
      <div class="pane-tabs">
        <div
          v-for="p in secondaryPanes"
          :key="p"
          class="pane-tab"
          :class="{ active: activePane === p }"
          @click="setActive(p)"
        >
          <span>{{ PANE_TITLES[p] }}</span>
          <button class="tab-close" @click.stop="closePane(p)">
            <svg width="10" height="10" viewBox="0 0 10 10" fill="none">
              <path d="M1 1L9 9M9 1L1 9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
            </svg>
          </button>
        </div>
      </div>
      <div class="secondary-content">
        <component :is="paneComponents[activePane]" />
      </div>
    </div>
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

.secondary-content {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
</style>
