<script setup>
import { ref, computed, onMounted } from 'vue'
import {
  Plus,
  Pencil,
  Trash2,
  RefreshCw,
  ChevronDown,
  ChevronRight,
  Loader2,
  Download,
  Upload,
  Copy,
  Check,
} from 'lucide-vue-next'
import ToolIcon from './ToolIcon.vue'
import ToolEditDialog from './ToolEditDialog.vue'
import McpConfigDialog from './McpConfigDialog.vue'
import { useToolsStore } from '@/stores/tools'
import { useUiStore } from '@/stores/ui'

/**
 * 工具配置
 *
 * 按**来源类型**分组展示（内置工具 / MCP 服务器 / 自定义工具），而不是混在一个列表里：
 * 一条「内置工具」「CLI」「HTTP API」配置就是一个工具，而一条「MCP 服务器」配置是一台
 * 服务器 + N 个子工具（可单独开关）。分组让这个粒度差异在界面上就看得见。
 */
const toolsStore = useToolsStore()
const ui = useUiStore()

const keyword = ref('')
const dialogVisible = ref(false)
const editingTool = ref(null)
const expanded = ref({})
const copied = ref('')
// MCP 配置导入/导出弹窗
const mcpVisible = ref(false)
const mcpMode = ref('import')

function openMcp(mode) {
  mcpMode.value = mode
  mcpVisible.value = true
}

const TYPE_LABEL = { builtin: '内置', cli: 'CLI', mcp: 'MCP', http: 'HTTP' }
const EXPOSURE_LABEL = { direct: '直出', router: '路由器', internal: '不暴露' }
const EXPOSURE_HINT = {
  direct: '直接暴露给模型：少一轮发现往返，但工具多时 prompt 会变大',
  router: '经工具路由器发现：不占 prompt，模型需要时先 list/describe',
  internal: '不暴露给模型：仅供内部使用，模型看不到也用不了',
}

function matches(t) {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return true
  return (
    t.name.toLowerCase().includes(kw) ||
    (t.label || '').toLowerCase().includes(kw) ||
    (t.description || '').toLowerCase().includes(kw)
  )
}

