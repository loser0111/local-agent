<script setup>
import { computed, ref, watch } from 'vue'
import { usePermissionStore } from '@/stores/permissions'

/**
 * 授权弹窗
 *
 * 后端在工具循环里阻塞等待答复：弹窗关闭/超时/取消一律按拒绝处理，
 * 因此这里必须给出明确的三个选项（拒绝 / 允许一次 / 本会话允许），
 * 以及「写规则」这一持久化路径。
 */
const permissionStore = usePermissionStore()

const req = computed(() => permissionStore.pending)
const visible = computed(() => !!req.value)

const ruleText = ref('')
const layer = ref('local')
const showAdvanced = ref(false)

/** 复合命令的各段（>1 段时提示用户这是复合命令，避免只看整行） */
const units = computed(() => (Array.isArray(req.value?.units) ? req.value.units : []))

/** 预填一条建议规则：取第一段做精确匹配（不默认生成前缀规则，避免过度放宽） */
function suggestRule(r) {
  if (!r) return ''
  const first = (Array.isArray(r.units) && r.units[0]) || r.subject || ''
  const spec = String(first).trim().slice(0, 120)
  return spec ? `${r.tool}(${spec})` : r.tool
}

watch(req, (r) => {
  if (!r) return
  showAdvanced.value = false
  layer.value = 'local'
  ruleText.value = suggestRule(r)
})

async function deny() {
  await permissionStore.answer({ decision: 'deny', scope: 'once' })
}

async function allowOnce() {
  await permissionStore.answer({ decision: 'allow', scope: 'once' })
}

async function allowSession() {
  await permissionStore.answer({ decision: 'allow', scope: 'session' })
}

async function allowWithRule() {
  const rule = ruleText.value.trim()
  if (!rule) {
    showAdvanced.value = true
    return
  }
  await permissionStore.answer({ decision: 'allow', scope: 'rule', rule, layer: layer.value })
}
</script>

<template>
  <div v-if="visible" class="permission-overlay">
    <div class="permission-dialog" role="dialog" aria-modal="true">
      <div class="dialog-header">
        <span class="lock-icon">🔒</span>
        <h3 class="dialog-title">需要你的确认</h3>
        <span class="dialog-stage">{{ req.stage }}</span>
      </div>

      <div class="dialog-body">
        <p class="reason">{{ req.reason }}</p>

        <div class="field">
          <span class="field-label">工具</span>
          <span class="field-value mono">{{ req.tool }}</span>
        </div>

        <div class="field field-block">
          <span class="field-label">待执行内容</span>
          <pre class="subject mono">{{ req.subject }}</pre>
        </div>

        <div v-if="units.length > 1" class="units">
          <div class="units-hint">
            这是一条复合命令，共 {{ units.length }} 段；下方每段都会被单独判定，
            授权需要每一段都被覆盖才会放行。
          </div>
          <ol class="units-list">
            <li v-for="(u, i) in units" :key="i" class="mono">{{ u }}</li>
          </ol>
        </div>

        <div class="waiting-hint">
          后端正在等待你的答复。超时、取消或没有应答都会按<strong>拒绝</strong>处理。
        </div>

        <div v-if="showAdvanced" class="advanced">
          <div class="field">
            <span class="field-label">规则</span>
            <input v-model="ruleText" class="input mono" placeholder="如 exec_shell(git status)" />
          </div>
          <div class="field">
            <span class="field-label">写入层</span>
            <select v-model="layer" class="input">
              <option value="local">项目本地（permissions.local.json，建议 gitignore）</option>
              <option value="project">项目级（permissions.json，随仓库共享）</option>
              <option value="user">用户全局（~/.local-agent/permissions.json）</option>
            </select>
          </div>
          <div class="advanced-hint">
            写成 <code>Tool(命令段:*)</code> 是前缀规则（只在逐段判定里生效），
            <code>Tool(命令段)</code> 是精确匹配。
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn btn-danger" :disabled="permissionStore.answering" @click="deny">
          拒绝
        </button>
        <div class="spacer"></div>
        <button class="btn btn-ghost" :disabled="permissionStore.answering" @click="showAdvanced = !showAdvanced">
          {{ showAdvanced ? '收起规则' : '写成规则' }}
        </button>
        <button class="btn btn-ghost" :disabled="permissionStore.answering" @click="allowOnce">
          允许一次
        </button>
        <button class="btn btn-ghost" :disabled="permissionStore.answering" @click="allowSession">
          本会话允许
        </button>
        <button
          v-if="showAdvanced"
          class="btn btn-primary"
          :disabled="permissionStore.answering || !ruleText.trim()"
          @click="allowWithRule"
        >
          允许并写入
        </button>
      </div>

      <div v-if="permissionStore.error" class="dialog-error">{{ permissionStore.error }}</div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.permission-overlay {
  position: fixed;
  inset: 0;
  z-index: 200;
  display: flex;
  align-items: center;
  justify-content: center;
  background-color: $color-overlay;
}

