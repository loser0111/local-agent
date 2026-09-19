<script setup>
import { reactive, ref, computed, watch } from 'vue'
import { X, Plus, Trash2, Plug, Loader2 } from 'lucide-vue-next'
import ToolIcon from './ToolIcon.vue'
import { TOOL_ICON_NAMES } from './toolIcons'
import { useToolsStore } from '@/stores/tools'

const props = defineProps({
  visible: Boolean,
  tool: { type: Object, default: null }, // 编辑传入，null 为新增
})
const emit = defineEmits(['close', 'saved'])

const toolsStore = useToolsStore()

// 来源类型（与 Go 侧 SourceKind 一致：builtin 由程序内置，这里只能新增后三种）
const TYPE_OPTIONS = [
  { value: 'cli', label: 'CLI 工具', desc: '执行本地命令行', icon: 'terminal' },
  { value: 'mcp', label: 'MCP 服务器', desc: '接入一个 MCP Server', icon: 'blocks' },
  { value: 'http', label: 'HTTP API', desc: '调用 HTTP 接口', icon: 'webhook' },
]

// 暴露策略：决定该来源的工具怎么给模型看
const EXPOSURE_OPTIONS = [
  { value: 'router', label: '经工具路由器发现（默认）', desc: '不占 prompt，模型需要时先 list/describe' },
  { value: 'direct', label: '直接暴露给模型', desc: '少一轮往返，但工具多时 prompt 会变大' },
  { value: 'internal', label: '不暴露给模型', desc: '仅保留配置，模型看不到也用不了' },
]

const emptyForm = () => ({
  id: '',
  name: '',
  label: '',
  description: '',
  kind: 'cli',
  exposure: 'router',
  icon: 'terminal',
  enabled: true,
  builtin: false,
  // cli
  command: '',
  // api
  method: 'GET',
  url: '',
  body: '',
  apiTimeout: 15,
  cliTimeout: 60,
  // mcp（用官方 type 取值：streamable-http / sse / stdio）
  mcpType: 'streamable-http',
  mcpCommand: '',
  mcpArgs: '',
  mcpURL: '',
  discovered: [],
  disabledTools: [],
  // 键值对行（headers / env）
  apiHeaders: [{ key: '', value: '' }],
  mcpEnv: [{ key: '', value: '' }],
  mcpHeaders: [{ key: '', value: '' }],
  // 自定义参数
  parameters: [],
})

const form = reactive(emptyForm())
const saving = ref(false)
const testing = ref(false)
const testError = ref('')
const iconPickerOpen = ref(false)
const iconKeyword = ref('')
const errorMsg = ref('')

const isEdit = computed(() => !!form.id)
const isBuiltin = computed(() => form.builtin)
const isMCP = computed(() => form.kind === 'mcp')

// 图标选择器过滤
const iconChoices = computed(() => {
  const kw = iconKeyword.value.trim().toLowerCase()
  if (!kw) return TOOL_ICON_NAMES
  return TOOL_ICON_NAMES.filter((n) => n.includes(kw))
})

watch(
  () => props.visible,
  (v) => {
    if (!v) return
    errorMsg.value = ''
    testError.value = ''
    iconPickerOpen.value = false
    Object.assign(form, emptyForm())
    if (props.tool) {
      const t = props.tool
      Object.assign(form, {
        id: t.id || '',
        name: t.name || '',
        label: t.label || '',
        description: t.description || '',
        kind: t.kind || 'cli',
        exposure: t.exposure || 'router',
        icon: t.icon || 'zap',
        enabled: t.enabled !== false,
        builtin: !!t.builtin,
        parameters: (t.parameters || []).map((p) => ({ ...p })),
        discovered: t.discovered || [],
        disabledTools: [...(t.disabledTools || [])],
      })
      // 配置已是类型化字段（cli / http / mcp），不再从弱类型 config 里解析
      if (t.kind === 'cli' && t.cli) {
        form.command = t.cli.command || ''
        form.cliTimeout = t.cli.timeout || 60
      } else if (t.kind === 'http' && t.http) {
        form.method = t.http.method || 'GET'
        form.url = t.http.url || ''
        form.body = t.http.body || ''
        form.apiTimeout = t.http.timeout || 15
        form.apiHeaders = kvFromObject(t.http.headers)
      } else if (t.kind === 'mcp' && t.mcp) {
        form.mcpType = t.mcp.type || 'streamable-http'
        form.mcpCommand = t.mcp.command || ''
        form.mcpArgs = (t.mcp.args || []).join('\n')
        form.mcpURL = t.mcp.url || ''
        form.mcpEnv = kvFromObject(t.mcp.env)
        form.mcpHeaders = kvFromObject(t.mcp.headers)
      }
    }
  }
)

