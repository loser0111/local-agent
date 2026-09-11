<script setup>
import { reactive, ref, onMounted } from 'vue'
import { PERMISSION_MODES } from '@/types'
import { fetchModelNames } from '@/api/model'

const emit = defineEmits(['close', 'confirm'])

const modelNames = ref([])

onMounted(async () => {
  try {
    modelNames.value = await fetchModelNames()
  } catch (e) {
    console.error('加载模型列表失败:', e)
  }
})

const form = reactive({
  environment: 'local',
  project: 'e:\\learn\\local-agent',
  model: '',
  permissionMode: 'manual',
})

function confirm() {
  emit('confirm', { ...form, title: '新会话' })
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

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: $space-sm;
  padding: $space-lg $space-xl;
  border-top: 1px solid $color-border;
}
</style>
