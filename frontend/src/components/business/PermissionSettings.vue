<script setup>
import { ref, reactive, computed, onMounted } from 'vue'
import { useSessionStore } from '@/stores/session'
import {
  PERMISSION_MODES,
  PERMISSION_BUCKETS,
  fetchPermissionConfig,
  changeDefaultMode,
  changePermissionMode,
  addRule,
  removeRule,
  revokeGrant,
  fetchAudit,
  fetchGrants,
  clearGrants,
} from '@/api/permission'

const sessionStore = useSessionStore()

const loading = ref(false)
const errorMsg = ref('')
const noticeMsg = ref('')
const config = ref(null)
const audit = ref([])
const grants = ref([])

// 新增规则表单
const newRule = reactive({ bucket: 'allow', rule: '', source: 'local' })

const sessionId = computed(() => sessionStore.currentSessionId || '')

const SOURCE_LABEL = {
  builtin: '内置',
  user: '用户全局',
  project: '项目级',
  local: '项目本地',
}

const layers = [
  { value: 'local', label: '项目本地层（settings.local.json，推荐）' },
  { value: 'project', label: '项目级（settings.json，可提交版本控制）' },
  { value: 'user', label: '用户全局（~/.local-agent/settings.json）' },
]

/** 把规则对象或字符串统一成展示用文本 */
function ruleText(r) {
  return typeof r === 'string' ? r : r.raw || ''
}

function ruleSource(r) {
  return typeof r === 'string' ? '' : r.source || ''
}

async function load() {
  loading.value = true
  errorMsg.value = ''
  try {
    config.value = await fetchPermissionConfig(sessionId.value)
    audit.value = await fetchAudit(sessionId.value)
    grants.value = await fetchGrants(sessionId.value)
  } catch (e) {
    errorMsg.value = `加载权限配置失败：${e.message || e}`
  } finally {
    loading.value = false
  }
}

function flash(msg) {
  noticeMsg.value = msg
  setTimeout(() => {
    if (noticeMsg.value === msg) noticeMsg.value = ''
  }, 2500)
}

async function onDefaultModeChange(e) {
  try {
    await changeDefaultMode(sessionId.value, e.target.value, newRule.source)
    flash(`默认模式已改为 ${e.target.value}`)
    await load()
  } catch (err) {
    errorMsg.value = `修改默认模式失败：${err.message || err}`
  }
}

async function onSessionModeChange(e) {
  try {
    await changePermissionMode(sessionId.value, e.target.value)
    flash('本会话模式已切换（不写入配置文件）')
  } catch (err) {
    // 会话尚未初始化引擎时会失败，提示用户先发一条消息
    errorMsg.value = `切换会话模式失败：${err.message || err}`
  }
}

async function handleAdd() {
  const { bucket, rule, source } = newRule
  if (!rule.trim()) {
    errorMsg.value = '规则不能为空'
    return
  }
  try {
    await addRule(sessionId.value, bucket, rule.trim(), source)
    newRule.rule = ''
    flash('规则已添加')
    await load()
  } catch (e) {
    errorMsg.value = `添加规则失败：${e.message || e}`
  }
}

async function handleRemove(bucket, r) {
  const text = ruleText(r)
  const source = ruleSource(r) || 'local'
  if (source === 'builtin') {
    errorMsg.value = '内置规则不可删除'
    return
  }
  if (!confirm(`确定删除规则 "${text}" 吗？`)) return
  try {
    await removeRule(sessionId.value, bucket, text, source)
    flash('规则已删除')
    await load()
  } catch (e) {
    errorMsg.value = `删除规则失败：${e.message || e}`
  }
}

async function handleClearGrants() {
  if (!confirm('清空本会话的授权吗？清空后相关操作会重新询问。')) return
  try {
    await clearGrants(sessionId.value)
    flash('本会话授权已清空')
    await load()
  } catch (e) {
    errorMsg.value = `清空授权失败：${e.message || e}`
  }
}

