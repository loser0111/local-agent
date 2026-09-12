<script setup>
import { ref, computed, onMounted } from 'vue'
import { Plus, Pencil, Trash2, RefreshCw, ChevronDown, ChevronRight, Loader2 } from 'lucide-vue-next'
import ToolIcon from './ToolIcon.vue'
import ToolEditDialog from './ToolEditDialog.vue'
import { useToolsStore } from '@/stores/tools'

const toolsStore = useToolsStore()

const keyword = ref('')
const dialogVisible = ref(false)
const editingTool = ref(null)
const expanded = ref({})

const TYPE_LABEL = { builtin: '内置', cli: 'CLI', mcp: 'MCP', api: 'API' }

const filteredTools = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return toolsStore.tools
  return toolsStore.tools.filter(
    (t) =>
      t.name.toLowerCase().includes(kw) ||
      (t.label || '').toLowerCase().includes(kw) ||
      (t.description || '').toLowerCase().includes(kw)
  )
})

onMounted(() => {
  toolsStore.load()
})

function openAdd() {
  editingTool.value = null
  dialogVisible.value = true
}

function openEdit(tool) {
  editingTool.value = tool
  dialogVisible.value = true
}

async function handleToggle(tool) {
  try {
    await toolsStore.toggle(tool.id, !tool.enabled)
  } catch (e) {
    alert(`切换失败：${e.message || e}`)
  }
}

async function handleDelete(tool) {
  if (!confirm(`确定删除工具「${tool.label || tool.name}」吗？`)) return
  try {
    await toolsStore.remove(tool.id)
  } catch (e) {
    alert(`删除失败：${e.message || e}`)
  }
}

async function refresh() {
  await toolsStore.load(true)
}

function toggleExpand(id) {
  expanded.value[id] = !expanded.value[id]
}
</script>

<template>
  <div class="tool-settings">
    <div class="toolbar">
      <input v-model="keyword" class="input search" placeholder="搜索工具名称或描述..." />
      <button class="btn btn-ghost btn-sm" @click="refresh">
        <Loader2 v-if="toolsStore.loading" :size="13" class="spin" />
        <RefreshCw v-else :size="13" />
        刷新
      </button>
      <button class="btn btn-primary btn-sm" @click="openAdd">
        <Plus :size="14" /> 添加工具
      </button>
    </div>

    <div v-if="toolsStore.loading && filteredTools.length === 0" class="list-tip">加载中...</div>
    <div v-else-if="filteredTools.length === 0" class="list-tip">
      {{ keyword ? '没有匹配的工具' : '暂无工具，点击右上角「添加工具」开始配置' }}
    </div>

    <div v-else class="tool-list">
      <div v-for="t in filteredTools" :key="t.id" class="tool-card" :class="{ disabled: !t.enabled }">
        <div class="tool-main">
          <div class="tool-icon-wrap" :class="`type-${t.type}`">
            <ToolIcon :name="t.icon || 'zap'" :size="18" />
          </div>

          <div class="tool-info" @click="t.type === 'mcp' && toggleExpand(t.id)">
            <div class="tool-title-row">
              <span class="tool-label">{{ t.label || t.name }}</span>
              <span class="tool-name">{{ t.name }}</span>
              <span class="type-badge" :class="`badge-${t.type}`">{{ TYPE_LABEL[t.type] }}</span>
              <!-- MCP 连接状态 -->
              <span v-if="t.type === 'mcp'" class="mcp-status">
                <span
                  class="status-dot"
                  :class="{
                    ok: t.status?.connected,
                    err: !t.status?.connected && t.status?.error,
                    idle: !t.status?.connected && !t.status?.error,
                  }"
                ></span>
                <span v-if="t.status?.connected" class="status-text">{{ t.status.toolCount }} 个工具</span>
                <span v-else-if="t.status?.error" class="status-text error-text" :title="t.status.error">连接失败</span>
                <span v-else class="status-text">未连接 · {{ t.status?.toolCount || t.discovered?.length || 0 }} 个工具</span>
                <ChevronDown v-if="expanded[t.id]" :size="13" />
                <ChevronRight v-else :size="13" />
              </span>
            </div>
            <div class="tool-desc">{{ t.description }}</div>
          </div>

          <div class="tool-actions">
            <!-- 启用开关 -->
            <label class="switch" :title="t.enabled ? '点击停用' : '点击启用'">
              <input type="checkbox" :checked="t.enabled" @change="handleToggle(t)" />
              <span class="slider"></span>
            </label>
            <button class="icon-btn" title="编辑" @click="openEdit(t)"><Pencil :size="14" /></button>
            <button v-if="!t.builtin" class="icon-btn danger" title="删除" @click="handleDelete(t)">
              <Trash2 :size="14" />
            </button>
          </div>
        </div>

        <!-- MCP 子工具展开 -->
        <div v-if="t.type === 'mcp' && expanded[t.id]" class="sub-tools">
          <div v-if="!t.discovered || t.discovered.length === 0" class="sub-empty">
            尚未发现工具，编辑该工具并点击「测试连接并发现工具」
          </div>
          <div v-for="d in t.discovered" :key="d.name" class="sub-tool-item">
            <span class="sub-name">{{ d.name }}</span>
            <span class="sub-desc">{{ d.description }}</span>
            <span v-if="t.disabledTools?.includes(d.name)" class="sub-off">已禁用</span>
          </div>
        </div>
      </div>
    </div>

    <ToolEditDialog
      :visible="dialogVisible"
      :tool="editingTool"
      @close="dialogVisible = false"
      @saved="() => {}"
    />
  </div>
