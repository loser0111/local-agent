<script setup>
import { ref } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'

const planItems = ref([
  { id: 1, title: '分析现有认证模块结构', status: 'done' },
  { id: 2, title: '设计 JWT Token 生成与验证逻辑', status: 'done' },
  { id: 3, title: '实现登录 API 端点', status: 'running' },
  { id: 4, title: '添加密码哈希与盐值处理', status: 'pending' },
  { id: 5, title: '编写单元测试', status: 'pending' },
])

function approve() {
  alert('计划已批准，开始执行...')
}
</script>

<template>
  <div class="plan-pane">
    <PaneHeader type="plan" />
    <div class="plan-body scroll-container">
      <div class="plan-title">📋 实现用户登录功能</div>
      <div class="plan-list">
        <div
          v-for="item in planItems"
          :key="item.id"
          class="plan-item"
          :class="item.status"
        >
          <span class="plan-icon">
            <template v-if="item.status === 'done'">✅</template>
            <template v-else-if="item.status === 'running'">🔄</template>
            <template v-else>⏳</template>
          </span>
          <span class="plan-text">{{ item.title }}</span>
        </div>
      </div>
      <div class="plan-footer">
        <button class="btn btn-primary" @click="approve">批准计划并执行</button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.plan-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: $color-bg-primary;
}

.plan-body {
  flex: 1;
  padding: $space-lg;
}

.plan-title {
  font-size: $font-size-lg;
  font-weight: $font-weight-semibold;
  margin-bottom: $space-lg;
}

.plan-list {
  display: flex;
  flex-direction: column;
  gap: $space-md;
}

.plan-item {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-sm $space-md;
  background-color: $color-bg-secondary;
  border-radius: $radius-md;
  border-left: 3px solid transparent;

  &.done {
    border-left-color: $color-success;
    .plan-text {
      color: $color-text-muted;
      text-decoration: line-through;
    }
  }
  &.running {
    border-left-color: $color-warning;
  }
}

.plan-icon {
  font-size: $font-size-md;
}

.plan-text {
  font-size: $font-size-sm;
}

.plan-footer {
  margin-top: $space-xl;
  display: flex;
  justify-content: center;
}
</style>