/** 逐条撤销会话授权。「永久」级别的会连带删除配置文件里的对应允许规则。 */
async function handleRevokeGrant(g) {
  const label = `${g.toolName}${g.spec ? '(' + g.spec + ')' : ''}`
  const extra = g.scope === 'always' ? '\n（该授权是「永久」级别，会同时删除配置文件中对应的允许规则）' : ''
  if (!confirm(`撤销授权「${label}」吗？${extra}`)) return
  try {
    await revokeGrant(sessionId.value, g)
    flash('授权已撤销')
    // 「永久」级别会改配置文件，需要连同规则列表一起刷新
    await load()
  } catch (e) {
    errorMsg.value = `撤销授权失败：${e.message || e}`
  }
}

const DECISION_LABEL = { allow: '放行', deny: '拒绝', ask: '询问' }

onMounted(load)
</script>

<template>
  <div class="settings-panel">
    <div v-if="errorMsg" class="error-banner">{{ errorMsg }}</div>
    <div v-if="noticeMsg" class="notice-banner">{{ noticeMsg }}</div>
    <div v-if="loading" class="list-tip">加载中...</div>

    <template v-else-if="config">
      <!-- 默认权限模式 -->
      <div class="card">
        <h3 class="card-title">默认权限模式</h3>
        <div class="form-group">
          <label>模式</label>
          <select
            class="input"
            :value="config.defaultMode"
            style="max-width: 360px"
            @change="onDefaultModeChange"
          >
            <option v-for="m in PERMISSION_MODES" :key="m.value" :value="m.value">
              {{ m.label }} — {{ m.desc }}
            </option>
          </select>
        </div>
        <div class="hint">
          新建会话时使用该模式。当前已加载：<b>{{ config.defaultMode }}</b>
        </div>
      </div>

      <!-- 当前会话模式 -->
      <div class="card">
        <h3 class="card-title">当前会话模式</h3>
        <div class="form-group">
          <label>会话运行时模式（不写入配置文件）</label>
          <select
            class="input"
            :value="config.mode"
            style="max-width: 360px"
            @change="onSessionModeChange"
          >
            <option v-for="m in PERMISSION_MODES" :key="m.value" :value="m.value">
              {{ m.label }}
            </option>
          </select>
        </div>
        <div class="hint">
          会话尚未发生对话时可能切换失败 —— 权限引擎在首次对话时创建。
        </div>
      </div>

      <!-- 新增规则 -->
      <div class="card">
        <h3 class="card-title">新增规则</h3>
        <div class="form-row">
          <div class="form-group">
            <label>桶</label>
            <select v-model="newRule.bucket" class="input">
              <option v-for="b in PERMISSION_BUCKETS" :key="b.key" :value="b.key">
                {{ b.label }} — {{ b.desc }}
              </option>
            </select>
          </div>
          <div class="form-group">
            <label>写入层</label>
            <select v-model="newRule.source" class="input">
              <option v-for="l in layers" :key="l.value" :value="l.value">{{ l.label }}</option>
            </select>
          </div>
        </div>
        <div class="form-group">
          <label>规则</label>
          <input
            v-model="newRule.rule"
            class="input mono"
            spellcheck="false"
            placeholder="exec_shell(git:*)  或  my_api(domain:*.example.com)  或  read_skill(pdf-report)"
          />
        </div>
        <div class="hint">
          命令前缀用 <code>:*</code> 结尾（如 <code>exec_shell(npm run test:*)</code>）；
          裸工具名匹配该工具的全部调用。
        </div>
        <button class="btn btn-primary" :disabled="!newRule.rule.trim()" @click="handleAdd">
          添加规则
        </button>
      </div>

      <!-- 规则列表 -->
      <div v-for="b in PERMISSION_BUCKETS" :key="b.key" class="card">
        <div class="card-header">
          <h3 class="card-title">
            {{ b.label }}规则（{{ (config[b.key] || []).length }}）
          </h3>
        </div>
        <div v-if="!(config[b.key] || []).length" class="list-tip">暂无规则</div>
        <div v-else class="rule-list">
          <div v-for="(r, i) in config[b.key]" :key="i" class="rule-item">
            <code class="rule-text">{{ ruleText(r) }}</code>
            <span v-if="ruleSource(r)" class="src-tag" :class="'src-' + ruleSource(r)">
              {{ SOURCE_LABEL[ruleSource(r)] || ruleSource(r) }}
            </span>
            <button
              class="btn btn-danger btn-sm"
              :disabled="ruleSource(r) === 'builtin'"
              @click="handleRemove(b.key, r)"
            >
              删除
            </button>
          </div>
        </div>
      </div>

      <!-- 内置规则（只读） -->
      <div class="card">
        <h3 class="card-title">内置规则（只读）</h3>
        <div class="hint">
          内置 <b>拒绝</b> 名单针对灾难性且不可逆的操作（递归删除根目录、格式化磁盘、
          覆写块设备等），<b>不可撤销</b>；内置 <b>询问</b> 针对敏感路径（.env、私钥等），
          属于防误读，可在弹窗中当场批准。
        </div>
        <div class="rule-list">
          <div v-for="(r, i) in config.builtinDeny || []" :key="'d' + i" class="rule-item">
            <code class="rule-text">{{ ruleText(r) }}</code>
            <span class="src-tag src-builtin">内置拒绝 · 不可删除</span>
          </div>
          <div v-for="(r, i) in config.builtinAsk || []" :key="'a' + i" class="rule-item">
            <code class="rule-text">{{ ruleText(r) }}</code>
            <span class="src-tag src-builtin">内置询问 · 不可删除</span>
          </div>
        </div>
        <div class="hint">
          这些条目没有删除按钮，也不出现在上面的可编辑列表里 —— 它们由代码内置，无法移除。
          如需放行某个被内置询问拦住的操作（例如读取 .env），请在授权弹窗里当场批准。
        </div>
      </div>

      <!-- 本会话授权 -->
      <div class="card">
        <div class="card-header">
          <h3 class="card-title">本会话授权（{{ grants.length }}）</h3>
          <button class="btn btn-ghost btn-sm" :disabled="!grants.length" @click="handleClearGrants">
            清空
          </button>
        </div>
        <div v-if="!grants.length" class="list-tip">暂无授权</div>
        <div v-else class="rule-list">
          <div v-for="(g, i) in grants" :key="i" class="rule-item">
            <code class="rule-text">{{ g.toolName }}{{ g.spec ? '(' + g.spec + ')' : '' }}</code>
            <span class="src-tag" :class="g.scope === 'always' ? 'src-local' : 'src-project'">
              {{ g.scope === 'always' ? '永久' : '本会话' }}
            </span>
            <button class="btn btn-danger btn-sm" @click="handleRevokeGrant(g)">撤销</button>
          </div>
        </div>
        <div class="hint">
          「本会话允许」记下的授权只存在于内存、不写入配置文件，会话结束即失效；
          「永久」级别则同时写成了允许规则，撤销时会一并删除那条规则。
        </div>
      </div>

      <!-- 审计 -->
      <div class="card">
        <h3 class="card-title">最近决策（{{ audit.length }}）</h3>
        <div v-if="!audit.length" class="list-tip">暂无记录</div>
        <div v-else class="rule-list">
          <div v-for="(a, i) in audit.slice(-20).reverse()" :key="i" class="audit-item">
            <span class="decision" :class="'dec-' + a.decision">
              {{ DECISION_LABEL[a.decision] || a.decision }}
            </span>
            <code class="rule-text">{{ a.toolName }}{{ a.command ? ' · ' + a.command : '' }}</code>
            <span class="audit-reason">{{ a.reason }}</span>
          </div>
        </div>
      </div>

      <!-- 配置来源与错误 -->
      <div class="card">
        <h3 class="card-title">配置来源</h3>
        <div class="hint">
          全局：<code>{{ config.globalPath }}</code>
          <template v-if="config.projectDir">
            <br />项目：<code>{{ config.projectDir }}/.local-agent/</code>
          </template>
        </div>
        <div v-if="(config.sources || []).length" class="sources">
          <div v-for="(s, i) in config.sources" :key="i" class="src-line">已加载：{{ s }}</div>
        </div>
        <div v-if="(config.errors || []).length" class="error-banner">
          <div v-for="(e, i) in config.errors" :key="i">{{ e }}</div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped lang="scss">
