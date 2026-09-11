<script setup>
import { ref } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'

const subagents = ref([
  {
    id: 1,
    name: '前端构建 Agent',
    status: 'running',
    currentTool: 'Edit',
    duration: '45s',
  },
  {
    id: 2,
    name: '后端 API Agent',
    status: 'waiting',
    currentTool: '-',
    duration: '2m 10s',
  },
])

const expanded = ref({})

function toggle(id) {
  expanded.value[id] = !expanded.value[id]
}
</script>

<template>
  <div class="subagent-pane">
    <PaneHeader type="subagent" />
    <div class="subagent-body scroll-container">
      <div
        v-for="sa in subagents"
        :key="sa.id"
        class="subagent-card"
        :class="sa.status"
      >
        <div class="card-header" @click="toggle(sa.id)">
          <span class="status-dot" :class="sa.status" />
          <span class="sa-name">{{ sa.name }}</span>
          <span class="sa-duration">{{ sa.duration }}</span>
          <span class="expand-icon">{{ expanded[sa.id] ? '▼' : '▶' }}</span>
        </div>
        <div class="card-meta">
          <span class="status-text">
            {{ sa.status === 'running' ? '运行中' : sa.status === 'waiting' ? '等待输入' : '已完成' }}
          </span>
          <span v-if="sa.status === 'running'">· 正在执行 {{ sa.currentTool }}</span>
        </div>
        <div v-if="expanded[sa.id]" class="card-detail">
          <div class="detail-label">工具调用日志</div>
          <div class="log-line">- Read app.go (0.5s)</div>
          <div class="log-line">- Edit app.go (1.2s)</div>
          <div class="log-line">- Edit main.go (0.8s)</div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.subagent-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: $color-bg-primary;
}

.subagent-body {
  flex: 1;
  padding: $space-md;
}

.subagent-card {
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  padding: $space-md;
  margin-bottom: $space-md;
  cursor: pointer;

  &.running {
    border-color: rgba(245, 158, 11, 0.3);
  }
}

.card-header {
  display: flex;
  align-items: center;
  gap: $space-sm;
}

.sa-name {
  flex: 1;
  font-size: $font-size-sm;
  font-weight: $font-weight-medium;
}

.sa-duration {
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.expand-icon {
  font-size: 8px;
  color: $color-text-muted;
}

.card-meta {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  margin-top: $space-xs;
  padding-left: 20px;
}

.status-text.running {
  color: $color-warning;
}

.card-detail {
  margin-top: $space-md;
  padding-top: $space-sm;
  border-top: 1px solid $color-border;
}

.detail-label {
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin-bottom: $space-xs;
}

.log-line {
  font-family: $font-family-mono;
  font-size: $font-size-xs;
  color: $color-text-secondary;
  padding: 2px 0;
}
</style>
