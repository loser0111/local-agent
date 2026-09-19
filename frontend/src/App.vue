<script setup>
import { onMounted } from 'vue'
import { RouterView } from 'vue-router'
import { onUserInteraction } from '@/api/interaction'
import { usePermissionStore } from '@/stores/permissions'
import { useAskStore } from '@/stores/asks'
import UiDialogHost from '@/components/business/UiDialogHost.vue'
import PermissionDialog from '@/components/business/PermissionDialog.vue'
import AskUserDialog from '@/components/business/AskUserDialog.vue'

const permissionStore = usePermissionStore()
const askStore = useAskStore()

// 授权与提问请求在应用根部统一收口：它们无论从哪条链路发起（聊天 / 计划执行）
// 都必须能弹窗，因此不能依赖某次调用的回调。订阅进程级常驻，不取消（原因见 api/interaction.js）。
onMounted(() => {
  onUserInteraction((ev) => {
    if (!ev) return
    if (ev.type === 'permission_request' && ev.permission) {
      permissionStore.setPending(ev.permission)
    } else if (ev.type === 'ask_user' && ev.ask) {
      askStore.setPending(ev.ask)
    }
  })
})
</script>

<template>
  <RouterView />

  <!-- 授权 / 提问弹窗：与上面的 onUserInteraction 收口在同一层。
       它们原先挂在 ChatPane 上，而 ChatPane 只在工作区渲染 —— 在设置页时
       权限请求弹不出来，后端在工具循环里阻塞等待，最终只能按超时拒绝处理。 -->
  <PermissionDialog />
  <AskUserDialog />

  <!-- 应用内确认框与提示条：任何页面都能用，不受宿主 webview 对话框能力影响 -->
  <UiDialogHost />
</template>

<style>
/* 全局样式已在 assets/styles/global.scss 中定义 */
</style>