// 分组：内置 / MCP / 自定义（CLI + HTTP）
const groups = computed(() => {
  const all = toolsStore.tools.filter(matches)
  const pick = (kinds) => all.filter((t) => kinds.includes(t.kind))
  return [
    {
      key: 'builtin',
      title: '内置工具',
      hint: '随程序内置，不可删除',
      items: pick(['builtin']),
    },
    {
      key: 'mcp',
      title: 'MCP 服务器',
      hint: '一台服务器提供多个子工具，可逐个开关；支持粘贴官方 mcpServers 片段导入',
      items: pick(['mcp']),
    },
    {
      key: 'custom',
      title: '自定义工具（CLI / HTTP API）',
      hint: '命令模板或 HTTP 接口封装成工具',
      items: pick(['cli', 'http']),
    },
  ].filter((g) => g.items.length > 0)
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
  const ok = await ui.ask({
    title: '删除工具',
    message:
      tool.kind === 'mcp'
        ? `确定删除 MCP 服务器「${tool.label || tool.name}」吗？其下全部子工具将对模型不可用。`
        : `确定删除工具「${tool.label || tool.name}」吗？`,
    confirmText: '删除',
  })
  if (!ok) return
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

/** MCP 子工具开关：启停写回 DisabledTools（单个子工具粒度，不影响整台服务器） */
async function handleSubToggle(server, sub) {
  const enabled = server.disabledTools?.includes(sub.name)
  try {
    await toolsStore.setSubTool(server.id, sub.name, !!enabled)
  } catch (e) {
    alert(`切换子工具失败：${e.message || e}`)
  }
}

/** 复制子工具的完整名（模型与权限规则用的就是这个名字） */
async function copyToolName(server, sub) {
  const full = `mcp__${server.name}__${sub.name}`
  try {
    await navigator.clipboard.writeText(full)
    copied.value = full
    setTimeout(() => {
      if (copied.value === full) copied.value = ''
    }, 1600)
  } catch {
    ui.notify(`复制失败，请手动记下：${full}`, 'error')
  }
}

/** MCP 接入方式与地址（展示用） */
function addrOf(t) {
  if (t.kind !== 'mcp' || !t.mcp) return ''
  const target = t.mcp.url || t.mcp.command || ''
  return target ? `${t.mcp.type || 'streamable-http'} · ${target}` : t.mcp.type || ''
}

/** 非 MCP 来源的一行配置摘要 */
function configHint(t) {
  if (t.kind === 'cli' && t.cli) return t.cli.command
  if (t.kind === 'http' && t.http) return `${t.http.method || 'GET'} ${t.http.url}`
  return ''
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
      <button class="btn btn-ghost btn-sm" title="粘贴官方 mcpServers 片段即可导入" @click="openMcp('import')">
        <Download :size="13" /> 导入 MCP
      </button>
      <button class="btn btn-ghost btn-sm" title="导出为官方 mcpServers 片段，方便分享" @click="openMcp('export')">
        <Upload :size="13" /> 导出 MCP
      </button>
      <button class="btn btn-primary btn-sm" @click="openAdd">
        <Plus :size="14" /> 添加工具
      </button>
    </div>

    <div v-if="toolsStore.loading && toolsStore.tools.length === 0" class="list-tip">加载中...</div>
    <div v-else-if="groups.length === 0" class="list-tip">
      {{ keyword ? '没有匹配的工具' : '暂无工具，点击右上角「添加工具」开始配置' }}
    </div>

    <div v-else class="tool-groups">
      <section v-for="g in groups" :key="g.key" class="tool-group">
        <div class="group-header">
          <span class="group-title">{{ g.title }}</span>
          <span class="group-count">{{ g.items.length }}</span>
          <span class="group-hint">{{ g.hint }}</span>
        </div>

        <div class="tool-list">
          <div v-for="t in g.items" :key="t.id" class="tool-card" :class="{ disabled: !t.enabled }">
            <div class="tool-main">
              <div class="tool-icon-wrap" :class="`type-${t.kind}`">
                <ToolIcon :name="t.icon || 'zap'" :size="18" />
              </div>

              <div class="tool-info" @click="t.kind === 'mcp' && toggleExpand(t.id)">
                <div class="tool-title-row">
                  <span class="tool-label">{{ t.label || t.name }}</span>
                  <span class="tool-name">{{ t.name }}</span>
                  <span class="type-badge" :class="`badge-${t.kind}`">{{ TYPE_LABEL[t.kind] || t.kind }}</span>
                  <!-- 暴露策略 -->
                  <span class="exposure-badge" :title="EXPOSURE_HINT[t.exposure] || ''">
                    {{ EXPOSURE_LABEL[t.exposure] || EXPOSURE_LABEL.router }}
                  </span>

                  <!-- MCP 连接状态 -->
                  <span v-if="t.kind === 'mcp'" class="mcp-status">
                    <span
                      class="status-dot"
                      :class="{
                        ok: t.status?.connected,
                        err: !t.status?.connected && t.status?.error,
                        idle: !t.status?.connected && !t.status?.error,
                      }"
                    ></span>
                    <span v-if="t.status?.connected" class="status-text">{{ t.status.toolCount }} 个子工具</span>
                    <span v-else-if="t.status?.error" class="status-text error-text" :title="t.status.error">
                      连接失败
                    </span>
                    <span v-else class="status-text">
                      未连接 · {{ t.status?.toolCount || t.discovered?.length || 0 }} 个子工具
                    </span>
                    <ChevronDown v-if="expanded[t.id]" :size="13" />
                    <ChevronRight v-else :size="13" />
                  </span>
                </div>
                <div class="tool-desc">{{ t.description }}</div>
                <div v-if="addrOf(t)" class="tool-addr mono" :title="addrOf(t)">{{ addrOf(t) }}</div>
                <div v-else-if="configHint(t)" class="tool-addr mono" :title="configHint(t)">
                  {{ configHint(t) }}
                </div>
              </div>

              <div class="tool-actions">
                <!-- 启用开关（MCP 是整台服务器的启停） -->
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

            <!-- MCP 子工具：可单独开关；完整名可一键复制（权限规则要用它） -->
            <div v-if="t.kind === 'mcp' && expanded[t.id]" class="sub-tools">
              <div v-if="!t.discovered || t.discovered.length === 0" class="sub-empty">
                尚未发现子工具，编辑该服务器并点击「测试连接并发现工具」
              </div>
              <template v-else>
                <div class="sub-hint">
                  子工具按名称单独开关；写权限规则时使用右侧完整名（可直接复制）。
                </div>
                <div v-for="d in t.discovered" :key="d.name" class="sub-tool-item">
                  <label class="switch sub-switch" :title="t.disabledTools?.includes(d.name) ? '点击启用' : '点击停用'">
                    <input
                      type="checkbox"
                      :checked="!t.disabledTools?.includes(d.name)"
                      @change="handleSubToggle(t, d)"
                    />
                    <span class="slider"></span>
                  </label>
                  <span class="sub-body">
                    <span class="sub-name">{{ d.name }}</span>
                    <span class="sub-desc">{{ d.description }}</span>
                  </span>
                  <span class="sub-full mono" :title="`mcp__${t.name}__${d.name}`">
                    mcp__{{ t.name }}__{{ d.name }}
                  </span>
                  <button class="icon-btn" title="复制完整工具名" @click="copyToolName(t, d)">
                    <Check v-if="copied === `mcp__${t.name}__${d.name}`" :size="13" />
                    <Copy v-else :size="13" />
                  </button>
                  <span v-if="t.disabledTools?.includes(d.name)" class="sub-off">已停用</span>
                </div>
              </template>
            </div>
          </div>
        </div>
      </section>
    </div>

    <McpConfigDialog
      :visible="mcpVisible"
      :mode="mcpMode"
      @close="mcpVisible = false"
      @imported="() => {}"
    />

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
  display: flex;
  flex-direction: column;
  height: 100%;
}

.toolbar {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-md;
  border-bottom: 1px solid $color-border;

  .search {
    flex: 1;
    max-width: 320px;
  }
}

.btn-sm {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.spin {
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.list-tip {
  padding: $space-lg;
  text-align: center;
  color: $color-text-muted;
  font-size: $font-size-sm;
}

.tool-groups {
  flex: 1;
  overflow-y: auto;
  padding: $space-md;
}

.tool-group {
  margin-bottom: $space-lg;

  &:last-child {
    margin-bottom: 0;
  }
}

.group-header {
  display: flex;
  align-items: baseline;
  gap: $space-sm;
  margin-bottom: $space-sm;
  padding-bottom: 6px;
  border-bottom: 1px solid $color-border;
}

.group-title {
  font-size: $font-size-sm;
  font-weight: $font-weight-medium;
  color: $color-text-primary;
}

.group-count {
  font-size: 11px;
  color: $color-text-muted;
  padding: 0 6px;
  border-radius: 8px;
  background-color: rgb(var(--color-bg-tertiary-rgb) / 0.6);
}

.group-hint {
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.tool-list {
  display: flex;
  flex-direction: column;
  gap: $space-sm;
}

.tool-card {
  border: 1px solid $color-border;
  border-radius: $radius-md;
  background-color: $color-bg-secondary;
  overflow: hidden;

  &.disabled {
    opacity: 0.6;
  }
}

.tool-main {
  display: flex;
  align-items: flex-start;
  gap: $space-md;
  padding: $space-md;
}

.tool-icon-wrap {
  flex-shrink: 0;
  width: 34px;
  height: 34px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: $radius-sm;
  background-color: $color-bg-tertiary;
  color: $color-text-secondary;

  &.type-builtin {
    color: $color-primary;
  }
  &.type-cli {
    color: $color-info;
  }
  &.type-mcp {
    color: $color-warning;
  }
  &.type-http {
    color: $color-success;
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
  flex-wrap: wrap;
}

.tool-label {
  font-size: $font-size-sm;
  font-weight: $font-weight-medium;
  color: $color-text-primary;
}

.tool-name {
  font-size: $font-size-xs;
  color: $color-text-muted;
  font-family: $font-family-mono;
}

.type-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 8px;
  background-color: rgb(var(--color-bg-tertiary-rgb) / 0.8);
  color: $color-text-secondary;

  &.badge-builtin {
    color: $color-primary;
  }
  &.badge-mcp {
    color: $color-warning;
  }
}

.exposure-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 8px;
  border: 1px solid $color-border;
  color: $color-text-secondary;
}

.mcp-status {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: $font-size-xs;
  color: $color-text-secondary;

  .status-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;

    &.ok {
      background-color: $color-success;
    }
    &.err {
      background-color: $color-error;
    }
    &.idle {
      background-color: $color-text-muted;
    }
  }

  .status-text {
    white-space: nowrap;
  }

  .error-text {
    color: $color-error;
  }
}

.tool-desc {
  margin-top: 4px;
  font-size: $font-size-xs;
  color: $color-text-secondary;
  line-height: 1.5;
}

.tool-addr {
  margin-top: 4px;
  font-size: 11px;
  color: $color-text-muted;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mono {
  font-family: $font-family-mono;
}

.tool-actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: $space-sm;
}

.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: $radius-sm;
  color: $color-text-secondary;

  &:hover {
    background-color: $color-bg-tertiary;
    color: $color-text-primary;
  }

  &.danger:hover {
    color: $color-error;
  }
}

.switch {
  position: relative;
  display: inline-block;
  width: 34px;
  height: 18px;
  flex-shrink: 0;

  input {
    opacity: 0;
    width: 0;
    height: 0;
  }

  .slider {
    position: absolute;
    inset: 0;
    background-color: $color-bg-tertiary;
    border-radius: 10px;
    transition: background-color $transition-fast;
    cursor: pointer;

    &::before {
      content: '';
      position: absolute;
      width: 14px;
      height: 14px;
      left: 2px;
      top: 2px;
      background-color: #fff;
      border-radius: 50%;
      transition: transform $transition-fast;
    }
  }

  input:checked + .slider {
    background-color: $color-primary;

    &::before {
      transform: translateX(16px);
    }
  }
}

.sub-tools {
  border-top: 1px solid $color-border;
  padding: $space-sm $space-md $space-md;
  background-color: $color-bg-primary;
}

.sub-empty {
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.sub-hint {
  font-size: 11px;
  color: $color-text-muted;
  margin-bottom: $space-xs;
}

.sub-tool-item {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: 5px 0;

  & + .sub-tool-item {
    border-top: 1px dashed rgb(var(--color-bg-tertiary-rgb) / 0.7);
  }
}

.sub-switch {
  width: 30px;
  height: 16px;

  .slider::before {
    width: 12px;
    height: 12px;
  }

  input:checked + .slider::before {
    transform: translateX(14px);
  }
}

.sub-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.sub-name {
  font-size: $font-size-xs;
  color: $color-text-primary;
}

.sub-desc {
  font-size: 11px;
  color: $color-text-muted;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sub-full {
  font-size: 11px;
  color: $color-text-secondary;
  max-width: 300px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sub-off {
  font-size: 10px;
  color: $color-text-muted;
  padding: 1px 6px;
  border-radius: 8px;
  background-color: rgb(var(--color-bg-tertiary-rgb) / 0.8);
}
</style>
