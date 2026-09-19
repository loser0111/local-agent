<script setup>
import { reactive, ref, computed, onMounted } from 'vue'
import { BookOpen } from 'lucide-vue-next'
import { PERMISSION_MODES } from '@/types'
import { useSettingStore } from '@/stores/setting'
import { fetchModelNames } from '@/api/model'
import { useToolsStore } from '@/stores/tools'
import { useSkillsStore } from '@/stores/skills'
import ToolIcon from './ToolIcon.vue'
import { PickDirectory } from '@/../wailsjs/go/main/App'
import { useUiStore } from '@/stores/ui'

const ui = useUiStore()

const emit = defineEmits(['close', 'confirm'])

const modelNames = ref([])
const toolsStore = useToolsStore()
const settingStore = useSettingStore()
const skillsStore = useSkillsStore()
// 选中的**工具名**（MCP 子工具用完整名 mcp__server__tool）；全部选中时以空数组提交
// （后端语义：空=全部已启用工具）。P3 之后白名单是工具粒度，不再按来源 ID 过滤。
const selectedToolIds = ref([])
// 选中的技能 ID；语义同上（空=全部已启用技能）
const selectedSkillIds = ref([])

// 选择器结构：内置/CLI/HTTP 各一项；MCP 服务器展开成它的（未停用的）子工具
const toolPicker = computed(() =>
  toolsStore.enabledTools.map((t) => {
    if (t.kind === 'mcp') {
      const disabled = t.disabledTools || []
      const subs = (t.discovered || [])
        .filter((d) => !disabled.includes(d.name))
        .map((d) => ({ value: `mcp__${t.name}__${d.name}`, label: d.name }))
      return { key: t.id, kind: 'mcp', title: t.label || t.name, icon: t.icon, subs }
    }
    return { key: t.id, kind: t.kind, value: t.name, label: t.label || t.name, icon: t.icon }
  })
)

// 全部可选项的工具名（用于"全选"判定与计数）
const allToolValues = computed(() =>
  toolPicker.value.flatMap((g) => (g.kind === 'mcp' ? g.subs.map((sub) => sub.value) : [g.value]))
)

const allToolsSelected = computed(
  () => allToolValues.value.length > 0 && selectedToolIds.value.length === allToolValues.value.length
)
const allSkillsSelected = computed(
  () => selectedSkillIds.value.length === skillsStore.enabledSkills.length
)

onMounted(async () => {
  try {
    modelNames.value = await fetchModelNames()
    // 优先使用设置的默认模型
    const dm = settingStore.settings.defaultModel
    if (dm && modelNames.value.includes(dm)) {
      form.model = dm
    } else if (modelNames.value.length > 0) {
      form.model = modelNames.value[0]
    }
  } catch (e) {
    console.error('加载模型列表失败:', e)
  }
  try {
    await toolsStore.load()
    selectedToolIds.value = allToolValues.value
  } catch (e) {
    console.error('加载工具列表失败:', e)
  }
  try {
    await skillsStore.load()
    selectedSkillIds.value = skillsStore.enabledSkills.map((s) => s.id)
  } catch (e) {
    console.error('加载技能列表失败:', e)
  }
})

const form = reactive({
  environment: 'local',
  project: 'e:\\learn\\local-agent',
  model: '',
  permissionMode: 'manual',
})

/**
 * 浏览选择工作区文件夹
 * Wails 环境调用系统目录选择对话框；浏览器 mock 模式降级为手动输入框
 */
async function browseProject() {
  if (window.go && window.go.main && window.go.main.App) {
    try {
      const dir = await PickDirectory()
      if (dir) form.project = dir
    } catch (e) {
      console.error('打开目录选择器失败:', e)
    }
  } else {
    // 用应用内输入框：原生 prompt 在部分 webview 上静默返回 null
    const dir = await ui.askText({
      title: '输入工作区文件夹路径',
      message: '浏览器开发模式下没有系统目录选择器，请手动填写。',
      placeholder: '/Users/你/项目目录',
      value: form.project,
    })
    if (dir) form.project = dir
  }
}

/** 整台 MCP 服务器全选 / 全不选（逐个加入子工具名） */
function toggleServer(group) {
  if (group.kind !== 'mcp') return
  const values = group.subs.map((sub) => sub.value)
  const allOn = values.every((v) => selectedToolIds.value.includes(v))
  if (allOn) {
    selectedToolIds.value = selectedToolIds.value.filter((v) => !values.includes(v))
  } else {
    const set = new Set([...selectedToolIds.value, ...values])
    selectedToolIds.value = [...set]
  }
}

function serverAllOn(group) {
  return group.kind === 'mcp' && group.subs.length > 0 &&
    group.subs.every((sub) => selectedToolIds.value.includes(sub.value))
}

