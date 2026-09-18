<script setup>
import { computed, onMounted, ref } from 'vue'
import { usePermissionStore } from '@/stores/permissions'
import { useSessionStore } from '@/stores/session'
import { PERMISSION_MODES } from '@/types'

/**
 * 权限配置面板
 *
 * 展示的是"这次会话实际生效的东西"：模式、三层合并后的规则、每条规则的来源文件、
 * 本会话授权、以及判定审计。规则的新增/删除会写回对应的配置文件。
 */
const permissionStore = usePermissionStore()
const sessionStore = useSessionStore()

const sessionId = computed(() => sessionStore.currentSessionId)

const newRule = ref('')
const newBucket = ref('allow')
const newScope = ref('local')
const busy = ref(false)
const actionError = ref('')
const actionOk = ref('')

const BUCKETS = [
  { value: 'deny', label: 'Deny（拒绝，优先于一切）' },
  { value: 'ask', label: 'Ask（询问，优先于只读放行）' },
  { value: 'allow', label: 'Allow（放行，需每一段都被覆盖）' },
]

const SCOPES = [
  { value: 'local', label: '项目本地（permissions.local.json）' },
  { value: 'project', label: '项目级（permissions.json）' },
  { value: 'user', label: '用户全局（~/.local-agent/permissions.json）' },
]

async function refresh() {
  actionError.value = ''
  await permissionStore.load(sessionId.value)
  await permissionStore.loadAudit(sessionId.value)
}

onMounted(refresh)

/** 规则的来源文件 → 所在层（用于删除时定位配置文件） */
function scopeOfSource(source) {
  const hit = permissionStore.sources.find((s) => s.path === source)
  return hit ? hit.scope : 'local'
}

async function changeMode(e) {
  const mode = e.target.value
  if (!sessionId.value) return
  busy.value = true
  actionError.value = ''
  try {
    await permissionStore.setMode(sessionId.value, mode)
    // 同步会话列表，顶栏的模式选择器读的是会话字段
    await sessionStore.refreshSessions()
  } catch (err) {
    actionError.value = `切换模式失败：${err.message || err}`
  } finally {
    busy.value = false
  }
}

async function submitRule() {
  const rule = newRule.value.trim()
  if (!rule) {
    actionError.value = '请填写规则，例如 exec_shell(git status)'
    return
  }
  busy.value = true
  actionError.value = ''
  actionOk.value = ''
  try {
    await permissionStore.addRule(sessionId.value, newScope.value, newBucket.value, rule)
    newRule.value = ''
    actionOk.value = `已写入 ${newBucket.value} 规则：${rule}`
    await permissionStore.loadAudit(sessionId.value)
  } catch (err) {
    actionError.value = `写入失败：${err.message || err}`
  } finally {
    busy.value = false
  }
}

async function dropRule(r) {
  const scope = scopeOfSource(r.source)
  busy.value = true
  actionError.value = ''
  actionOk.value = ''
  try {
    await permissionStore.removeRule(sessionId.value, scope, r.bucket, r.rule)
    actionOk.value = `已移除规则：${r.rule}`
  } catch (err) {
    actionError.value = `移除失败：${err.message || err}`
  } finally {
    busy.value = false
  }
}

async function dropGrants() {
  busy.value = true
  actionError.value = ''
  actionOk.value = ''
  try {
    await permissionStore.clearGrants(sessionId.value)
    actionOk.value = '已清空本会话的放行记录'
  } catch (err) {
    actionError.value = `清空失败：${err.message || err}`
  } finally {
    busy.value = false
  }
}

function formatTime(ts) {
  if (!ts) return ''
  const d = new Date(ts)
  const pad = (n) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}
</script>