.settings-panel {
  max-width: 720px;
}

.card {
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
  margin-bottom: $space-md;
}

.card-header .card-title {
  margin-bottom: 0;
}

.form-group {
  margin-bottom: $space-md;

  > label {
    display: block;
    font-size: $font-size-sm;
    font-weight: $font-weight-medium;
    margin-bottom: $space-sm;
    color: $color-text-primary;
  }
}

.form-row {
  display: flex;
  gap: $space-md;

  .form-group {
    flex: 1;
  }
}

.input {
  width: 100%;
  padding: $space-sm $space-md;
  background-color: $color-bg-primary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  color: $color-text-primary;
  font-size: $font-size-sm;
  outline: none;

  &.mono {
    font-family: $font-family-mono;
    font-size: $font-size-xs;
  }

  &:focus {
    border-color: $color-primary;
  }
}

.hint {
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin: $space-sm 0;

  code {
    font-family: $font-family-mono;
    color: $color-text-secondary;
  }
}

.list-tip {
  text-align: center;
  color: $color-text-muted;
  font-size: $font-size-sm;
  padding: $space-lg 0;
}

.rule-list {
  display: flex;
  flex-direction: column;
  gap: $space-sm;
}

.rule-item {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-sm $space-md;
  background-color: $color-bg-primary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
}

