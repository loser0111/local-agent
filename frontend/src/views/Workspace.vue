<script setup>
import { ref, watch, onMounted } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useChatStore } from '@/stores/chat'
import { listRunningSessions } from '@/api/session'
import TopBar from '@/components/layout/TopBar.vue'
import SessionSidebar from '@/components/layout/SessionSidebar.vue'
import PaneContainer from '@/components/layout/PaneContainer.vue'
import ResizeDivider from '@/components/layout/ResizeDivider.vue'
import NewSessionDialog from '@/components/business/NewSessionDialog.vue'

const sessionStore = useSessionStore()
const chatStore = useChatStore()

const sidebarCollapsed = ref(false)
const showNewSession = ref(false)

// 左侧栏宽度（可拖拽调整）
const SIDEBAR_DEFAULT = 240
const SIDEBAR_MIN = 160
const SIDEBAR_MAX = 480
const sidebarWidth = ref(SIDEBAR_DEFAULT)

function onSidebarDrag(dx) {
  sidebarWidth.value = Math.max(SIDEBAR_MIN, Math.min(SIDEBAR_MAX, sidebarWidth.value + dx))
}

onMounted(async () => {
  // 从后端加载历史会话列表
  await sessionStore.init()
  // 会话列表就绪后，把"哪几条会话正在跑"也从后端补回来：
  // 页面重载（wails dev 热更新 / 手动刷新）会清空前端内存态，而后端的运行还在跑，
  // 不补的话界面会把它显示成空闲，用户点发送只会收到互斥拒绝、看不出原因。
  chatStore.hydrateRunning(await listRunningSessions())
})

// 切换会话时加载该会话的历史消息
watch(
  () => sessionStore.currentSessionId,
  (id) => {
    chatStore.loadMessages(id)
  }
)

function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value
}

async function handleNewSession(config) {
  await sessionStore.createSession(config)
  showNewSession.value = false
}
</script>

<template>
  <div class="workspace">
    <TopBar @toggle-sidebar="toggleSidebar" />
    <div class="workspace-body">
      <SessionSidebar v-model:collapsed="sidebarCollapsed" :width="sidebarWidth" @new-session="showNewSession = true" />
      <ResizeDivider v-if="!sidebarCollapsed" @drag="onSidebarDrag" />
      <PaneContainer />
    </div>
    <NewSessionDialog
      v-if="showNewSession"
      @close="showNewSession = false"
      @confirm="handleNewSession"
    />
  </div>
</template>

<style scoped lang="scss">
.workspace {
  display: flex;
  flex-direction: column;
  height: 100%;
  width: 100%;
}

.workspace-body {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}
</style>
