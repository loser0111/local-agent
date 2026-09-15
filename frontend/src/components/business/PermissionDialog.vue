<script setup>
import { ref, computed, watch } from 'vue'
import { usePermissionsStore } from '@/stores/permissions'

const permStore = usePermissionsStore()

const req = computed(() => permStore.current)

// 「永久允许」的展开态与可编辑规则
const showAlways = ref(false)
const editedRule = ref('')
const expanded = ref(false)

// 每次切换到新请求时重置本地状态
watch(
  () => req.value?.requestId,
  () => {
    showAlways.value = false
    expanded.value = false
    editedRule.value = req.value?.suggestRule || ''
  },
  { immediate: true }
)

const RISK_LABEL = {
  read: '只读',
  write: '修改本机状态',
  network: '网络访问',
  process: '启动进程',
}

const riskLabel = computed(() => RISK_LABEL[req.value?.risk] || req.value?.risk || '未知')

// 与 variables.scss 的 Dracula 配色保持一致（此处用于内联 style，故用字面量）
const riskColor = computed(() => {
  switch (req.value?.risk) {
    case 'read':
      return '#50fa7b'
    case 'network':
      return '#8be9fd'
    case 'process':
      return '#f1fa8c'
    default:
      return '#ffb86c'
  }
})

/** 命令有多段（复合命令被分解）时逐段展示 */
const segments = computed(() => {
  const argv = req.value?.argv || []
  if (argv.length <= 1) return []
  return argv
})

async function deny() {
  await permStore.respond('deny', 'once')
}

async function allowOnce() {
  await permStore.respond('allow', 'once')
}

async function allowSession() {
  await permStore.respond('allow', 'session')
}

/** 永久允许：先展开规则确认区，再二次确认落盘 */
function openAlways() {
  editedRule.value = req.value?.suggestRule || ''
  showAlways.value = true
}

async function confirmAlways() {
  await permStore.respond('allow', 'always', editedRule.value)
}
</script>

<template>
  <div v-if="req" class="perm-mask">
    <div class="perm-dialog">
      <div class="perm-header">
        <div class="perm-title">
          <span class="dot" :style="{ backgroundColor: riskColor }"></span>
          <h3>需要授权</h3>
        </div>
        <span class="risk-badge">{{ riskLabel }}</span>
      </div>

      <div class="perm-body">
        <!-- 工具与操作摘要 -->
        <div class="field">
          <span class="field-label">工具</span>
          <span class="field-value">
            {{ req.toolLabel || req.toolName }}
            <span class="tool-raw">({{ req.toolName }})</span>
          </span>
        </div>

        <div class="field">
          <span class="field-label">操作</span>
          <span class="field-value">{{ req.summary }}</span>
        </div>

        <!-- 命令 / 域名 -->
        <div v-if="req.command" class="field field-block">
          <span class="field-label">命令</span>
          <pre class="code-block">{{ req.command }}</pre>
        </div>

        <!-- 复合命令：逐段列出，让用户看清每一段 -->
        <div v-if="segments.length" class="field field-block">
          <span class="field-label">拆解（逐段校验）</span>
          <div class="segments">
            <code v-for="(s, i) in segments" :key="i">{{ s }}</code>
          </div>
        </div>

        <!-- 为什么需要确认 -->
        <div class="reason">
          {{ req.reason }}
        </div>

        <!-- ask 规则提示：避免「我明明点了永久允许怎么还问」被当成 bug -->
        <div v-if="req.matchedRule" class="rule-notice">
          本次询问由 <b>ask</b> 规则触发：<code>{{ req.matchedRule }}</code>
          <span v-if="req.ruleSource" class="rule-source">（来源：{{ req.ruleSource }}）</span>
          <div class="rule-hint">
            ask 规则优先于历史授权。若希望不再询问，请到「设置 - 权限配置」删除该规则。
          </div>
        </div>

        <!-- 展开完整参数 -->
        <button class="expand-btn" @click="expanded = !expanded">
          {{ expanded ? '收起完整参数' : '查看完整参数' }}
        </button>
        <pre v-if="expanded" class="code-block raw">{{ JSON.stringify(req.raw, null, 2) }}</pre>

        <!-- 永久允许的规则确认区 -->
        <div v-if="showAlways" class="always-panel">
          <label>将写入的规则</label>
          <input v-model="editedRule" class="input mono" spellcheck="false" />
          <div class="always-hint">
            可自行放宽，例如把 <code>exec_shell(git status)</code> 改成
            <code>exec_shell(git:*)</code> 以允许整类命令。
          </div>
          <div class="always-hint">
            写入「项目本地层」<code>.local-agent/settings.local.json</code>；无项目目录时写入用户全局层。
          </div>
        </div>
      </div>

      <div class="perm-footer">
        <button class="btn btn-danger" :disabled="permStore.submitting" @click="deny">
          拒绝
        </button>
        <div class="spacer"></div>

        <template v-if="!showAlways">
          <button class="btn" :disabled="permStore.submitting" @click="allowOnce">
            仅本次允许
          </button>
          <button class="btn" :disabled="permStore.submitting" @click="allowSession">
            本会话允许
          </button>
          <button class="btn btn-warn" :disabled="permStore.submitting" @click="openAlways">
            永久允许…
          </button>
        </template>
        <template v-else>
          <button class="btn" :disabled="permStore.submitting" @click="showAlways = false">
            返回
          </button>
          <button
            class="btn btn-warn"
            :disabled="permStore.submitting || !editedRule.trim()"
            @click="confirmAlways"
          >
            确认写入并允许
          </button>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.perm-mask {
  position: fixed;
  inset: 0;
  background-color: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1100;
}

