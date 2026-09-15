<script setup>
import { ref, computed, watch } from 'vue'
import ToolCallCard from './ToolCallCard.vue'

/**
 * 模型思考 / 工具调用过程折叠块（Claude Code 风格）
 * - 流式执行中：自动展开，实时展示每个工具调用状态
 * - 执行完成：自动折叠，标题行展示摘要（可手动展开查看细节）
 */
const props = defineProps({
  toolCalls: { type: Array, default: () => [] },
  streaming: { type: Boolean, default: false },
})

// 用户是否手动切换过折叠状态（手动操作后不再自动折叠）
const userToggled = ref(false)
// 展开状态：流式中默认展开，完成后默认折叠
const expanded = ref(props.streaming)

watch(
  () => props.streaming,
  (streaming) => {
    if (!userToggled.value) {
      expanded.value = streaming
    }
  }
)

function toggle() {
  userToggled.value = true
  expanded.value = !expanded.value
}

// ===== 工具名称友好化 =====
function truncate(str, n = 40) {
  const s = String(str || '')
  return s.length > n ? s.slice(0, n) + '…' : s
}

function toolLabel(tc) {
  const args = tc.args || {}
  switch (tc.name) {
    case 'exec_shell':
      return args.cmd ? truncate(args.cmd, 36) : '终端命令'
    case 'tool_router': {
      if (args.action === 'list') return '查询可用工具'
      if (args.action === 'execute') return `调用工具 ${args.tool_name || ''}`
      return '工具路由'
    }
    default:
      return tc.name
  }
}

// ===== 状态统计 =====
const runningCount = computed(() => props.toolCalls.filter((t) => t.status === 'running').length)
const pendingCount = computed(() => props.toolCalls.filter((t) => t.status === 'pending').length)
const deniedCount = computed(() => props.toolCalls.filter((t) => t.status === 'denied').length)
const errorCount = computed(() => props.toolCalls.filter((t) => t.status === 'error').length)
const successCount = computed(() => props.toolCalls.filter((t) => t.status === 'success').length)
const totalDuration = computed(() =>
  props.toolCalls.reduce((sum, t) => sum + (t.duration || 0), 0)
)

// 等待授权也算「进行中」，否则会显示成「已完成 0 个操作」而让人以为卡死了
const isRunning = computed(() => props.streaming || runningCount.value > 0 || pendingCount.value > 0)
const hasError = computed(() => errorCount.value > 0)

// 折叠态摘要
const summary = computed(() => {
  if (pendingCount.value > 0) {
    return `${pendingCount.value} 个工具等待授权…`
  }
  if (isRunning.value) {
    return runningCount.value > 0
      ? `正在执行 ${runningCount.value} 个工具…`
      : '正在思考…'
  }
  if (hasError.value) {
    return `${errorCount.value} 个工具执行失败`
  }
  if (deniedCount.value > 0) {
    return `已完成 ${successCount.value} 个操作，${deniedCount.value} 个被权限拒绝`
  }
  return `已完成 ${successCount.value} 个操作`
})

// 折叠态展示的工具标签（去重 + 最多展示 3 个）
const MAX_VISIBLE_LABELS = 3
const labels = computed(() => {
  const seen = new Set()
  const unique = []
  for (const tc of props.toolCalls) {
    const label = toolLabel(tc)
    if (!seen.has(label)) {
      seen.add(label)
      unique.push(label)
    }
  }
  return unique.slice(0, MAX_VISIBLE_LABELS)
})
const hiddenLabelCount = computed(() => {
  const seen = new Set(props.toolCalls.map(toolLabel))
  return Math.max(0, seen.size - MAX_VISIBLE_LABELS)
})
</script>

