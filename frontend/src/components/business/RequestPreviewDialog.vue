<script setup>
import { ref, computed, watch } from 'vue'
import { X, Loader2, Copy, ChevronDown, ChevronRight, Info } from 'lucide-vue-next'
import { getLastLLMRequest } from '@/api/session'
import { useUiStore } from '@/stores/ui'

const props = defineProps({
  visible: Boolean,
  sessionId: { type: String, default: '' },
})
const emit = defineEmits(['close'])

const ui = useUiStore()

const snap = ref(null)
const loading = ref(false)
const errorMsg = ref('')
const showSystem = ref(false)
const expanded = ref({})

/** 单条消息默认展示的字符数；超出的靠"展开"看全 */
const PREVIEW_CHARS = 400

watch(
  () => props.visible,
  async (v) => {
    if (!v) return
    errorMsg.value = ''
    expanded.value = {}
    showSystem.value = false
    snap.value = null
    if (!props.sessionId) return
    loading.value = true
    try {
      snap.value = await getLastLLMRequest(props.sessionId)
    } catch (e) {
      errorMsg.value = `读取失败：${e.message || e}`
    } finally {
      loading.value = false
    }
  }
)

const usageText = computed(() => {
  const s = snap.value
  if (!s) return ''
  const pct = s.windowTokens ? Math.round((s.estimatedTokens / s.windowTokens) * 100) : 0
  return `≈${s.estimatedTokens} / ${s.windowTokens} token（${pct}%）`
})

function roleLabel(role) {
  switch (role) {
    case 'system':
      return '系统'
    case 'user':
      return '用户'
    case 'assistant':
      return '助手'
    case 'tool':
      return '工具'
    default:
      return role
  }
}

function preview(content) {
  const s = content || ''
  return s.length > PREVIEW_CHARS ? s.slice(0, PREVIEW_CHARS) + `\n…（共 ${s.length} 字符）` : s
}

function toggle(i) {
  expanded.value = { ...expanded.value, [i]: !expanded.value[i] }
}

async function copyJSON() {
  if (!snap.value) return
  try {
    await navigator.clipboard.writeText(JSON.stringify(snap.value, null, 2))
    ui.notify('已复制请求快照 JSON', 'success')
  } catch (e) {
    ui.notify(`复制失败：${e.message || e}`, 'error')
  }
}
</script>

<template>
  <div v-if="visible" class="dialog-mask" @click.self="emit('close')">
    <div class="dialog">
      <div class="dialog-header">
        <h3>实际发给模型的请求</h3>
        <div class="header-actions">
          <button v-if="snap" class="icon-btn" title="复制完整 JSON" @click="copyJSON">
            <Copy :size="15" />
          </button>
          <button class="icon-btn" @click="emit('close')"><X :size="16" /></button>
        </div>
      </div>

      <div class="dialog-body">
        <div v-if="loading" class="tip">
          <Loader2 :size="13" class="spin" /> 读取中…
        </div>
        <div v-else-if="errorMsg" class="error-banner">{{ errorMsg }}</div>
        <div v-else-if="!snap" class="tip">
          本会话还没有发起过请求。<br />
          快照只保留在内存里，应用重启后需要再发一条消息才会重新记录。
        </div>

        <template v-else>
          <div class="meta-grid">
            <div><span class="k">轮次</span><span class="v">{{ snap.turn + 1 }}</span></div>
            <div><span class="k">模型</span><span class="v mono">{{ snap.model }}</span></div>
            <div><span class="k">用量</span><span class="v">{{ usageText }}</span></div>
            <div>
              <span class="k">消息数</span>
              <span class="v">{{ snap.messages.length }} 条</span>
            </div>
          </div>

          <div v-if="snap.coveredMsgs > 0 || snap.compactedThisTurn" class="notice">
            <Info :size="12" />
            <span v-if="snap.compactedThisTurn">本轮刚做过摘要压缩；</span>
            <span>摘要覆盖前 {{ snap.coveredMsgs }} 条消息（在下方标为「摘要」那条）</span>
          </div>

          <div class="section">
            <button class="section-head" @click="showSystem = !showSystem">
              <ChevronDown v-if="showSystem" :size="13" />
              <ChevronRight v-else :size="13" />
              系统提示（{{ snap.systemPrompt.length }} 字符）
            </button>
            <pre v-if="showSystem" class="block">{{ snap.systemPrompt }}</pre>
          </div>

          <div class="section">
            <div class="section-head static">
              工具定义（{{ snap.toolNames.length }} 个，估算 {{ snap.toolSchemaTokens }} token）
            </div>
            <div class="tool-names">
              <span v-for="n in snap.toolNames" :key="n" class="tag">{{ n }}</span>
              <span v-if="!snap.toolNames.length" class="muted">（本会话没有可用工具）</span>
            </div>
          </div>

          <div class="section">
            <div class="section-head static">消息序列（{{ snap.messages.length }} 条）</div>
            <div class="msg-list">
              <div
                v-for="(m, i) in snap.messages"
                :key="i"
                class="msg"
                :class="{ summary: i === snap.summaryIndex }"
              >
                <div class="msg-head" @click="toggle(i)">
                  <span class="idx mono">#{{ i }}</span>
                  <span class="role" :class="'role-' + m.role">{{ roleLabel(m.role) }}</span>
                  <span v-if="i === snap.summaryIndex" class="tag tag-summary">摘要（替代原文）</span>
                  <span v-if="m.tool_calls?.length" class="tag">{{ m.tool_calls.length }} 个调用</span>
                  <span v-if="m.tool_call_id" class="tag mono">↩ {{ m.tool_call_id }}</span>
                  <span class="size">{{ m.content.length }} 字符</span>
                  <ChevronDown v-if="expanded[i]" :size="12" class="expand" />
                  <ChevronRight v-else :size="12" class="expand" />
                </div>
                <pre class="block msg-body">{{ expanded[i] ? m.content : preview(m.content) }}</pre>
              </div>
            </div>
          </div>

          <div class="footnote">
            这是**内部统一格式**（OpenAI 形状）的序列，也就是压缩与预算之后实际交给模型的那一份。
            系统提示与工具定义不计入 messages，但它们是每次请求的固定开销，已在上方单独列出。
          </div>
        </template>
      </div>

      <div class="dialog-footer">
        <button class="btn btn-ghost" @click="emit('close')">关闭</button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.dialog-mask {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  padding: $space-lg;
}

