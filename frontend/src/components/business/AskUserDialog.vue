<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import { useAskStore } from '@/stores/asks'
import { useSessionStore } from '@/stores/session'

/**
 * 模型提问弹窗（ask_user 工具）
 *
 * 后端在工具调用里阻塞等待；这里收集答复并回传，答复随后作为工具结果回到模型手里。
 * 支持：单选/多选选项、自由文本补充、跳过（= 用户未作答，模型会自行决策并说明假设）。
 */
const askStore = useAskStore()
const sessionStore = useSessionStore()

const req = computed(() => askStore.pending)
const visible = computed(() => !!req.value && !!req.value.id)
const questions = computed(() => req.value?.questions || [])
const submitting = computed(() => askStore.submitting)

const textRefs = ref([])

// 单选且只有一个问题时，点选项即提交（少一次点击）；问题多于一个时逐个作答
const quickSubmit = computed(() => questions.value.length === 1 && !questions.value[0]?.multiSelect)

/** 第 i 个问题是否已选该选项 */
function isSelected(qIndex, label) {
  return (askStore.answers[qIndex]?.selected || []).includes(label)
}

function pick(qIndex, label, multiSelect) {
  askStore.toggleOption(qIndex, label, multiSelect)
  if (quickSubmit.value && askStore.isAnswered(0)) {
    askStore.submit()
  }
}

function onText(qIndex, value) {
  askStore.setText(qIndex, value)
}

function onTextEnter() {
  if (canSubmit.value) askStore.submit()
}

const canSubmit = computed(() => !submitting.value && questions.value.some((_, i) => askStore.isAnswered(i)))

const answeredCount = computed(() => questions.value.filter((_, i) => askStore.isAnswered(i)).length)

async function skip() {
  await askStore.skip(sessionStore.currentSessionId)
}

watch(visible, async (v) => {
  if (!v) return
  textRefs.value = []
  await nextTick()
})
</script>

<template>
  <div v-if="visible" class="ask-overlay">
    <div class="ask-dialog" role="dialog" aria-modal="true">
      <div class="dialog-header">
        <span class="ask-icon">💬</span>
        <h3 class="dialog-title">模型需要你的确认</h3>
        <span class="dialog-count">
          {{ answeredCount }}/{{ questions.length }} 已作答
        </span>
      </div>

      <div class="dialog-body">
        <div v-for="(q, qi) in questions" :key="qi" class="question">
          <div class="question-head">
            <span v-if="q.header" class="question-header">{{ q.header }}</span>
            <span v-if="q.multiSelect" class="question-multi">可多选</span>
          </div>
          <p class="question-text">{{ q.question }}</p>

          <div v-if="q.options && q.options.length" class="options">
            <button
              v-for="opt in q.options"
              :key="opt.label"
              type="button"
              class="option"
              :class="{ active: isSelected(qi, opt.label) }"
              :disabled="submitting"
              @click="pick(qi, opt.label, q.multiSelect)"
            >
              <span class="option-mark">{{ isSelected(qi, opt.label) ? (q.multiSelect ? '☑' : '◉') : (q.multiSelect ? '☐' : '○') }}</span>
              <span class="option-body">
                <span class="option-label">{{ opt.label }}</span>
                <span v-if="opt.description" class="option-desc">{{ opt.description }}</span>
              </span>
            </button>
          </div>

          <input
            v-if="q.allowFreeText"
            :ref="(el) => (textRefs[qi] = el)"
            class="input free-text"
            :placeholder="q.options && q.options.length ? '也可以直接输入其它答案 / 补充说明' : '请输入你的答复'"
            :value="askStore.answers[qi]?.text || ''"
            :disabled="submitting"
            @input="onText(qi, $event.target.value)"
            @keyup.enter="onTextEnter"
          />
        </div>

        <div class="waiting-hint">
          模型正在等待你的答复；选择或输入后它会在同一轮里继续执行。
          点「跳过」则不提供答复，模型会自行决策并说明假设。
        </div>
      </div>

      <div class="dialog-footer">
        <span class="spacer"></span>
        <button class="btn" :disabled="submitting" @click="skip">跳过</button>
        <button class="btn btn-primary" :disabled="!canSubmit" @click="askStore.submit()">
          {{ submitting ? '提交中...' : '提交答复' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.ask-overlay {
  position: fixed;
  inset: 0;
  background-color: $color-overlay;
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 900;
}

.ask-dialog {
  width: min(560px, 92vw);
  max-height: 86vh;
  display: flex;
  flex-direction: column;
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  box-shadow: $shadow-lg;
}

.dialog-header {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-md;
  border-bottom: 1px solid $color-border;

  .ask-icon {
    font-size: 16px;
  }

  .dialog-title {
    margin: 0;
    font-size: $font-size-sm;
    color: $color-text-primary;
  }

  .dialog-count {
    margin-left: auto;
    font-size: $font-size-xs;
    color: $color-text-muted;
  }
}

.dialog-body {
  padding: $space-md;
  overflow-y: auto;
}

.question {
  margin-bottom: $space-md;

  &:last-child {
    margin-bottom: 0;
  }
}

.question-head {
  display: flex;
  align-items: center;
  gap: $space-xs;
  margin-bottom: 4px;
}

.question-header {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 10px;
  background-color: rgb(var(--color-bg-tertiary-rgb) / 0.8);
  color: $color-text-secondary;
  font-size: 11px;
}

.question-multi {
  font-size: 11px;
  color: $color-text-muted;
}

.question-text {
  margin: 0 0 $space-sm;
  font-size: $font-size-sm;
  color: $color-text-primary;
  line-height: 1.5;
}

.options {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: $space-sm;
}

.option {
  display: flex;
  align-items: flex-start;
  gap: $space-sm;
  width: 100%;
  padding: 8px 10px;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  background-color: $color-bg-primary;
  text-align: left;
  font: inherit;
  cursor: pointer;
  transition: border-color $transition-fast, background-color $transition-fast;

  &:hover:not(:disabled) {
    border-color: $color-primary;
  }

  &.active {
    border-color: $color-primary;
    background-color: rgb(var(--color-primary-rgb) / 0.08);
  }

  &:disabled {
    opacity: 0.6;
    cursor: default;
  }
}

.option-mark {
  color: $color-primary;
  font-size: $font-size-sm;
  line-height: 1.4;
}

.option-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.option-label {
  font-size: $font-size-sm;
  color: $color-text-primary;
}

.option-desc {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  line-height: 1.4;
}

.free-text {
  width: 100%;
}

.waiting-hint {
  margin-top: $space-sm;
  padding-top: $space-sm;
  border-top: 1px solid $color-border;
  font-size: $font-size-xs;
  color: $color-text-muted;
  line-height: 1.5;
}

.dialog-footer {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-md;
  border-top: 1px solid $color-border;

  .spacer {
    flex: 1;
  }
}
</style>