function kvFromObject(obj) {
  const entries = Object.entries(obj || {})
  if (entries.length === 0) return [{ key: '', value: '' }]
  return entries.map(([key, value]) => ({ key, value }))
}

function kvToObject(rows) {
  const o = {}
  for (const r of rows) {
    if (r.key.trim()) o[r.key.trim()] = r.value
  }
  return o
}

function selectType(type) {
  if (isEdit.value) return
  form.kind = type
  form.icon = { cli: 'terminal', mcp: 'blocks', http: 'webhook' }[type]
}

// ===== 参数行编辑 =====
function addParam() {
  form.parameters.push({ name: '', description: '', required: false })
}
function removeParam(i) {
  form.parameters.splice(i, 1)
}

// ===== 键值行 =====
function addKV(field) {
  form[field].push({ key: '', value: '' })
}
function removeKV(field, i) {
  form[field].splice(i, 1)
}

// ===== 构造保存载荷 =====
function buildPayload() {
  const payload = {
    id: form.id || undefined,
    name: form.name.trim(),
    label: form.label.trim() || form.name.trim(),
    description: form.description.trim(),
    kind: form.kind,
    exposure: form.exposure,
    icon: form.icon,
    enabled: form.enabled,
    parameters: form.parameters
      .filter((p) => p.name.trim())
      .map((p) => ({ name: p.name.trim(), description: p.description.trim(), required: !!p.required })),
    disabledTools: form.disabledTools,
  }
  // 各来源类型写到各自的类型化字段（MCP 用官方形状，导入导出与生态一致）
  if (form.kind === 'cli') {
    payload.cli = { command: form.command, timeout: Number(form.cliTimeout) || 60 }
  } else if (form.kind === 'http') {
    payload.http = {
      method: form.method,
      url: form.url,
      headers: kvToObject(form.apiHeaders),
      body: form.body,
      timeout: Number(form.apiTimeout) || 15,
    }
  } else if (form.kind === 'mcp') {
    payload.mcp = {
      type: form.mcpType,
      url: form.mcpURL,
      headers: kvToObject(form.mcpHeaders),
      command: form.mcpCommand,
      args: form.mcpArgs.split('\n').map((s) => s.trim()).filter(Boolean),
      env: kvToObject(form.mcpEnv),
    }
  }
  return payload
}

async function handleSave() {
  errorMsg.value = ''
  if (!form.name.trim()) {
    errorMsg.value = '工具名称不能为空'
    return
  }
  if (!form.description.trim()) {
    errorMsg.value = '工具描述不能为空（大模型依靠描述判断何时使用该工具）'
    return
  }
  if (form.kind === 'cli' && !form.command.trim()) {
    errorMsg.value = 'CLI 工具需要填写命令模板'
    return
  }
  if (form.kind === 'http' && !form.url.trim()) {
    errorMsg.value = 'HTTP API 需要填写请求 URL'
    return
  }
  if (form.kind === 'mcp' && form.mcpType === 'stdio' && !form.mcpCommand.trim()) {
    errorMsg.value = 'stdio 接入需要填写启动命令'
    return
  }
  if (form.kind === 'mcp' && form.mcpType !== 'stdio' && !form.mcpURL.trim()) {
    errorMsg.value = 'HTTP/SSE 接入需要填写 Server URL'
    return
  }
  saving.value = true
  try {
    const saved = await toolsStore.save(buildPayload())
    emit('saved', saved)
    emit('close')
  } catch (e) {
    errorMsg.value = `保存失败：${e.message || e}`
  } finally {
    saving.value = false
  }
}