.permission-dialog {
  width: min(680px, 92vw);
  max-height: 86vh;
  display: flex;
  flex-direction: column;
  background-color: $color-bg-secondary;
  border: 1px solid rgb(var(--color-error-rgb) / 0.35);
  border-radius: $radius-md;
  box-shadow: $shadow-lg;
  overflow: hidden;
}

.dialog-header {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: 12px 16px;
  border-bottom: 1px solid $color-border;

  .lock-icon {
    font-size: 15px;
  }

  .dialog-title {
    font-size: $font-size-sm;
    font-weight: $font-weight-semibold;
    color: $color-text-primary;
    margin: 0;
  }

  .dialog-stage {
    margin-left: auto;
    font-size: 11px;
    color: $color-text-muted;
    font-family: monospace;
  }
}

.dialog-body {
  padding: 14px 16px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.reason {
  margin: 0;
  font-size: $font-size-xs;
  color: $color-text-secondary;
}

.field {
  display: flex;
  align-items: center;
  gap: $space-sm;

  .field-label {
    flex-shrink: 0;
    width: 76px;
    font-size: $font-size-xs;
    color: $color-text-muted;
  }

  .field-value {
    font-size: $font-size-xs;
    color: $color-text-primary;
    word-break: break-all;
  }

  .input {
    flex: 1;
  }
}

.field-block {
  display: block;

  .field-label {
    display: block;
    margin-bottom: 4px;
  }
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.subject {
  margin: 0;
  padding: 8px 10px;
  background-color: rgb(var(--color-bg-tertiary-rgb) / 0.5);
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  font-size: 12px;
  color: $color-text-primary;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 140px;
  overflow-y: auto;
}

.units {
  border: 1px solid rgb(var(--color-warning-rgb) / 0.35);
  border-radius: $radius-sm;
  padding: 8px 10px;
  background-color: rgb(var(--color-warning-rgb) / 0.06);
}

.units-hint {
  font-size: 11px;
  color: $color-warning;
  margin-bottom: 6px;
}

.units-list {
  margin: 0;
  padding-left: 18px;
  font-size: 11px;
  color: $color-text-secondary;

  li {
    word-break: break-all;
  }
}

.waiting-hint {
  font-size: 11px;
  color: $color-text-muted;

  strong {
    color: $color-error;
  }
}

.advanced {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 8px;
  border-top: 1px dashed $color-border;
}

.advanced-hint {
  font-size: 11px;
  color: $color-text-muted;
  line-height: 1.6;

  code {
    padding: 0 4px;
    background-color: rgb(var(--color-bg-tertiary-rgb) / 0.6);
    border-radius: 3px;
  }
}

.dialog-footer {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: 12px 16px;
  border-top: 1px solid $color-border;

  .spacer {
    flex: 1;
  }
}

.dialog-error {
  padding: 8px 16px;
  font-size: 11px;
  color: $color-error;
  border-top: 1px solid $color-border;
}
</style>
