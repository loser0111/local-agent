<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useSettingStore } from '@/stores/setting'
import { PERMISSION_MODES } from '@/types'
import { fetchModels, addModel, deleteModel } from '@/api/model'
import ToolSettings from '@/components/business/ToolSettings.vue'

const router = useRouter()
const settingStore = useSettingStore()

const activeTab = ref('general')

const tabs = [
  { key: 'general', label: '通用设置' },
  { key: 'model', label: '模型配置' },
  { key: 'tool', label: '工具配置' },
  { key: 'permission', label: '权限配置' },
  { key: 'about', label: '关于' },
]

function goBack() {
  router.push('/')
}

// ===== 模型管理 =====
const models = ref([])
const loading = ref(false)
const errorMsg = ref('')
const submitting = ref(false)

const form = reactive({
  name: '',
  alias: '',
  modelId: '',
  apiKey: '',
  url: '',
})

async function loadModels() {
  loading.value = true
  errorMsg.value = ''
  try {
    models.value = await fetchModels()
  } catch (e) {
    errorMsg.value = `加载模型失败：${e.message || e}`
  } finally {
    loading.value = false
  }
}

async function handleAdd() {
  if (!form.name.trim()) {
    errorMsg.value = '模型名称不能为空'
    return
  }
  submitting.value = true
  errorMsg.value = ''
  try {
    await addModel({
      name: form.name.trim(),
      alias: form.alias.trim(),
      modelId: form.modelId.trim(),
      apiKey: form.apiKey,
      url: form.url.trim(),
    })
    // 清空表单
    form.name = ''
    form.alias = ''
    form.modelId = ''
    form.apiKey = ''
    form.url = ''
    await loadModels()
  } catch (e) {
    errorMsg.value = `添加失败：${e.message || e}`
  } finally {
    submitting.value = false
  }
}

async function handleDelete(name) {
  if (!confirm(`确定删除模型 "${name}" 吗？`)) return
  try {
    await deleteModel(name)
    await loadModels()
  } catch (e) {
    errorMsg.value = `删除失败：${e.message || e}`
  }
}

onMounted(() => {
  loadModels()
})
</script>

