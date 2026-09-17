<script setup>
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useSettingStore } from '@/stores/setting'
import { fetchModels, addModel, deleteModel, updateModel, getModelFull, testModelConnection } from '@/api/model'
import { fetchModelNames } from '@/api/model'
import ToolSettings from '@/components/business/ToolSettings.vue'
import SkillSettings from '@/components/business/SkillSettings.vue'
import PermissionSettings from '@/components/business/PermissionSettings.vue'

const router = useRouter()
const settingStore = useSettingStore()

const activeTab = ref('general')

const tabs = [
  { key: 'general', label: '通用设置' },
  { key: 'model', label: '模型配置' },
  { key: 'tool', label: '工具配置' },
  { key: 'skill', label: '技能配置' },
  { key: 'permission', label: '权限配置' },
  { key: 'about', label: '关于' },
]

function goBack() {
  router.push('/')
}

// ===== 通用设置：默认模型 =====
const modelNamesForDefault = ref([])

async function loadModelNamesForDefault() {
  try {
    modelNamesForDefault.value = await fetchModelNames()
  } catch (e) {
    console.error('加载模型列表失败:', e)
  }
}

// ===== 模型管理 =====
const models = ref([])
const loading = ref(false)
const errorMsg = ref('')
const submitting = ref(false)
const testingModel = ref(false)
const testResult = ref(null) // { success: boolean, message: string }

const form = reactive({
  name: '',
  alias: '',
  modelId: '',
  apiKey: '',
  url: '',
  protocol: '',
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
      protocol: form.protocol || '',
    })
    form.name = ''
    form.alias = ''
    form.modelId = ''
    form.apiKey = ''
    form.url = ''
    form.protocol = ''
    await loadModels()
    await loadModelNamesForDefault()
  } catch (e) {
    errorMsg.value = `添加失败：${e.message || e}`
  } finally {
    submitting.value = false
  }
}

async function handleDelete(name) {
  if (!confirm(`确定删除模型 "${name}" 吗？`)) return
  errorMsg.value = ''
  try {
    await deleteModel(name)
    await loadModels()
    await loadModelNamesForDefault()
  } catch (e) {
    errorMsg.value = `删除失败：${e.message || e}`
  }
}

async function handleTestConnection() {
  if (!form.url.trim() || !form.apiKey) {
    testResult.value = { success: false, message: '请先填写 URL 和 API Key' }
    return
  }
  testingModel.value = true
  testResult.value = null
  try {
    await testModelConnection({
      name: form.name.trim() || '__test__',
      modelId: form.modelId.trim() || form.name.trim(),
      apiKey: form.apiKey,
      url: form.url.trim(),
      protocol: form.protocol || '',
    })
    testResult.value = { success: true, message: '连接成功！' }
  } catch (e) {
    testResult.value = { success: false, message: `连接失败：${e.message || e}` }
  } finally {
    testingModel.value = false
  }
}

// ===== 编辑模型 =====
const editing = ref(false)
const editName = ref('')  // 原模型名称
const editForm = reactive({
  name: '',      // 新模型名称（可修改 = 重命名）
  alias: '',
  modelId: '',
  apiKey: '',
  url: '',
  protocol: '',
})
const editSubmitting = ref(false)
const editTesting = ref(false)
const editTestResult = ref(null)

async function startEdit(m) {
  editing.value = true
  editName.value = m.name
  // 使用 getModelFull 获取不脱敏的完整 API Key
  try {
    const full = await getModelFull(m.name)
    editForm.name = full.name
    editForm.alias = full.alias || ''
    editForm.modelId = full.modelId || ''
    editForm.apiKey = full.apiKey || ''
    editForm.url = full.url || ''
    editForm.protocol = full.protocol || ''
  } catch (e) {
    // 回退到脱敏数据
    editForm.name = m.name
    editForm.alias = m.alias || ''
    editForm.modelId = m.modelId || ''
    editForm.apiKey = m.apiKey || ''
    editForm.url = m.url || ''
    editForm.protocol = m.protocol || ''
  }
  editTestResult.value = null
}

function cancelEdit() {
  editing.value = false
  editName.value = ''
  editForm.name = ''
  editForm.alias = ''
  editForm.modelId = ''
  editForm.apiKey = ''
  editForm.url = ''
  editForm.protocol = ''
  editTestResult.value = null
}

