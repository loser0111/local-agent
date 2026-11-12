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
// 重试规划进行中。不并进 executing：它的语义是"计划步骤正在逐步跑"，
// 而重试只是一次普通模型调用，停止按钮不该因此走计划取消那条路。
const retrying = ref(false)

const editable = computed(
  () => plan.value?.status === 'awaiting_approval' && !executing.value && !chatStore.isGenerating
)
const busy = computed(() => executing.value || saving.value || retrying.value)

// 「没跑完」的三种状态对用户是同一件事，恢复动作也应当一致（原地续跑）。
// 与后端 resumePlan 接受的集合保持一致。
const RESUMABLE = ['failed', 'interrupted', 'cancelled']
const resumable = computed(() => RESUMABLE.includes(plan.value?.status))
const planFailed = computed(() => plan.value?.status === 'plan_failed')
const planning = computed(() => plan.value?.status === 'planning')
const draftValid = computed(
  () =>
    draft.value &&
    draft.value.steps.length > 0 &&
    draft.value.steps.every((s) => s.title && s.title.trim())
)

const STATUS_META = {
  // planning / plan_failed 是规划阶段的状态：它们的存在本身就是"规划失败不再静默"——
  // 失败时那条记录还在、还带着原始请求，所以能一键重试，不必让用户重打一遍需求。
  planning: { label: '规划中', cls: 'info' },
  plan_failed: { label: '规划失败', cls: 'error' },
  awaiting_approval: { label: '待审核', cls: 'warning' },
  running: { label: '执行中', cls: 'info' },
  completed: { label: '已完成', cls: 'success' },
  failed: { label: '失败', cls: 'error' },
  // interrupted 与 failed 分开：前者是"没人接着跑了"（进程退出、运行登记丢失），
  // 不含"这一步做错了"的判断，恢复动作也是原地续跑。
  interrupted: { label: '已中断', cls: 'warning' },
  cancelled: { label: '已取消', cls: 'muted' },
}

const STEP_ICON = {
  pending: '⏳',
  running: '🔄',
  done: '✅',
  failed: '❌',
  interrupted: '⚠️',
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
  // 会话 ID 显式传：计划执行的事件要按会话路由，不能靠"当前显示的会话"去猜
  // （用户完全可以在执行期间切到别的会话）
  await planStore.execute(sessionId.value, saved.id, settingStore.settings.streamResponse)
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

/**
 * 从断点继续执行：已完成的步骤不会重跑。
 *
 * 走 planStore.resume 而不是 execute：后者只接受待审核的计划（后端会拒绝其余状态），
 * 语义也确实是两件事——"批准这份计划" vs "把没跑完的接着跑完"。
 */
async function resumeExec() {
  if (!plan.value) return
  await planStore.resume(sessionId.value, plan.value.id, settingStore.settings.streamResponse)
}

/** 规划失败后重试：复用后端保存的原始请求，不需要用户重打需求 */
async function retryPlanning() {
  if (!plan.value) return
  retrying.value = true
  try {
    const res = await planStore.retry(sessionId.value, plan.value.id, settingStore.settings.streamResponse)
    if (res?.error && !res?.plan) {
      alert(`重试规划失败：${res.error}`)
    }
  } finally {
    retrying.value = false
  }
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
        <p class="empty-hint">在聊天输入框开启「计划」并发送复杂任务即可生成（该开关按会话保存）。</p>
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
          <!-- 规划中：只有取消（它同样走计划取消，后端会断开在途的规划请求） -->
          <template v-if="planning">
            <p class="resume-hint">正在生成计划…（网络不稳定时可以取消后重试）</p>
            <button class="btn cancel-btn" @click="cancelExec">取消</button>
          </template>
          <!-- 规划失败：原始请求已保存，一键重试即可，不需要重打需求 -->
          <template v-else-if="planFailed">
            <p class="resume-hint">
              生成计划时出错了{{ plan.error ? `：${plan.error}` : '。' }}原始请求已保存，可直接重试。
            </p>
            <button class="btn btn-primary" :disabled="busy" @click="retryPlanning">
              {{ retrying ? '重试中...' : '重试规划' }}
            </button>
          </template>
          <template v-else-if="plan.status === 'awaiting_approval'">
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
          <!-- 失败/中断/取消：可从断点继续，也可先改计划再执行 -->
          <template v-else-if="resumable">
            <p class="resume-hint">
              <template v-if="plan.status === 'failed'">
                这一步失败了{{ plan.error ? `：${plan.error}` : '。' }}已完成与未执行的步骤都保留着，可以直接继续。
              </template>
              <template v-else-if="plan.status === 'interrupted'">
                执行被中断了{{ plan.error ? `（${plan.error}）` : '。' }}已完成与未执行的步骤都保留着，可以直接继续。
              </template>
              <template v-else>计划已取消，已完成的步骤会保留。</template>
            </p>
            <button class="btn btn-primary" :disabled="busy" @click="resumeExec">
              {{ executing ? '执行中...' : '继续执行' }}
            </button>
            <button class="btn" :disabled="busy" @click="reopenForEdit">编辑计划</button>
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