<template>
  <div class="permission-settings">
    <div v-if="permissionStore.loading" class="hint">加载中…</div>

    <template v-else>
      <!-- 提示与错误 -->
      <div v-for="(w, i) in permissionStore.warnings" :key="`w${i}`" class="banner banner-warn">
        {{ w }}
      </div>
      <div v-if="actionError" class="banner banner-error">{{ actionError }}</div>
      <div v-if="actionOk" class="banner banner-ok">{{ actionOk }}</div>

      <!-- 会话模式 -->
      <div class="form-group">
        <label>当前会话的权限模式</label>
        <select :value="permissionStore.mode" class="input" style="max-width: 420px" :disabled="busy" @change="changeMode">
          <option v-for="p in PERMISSION_MODES" :key="p.value" :value="p.value">
            {{ p.label }} — {{ p.desc }}
          </option>
        </select>
        <div class="hint">
          权限模式随会话保存。工作目录：<span class="mono">{{ permissionStore.state?.projectDir || '未设置' }}</span>
        </div>
      </div>

      <!-- 新增规则 -->
      <div class="card">
        <div class="card-title">新增规则</div>
        <div class="rule-form">
          <select v-model="newBucket" class="input" style="max-width: 220px">
            <option v-for="b in BUCKETS" :key="b.value" :value="b.value">{{ b.label }}</option>
          </select>
          <input v-model="newRule" class="input mono" placeholder="如 exec_shell(git status) 或 exec_shell(npm test:*)" />
          <select v-model="newScope" class="input" style="max-width: 240px">
            <option v-for="s in SCOPES" :key="s.value" :value="s.value">{{ s.label }}</option>
          </select>
          <button class="btn btn-primary btn-sm" :disabled="busy || !newRule.trim()" @click="submitRule">
            添加
          </button>
        </div>
        <div class="hint">
          语法：<code>Tool</code> 工具级、<code>Tool(命令段)</code> 精确、<code>Tool(命令段:*)</code> 前缀。
          前缀规则只在逐段判定里生效；放行类规则要求复合命令的<strong>每一段都被覆盖</strong>。
        </div>
      </div>

      <!-- 生效规则 -->
      <div class="card">
        <div class="card-title">生效规则（{{ permissionStore.rules.length }}）</div>
        <div v-if="permissionStore.rules.length === 0" class="hint">暂无规则，判定完全由模式与内置名单决定。</div>
        <div v-else class="rule-list">
          <div v-for="(r, i) in permissionStore.rules" :key="`r${i}`" class="rule-row">
            <span class="tag" :class="`tag-${r.bucket}`">{{ r.bucket }}</span>
            <span class="mono rule-text">{{ r.rule }}</span>
            <span class="rule-source" :title="r.source">{{ r.source }}</span>
            <button class="icon-btn" :disabled="busy" title="移除该规则" @click="dropRule(r)">✕</button>
          </div>
        </div>
      </div>

      <!-- 配置层 -->
      <div class="card">
        <div class="card-title">规则来源（三层合并）</div>
        <div v-for="s in permissionStore.sources" :key="s.scope" class="source-row">
          <span class="tag tag-plain">{{ s.scope }}</span>
          <span class="mono source-path">{{ s.path }}</span>
          <span class="source-counts">
            {{ s.exist ? `deny ${s.deny} · ask ${s.ask} · allow ${s.allow}` : '不存在' }}
          </span>
        </div>
      </div>

      <!-- 会话授权 -->
      <div class="card">
        <div class="card-title">
          本会话授权（{{ permissionStore.grants.length }}）
          <button
            v-if="permissionStore.grants.length > 0"
            class="btn btn-ghost btn-sm"
            :disabled="busy"
            @click="dropGrants"
          >
            清空
          </button>
        </div>
        <div v-if="permissionStore.grants.length === 0" class="hint">
          还没有「本会话允许」记录。授权只存在内存里，换会话或重启即失效。
        </div>
        <div v-else class="rule-list">
          <div v-for="(g, i) in permissionStore.grants" :key="`g${i}`" class="rule-row">
            <span class="tag tag-allow">session</span>
            <span class="mono rule-text">{{ g.tool }}{{ g.spec ? `(${g.spec})` : '' }}</span>
          </div>
        </div>
      </div>

      <!-- 审计 -->
      <div class="card">
        <div class="card-title">
          判定审计（最近 {{ permissionStore.audit.length }} 条）
          <button class="btn btn-ghost btn-sm" :disabled="busy" @click="refresh">刷新</button>
        </div>
        <div v-if="permissionStore.audit.length === 0" class="hint">还没有判定记录。</div>
        <div v-else class="audit-list">
          <div v-for="(a, i) in permissionStore.audit" :key="`a${i}`" class="audit-row">
            <span class="audit-time">{{ formatTime(a.time) }}</span>
            <span class="tag" :class="`tag-${a.decision}`">{{ a.decision }}</span>
            <span class="audit-stage mono">{{ a.stage }}</span>
            <span class="audit-subject mono" :title="a.subject">{{ a.subject }}</span>
            <span v-if="a.rule" class="audit-rule mono" :title="a.rule">{{ a.rule }}</span>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped lang="scss">