</template>

<style scoped lang="scss">
.tool-settings {
  max-width: 760px;
}

.toolbar {
  display: flex;
  gap: $space-sm;
  margin-bottom: $space-lg;

  .search {
    flex: 1;
  }
}

.btn-sm {
  padding: 5px 12px;
  font-size: $font-size-xs;
  gap: 5px;
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
}

.list-tip {
  text-align: center;
  color: $color-text-muted;
  font-size: $font-size-sm;
  padding: $space-xl 0;
}

.tool-list {
  display: flex;
  flex-direction: column;
  gap: $space-sm;
}

.tool-card {
  background: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  transition: border-color $transition-fast;

  &:hover {
    border-color: $color-border-light;
  }

  &.disabled {
    opacity: 0.55;
  }
}

.tool-main {
  display: flex;
  align-items: center;
  gap: $space-md;
  padding: $space-md;
}

.tool-icon-wrap {
  width: 38px;
  height: 38px;
  border-radius: $radius-sm;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;

  &.type-builtin {
    background: rgba(250, 204, 21, 0.12);
    color: #facc15;
  }
  &.type-cli {
    background: rgba(34, 197, 94, 0.12);
    color: #22c55e;
  }
  &.type-mcp {
    background: rgba(59, 130, 246, 0.12);
    color: #3b82f6;
  }
  &.type-api {
    background: rgba(168, 85, 247, 0.12);
    color: #a855f7;
  }
}

.tool-info {
  flex: 1;
  min-width: 0;
}

.tool-title-row {
  display: flex;
  align-items: center;
  gap: $space-sm;
}

.tool-label {
  font-size: $font-size-sm;
  font-weight: $font-weight-semibold;
  color: $color-text-primary;
}

.tool-name {
  font-size: $font-size-xs;
  font-family: monospace;
  color: $color-text-muted;
}

.type-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 8px;
  font-weight: $font-weight-medium;

  &.badge-builtin {
    background: rgba(250, 204, 21, 0.15);
    color: #facc15;
  }
  &.badge-cli {
    background: rgba(34, 197, 94, 0.15);
    color: #22c55e;
  }
  &.badge-mcp {
    background: rgba(59, 130, 246, 0.15);
    color: #3b82f6;
  }
  &.badge-api {
    background: rgba(168, 85, 247, 0.15);
    color: #c084fc;
  }
}

.mcp-status {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-left: auto;
  cursor: pointer;

  .status-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    flex-shrink: 0;

    &.ok {
      background: $color-success;
      box-shadow: 0 0 5px rgba(34, 197, 94, 0.6);
    }
    &.err {
      background: $color-error;
    }
    &.idle {
      background: $color-text-muted;
    }
  }

  .status-text {
    font-size: $font-size-xs;
    color: $color-text-muted;
  }

  .error-text {
    color: $color-error;
  }
}

.tool-desc {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  margin-top: 4px;
  display: -webkit-box;
  -webkit-line-clamp: 1;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.tool-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  background: transparent;
  border: none;
  border-radius: 4px;
  color: $color-text-secondary;
  cursor: pointer;

  &:hover {
    background: $color-bg-tertiary;
    color: $color-text-primary;
  }

  &.danger:hover {
    color: $color-error;
  }
}

// 开关
.switch {
  position: relative;
  display: inline-block;
  width: 36px;
  height: 20px;
  margin-right: 4px;

  input {
    opacity: 0;
    width: 0;
    height: 0;
  }

  .slider {
    position: absolute;
    inset: 0;
    background: $color-bg-tertiary;
    border: 1px solid $color-border;
    border-radius: 20px;
    cursor: pointer;
    transition: $transition-fast;

    &::before {
      content: '';
      position: absolute;
      width: 14px;
      height: 14px;
      left: 2px;
      top: 2px;
      background: $color-text-muted;
      border-radius: 50%;
      transition: $transition-fast;
    }
  }

  input:checked + .slider {
    background: rgba(124, 58, 237, 0.35);
    border-color: $color-primary;

    &::before {
      transform: translateX(16px);
      background: $color-primary;
    }
  }
}

// 子工具
.sub-tools {
  border-top: 1px solid $color-border;
  padding: $space-sm $space-md $space-sm 56px;
}

.sub-empty {
  font-size: $font-size-xs;
  color: $color-text-muted;
  padding: 4px 0;
}

.sub-tool-item {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: 4px 0;
  font-size: $font-size-xs;
}

.sub-name {
  font-family: monospace;
  color: $color-info;
  flex-shrink: 0;
}

.sub-desc {
  color: $color-text-muted;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sub-off {
  color: $color-text-muted;
  font-size: 10px;
  border: 1px solid $color-border;
  border-radius: 6px;
  padding: 0 5px;
  flex-shrink: 0;
}

.spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
