<script setup>
import { reactive, ref, computed, onMounted } from 'vue'
import { PERMISSION_MODES } from '@/types'
import { fetchModelNames } from '@/api/model'
import { useToolsStore } from '@/stores/tools'
import ToolIcon from './ToolIcon.vue'

const emit = defineEmits(['close', 'confirm'])

const modelNames = ref([])
const toolsStore = useToolsStore()
// 选中的工具 ID；全部选中时以空数组提交（后端语义：空=全部已启用工具）
const selectedToolIds = ref([])

const allToolsSelected = computed(
  () => selectedToolIds.value.length === toolsStore.enabledTools.length
)

onMounted(async () => {
  try {
    modelNames.value = await fetchModelNames()
  } catch (e) {
    console.error('加载模型列表失败:', e)
  }
  try {
    await toolsStore.load()
    selectedToolIds.value = toolsStore.enabledTools.map((t) => t.id)
  } catch (e) {
    console.error('加载工具列表失败:', e)
  }
})

const form = reactive({
  environment: 'local',
  project: 'e:\\learn\\local-agent',
  model: '',
  permissionMode: 'manual',
})

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

const TYPE_LABEL = { builtin: '内置', cli: 'CLI', mcp: 'MCP', api: 'API' }

function confirm() {
  // 全选 → 空数组（全部启用）；部分选择 → 白名单
  const enabledTools = allToolsSelected.value ? [] : [...selectedToolIds.value]
  emit('confirm', { ...form, title: '新会话', enabledTools })
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
          <label>项目文件夹</label>
          <div class="path-input">
            <input v-model="form.project" class="input" />
            <button class="btn btn-default">浏览</button>
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
            <span class="tool-count">（{{ selectedToolIds.length }}/{{ toolsStore.enabledTools.length }}）</span>
          </label>
          <div v-if="toolsStore.enabledTools.length === 0" class="tool-empty">
            暂无已启用工具，可在「设置 - 工具配置」中添加
          </div>
          <div v-else class="tool-picker">
            <label class="tool-all">
              <input type="checkbox" :checked="allToolsSelected" @change="toggleAll" />
              <span>全选</span>
            </label>
            <label
              v-for="t in toolsStore.enabledTools"
              :key="t.id"
              class="tool-option"
            >
              <input
                type="checkbox"
                :checked="selectedToolIds.includes(t.id)"
                @change="toggleTool(t.id)"
              />
              <ToolIcon :name="t.icon || 'zap'" :size="14" class="opt-icon" />
              <span class="opt-label">{{ t.label || t.name }}</span>
              <span class="opt-type">{{ TYPE_LABEL[t.type] }}</span>
            </label>
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn btn-default" @click="$emit('close')">取消</button>
        <button class="btn btn-primary" @click="confirm">创建会话</button>
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
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: $space-sm;
  padding: $space-lg $space-xl;
  border-top: 1px solid $color-border;
}
</style>