.permission-settings {
  display: flex;
  flex-direction: column;
  gap: $space-md;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.banner {
  padding: 8px 12px;
  border-radius: $radius-sm;
  font-size: $font-size-xs;
  border: 1px solid $color-border;
}

.banner-warn {
  color: $color-warning;
  border-color: rgb(var(--color-warning-rgb) / 0.4);
  background-color: rgb(var(--color-warning-rgb) / 0.06);
}

.banner-error {
  color: $color-error;
  border-color: rgb(var(--color-error-rgb) / 0.4);
  background-color: rgb(var(--color-error-rgb) / 0.06);
}

.banner-ok {
  color: $color-success;
  border-color: rgb(var(--color-success-rgb) / 0.35);
  background-color: rgb(var(--color-success-rgb) / 0.06);
}

.card {
  border: 1px solid $color-border;
  border-radius: $radius-md;
  padding: 12px;
  background-color: rgb(var(--color-bg-tertiary-rgb) / 0.18);
}

.card-title {
  display: flex;
  align-items: center;
  gap: $space-sm;
  margin-bottom: 8px;
  font-size: $font-size-xs;
  font-weight: $font-weight-semibold;
  color: $color-text-primary;

  .btn {
    margin-left: auto;
  }
}

.rule-form {
  display: flex;
  flex-wrap: wrap;
  gap: $space-sm;
  align-items: center;

  .input {
    flex: 1;
    min-width: 160px;
  }
}

.rule-list,
.audit-list {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.rule-row,
.source-row,
.audit-row {
  display: flex;
  align-items: center;
  gap: $space-sm;
  font-size: $font-size-xs;
  color: $color-text-secondary;
}

.rule-text {
  color: $color-text-primary;
  word-break: break-all;
}

.rule-source,
.source-path {
  color: $color-text-muted;
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rule-row .rule-source {
  margin-left: auto;
  max-width: 45%;
}

.source-counts {
  margin-left: auto;
  color: $color-text-muted;
  font-size: 11px;
}

.tag {
  flex-shrink: 0;
  padding: 1px 8px;
  border-radius: 10px;
  font-size: 10px;
  font-weight: $font-weight-semibold;
  background-color: rgb(var(--color-bg-tertiary-rgb) / 0.85);
  color: $color-text-secondary;
}

.tag-deny {
  background-color: rgb(var(--color-error-rgb) / 0.2);
  color: $color-error;
}

.tag-ask {
  background-color: rgb(var(--color-warning-rgb) / 0.2);
  color: $color-warning;
}

.tag-allow {
  background-color: rgb(var(--color-success-rgb) / 0.16);
  color: $color-success;
}

.tag-plain {
  background-color: rgb(var(--color-text-muted-rgb) / 0.3);
}

.audit-time {
  width: 62px;
  flex-shrink: 0;
  color: $color-text-muted;
  font-size: 11px;
  font-family: monospace;
}

.audit-stage {
  flex-shrink: 0;
  font-size: 11px;
  color: $color-text-muted;
}

.audit-subject {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.audit-rule {
  margin-left: auto;
  flex-shrink: 0;
  max-width: 32%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: $color-text-muted;
  font-size: 11px;
}

.icon-btn {
  flex-shrink: 0;
  width: 20px;
  height: 20px;
  border-radius: $radius-sm;
  color: $color-text-muted;

  &:hover:not(:disabled) {
    color: $color-error;
    background-color: rgb(var(--color-error-rgb) / 0.12);
  }
}
</style>