function toggleTool(id) {
  const idx = selectedToolIds.value.indexOf(id)
  if (idx > -1) selectedToolIds.value.splice(idx, 1)
  else selectedToolIds.value.push(id)
}

function toggleAll() {
  if (allToolsSelected.value) {
    selectedToolIds.value = []
  } else {
    selectedToolIds.value = toolsStore.enabledTools.map((t) => t.id)
  }
}

function toggleSkill(id) {
  const idx = selectedSkillIds.value.indexOf(id)
  if (idx > -1) selectedSkillIds.value.splice(idx, 1)
  else selectedSkillIds.value.push(id)
}

function toggleAllSkills() {
  if (allSkillsSelected.value) {
    selectedSkillIds.value = []
  } else {
    selectedSkillIds.value = skillsStore.enabledSkills.map((s) => s.id)
  }
}

const TYPE_LABEL = { builtin: '内置', cli: 'CLI', mcp: 'MCP', http: 'HTTP' }

function submitForm() {
  // 全选 → 空数组（全部启用）；部分选择 → 白名单
  const enabledTools = allToolsSelected.value ? [] : [...selectedToolIds.value]
  const enabledSkills = allSkillsSelected.value ? [] : [...selectedSkillIds.value]
  emit('confirm', { ...form, title: '新会话', enabledTools, enabledSkills })
}
</script>

<template>
  <div class="dialog-mask" @click.self="$emit('close')">
    <div class="dialog">
      <div class="dialog-header">
        <h3>新建会话</h3>
        <button class="close-btn" @click="$emit('close')">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M2 2L14 14M14 2L2 14" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
          </svg>
        </button>
      </div>

      <div class="dialog-body">
        <div class="form-group">
          <label>环境</label>
          <div class="radio-group">
            <label class="radio-item">
              <input type="radio" v-model="form.environment" value="local" />
              <span>Local</span>
            </label>
          </div>
        </div>

        <div class="form-group">
          <label>项目文件夹（工作区）</label>
          <div class="path-input">
            <input v-model="form.project" class="input" placeholder="选择或输入工作区路径" />
            <button class="btn btn-default" @click="browseProject">浏览</button>
          </div>
        </div>

        <div class="form-group">
          <label>模型</label>
          <select v-model="form.model" class="input">
            <option value="">请选择模型</option>
            <option v-for="m in modelNames" :key="m" :value="m">{{ m }}</option>
          </select>
        </div>

        <div class="form-group">
          <label>权限模式</label>
          <select v-model="form.permissionMode" class="input">
            <option v-for="p in PERMISSION_MODES" :key="p.value" :value="p.value">
              {{ p.label }} - {{ p.desc }}
            </option>
          </select>
        </div>

        <!-- 本会话可用工具 -->
        <div class="form-group">
          <label>
            可用工具
            <span class="tool-count">（{{ selectedToolIds.length }}/{{ allToolValues.length }}）</span>
          </label>
          <div v-if="toolsStore.enabledTools.length === 0" class="tool-empty">
            暂无已启用工具，可在「设置 - 工具配置」中添加
          </div>
          <div v-else class="tool-picker">
            <label class="tool-all">
              <input type="checkbox" :checked="allToolsSelected" @change="toggleAll" />
              <span>全选</span>
            </label>
            <template v-for="g in toolPicker" :key="g.key">
              <!-- MCP 服务器：展开成子工具，可整台全选或逐个勾选 -->
              <div v-if="g.kind === 'mcp'" class="tool-subgroup">
                <label class="tool-option tool-server">
                  <input type="checkbox" :checked="serverAllOn(g)" @change="toggleServer(g)" />
                  <ToolIcon :name="g.icon || 'blocks'" :size="14" class="opt-icon" />
                  <span class="opt-label">{{ g.title }}</span>
                  <span class="opt-type">MCP · {{ g.subs.length }} 个子工具</span>
                </label>
                <div v-if="g.subs.length === 0" class="tool-empty-hint">
                  尚未发现子工具（在设置页对该服务器「测试连接并发现工具」）
                </div>
                <label v-for="sub in g.subs" :key="sub.value" class="tool-option tool-sub">
                  <input
                    type="checkbox"
                    :checked="selectedToolIds.includes(sub.value)"
                    @change="toggleTool(sub.value)"
                  />
                  <span class="opt-label mono">{{ sub.value }}</span>
                </label>
              </div>

              <!-- 内置 / CLI / HTTP：一条配置就是一个工具 -->
              <label v-else class="tool-option">
                <input type="checkbox" :checked="selectedToolIds.includes(g.value)" @change="toggleTool(g.value)" />
                <ToolIcon :name="g.icon || 'zap'" :size="14" class="opt-icon" />
                <span class="opt-label">{{ g.label }}</span>
                <span class="opt-type">{{ TYPE_LABEL[g.kind] }}</span>
              </label>
            </template>
          </div>
        </div>

        <!-- 本会话可用技能 -->
        <div class="form-group">
          <label>
            可用技能
            <span class="tool-count">（{{ selectedSkillIds.length }}/{{ skillsStore.enabledSkills.length }}）</span>
          </label>
          <div v-if="skillsStore.enabledSkills.length === 0" class="tool-empty">
            暂无已启用技能，可在「设置 - 技能配置」中添加
          </div>
          <div v-else class="tool-picker">
            <label class="tool-all">
              <input type="checkbox" :checked="allSkillsSelected" @change="toggleAllSkills" />
              <span>全选</span>
            </label>
            <label
              v-for="s in skillsStore.enabledSkills"
              :key="s.id"
              class="tool-option"
            >
              <input
                type="checkbox"
                :checked="selectedSkillIds.includes(s.id)"
                @change="toggleSkill(s.id)"
              />
              <BookOpen :size="14" class="opt-icon" />
              <span class="opt-label">{{ s.name }}</span>
              <span v-if="s.disableModelInvocation" class="opt-type opt-inject">仅手动</span>
            </label>
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn btn-default" @click="$emit('close')">取消</button>
        <button class="btn btn-primary" @click="submitForm">创建会话</button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.dialog-mask {
  position: fixed;
  inset: 0;
  background-color: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.dialog {
  width: 480px;
  max-width: 90vw;
  max-height: 90vh;
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-lg;
  box-shadow: $shadow-lg;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.dialog-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: $space-lg $space-xl;
  border-bottom: 1px solid $color-border;

  h3 {
    font-size: $font-size-lg;
    font-weight: $font-weight-semibold;
  }
}

.close-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: $radius-sm;
  color: $color-text-secondary;
  transition: all $transition-fast;

  &:hover {
    background-color: $color-bg-tertiary;
    color: $color-text-primary;
  }
}

