<script setup>
import { ref, computed, watch, onMounted } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'
import { useSessionStore } from '@/stores/session'
import { useChatStore } from '@/stores/chat'
import { usePaneStore } from '@/stores/pane'
import { usePlanStore } from '@/stores/plan'
import { useSettingStore } from '@/stores/setting'

const sessionStore = useSessionStore()
const chatStore = useChatStore()
const paneStore = usePaneStore()
const planStore = usePlanStore()
const settingStore = useSettingStore()

const sessionId = computed(() => sessionStore.currentSessionId)
const plan = computed(() => planStore.plan)
const executing = computed(() => planStore.executing)

// 审核编辑草稿（仅 awaiting_approval 可编辑）
const draft = ref(null)
const saving = ref(false)

const editable = computed(
  () => plan.value?.status === 'awaiting_approval' && !executing.value && !chatStore.isGenerating
)
const busy = computed(() => executing.value || saving.value)
const draftValid = computed(
  () =>
    draft.value &&
    draft.value.steps.length > 0 &&
    draft.value.steps.every((s) => s.title && s.title.trim())
)

const STATUS_META = {
  awaiting_approval: { label: '待审核', cls: 'warning' },
  running: { label: '执行中', cls: 'info' },
  completed: { label: '已完成', cls: 'success' },
  failed: { label: '失败', cls: 'error' },
  cancelled: { label: '已取消', cls: 'muted' },
}

const STEP_ICON = {
  pending: '⏳',
  running: '🔄',
  done: '✅',
  failed: '❌',
  skipped: '⏭️',
}

function loadPlan() {
  planStore.loadForSession(sessionId.value)
}

onMounted(loadPlan)
watch(sessionId, loadPlan)

// 进入/离开可编辑状态时初始化或清空草稿
watch(
  () => plan.value?.status,
  (status) => {
    if (status === 'awaiting_approval' && plan.value) {
      draft.value = JSON.parse(
        JSON.stringify({
          title: plan.value.title,
          steps: plan.value.steps.map((s) => ({
            index: s.index,
            title: s.title,
            detail: s.detail || '',
            status: s.status,
          })),
        })
      )
    } else {
      draft.value = null
    }
  },
  { immediate: true }
)

function addStep() {
  if (!draft.value) return
  draft.value.steps.push({
    index: draft.value.steps.length,
    title: '',
    detail: '',
    status: 'pending',
  })
}

function removeStep(i) {
  if (!draft.value || draft.value.steps.length <= 1) return
  draft.value.steps.splice(i, 1)
}

/**
 * 保存草稿到后端；失败返回 null
 */
async function persistDraft() {
  if (!draft.value || !plan.value) return plan.value
  saving.value = true
  try {
    return await planStore.save({
      ...plan.value,
      title: draft.value.title,
      steps: draft.value.steps,
    })
  } catch (e) {
    alert(`保存计划失败：${e?.message || e}`)
    return null
  } finally {
    saving.value = false
  }
}

async function approve() {
  const saved = await persistDraft()
  if (!saved) return
  await planStore.execute(saved.id, settingStore.settings.streamResponse)
}

async function saveOnly() {
  await persistDraft()
}

async function cancelExec() {
  if (!plan.value) return
  try {
    await planStore.cancel(plan.value.id)
  } catch (e) {
    // 取消失败要显式暴露：以前只 console.warn，卡在执行中时用户完全看不出原因
    alert(`取消失败：${e?.message || e}`)
  }
}

/** 从失败/取消处继续执行：已完成的步骤不会重跑 */
async function resumeExec() {
  if (!plan.value) return
  await planStore.execute(plan.value.id, settingStore.settings.streamResponse)
}

/** 退回待审核以便修改步骤（失败后「修改后重试」） */
async function reopenForEdit() {
  if (!plan.value) return
  try {
    await planStore.reopen(plan.value.id)
  } catch (e) {
    alert(`无法修改计划：${e?.message || e}`)
  }
}