<template>
  <div class="settings">
    <div class="settings-header">
      <button class="btn btn-ghost" @click="goBack">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
          <path d="M10 3L5 8l5 5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        返回
      </button>
      <h2>设置</h2>
    </div>

    <div class="settings-body">
      <div class="settings-nav">
        <div
          v-for="tab in tabs"
          :key="tab.key"
          class="nav-item"
          :class="{ active: activeTab === tab.key }"
          @click="activeTab = tab.key"
        >
          {{ tab.label }}
        </div>
      </div>

      <div class="settings-content">
        <!-- 通用设置 -->
        <div v-if="activeTab === 'general'" class="settings-panel">
          <div class="form-group">
            <label>主题</label>
            <select v-model="settingStore.settings.theme" class="input" style="max-width: 240px">
              <option value="dark">深色</option>
              <option value="light">浅色</option>
            </select>
          </div>
          <div class="form-group">
            <label>语言</label>
            <select v-model="settingStore.settings.language" class="input" style="max-width: 240px">
              <option value="zh-CN">简体中文</option>
              <option value="en-US">English</option>
            </select>
          </div>
          <div class="form-group">
            <label>字体大小：{{ settingStore.settings.fontSize }}px</label>
            <input
              type="range"
              min="12"
              max="20"
              v-model.number="settingStore.settings.fontSize"
            />
          </div>
          <div class="form-group checkbox-group">
            <label>
              <input type="checkbox" v-model="settingStore.settings.autoSave" />
              自动保存
            </label>
          </div>
          <div class="form-group checkbox-group">
            <label>
              <input type="checkbox" v-model="settingStore.settings.autoArchive" />
              PR 合并后自动归档会话
            </label>
          </div>
        </div>

        <!-- 模型配置 -->
        <div v-if="activeTab === 'model'" class="settings-panel">
          <div v-if="errorMsg" class="error-banner">{{ errorMsg }}</div>

          <!-- 添加模型表单 -->
          <div class="model-form-card">
            <h3 class="card-title">添加模型</h3>
            <div class="form-row">
              <div class="form-group">
                <label>模型名称 <span class="required">*</span></label>
                <input v-model="form.name" class="input" placeholder="如：deepseek" />
              </div>
              <div class="form-group">
                <label>模型别名</label>
                <input v-model="form.alias" class="input" placeholder="如：DeepSeek Chat" />
              </div>
            </div>
            <div class="form-group">
              <label>模型 ID</label>
              <input v-model="form.modelId" class="input" placeholder="如：deepseek-chat（发送给 LLM API 的实际模型 ID）" />
            </div>
            <div class="form-group">
              <label>API Key</label>
              <input
                v-model="form.apiKey"
                type="password"
                class="input"
                placeholder="输入 API Key"
              />
            </div>
            <div class="form-group">
              <label>API 端点 URL</label>
              <input
                v-model="form.url"
                class="input"
                placeholder="https://api.deepseek.com/chat/completions"
              />
            </div>
            <button
              class="btn btn-primary"
              :disabled="submitting || !form.name.trim()"
              @click="handleAdd"
            >
              {{ submitting ? '添加中...' : '添加模型' }}
            </button>
          </div>

          <!-- 模型列表 -->
          <div class="model-list-card">
            <div class="card-header">
              <h3 class="card-title">已配置模型（{{ models.length }}）</h3>
              <button class="btn btn-ghost btn-sm" :disabled="loading" @click="loadModels">
                刷新
              </button>
            </div>

            <div v-if="loading" class="list-tip">加载中...</div>
            <div v-else-if="models.length === 0" class="list-tip">暂无模型，请在上方添加</div>

            <div v-else class="model-list">
              <div v-for="m in models" :key="m.name" class="model-item">
                <div class="model-info">
                  <div class="model-name">
                    {{ m.alias || m.name }}
                    <span v-if="m.alias" class="model-name-raw">({{ m.name }})</span>
                  </div>
                  <div v-if="m.modelId" class="model-modelid">
                    Model ID: {{ m.modelId }}
                  </div>
                  <div class="model-url" :title="m.url">{{ m.url || '未配置 URL' }}</div>
                  <div class="model-apikey">
                    API Key: {{ m.apiKey ? '••••••••' + m.apiKey.slice(-4) : '未配置' }}
                  </div>
                </div>
                <button class="btn btn-danger btn-sm" @click="handleDelete(m.name)">
                  删除
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- 工具配置 -->
        <ToolSettings v-if="activeTab === 'tool'" />

        <!-- 权限配置 -->
        <div v-if="activeTab === 'permission'" class="settings-panel">
          <div class="form-group">
            <label>默认权限模式</label>
            <select
              v-model="settingStore.settings.defaultPermissionMode"
              class="input"
              style="max-width: 360px"
            >
              <option v-for="p in PERMISSION_MODES" :key="p.value" :value="p.value">
                {{ p.label }} - {{ p.desc }}
              </option>
            </select>
          </div>
          <div class="hint">权限模式控制 AI 在会话中的自主程度，可在会话中随时切换。</div>
        </div>

        <!-- 关于 -->
        <div v-if="activeTab === 'about'" class="settings-panel">
          <div class="about-info">
            <div class="about-item">
              <span class="label">应用名称</span>
              <span class="value">local-agent</span>
            </div>
            <div class="about-item">
              <span class="label">版本</span>
              <span class="value">0.1.0</span>
            </div>
            <div class="about-item">
              <span class="label">技术栈</span>
              <span class="value">Go + Vue 3 + Wails</span>
            </div>
            <div class="about-item">
              <span class="label">描述</span>
              <span class="value">本地 AI 编程助手</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.settings {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: $color-bg-primary;
}