.perm-dialog {
  width: 560px;
  max-width: 92vw;
  max-height: 88vh;
  display: flex;
  flex-direction: column;
  background-color: $color-bg-secondary;
  border: 1px solid $color-border-light;
  border-radius: $radius-lg;
  box-shadow: $shadow-lg;
  overflow: hidden;
}

.perm-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: $space-lg $space-xl;
  border-bottom: 1px solid $color-border;
}

.perm-title {
  display: flex;
  align-items: center;
  gap: $space-sm;

  h3 {
    font-size: $font-size-lg;
    font-weight: $font-weight-semibold;
    color: $color-text-primary;
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }
}

.risk-badge {
  padding: 2px $space-sm;
  font-size: $font-size-xs;
  color: $color-warning;
  border: 1px solid rgba(255, 184, 108, 0.5);
  border-radius: $radius-sm;
}

.perm-body {
  padding: $space-xl;
  overflow-y: auto;
  flex: 1;
}

.field {
  display: flex;
  gap: $space-md;
  margin-bottom: $space-md;
  font-size: $font-size-sm;

  &.field-block {
    flex-direction: column;
    gap: $space-xs;
  }
}

.field-label {
  width: 96px;
  flex-shrink: 0;
  color: $color-text-muted;
}

.field-value {
  color: $color-text-primary;
  word-break: break-all;
}

.tool-raw {
  color: $color-text-muted;
  font-size: $font-size-xs;
}

.code-block {
  margin: 0;
  padding: $space-sm $space-md;
  background-color: $color-bg-primary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  font-family: $font-family-mono;
  font-size: $font-size-xs;
  color: $color-info;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 180px;
  overflow-y: auto;

  &.raw {
    margin-top: $space-sm;
    color: $color-text-secondary;
  }
}

.segments {
  display: flex;
  flex-direction: column;
  gap: $space-xs;

  code {
    padding: 2px $space-sm;
    background-color: $color-bg-primary;
    border-left: 2px solid $color-warning;
    font-family: $font-family-mono;
    font-size: $font-size-xs;
    color: $color-text-primary;
    word-break: break-all;
  }
}

.reason {
  margin: $space-md 0;
  padding: $space-sm $space-md;
  background-color: rgba(255, 184, 108, 0.08);
  border-left: 3px solid $color-warning;
  border-radius: $radius-sm;
  font-size: $font-size-sm;
  color: $color-text-secondary;
}

.rule-notice {
  margin-bottom: $space-md;
  padding: $space-sm $space-md;
  background-color: rgba(139, 233, 253, 0.08);
  border-left: 3px solid $color-info;
  border-radius: $radius-sm;
  font-size: $font-size-xs;
  color: $color-text-secondary;

  code {
    font-family: $font-family-mono;
    color: $color-info;
  }

  .rule-source {
    color: $color-text-muted;
  }

  .rule-hint {
    margin-top: $space-xs;
    color: $color-text-muted;
  }
}

.expand-btn {
  margin: $space-sm 0;
  font-size: $font-size-xs;
  color: $color-text-muted;

  &:hover {
    color: $color-primary;
  }
}

.always-panel {
  margin-top: $space-md;
  padding: $space-md;
  background-color: $color-bg-primary;
  border: 1px solid $color-border-light;
  border-radius: $radius-sm;

  > label {
    display: block;
    margin-bottom: $space-xs;
    font-size: $font-size-xs;
    color: $color-text-muted;
  }
}

.input {
  width: 100%;
  padding: $space-sm $space-md;
  background-color: $color-bg-secondary;
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

.always-hint {
  margin-top: $space-xs;
  font-size: $font-size-xs;
  color: $color-text-muted;

  code {
    font-family: $font-family-mono;
    color: $color-text-secondary;
  }
}

.perm-footer {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-lg $space-xl;
  border-top: 1px solid $color-border;
}

.spacer {
  flex: 1;
}

.btn {
  padding: $space-sm $space-lg;
  font-size: $font-size-sm;
  border-radius: $radius-sm;
  border: 1px solid $color-border;
  color: $color-text-primary;
  background-color: $color-bg-tertiary;
  transition: all $transition-fast;

  &:not(:disabled):hover {
    border-color: $color-border-light;
  }

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  &.btn-danger {
    background-color: rgba(255, 85, 85, 0.15);
    border-color: rgba(255, 85, 85, 0.4);
    color: $color-error;
  }

  &.btn-warn {
    background-color: rgba(255, 184, 108, 0.15);
    border-color: rgba(255, 184, 108, 0.4);
    color: $color-warning;
  }
}
</style>