async function handleUpdate() {
  if (!editForm.name.trim()) {
    errorMsg.value = '模型名称不能为空'
    return
  }
  editSubmitting.value = true
  errorMsg.value = ''
  try {
    await updateModel(editName.value, {
      name: editForm.name.trim(),
      alias: editForm.alias.trim(),
      modelId: editForm.modelId.trim(),
      apiKey: editForm.apiKey,
      url: editForm.url.trim(),
      protocol: editForm.protocol || '',
    })
    cancelEdit()
    await loadModels()
    await loadModelNamesForDefault()
  } catch (e) {
    errorMsg.value = `更新失败：${e.message || e}`
  } finally {
    editSubmitting.value = false
  }
}

async function handleEditTestConnection() {
  if (!editForm.url.trim() || !editForm.apiKey) {
    editTestResult.value = { success: false, message: '请先填写 URL 和 API Key' }
    return
  }
  editTesting.value = true
  editTestResult.value = null
  try {
    await testModelConnection({
      name: editForm.name.trim() || '__test__',
      modelId: editForm.modelId.trim() || editForm.name.trim(),
      apiKey: editForm.apiKey,
      url: editForm.url.trim(),
      protocol: editForm.protocol || '',
    })
    editTestResult.value = { success: true, message: '连接成功！' }
  } catch (e) {
    editTestResult.value = { success: false, message: `连接失败：${e.message || e}` }
  } finally {
    editTesting.value = false
  }
}