function backToChat() {
  if (sessionId.value) paneStore.closePane(sessionId.value, 'plan')
}
</script>

<template>
  <div class="plan-pane">
    <PaneHeader type="plan" />
    <div class="plan-body scroll-container">
      <!-- 空态 -->
      <div v-if="!plan" class="plan-empty">
        <div class="empty-icon">🗒️</div>
        <p>本会话还没有计划。</p>
        <p class="empty-hint">在聊天输入框开启「计划」并发送复杂任务即可生成。</p>
      </div>

      <template v-else>
        <!-- 标题区 -->
        <div class="plan-header">
          <span class="plan-title">{{ plan.title || '未命名计划' }}</span>
          <span class="plan-status" :class="STATUS_META[plan.status]?.cls">
            {{ STATUS_META[plan.status]?.label || plan.status }}
          </span>
        </div>

        <!-- 步骤列表 -->
        <div class="plan-list">
          <!-- 待审核：可编辑 -->
          <template v-if="editable">
            <div v-for="(step, i) in draft.steps" :key="i" class="plan-item editing">
              <span class="step-no">{{ i + 1 }}</span>
              <div class="step-edit">
                <input v-model="step.title" class="step-input" placeholder="步骤标题" />
                <div v-if="step.detail" class="step-detail">{{ step.detail }}</div>
              </div>
              <button
                class="step-del"
                title="删除步骤"
                :disabled="draft.steps.length <= 1"
                @click="removeStep(i)"
              >
                ✕
              </button>
            </div>
            <button class="add-step-btn" @click="addStep">＋ 添加步骤</button>
          </template>

          <!-- 其他状态：只读展示 -->
          <template v-else>
            <div
              v-for="step in plan.steps"
              :key="step.index"
              class="plan-item"
              :class="step.status"
            >
              <span class="plan-icon">{{ STEP_ICON[step.status] || '⏳' }}</span>
              <div class="step-body">
                <span class="plan-text">{{ step.title }}</span>
                <div v-if="step.summary" class="step-summary">{{ step.summary }}</div>
                <div v-if="step.error" class="step-error">{{ step.error }}</div>
              </div>
            </div>
          </template>
        </div>

        <!-- 底部按钮 -->
        <div class="plan-footer">
          <template v-if="plan.status === 'awaiting_approval'">
            <button class="btn" :disabled="busy" @click="saveOnly">保存修改</button>
            <button
              class="btn btn-primary"
              :disabled="busy || !draftValid"
              @click="approve"
            >
              {{ saving ? '保存中...' : executing ? '执行中...' : '批准并执行' }}
            </button>
          </template>
          <template v-else-if="plan.status === 'running'">
            <button class="btn cancel-btn" @click="cancelExec">取消执行</button>
          </template>
          <!-- 失败/取消：可从断点继续，也可先改计划再执行 -->
          <template v-else-if="plan.status === 'failed' || plan.status === 'cancelled'">
            <p class="resume-hint">
              <template v-if="plan.status === 'failed'">
                执行中断了。可以直接重新执行（已完成的步骤不会重跑），也可以先修改计划再执行。
              </template>
              <template v-else>计划已取消，已完成的步骤会保留。</template>
            </p>
            <button class="btn btn-primary" :disabled="busy" @click="resumeExec">
              {{ executing ? '执行中...' : '重新执行' }}
            </button>
            <button class="btn" :disabled="busy" @click="reopenForEdit">修改后重试</button>
          </template>
          <template v-else>
            <p class="terminal-hint">
              计划已结束（{{ STATUS_META[plan.status]?.label }}）。
            </p>
            <button class="btn" @click="backToChat">返回聊天</button>
          </template>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped lang="scss">
.plan-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: $color-bg-primary;
}

.plan-body {
  flex: 1;
  padding: $space-lg;
}

.plan-header {
  display: flex;
  align-items: center;
  gap: $space-md;
  margin-bottom: $space-lg;
}

