<script setup>
import { ref, computed, watch } from 'vue'

const props = defineProps({
  toolCall: { type: Object, required: true },
  // Verbose 视图模式下默认展开参数与输出（完整展示工具调用过程）
  defaultExpanded: { type: Boolean, default: false },
})

// 用户是否手动切换过（手动操作后不再跟随视图模式）
const userToggled = ref(false)
const expanded = ref(props.defaultExpanded)

watch(
  () => props.defaultExpanded,
  (v) => {
    if (!userToggled.value) expanded.value = v
  }
)

const statusText = computed(() => {
  switch (props.toolCall.status) {
    case 'running':
      return '运行中'
    case 'pending':
      return '等待授权'
    case 'success':
      return '完成'
    case 'error':
      return '失败'
    default:
      return ''
  }
})

const statusColor = computed(() => {
  switch (props.toolCall.status) {
    case 'running':
      return '#ffb86c'
    case 'pending':
      return '#f5a623'
    case 'success':
      return '#50fa7b'
    case 'error':
      return '#ff5555'
    default:
      return '#6272a4'
  }
})

function toggleExpand() {
  userToggled.value = true
  expanded.value = !expanded.value
}

// 工具名称友好化
const friendlyName = computed(() => {
  const map = {
    exec_shell: '终端命令',
    tool_router: '工具路由',
    read_file: '读取文件',
    write_file: '写入文件',
    edit_file: '编辑文件',
    glob: '查找文件',
    grep: '搜索内容',
    list_dir: '列出目录',
  }
  return map[props.toolCall.name] || props.toolCall.name
})

// 标题栏展示的关键参数
const argSummary = computed(() => {
  const args = props.toolCall.args || {}
  if (args.cmd) return args.cmd
  if (args.path) return args.path
  if (args.action === 'list') return 'list'
  if (args.action === 'execute') return args.tool_name || 'execute'
  return ''
})
</script>

<template>
  <div class="tool-call-card" :class="toolCall.status">
    <button
      type="button"
      class="tool-call-header"
      :aria-expanded="expanded"
      @click="toggleExpand"
    >
      <span class="tool-icon">🔧</span>
      <span class="tool-name">{{ friendlyName }}</span>
      <span class="tool-args" v-if="argSummary" :title="argSummary">· {{ argSummary }}</span>
      <span class="tool-status" :style="{ color: statusColor }">
        <span v-if="toolCall.status === 'running'" class="mini-spinner"></span>
        <span v-else-if="toolCall.status === 'pending'" class="mini-spinner pending-spinner"></span>
        {{ statusText }}
        <span v-if="toolCall.duration"> · {{ toolCall.duration >= 1 ? toolCall.duration.toFixed(1) : (toolCall.duration * 1000).toFixed(0) + 'ms' }}</span>
      </span>
      <span class="expand-icon">{{ expanded ? '▼' : '▶' }}</span>
    </button>

    <div v-if="expanded" class="tool-call-detail">
      <div v-if="toolCall.args && Object.keys(toolCall.args).length" class="detail-section">
        <div class="detail-label">参数</div>
        <pre class="detail-content">{{ JSON.stringify(toolCall.args, null, 2) }}</pre>
      </div>
      <!-- 改动文件：由后端按工具调用归因（write_file / edit_file 才有值） -->
      <div v-if="toolCall.files && toolCall.files.length" class="detail-section">
        <div class="detail-label">改动文件（{{ toolCall.files.length }}）</div>
        <ul class="file-list">
          <li v-for="(f, i) in toolCall.files" :key="i" class="mono">{{ f }}</li>
        </ul>
      </div>
      <div v-if="toolCall.result" class="detail-section">
        <div class="detail-label">结果</div>
        <pre class="detail-content">{{ toolCall.result }}</pre>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.tool-call-card {
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  overflow: hidden;
  transition: border-color $transition-fast;

  &.running {
    border-color: rgba(245, 158, 11, 0.5);
  }
  // 等待用户授权：与运行中区分（更醒目的橙色描边）
  &.pending {
    border-color: rgba(245, 166, 35, 0.75);
    box-shadow: 0 0 0 1px rgba(245, 166, 35, 0.25);
  }
  &.success {
    border-color: rgba(16, 185, 129, 0.3);
  }
  &.error {
    border-color: rgba(239, 68, 68, 0.5);
  }
}

.tool-call-header {
  // button 样式重置
  width: 100%;
  border: none;
  background: transparent;
  font: inherit;
  text-align: left;

  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-sm $space-md;
  cursor: pointer;
  font-size: $font-size-sm;

  &:hover {
    background-color: $color-bg-tertiary;
  }

  &:focus-visible {
    outline: 2px solid rgba(189, 147, 249, 0.6);
    outline-offset: -2px;
  }
}

.tool-icon {
  font-size: $font-size-sm;
}

.tool-name {
  font-weight: $font-weight-medium;
  color: $color-text-primary;
}

.tool-args {
  color: $color-text-secondary;
  font-size: $font-size-xs;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 260px;
}

.tool-status {
  margin-left: auto;
  font-size: $font-size-xs;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.mini-spinner {
  width: 9px;
  height: 9px;
  border: 1.5px solid rgba(255, 184, 108, 0.3);
  border-top-color: $color-warning;
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
  display: inline-block;
}

// 等待授权：转得慢一些，暗示"卡在等人"而不是"正在干活"
.pending-spinner {
  border-top-color: #f5a623;
  animation-duration: 1.6s;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.expand-icon {
  font-size: 8px;
  color: $color-text-muted;
}

.tool-call-detail {
  padding: $space-sm $space-md;
  border-top: 1px solid $color-border;
  background-color: $color-bg-primary;
}

.detail-section {
  margin-bottom: $space-sm;

  &:last-child {
    margin-bottom: 0;
  }
}

.detail-label {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  margin-bottom: $space-xs;
}

.mono {
  font-family: $font-family-mono;
}

.file-list {
  margin: 0;
  padding-left: 18px;
  font-size: $font-size-xs;
  color: $color-text-primary;

  li {
    word-break: break-all;
  }
}

.detail-content {
  font-family: $font-family-mono;
  font-size: $font-size-xs;
  color: $color-text-secondary;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
  max-height: 200px;
  overflow: auto;
}
</style>