async function handleTest() {
  testError.value = ''
  if (form.mcpType === 'stdio' && !form.mcpCommand.trim()) {
    testError.value = '请先填写启动命令'
    return
  }
  if (form.mcpType !== 'stdio' && !form.mcpURL.trim()) {
    testError.value = '请先填写 Server URL'
    return
  }
  testing.value = true
  try {
    const payload = buildPayload()
    const discovered = await toolsStore.testConnection(payload)
    form.discovered = discovered
  } catch (e) {
    testError.value = `连接失败：${e.message || e}`
  } finally {
    testing.value = false
  }
}

function toggleDiscoveredTool(name) {
  const idx = form.disabledTools.indexOf(name)
  if (idx > -1) form.disabledTools.splice(idx, 1)
  else form.disabledTools.push(name)
}
</script>

<template>
  <div v-if="visible" class="dialog-mask" @click.self="emit('close')">
    <div class="dialog">
      <div class="dialog-header">
        <h3>{{ isEdit ? '编辑工具' : '添加工具' }}</h3>
        <button class="icon-btn" @click="emit('close')"><X :size="18" /></button>
      </div>

      <div class="dialog-body">
        <div v-if="errorMsg" class="error-banner">{{ errorMsg }}</div>

        <!-- 类型选择（仅新增） -->
        <div v-if="!isEdit" class="form-group">
          <label>工具类型 <span class="required">*</span></label>
          <div class="type-grid">
            <div
              v-for="t in TYPE_OPTIONS"
              :key="t.value"
              class="type-card"
              :class="{ active: form.kind === t.value }"
              @click="selectType(t.value)"
            >
              <ToolIcon :name="t.icon" :size="22" />
              <div class="type-name">{{ t.label }}</div>
              <div class="type-desc">{{ t.desc }}</div>
            </div>
          </div>
        </div>

        <!-- 图标选择 -->
        <div class="form-group">
          <label>图标</label>
          <div class="icon-row">
            <button class="icon-preview" @click="iconPickerOpen = !iconPickerOpen">
              <ToolIcon :name="form.icon" :size="18" />
            </button>
            <span class="icon-name-text">{{ form.icon }}</span>
          </div>
          <div v-if="iconPickerOpen" class="icon-picker">
            <input v-model="iconKeyword" class="input icon-search" placeholder="搜索图标..." />
            <div class="icon-grid">
              <button
                v-for="n in iconChoices"
                :key="n"
                class="icon-cell"
                :class="{ active: form.icon === n }"
                :title="n"
                @click="form.icon = n"
              >
                <ToolIcon :name="n" :size="17" />
              </button>
            </div>
          </div>
        </div>

        <!-- 通用字段 -->
        <div class="form-row">
          <div class="form-group">
            <label>工具名称 <span class="required">*</span></label>
            <input
              v-model="form.name"
              class="input"
              :disabled="isBuiltin"
              placeholder="如 weather、ffmpeg（字母/数字/下划线）"
            />
          </div>
          <div class="form-group">
            <label>显示名称</label>
            <input v-model="form.label" class="input" placeholder="界面展示名" />
          </div>
        </div>
        <div class="form-group">
          <label>工具描述 <span class="required">*</span></label>
          <textarea
            v-model="form.description"
            class="input"
            rows="2"
            :disabled="isBuiltin"
            placeholder="告诉大模型这个工具做什么、什么时候使用"
          ></textarea>
        </div>

        <!-- 暴露策略：决定该来源的工具怎么给模型看 -->
        <div class="form-group">
          <label>
            暴露策略
            <span class="checkbox-hint">（决定模型是"直接用得上"还是"要先发现"）</span>
          </label>
          <select v-model="form.exposure" class="input" style="max-width: 420px">
            <option v-for="e in EXPOSURE_OPTIONS" :key="e.value" :value="e.value">
              {{ e.label }} —— {{ e.desc }}
            </option>
          </select>
        </div>

        <!-- ===== CLI 配置 ===== -->
        <template v-if="form.kind === 'cli' && !isBuiltin">
          <div class="form-group">
            <label>命令模板 <span class="required">*</span></label>
            <input v-model="form.command" class="input code" placeholder="ffmpeg {{input}} 或 git log --oneline -20" />
            <div class="hint">用 {'{{参数名}}'} 引用下方定义的参数；留空参数则执行固定命令</div>
          </div>
          <div class="form-group compact">
            <label>超时（秒）</label>
            <input v-model.number="form.cliTimeout" type="number" min="1" class="input small" />
          </div>
        </template>

        <!-- ===== API 配置 ===== -->
        <template v-if="form.kind === 'http'">
          <div class="form-row">
            <div class="form-group compact method-group">
              <label>方法</label>
              <select v-model="form.method" class="input small">
                <option>GET</option>
                <option>POST</option>
                <option>PUT</option>
                <option>DELETE</option>
                <option>PATCH</option>
              </select>
            </div>
            <div class="form-group">
              <label>请求 URL <span class="required">*</span></label>
              <input v-model="form.url" class="input code" placeholder="https://api.example.com/v1/weather?city={{city}}" />
            </div>
          </div>
          <div class="form-group">
            <label>请求头 Headers</label>
            <div v-for="(row, i) in form.apiHeaders" :key="i" class="kv-row">
              <input v-model="row.key" class="input small" placeholder="Authorization" />
              <input v-model="row.value" class="input" placeholder="Bearer xxx 或 {{env.TOKEN}}" />
              <button class="icon-btn danger" @click="removeKV('apiHeaders', i)"><Trash2 :size="14" /></button>
            </div>
            <button class="btn btn-ghost btn-sm" @click="addKV('apiHeaders')"><Plus :size="13" /> 添加请求头</button>
          </div>
          <div v-if="form.method !== 'GET'" class="form-group">
            <label>请求体模板（JSON）</label>
            <textarea v-model="form.body" class="input code" rows="3" placeholder='{"city":"{{city}}"}'></textarea>
          </div>
          <div class="form-group compact">
            <label>超时（秒）</label>
            <input v-model.number="form.apiTimeout" type="number" min="1" class="input small" />
          </div>
        </template>

        <!-- ===== MCP 配置 ===== -->
        <template v-if="isMCP">
          <div class="form-group">
            <label>传输方式</label>
            <div class="seg">
              <button :class="{ active: form.mcpType === 'streamable-http' }" @click="form.mcpType = 'streamable-http'">Streamable HTTP</button>
              <button :class="{ active: form.mcpType === 'sse' }" @click="form.mcpType = 'sse'">SSE（旧版）</button>
              <button :class="{ active: form.mcpType === 'stdio' }" @click="form.mcpType = 'stdio'">stdio（本地进程）</button>
            </div>
            <div class="hint">与官方 mcpServers 片段一致（type 字段取值），可直接与其它客户端互导</div>
          </div>
          <template v-if="form.mcpType === 'stdio'">
            <div class="form-group">
              <label>启动命令 <span class="required">*</span></label>
              <input v-model="form.mcpCommand" class="input code" placeholder="npx" />
            </div>
            <div class="form-group">
              <label>参数（每行一个）</label>
              <textarea v-model="form.mcpArgs" class="input code" rows="3" :placeholder="'-y\n@modelcontextprotocol/server-filesystem\n.'"></textarea>
            </div>
            <div class="form-group">
              <label>环境变量</label>
              <div v-for="(row, i) in form.mcpEnv" :key="'e' + i" class="kv-row">
                <input v-model="row.key" class="input small" placeholder="API_KEY" />
                <input v-model="row.value" class="input" placeholder="value" />
                <button class="icon-btn danger" @click="removeKV('mcpEnv', i)"><Trash2 :size="14" /></button>
              </div>
              <button class="btn btn-ghost btn-sm" @click="addKV('mcpEnv')"><Plus :size="13" /> 添加环境变量</button>
            </div>
          </template>
          <template v-else>
            <div class="form-group">
              <label>Server URL <span class="required">*</span></label>
              <input v-model="form.mcpURL" class="input code" placeholder="https://example.com/mcp" />
            </div>
            <div class="form-group">
              <label>请求头 Headers</label>
              <div v-for="(row, i) in form.mcpHeaders" :key="'h' + i" class="kv-row">
                <input v-model="row.key" class="input small" placeholder="Authorization" />
                <input v-model="row.value" class="input" placeholder="Bearer xxx" />
                <button class="icon-btn danger" @click="removeKV('mcpHeaders', i)"><Trash2 :size="14" /></button>
              </div>
              <button class="btn btn-ghost btn-sm" @click="addKV('mcpHeaders')"><Plus :size="13" /> 添加请求头</button>
            </div>
          </template>

          <div class="form-group">
            <button class="btn btn-secondary" :disabled="testing" @click="handleTest">
              <Loader2 v-if="testing" :size="14" class="spin" />
              <Plug v-else :size="14" />
              {{ testing ? '连接中...' : '测试连接并发现工具' }}
            </button>
            <div v-if="testError" class="test-error">{{ testError }}</div>
            <div v-if="form.discovered.length" class="discovered">
              <div class="discovered-title">发现 {{ form.discovered.length }} 个工具（取消勾选可禁用单个工具）：</div>
              <label v-for="d in form.discovered" :key="d.name" class="discovered-item">
                <input
                  type="checkbox"
                  :checked="!form.disabledTools.includes(d.name)"
                  @change="toggleDiscoveredTool(d.name)"
                />
                <span class="d-name">{{ d.name }}</span>
                <span class="d-desc">{{ d.description }}</span>
              </label>
            </div>
          </div>
        </template>

        <!-- ===== 自定义参数（CLI/API） ===== -->
        <div v-if="(form.kind === 'cli' || form.kind === 'http') && !isBuiltin" class="form-group">
          <label>工具参数（大模型调用时填写）</label>
          <div v-for="(p, i) in form.parameters" :key="i" class="param-row">
            <input v-model="p.name" class="input small" placeholder="参数名，如 city" />
            <input v-model="p.description" class="input" placeholder="参数说明" />
            <label class="param-required">
              <input type="checkbox" v-model="p.required" /> 必填
            </label>
            <button class="icon-btn danger" @click="removeParam(i)"><Trash2 :size="14" /></button>
          </div>
          <button class="btn btn-ghost btn-sm" @click="addParam"><Plus :size="13" /> 添加参数</button>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn btn-ghost" @click="emit('close')">取消</button>
        <button class="btn btn-primary" :disabled="saving" @click="handleSave">
          {{ saving ? '保存中...' : '保存' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.dialog-mask {
  position: fixed;
  inset: 0;
  background: $color-overlay;
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.dialog {
  width: 640px;
  max-height: 86vh;
  background: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  display: flex;
  flex-direction: column;
}

.dialog-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: $space-md $space-lg;
  border-bottom: 1px solid $color-border;

  h3 {
    font-size: $font-size-md;
    font-weight: $font-weight-semibold;
  }
}

.dialog-body {
  padding: $space-lg;
  overflow-y: auto;
  flex: 1;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: $space-sm;
  padding: $space-md $space-lg;
  border-top: 1px solid $color-border;
}

.error-banner {
  padding: $space-sm $space-md;
  margin-bottom: $space-md;
  background-color: rgb(var(--color-error-rgb) / 0.12);
  border: 1px solid rgb(var(--color-error-rgb) / 0.4);
  border-radius: $radius-sm;
  color: $color-error;
  font-size: $font-size-sm;
}

.required {
  color: $color-error;
}

.hint {
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin-top: 4px;
}

.form-group {
  margin-bottom: $space-md;

  > label {
    display: block;
    font-size: $font-size-sm;
    font-weight: $font-weight-medium;
    margin-bottom: 6px;
  }
}

.form-group.compact {
  width: 180px;
}

.form-row {
  display: flex;
  gap: $space-md;

  .form-group {
    flex: 1;
  }
}

.input.small {
  width: 100%;
}

.input.code {
  font-family: 'Consolas', 'Courier New', monospace;
  font-size: $font-size-xs;
}

.method-group {
  max-width: 130px;
}

// 类型卡片
.type-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: $space-sm;
}

.type-card {
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  padding: $space-md;
  text-align: center;
  cursor: pointer;
  transition: all $transition-fast;
  color: $color-text-secondary;
  background: $color-bg-primary;

  &:hover {
    border-color: $color-primary;
  }

  &.active {
    border-color: $color-primary;
    background: rgb(var(--color-primary-rgb) / 0.1);
    color: $color-primary;
  }

  .type-name {
    font-size: $font-size-sm;
    font-weight: $font-weight-medium;
    margin-top: 6px;
  }

  .type-desc {
    font-size: $font-size-xs;
    color: $color-text-muted;
    margin-top: 2px;
  }
}

// 图标
.icon-row {
  display: flex;
  align-items: center;
  gap: $space-sm;
}

.icon-preview {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: $color-bg-tertiary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  color: $color-text-primary;
  cursor: pointer;

  &:hover {
    border-color: $color-primary;
    color: $color-primary;
  }
}

.icon-name-text {
  font-size: $font-size-xs;
  color: $color-text-muted;
  font-family: monospace;
}

.icon-picker {
  margin-top: $space-sm;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  padding: $space-sm;
  background: $color-bg-primary;
}

.icon-search {
  margin-bottom: $space-sm;
}

.icon-grid {
  display: grid;
  grid-template-columns: repeat(10, 1fr);
  gap: 4px;
  max-height: 168px;
  overflow-y: auto;
}

.icon-cell {
  aspect-ratio: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 4px;
  color: $color-text-secondary;
  cursor: pointer;

  &:hover {
    background: $color-bg-tertiary;
    color: $color-primary;
  }

  &.active {
    background: rgb(var(--color-primary-rgb) / 0.18);
    border-color: $color-primary;
    color: $color-primary;
  }
}

// 键值行/参数行
.kv-row,
.param-row {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 6px;

  .input.small {
    width: 160px;
    flex-shrink: 0;
  }
}

.param-required {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: $font-size-xs;
  color: $color-text-secondary;
  white-space: nowrap;
  flex-shrink: 0;

  input {
    accent-color: $color-primary;
  }
}

.icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  background: transparent;
  border: none;
  border-radius: 4px;
  color: $color-text-secondary;
  cursor: pointer;
  flex-shrink: 0;

  &:hover {
    background: $color-bg-tertiary;
    color: $color-text-primary;
  }

  &.danger:hover {
    color: $color-error;
  }
}

.btn-sm {
  padding: 4px 10px;
  font-size: $font-size-xs;
  gap: 4px;
  display: inline-flex;
  align-items: center;
}

// 分段选择
.seg {
  display: flex;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  overflow: hidden;
  width: fit-content;

  button {
    padding: 6px 12px;
    background: $color-bg-primary;
    border: none;
    border-right: 1px solid $color-border;
    color: $color-text-secondary;
    font-size: $font-size-xs;
    cursor: pointer;

    &:last-child {
      border-right: none;
    }

    &.active {
      background: rgb(var(--color-primary-rgb) / 0.15);
      color: $color-primary;
    }
  }
}

// MCP 测试
.test-error {
  margin-top: $space-sm;
  font-size: $font-size-xs;
  color: $color-error;
}

.discovered {
  margin-top: $space-md;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  padding: $space-sm;
  background: $color-bg-primary;
}

.discovered-title {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  margin-bottom: 6px;
}

.discovered-item {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: 4px 0;
  font-size: $font-size-xs;
  cursor: pointer;

  input {
    accent-color: $color-primary;
  }

  .d-name {
    font-family: monospace;
    color: $color-info;
    flex-shrink: 0;
  }

  .d-desc {
    color: $color-text-muted;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
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