.plan-title {
  flex: 1;
  min-width: 0;
  font-size: $font-size-lg;
  font-weight: $font-weight-semibold;
}

// 状态徽标
.plan-status {
  flex-shrink: 0;
  padding: 2px $space-sm;
  border-radius: $radius-sm;
  font-size: $font-size-xs;

  &.warning {
    color: $color-warning;
    background-color: $color-warning-soft;
  }
  &.info {
    color: $color-info;
    background-color: $color-info-soft;
  }
  &.success {
    color: $color-success;
    background-color: $color-success-soft;
  }
  &.error {
    color: $color-error;
    background-color: $color-error-soft;
  }
  &.muted {
    color: $color-text-muted;
    background-color: $color-bg-tertiary;
  }
}

.plan-list {
  display: flex;
  flex-direction: column;
  gap: $space-md;
}

.plan-item {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-sm $space-md;
  background-color: $color-bg-secondary;
  border-radius: $radius-md;
  border-left: 3px solid transparent;

  &.done {
    border-left-color: $color-success;
    .plan-text {
      color: $color-text-muted;
      text-decoration: line-through;
    }
  }
  &.running {
    border-left-color: $color-warning;
  }
  &.failed {
    border-left-color: $color-error;
  }
  &.skipped {
    border-left-color: $color-border;
    .plan-text {
      color: $color-text-muted;
    }
  }

  // 编辑态
  &.editing {
    align-items: flex-start;
    border-left-color: $color-primary;
  }
}

.plan-icon {
  font-size: $font-size-md;
  flex-shrink: 0;
}

.plan-text {
  font-size: $font-size-sm;
}

.step-body {
  flex: 1;
  min-width: 0;
}

.step-summary {
  margin-top: 2px;
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.step-error {
  margin-top: 2px;
  font-size: $font-size-xs;
  color: $color-error;
}

// 编辑态元素
.step-no {
  flex-shrink: 0;
  width: 20px;
  font-size: $font-size-xs;
  color: $color-text-muted;
  text-align: center;
  line-height: 32px;
}

.step-edit {
  flex: 1;
  min-width: 0;
}

.step-input {
  width: 100%;
  padding: $space-xs $space-sm;
  font-size: $font-size-sm;
  background-color: $color-bg-primary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  color: $color-text-primary;
  outline: none;
  transition: border-color $transition-fast;

  &:focus {
    border-color: $color-primary;
  }
}

.step-detail {
  margin-top: 2px;
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.step-del {
  flex-shrink: 0;
  padding: $space-xs;
  font-size: $font-size-xs;
  color: $color-text-muted;
  border-radius: $radius-sm;
  transition: all $transition-fast;

  &:hover:not(:disabled) {
    color: $color-error;
    background-color: $color-error-soft;
  }

  &:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }
}

.add-step-btn {
  align-self: flex-start;
  padding: $space-xs $space-md;
  font-size: $font-size-sm;
  color: $color-text-secondary;
  background-color: transparent;
  border: 1px dashed $color-border;
  border-radius: $radius-md;
  transition: all $transition-fast;

  &:hover {
    color: $color-primary;
    border-color: $color-primary;
  }
}

.plan-footer {
  margin-top: $space-xl;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: $space-md;
  flex-wrap: wrap;
}

.resume-hint {
  flex: 1;
  margin: 0;
  font-size: $font-size-xs;
  color: $color-text-secondary;
  line-height: 1.5;
}

.terminal-hint {
  width: 100%;
  text-align: center;
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.cancel-btn {
  color: $color-error;
  border-color: $color-error;

  &:hover {
    background-color: $color-error-soft;
  }
}

// 空态
.plan-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  text-align: center;
  color: $color-text-secondary;
  font-size: $font-size-sm;

  .empty-icon {
    font-size: 40px;
    margin-bottom: $space-md;
  }

  .empty-hint {
    margin-top: $space-xs;
    font-size: $font-size-xs;
    color: $color-text-muted;
  }
}
</style>
