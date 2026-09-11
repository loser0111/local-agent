<script setup>
import { ref, computed } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'

const tasks = ref([
  { id: 1, title: '分析现有认证模块结构', status: 'done' },
  { id: 2, title: '设计 JWT Token 生成与验证逻辑', status: 'done' },
  { id: 3, title: '实现登录 API 端点', status: 'running' },
  { id: 4, title: '添加密码哈希与盐值处理', status: 'pending' },
  { id: 5, title: '编写单元测试', status: 'pending' },
])

const completedCount = computed(() => tasks.value.filter((t) => t.status === 'done').length)
</script>

<template>
  <div class="tasks-pane">
    <PaneHeader type="tasks" :extra="`${completedCount}/${tasks.length} 完成`" />
    <div class="tasks-body scroll-container">
      <div
        v-for="task in tasks"
        :key="task.id"
        class="task-item"
        :class="task.status"
      >
        <span class="task-checkbox">
          <template v-if="task.status === 'done'">☑</template>
          <template v-else-if="task.status === 'running'">◐</template>
          <template v-else>☐</template>
        </span>
        <span class="task-title">{{ task.title }}</span>
        <span v-if="task.status === 'running'" class="task-status">进行中</span>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.tasks-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: $color-bg-primary;
}

.tasks-body {
  flex: 1;
  padding: $space-md;
}

.task-item {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-sm 0;
  border-bottom: 1px solid $color-border;

  &:last-child {
    border-bottom: none;
  }

  &.done .task-title {
    color: $color-text-muted;
    text-decoration: line-through;
  }

  &.running .task-title {
    color: $color-warning;
  }
}

.task-checkbox {
  font-size: $font-size-md;
  width: 20px;
  text-align: center;
}

.task-item.done .task-checkbox {
  color: $color-success;
}

.task-item.running .task-checkbox {
  color: $color-warning;
}

.task-title {
  flex: 1;
  font-size: $font-size-sm;
}

.task-status {
  font-size: $font-size-xs;
  color: $color-warning;
}
</style>