.dialog {
  background: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  width: 860px;
  max-width: 100%;
  max-height: 88vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.5);
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
    color: $color-text-primary;
  }
}

.header-actions {
  display: flex;
  gap: 2px;
}

.dialog-body {
  padding: $space-lg;
  overflow-y: auto;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  padding: $space-md $space-lg;
  border-top: 1px solid $color-border;
}

.tip {
  display: flex;
  align-items: center;
  gap: 6px;
  color: $color-text-muted;
  font-size: $font-size-sm;
  line-height: 1.8;
  padding: $space-lg 0;
}

.meta-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 4px 16px;
  margin-bottom: $space-md;

  > div {
    display: flex;
    gap: 8px;
    font-size: $font-size-xs;
  }

  .k {
    color: $color-text-muted;
    min-width: 44px;
  }

  .v {
    color: $color-text-primary;
  }
}

.notice {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: $font-size-xs;
  color: #c084fc;
  background: rgba(124, 58, 237, 0.1);
  border: 1px solid rgba(124, 58, 237, 0.3);
  border-radius: $radius-sm;
  padding: 6px 10px;
  margin-bottom: $space-md;
}

.section {
  margin-bottom: $space-md;
}

.section-head {
  display: flex;
  align-items: center;
  gap: 5px;
  width: 100%;
  background: transparent;
  border: none;
  padding: 4px 0;
  font-size: $font-size-sm;
  font-weight: $font-weight-medium;
  color: $color-text-primary;
  cursor: pointer;

  &.static {
    cursor: default;
  }
}

.tool-names {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 4px;
}

.muted {
  color: $color-text-muted;
  font-size: $font-size-xs;
}

.msg-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 4px;
}

.msg {
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  background: $color-bg-primary;

  &.summary {
    border-color: rgba(124, 58, 237, 0.5);
  }
}

.msg-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 8px;
  cursor: pointer;
  font-size: $font-size-xs;
}

.idx {
  color: $color-text-muted;
}

.role {
  font-weight: $font-weight-medium;

  &.role-system {
    color: #94a3b8;
  }
  &.role-user {
    color: #38bdf8;
  }
  &.role-assistant {
    color: #a78bfa;
  }
  &.role-tool {
    color: #22c55e;
  }
}

.size {
  margin-left: auto;
  color: $color-text-muted;
}

.expand {
  color: $color-text-muted;
}

.tag {
  display: inline-flex;
  align-items: center;
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 8px;
  background: rgba(148, 163, 184, 0.15);
  color: #94a3b8;

  &.tag-summary {
    background: rgba(124, 58, 237, 0.18);
    color: #c084fc;
  }
}

.block {
  margin: 0;
  padding: 0 $space-md $space-sm;
  font-family: monospace;
  font-size: $font-size-xs;
  line-height: 1.6;
  color: $color-text-secondary;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 320px;
  overflow-y: auto;
}

.msg-body {
  padding-top: 4px;
}

.footnote {
  margin-top: $space-md;
  padding-top: $space-sm;
  border-top: 1px solid $color-border;
  font-size: $font-size-xs;
  color: $color-text-muted;
  line-height: 1.7;
}

.error-banner {
  padding: $space-sm $space-md;
  margin-bottom: $space-md;
  background-color: rgba(255, 85, 85, 0.12);
  border: 1px solid rgba(255, 85, 85, 0.4);
  border-radius: $radius-sm;
  color: $color-error;
  font-size: $font-size-xs;
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

  &:hover {
    background: $color-bg-tertiary;
    color: $color-text-primary;
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