onMounted(() => {
  loadModels()
  loadModelNamesForDefault()
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
          <div class="form-group checkbox-group">
            <label>
              <input type="checkbox" v-model="settingStore.settings.streamResponse" />
              流式输出
              <span class="checkbox-hint">（逐字显示 AI 回复，需要模型服务支持 SSE；不支持时关闭此项）</span>
            </label>
          </div>
          <div class="form-group">
            <label>默认模型 <span class="checkbox-hint">（新建会话时自动选中，为空则不设默认）</span></label>
            <select
              v-model="settingStore.settings.defaultModel"
              class="input"
              style="max-width: 360px"
            >
              <option value="">不设默认</option>
              <option v-for="m in modelNamesForDefault" :key="m" :value="m">{{ m }}</option>
            </select>
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
              <label>API 端点 URL <span class="checkbox-hint">（选了协议后可只填 baseUrl，路径自动拼接）</span></label>
              <input
                v-model="form.url"
                class="input"
                placeholder="https://api.deepseek.com 或 https://api.deepseek.com/chat/completions"
              />
            </div>
            <div class="form-group">
              <label>协议 <span class="checkbox-hint">（留空则按 URL 自动推断）</span></label>
              <select v-model="form.protocol" class="input" style="max-width: 360px">
                <option value="">自动推断（默认）</option>
                <option value="openai">OpenAI（/chat/completions）</option>
                <option value="anthropic">Anthropic（/v1/messages）</option>
              </select>
            </div>
            <!-- 测试连接结果（添加表单） -->
            <div v-if="testResult" class="test-result" :class="testResult.success ? 'test-success' : 'test-error'">
              {{ testResult.message }}
            </div>
            <div class="form-actions">
              <button
                class="btn btn-ghost"
                :disabled="testingModel || !form.url.trim() || !form.apiKey"
                @click="handleTestConnection"
              >
                {{ testingModel ? '测试中...' : '测试连接' }}
              </button>
              <button
                class="btn btn-primary"
                :disabled="submitting || !form.name.trim()"
                @click="handleAdd"
              >
                {{ submitting ? '添加中...' : '添加模型' }}
              </button>
            </div>
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
                  <div class="model-protocol">
                    协议: {{ m.protocol || '自动推断' }}
                  </div>
                  <div class="model-apikey">
                    API Key: {{ m.apiKey ? m.apiKey : '未配置' }}
                  </div>
                </div>
                <div class="model-actions">
                  <button class="btn btn-ghost btn-sm" @click="startEdit(m)">
                    编辑
                  </button>
                  <button class="btn btn-danger btn-sm" @click="handleDelete(m.name)">
                    删除
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 编辑模型弹窗 -->
        <div v-if="editing" class="modal-overlay" @click.self="cancelEdit">
          <div class="modal-content">
            <div class="modal-header">
              <h3 class="card-title">编辑模型：{{ editName }}</h3>
              <button class="btn btn-ghost btn-sm" @click="cancelEdit">✕</button>
            </div>
            <div v-if="errorMsg" class="error-banner">{{ errorMsg }}</div>
            <div class="form-group">
              <label>模型名称 <span class="checkbox-hint">（修改后将自动更新所有引用该模型的会话）</span></label>
              <input v-model="editForm.name" class="input" placeholder="模型名称" />
            </div>
            <div class="form-group">
              <label>模型别名</label>
              <input v-model="editForm.alias" class="input" placeholder="如：DeepSeek Chat" />
            </div>
            <div class="form-group">
              <label>模型 ID</label>
              <input v-model="editForm.modelId" class="input" placeholder="如：deepseek-chat" />
            </div>
            <div class="form-group">
              <label>API Key</label>
              <input v-model="editForm.apiKey" type="password" class="input" placeholder="输入 API Key" />
            </div>
            <div class="form-group">
              <label>API 端点 URL <span class="checkbox-hint">（选了协议后可只填 baseUrl，路径自动拼接）</span></label>
              <input v-model="editForm.url" class="input" placeholder="https://api.deepseek.com 或 https://api.deepseek.com/chat/completions" />
            </div>
            <div class="form-group">
              <label>协议 <span class="checkbox-hint">（留空则按 URL 自动推断）</span></label>
              <select v-model="editForm.protocol" class="input" style="max-width: 360px">
                <option value="">自动推断（默认）</option>
                <option value="openai">OpenAI（/chat/completions）</option>
                <option value="anthropic">Anthropic（/v1/messages）</option>
              </select>
            </div>
            <!-- 测试连接结果（编辑弹窗） -->
            <div v-if="editTestResult" class="test-result" :class="editTestResult.success ? 'test-success' : 'test-error'">
              {{ editTestResult.message }}
            </div>
            <div class="modal-actions">
              <button
                class="btn btn-ghost"
                :disabled="editTesting || !editForm.url.trim() || !editForm.apiKey"
                @click="handleEditTestConnection"
              >
                {{ editTesting ? '测试中...' : '测试连接' }}
              </button>
              <div style="flex: 1"></div>
              <button class="btn btn-ghost" @click="cancelEdit" :disabled="editSubmitting">取消</button>
              <button class="btn btn-primary" @click="handleUpdate" :disabled="editSubmitting">
                {{ editSubmitting ? '保存中...' : '保存' }}
              </button>
            </div>
          </div>
        </div>

        <!-- 工具配置 -->
        <ToolSettings v-if="activeTab === 'tool'" />

        <!-- 技能配置 -->
        <SkillSettings v-if="activeTab === 'skill'" />

        <!-- 权限配置 -->
        <div v-if="activeTab === 'permission'" class="settings-panel">
          <PermissionSettings />
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
  max-width: 720px;
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

  .checkbox-hint {
    font-size: $font-size-xs;
    color: $color-text-muted;
    font-weight: $font-weight-normal;
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

.model-protocol {
  font-size: $font-size-xs;
  color: $color-info;
  margin-top: 2px;
}

.btn-sm {
  padding: $space-xs $space-md;
  font-size: $font-size-xs;
}

.model-actions {
  display: flex;
  gap: $space-xs;
}

// 测试连接结果
.test-result {
  padding: $space-sm $space-md;
  margin-bottom: $space-md;
  border-radius: $radius-sm;
  font-size: $font-size-sm;
  
  &.test-success {
    background-color: rgba(34, 197, 94, 0.12);
    border: 1px solid rgba(34, 197, 94, 0.4);
    color: #22c55e;
  }
  
  &.test-error {
    background-color: rgba(255, 85, 85, 0.12);
    border: 1px solid rgba(255, 85, 85, 0.4);
    color: $color-error;
  }
}

// 按钮行（添加表单）
.form-actions {
  display: flex;
  gap: $space-sm;
  align-items: center;
}

.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.modal-content {
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  padding: $space-xl;
  width: 500px;
  max-width: 90vw;
  max-height: 85vh;
  overflow-y: auto;
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: $space-lg;
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: $space-sm;
  margin-top: $space-lg;
  align-items: center;
}
</style>