.rule-text {
  flex: 1;
  font-family: $font-family-mono;
  font-size: $font-size-xs;
  color: $color-info;
  word-break: break-all;
}

.src-tag {
  padding: 1px $space-sm;
  font-size: 10px;
  border-radius: $radius-sm;
  border: 1px solid $color-border;
  color: $color-text-muted;
  white-space: nowrap;

  &.src-builtin {
    color: $color-error;
    border-color: rgba(255, 85, 85, 0.4);
  }

  &.src-user {
    color: $color-primary;
    border-color: rgba(189, 147, 249, 0.4);
  }

  &.src-project {
    color: $color-info;
    border-color: rgba(139, 233, 253, 0.4);
  }

  &.src-local {
    color: $color-warning;
    border-color: rgba(255, 184, 108, 0.4);
  }
}

.audit-item {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-xs $space-sm;
  border-bottom: 1px solid $color-border;
  font-size: $font-size-xs;

  &:last-child {
    border-bottom: none;
  }
}

.decision {
  width: 40px;
  flex-shrink: 0;
  text-align: center;
  border-radius: $radius-sm;

  &.dec-allow {
    color: $color-success;
  }

  &.dec-deny {
    color: $color-error;
  }

  &.dec-ask {
    color: $color-warning;
  }
}

.audit-reason {
  flex: 1;
  color: $color-text-muted;
  text-align: right;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sources {
  margin-top: $space-sm;
}

.src-line {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  font-family: $font-family-mono;
}

.error-banner {
  padding: $space-sm $space-md;
  margin-bottom: $space-md;
  background-color: rgba(255, 85, 85, 0.12);
  border: 1px solid rgba(255, 85, 85, 0.4);
  border-radius: $radius-sm;
  color: $color-error;
  font-size: $font-size-xs;
  // 这个面板很长，提示如果只在顶部、用户滚到规则列表就看不到，
  // 会出现「点了删除毫无反应」的错觉，所以做成吸附
  position: sticky;
  top: 0;
  z-index: 2;
  backdrop-filter: blur(2px);
}

.notice-banner {
  padding: $space-sm $space-md;
  margin-bottom: $space-md;
  background-color: rgba(80, 250, 123, 0.12);
  border: 1px solid rgba(80, 250, 123, 0.4);
  border-radius: $radius-sm;
  color: $color-success;
  font-size: $font-size-xs;
  position: sticky;
  top: 0;
  z-index: 2;
  backdrop-filter: blur(2px);
}

.btn {
  padding: $space-sm $space-md;
  font-size: $font-size-sm;
  border-radius: $radius-sm;
  border: 1px solid $color-border;
  background-color: $color-bg-tertiary;
  color: $color-text-primary;
  cursor: pointer;
  transition: all $transition-fast;

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  &.btn-primary {
    background-color: $color-primary;
    border-color: $color-primary;
    color: #21222c;
  }

  &.btn-danger {
    background-color: rgba(255, 85, 85, 0.15);
    border-color: rgba(255, 85, 85, 0.4);
    color: $color-error;
  }

  &.btn-ghost {
    background-color: transparent;
  }

  &.btn-sm {
    padding: 2px $space-sm;
    font-size: $font-size-xs;
  }
}
</style>