.settings-header {
  display: flex;
  align-items: center;
  gap: $space-md;
  height: $topbar-height;
  padding: 0 $space-xl;
  border-bottom: 1px solid $color-border;
  background-color: $color-bg-secondary;

  h2 {
    font-size: $font-size-lg;
    font-weight: $font-weight-semibold;
  }
}

.settings-body {
  display: flex;
  flex: 1;
  min-height: 0;
}

.settings-nav {
  width: 200px;
  border-right: 1px solid $color-border;
  padding: $space-lg 0;
  flex-shrink: 0;
}

.nav-item {
  padding: $space-sm $space-xl;
  font-size: $font-size-sm;
  color: $color-text-secondary;
  cursor: pointer;
  border-left: 2px solid transparent;
  transition: all $transition-fast;

  &:hover {
    background-color: $color-bg-tertiary;
    color: $color-text-primary;
  }

  &.active {
    color: $color-primary;
    border-left-color: $color-primary;
    background-color: rgba(124, 58, 237, 0.08);
  }
}

.settings-content {
  flex: 1;
  padding: $space-xl;
  overflow-y: auto;
}

.settings-panel {
  max-width: 600px;
}

.form-group {
  margin-bottom: $space-xl;

  > label {
    display: block;
    font-size: $font-size-sm;
    font-weight: $font-weight-medium;
    margin-bottom: $space-sm;
    color: $color-text-primary;
  }
}

.checkbox-group {
  label {
    display: flex;
    align-items: center;
    gap: $space-sm;
    cursor: pointer;
    font-weight: $font-weight-normal;
  }

  input[type='checkbox'] {
    accent-color: $color-primary;
    width: 16px;
    height: 16px;
  }
}

input[type='range'] {
  width: 240px;
  accent-color: $color-primary;
}

.hint {
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin-top: $space-sm;
}

.about-info {
  display: flex;
  flex-direction: column;
  gap: $space-md;
}

.about-item {
  display: flex;
  padding: $space-md 0;
  border-bottom: 1px solid $color-border;

  .label {
    width: 120px;
    color: $color-text-secondary;
    font-size: $font-size-sm;
  }

  .value {
    flex: 1;
    font-size: $font-size-sm;
    color: $color-text-primary;
  }
}

// ===== 模型管理 =====
.settings-panel {
  max-width: 720px;
}

.error-banner {
  padding: $space-sm $space-md;
  margin-bottom: $space-lg;
  background-color: rgba(255, 85, 85, 0.12);
  border: 1px solid rgba(255, 85, 85, 0.4);
  border-radius: $radius-sm;
  color: $color-error;
  font-size: $font-size-sm;
}

.model-form-card,
.model-list-card {
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  padding: $space-lg;
  margin-bottom: $space-lg;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: $space-md;
}

.card-title {
  font-size: $font-size-md;
  font-weight: $font-weight-semibold;
  color: $color-text-primary;
}

.form-row {
  display: flex;
  gap: $space-md;

  .form-group {
    flex: 1;
  }
}

.required {
  color: $color-error;
}

.list-tip {
  text-align: center;
  color: $color-text-muted;
  font-size: $font-size-sm;
  padding: $space-xl 0;
}

.model-list {
  display: flex;
  flex-direction: column;
  gap: $space-sm;
}

.model-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: $space-md;
  background-color: $color-bg-primary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  transition: border-color $transition-fast;

  &:hover {
    border-color: $color-border-light;
  }
}

.model-info {
  flex: 1;
  min-width: 0;
}

.model-name {
  font-size: $font-size-sm;
  font-weight: $font-weight-semibold;
  color: $color-text-primary;
}

.model-name-raw {
  font-weight: $font-weight-normal;
  color: $color-text-muted;
  font-size: $font-size-xs;
}

.model-modelid {
  font-size: $font-size-xs;
  color: $color-info;
  margin-top: 2px;
}

.model-url {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 400px;
}

.model-apikey {
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin-top: 2px;
}

.btn-sm {
  padding: $space-xs $space-md;
  font-size: $font-size-xs;
}
</style>