.dialog-body {
  padding: $space-xl;
  overflow-y: auto;
  flex: 1;
}

.form-group {
  margin-bottom: $space-lg;

  &:last-child {
    margin-bottom: 0;
  }

  label {
    display: block;
    font-size: $font-size-sm;
    font-weight: $font-weight-medium;
    margin-bottom: $space-sm;
    color: $color-text-primary;
  }
}

.radio-group {
  display: flex;
  gap: $space-lg;
}

.radio-item {
  display: flex;
  align-items: center;
  gap: $space-xs;
  cursor: pointer;
  font-size: $font-size-sm;
  color: $color-text-secondary;

  input[type='radio'] {
    accent-color: $color-primary;
  }
}

.path-input {
  display: flex;
  gap: $space-sm;
}

.tool-count {
  font-weight: normal;
  color: $color-text-muted;
  font-size: $font-size-xs;
}

.tool-empty {
  font-size: $font-size-xs;
  color: $color-text-muted;
  padding: $space-sm;
  border: 1px dashed $color-border;
  border-radius: $radius-sm;
}

.tool-picker {
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  padding: $space-sm;
  max-height: 150px;
  overflow-y: auto;
}

.tool-all {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: $font-size-xs;
  color: $color-text-secondary;
  cursor: pointer;
  padding-bottom: 6px;
  margin-bottom: 6px;
  border-bottom: 1px solid $color-border;

  input {
    accent-color: $color-primary;
  }
}

.tool-subgroup {
  display: flex;
  flex-direction: column;
}

.tool-server {
  font-weight: $font-weight-medium;
}

.tool-sub {
  padding-left: 22px;
}

.tool-empty-hint {
  padding: 2px 0 4px 22px;
  font-size: 11px;
  color: $color-text-muted;
}

.mono {
  font-family: $font-family-mono;
  font-size: 11px;
}

.tool-option {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 2px;
  font-size: $font-size-xs;
  color: $color-text-primary;
  cursor: pointer;

  input {
    accent-color: $color-primary;
  }

  .opt-icon {
    color: $color-primary;
    flex-shrink: 0;
  }

  .opt-label {
    flex: 1;
  }

  .opt-type {
    font-size: 10px;
    color: $color-text-muted;
    border: 1px solid $color-border;
    border-radius: 6px;
    padding: 0 5px;
  }

  .opt-inject {
    color: #c084fc;
    border-color: rgba(124, 58, 237, 0.5);
  }
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: $space-sm;
  padding: $space-lg $space-xl;
  border-top: 1px solid $color-border;
}
</style>