<template>
  <div class="tool-process" :class="{ running: isRunning, error: hasError && !isRunning }">
    <!-- 折叠标题行（语义化按钮，支持键盘操作） -->
    <button
      type="button"
      class="process-header"
      :aria-expanded="expanded"
      @click="toggle"
    >
      <span class="process-status-icon">
        <span v-if="isRunning" class="spinner"></span>
        <svg v-else-if="hasError" class="status-icon error-icon" viewBox="0 0 16 16" fill="none">
          <circle cx="8" cy="8" r="6.5" stroke="currentColor" stroke-width="1.4" />
          <path d="M8 4.5v4M8 11h.01" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
        </svg>
        <svg v-else class="status-icon success-icon" viewBox="0 0 16 16" fill="none">
          <circle cx="8" cy="8" r="6.5" stroke="currentColor" stroke-width="1.4" />
          <path d="M5 8.2l2 2 4-4.4" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </span>

      <span class="process-summary">{{ summary }}</span>

      <!-- 工具标签 -->
      <span v-if="!isRunning && labels.length" class="process-labels">
        <span v-for="(label, i) in labels" :key="i" class="process-label">{{ label }}</span>
        <span v-if="hiddenLabelCount > 0" class="process-label process-label-more">
          +{{ hiddenLabelCount }}
        </span>
      </span>

      <span v-if="!isRunning && totalDuration > 0" class="process-duration">
        {{ totalDuration >= 1 ? totalDuration.toFixed(1) + 's' : Math.round(totalDuration * 1000) + 'ms' }}
      </span>

      <svg
        class="chevron"
        :class="{ open: expanded }"
        width="12"
        height="12"
        viewBox="0 0 12 12"
        fill="none"
      >
        <path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>

    <!-- 展开内容（grid 高度动画） -->
    <div class="process-body" :class="{ open: expanded }">
      <div class="process-body-inner">
        <div
          v-for="(tc, i) in toolCalls"
          :key="tc.id"
          class="process-step"
        >
          <span class="step-index">{{ i + 1 }}</span>
          <ToolCallCard :tool-call="tc" class="step-card" />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.tool-process {
  border: 1px solid $color-border;
  border-radius: $radius-md;
  background-color: rgba(68, 71, 90, 0.25);
  overflow: hidden;
  transition: border-color $transition-fast, background-color $transition-fast;

  &.running {
    border-color: rgba(189, 147, 249, 0.45);
    background-color: rgba(189, 147, 249, 0.06);
  }

  &.error {
    border-color: rgba(255, 85, 85, 0.4);
  }
}

.process-header {
  // button 样式重置
  width: 100%;
  border: none;
  background: transparent;
  font: inherit;
  text-align: left;

  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: 7px 12px;
  cursor: pointer;
  user-select: none;
  font-size: $font-size-xs;
  color: $color-text-secondary;

  &:hover {
    background-color: rgba(255, 255, 255, 0.03);
    color: $color-text-primary;
  }

  &:focus-visible {
    outline: 2px solid rgba(189, 147, 249, 0.6);
    outline-offset: -2px;
  }
}

.process-status-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: $color-primary;
}

.status-icon {
  width: 13px;
  height: 13px;
}

.success-icon {
  color: $color-success;
}

.error-icon {
  color: $color-error;
}

// 旋转加载圈
.spinner {
  width: 12px;
  height: 12px;
  border: 1.5px solid rgba(189, 147, 249, 0.25);
  border-top-color: $color-primary;
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.process-summary {
  font-weight: $font-weight-medium;
  flex-shrink: 0;
}

.process-labels {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-left: 2px;
  min-width: 0;
  overflow: hidden;
}

.process-label {
  flex-shrink: 1;
  min-width: 0;
  padding: 1px 8px;
  border-radius: 10px;
  background-color: rgba(68, 71, 90, 0.8);
  color: $color-text-secondary;
  font-size: 11px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 220px;
}

.process-duration {
  flex-shrink: 0;
  color: $color-text-muted;
  font-size: 11px;
}

.chevron {
  flex-shrink: 0;
  color: $color-text-muted;
  transition: transform $transition-fast;
  margin-left: auto;

  &.open {
    transform: rotate(180deg);
  }
}

// 折叠/展开平滑动画
.process-body {
  display: grid;
  grid-template-rows: 0fr;
  transition: grid-template-rows 0.22s ease;

  &.open {
    grid-template-rows: 1fr;
  }
}

.process-body-inner {
  overflow: hidden;
  min-height: 0;
}

.process-body.open .process-body-inner {
  padding: 8px 12px 10px;
  border-top: 1px solid $color-border;
  display: flex;
  flex-direction: column;
  gap: $space-sm;
}

// 多轮调用步骤：序号 + 卡片
.process-step {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.step-index {
  flex-shrink: 0;
  width: 18px;
  height: 18px;
  margin-top: 5px;
  border-radius: 50%;
  background-color: #44475a;
  color: $color-text-secondary;
  font-size: 10px;
  font-weight: $font-weight-semibold;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.step-card {
  flex: 1;
  min-width: 0;
}

.process-label-more {
  font-weight: $font-weight-semibold;
}
</style>
