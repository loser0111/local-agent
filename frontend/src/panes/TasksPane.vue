<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'
import TaskFormDialog from '@/components/business/TaskFormDialog.vue'
import { useTaskStore } from '@/stores/task'

/**
 * 定时任务面板
 *
 * 这里展示的全部是**后端算好的状态**：列表项里的「下次触发」来自插件的
 * NextAfter 推导（不落盘、每次重算），界面上不做任何本地推断 ——
 * 否则界面显示的与调度器真正会做的迟早会不一致。
 */
const taskStore = useTaskStore()

const showForm = ref(false)
const editing = ref(null)
const expandedId = ref('')
const busyId = ref('')
const actionError = ref('')

const tasks = computed(() => taskStore.tasks)
const total = computed(() => taskStore.counts.total)
const softLimitHit = computed(() => total.value >= taskStore.counts.limit)

onMounted(async () => {
  taskStore.subscribe()
  await taskStore.load()
})

onUnmounted(() => {
  taskStore.unsubscribeEvents()
})

function openCreate() {
  editing.value = null
  showForm.value = true
}

function openEdit(task) {
  editing.value = task
  showForm.value = true
}

async function toggle(task) {
  await guard(task.id, () => taskStore.toggle(task.id, !task.enabled))
}

async function runNow(task) {
  await guard(task.id, () => taskStore.runNow(task.id))
}

async function snooze(task) {
  await guard(task.id, () => taskStore.snooze(task.id, 0))
}

async function complete(task) {
  await guard(task.id, () => taskStore.complete(task.id))
}

/**
 * 删除：二次确认里必须说明历史是否一并删除（默认保留）。
 * 历史是排查问题的依据，所以默认不勾选。
 */
async function remove(task) {
  const delHistory = confirm(
    `删除任务「${task.title}」？\n\n确定：删除\n按「取消」后仍可选择是否同时删除历史。`,
  )
  if (!delHistory) {
    // 用户没在上一问里确认，再问一次是否放弃
    const giveUp = confirm('不删除任务？')
    if (giveUp) return
  }
  const withHistory = confirm('是否一并删除该任务的触发历史？（默认保留，便于排查问题）')
  await guard(task.id, () => taskStore.remove(task.id, withHistory))
}

async function expand(task) {
  if (expandedId.value === task.id) {
    expandedId.value = ''
    return
  }
  expandedId.value = task.id
  await guard(task.id, () => taskStore.loadRuns(task.id, 50))
}

async function guard(id, fn) {
  busyId.value = id
  actionError.value = ''
  try {
    await fn()
  } catch (e) {
    actionError.value = e.message || String(e)
  } finally {
    busyId.value = ''
  }
}

async function onFormSaved() {
  showForm.value = false
  editing.value = null
  await taskStore.refresh()
}

/** 把后端时间（RFC3339）显示成本地「MM-DD HH:mm」 */
function fmt(iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 状态文案（结果全部是后端写入的原始值，界面只做翻译） */
function resultText(task) {
  const st = task.state || {}
  if (!st.lastResult) return '尚未触发'
  const map = {
    succeeded: '已完成',
    notified: '已提醒',
    skipped: '已跳过',
    missed: '已错过',
    running: '执行中',
    failed: '失败',
    timeout: '超时',
    aborted: '已中止',
    interrupted: '被中断',
  }
  const base = map[st.lastResult] || st.lastResult
  return st.lastError ? `${base} · ${st.lastError}` : base
}

function kindText(kind) {
  return kind === 'exec' ? '执行型' : '提醒型'
}

function triggerText(task) {
  const tr = task.trigger || {}
  switch (tr.kind) {
    case 'once':
      return `一次性 · ${fmt(tr.once?.at)}`
    case 'interval':
      return `每 ${tr.interval?.everyMinutes ?? '?'} 分钟`
    case 'recurring': {
      const r = tr.recurring || {}
      if (r.period === 'weekly') {
        const names = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']
        const days = (r.weekdays || []).map((w) => names[w] || w).join('、')
        return `每周 ${days} ${r.timeOfDay}`
      }
      if (r.period === 'monthly') return `每月 ${r.dayOfMonth} 日 ${r.timeOfDay}`
      return `每天 ${r.timeOfDay}`
    }
    case 'cron':
      return `cron · ${tr.cron?.expr || ''}`
    default:
      return '未设置'
  }
}
</script>

<template>
  <div class="tasks-pane">
    <PaneHeader type="tasks" :extra="`${taskStore.enabledCount}/${total} 启用中`" />

    <!-- 插件不可用：说清原因，而不是显示空列表让人以为「没任务」 -->
    <div v-if="!taskStore.available" class="notice">
      <div class="notice-title">定时任务插件未启用</div>
      <div class="notice-body">
        插件未加载或已停止，任务不会触发。请检查主程序日志中 <code>[desktop-plugin]</code> 开头的记录。
      </div>
    </div>

    <div v-else class="tasks-toolbar">
      <button class="btn" @click="openCreate">新增任务</button>
      <button class="btn ghost" :disabled="!taskStore.paused" @click="taskStore.setPaused(false)">
        恢复全部
      </button>
      <button class="btn ghost" :class="{ warn: taskStore.paused }" @click="taskStore.setPaused(true)">
        {{ taskStore.paused ? '已暂停（点击恢复）' : '暂停全部' }}
      </button>
      <span v-if="softLimitHit" class="hint">任务偏多（{{ total }}）</span>
    </div>

    <div v-if="actionError" class="error-bar">{{ actionError }}</div>

    <!-- 应用内提醒：系统通知不可用或免打扰时的兜底展示 -->
    <div v-if="taskStore.inAppNotices.length" class="inapp">
      <div v-for="(n, i) in taskStore.inAppNotices" :key="i" class="inapp-item">
        <b>{{ n.title }}</b>
        <span>{{ n.body }}</span>
        <button class="mini" @click="taskStore.dismissNotice(i)">知道了</button>
      </div>
    </div>

    <div class="tasks-body scroll-container">
      <div v-if="taskStore.loading && !tasks.length" class="empty">加载中…</div>
      <div v-else-if="!tasks.length" class="empty">还没有定时任务，点「新增任务」创建第一条。</div>

      <div v-for="task in tasks" :key="task.id" class="task-item" :class="{ off: !task.enabled }">
        <div class="task-main">
          <input
            type="checkbox"
            class="checkbox"
            :checked="task.enabled"
            :disabled="busyId === task.id"
            @change="toggle(task)"
          />
          <div class="task-info">
            <div class="task-title-row">
              <span class="task-title">{{ task.title }}</span>
              <span class="tag">{{ kindText(task.kind) }}</span>
              <span class="tag subtle">{{ triggerText(task) }}</span>
              <span v-if="task.state && task.state.finished" class="tag done">已结束</span>
            </div>
            <div class="task-meta">
              <span>下次：{{ task.nextFireAt ? fmt(task.nextFireAt) : '—' }}</span>
              <span v-if="task.snoozeUntil">稍后提醒：{{ fmt(task.snoozeUntil) }}</span>
              <span>上次：{{ resultText(task) }}</span>
            </div>
          </div>
          <div class="task-actions">
            <button class="mini" :disabled="busyId === task.id" @click="runNow(task)">执行</button>
            <button class="mini" :disabled="busyId === task.id" @click="snooze(task)">稍后</button>
            <button class="mini" :disabled="busyId === task.id" @click="complete(task)">完成</button>
            <button class="mini" @click="expand(task)">{{ expandedId === task.id ? '收起' : '历史' }}</button>
            <button class="mini" @click="openEdit(task)">编辑</button>
            <button class="mini danger" @click="remove(task)">删除</button>
          </div>
        </div>

        <div v-if="expandedId === task.id" class="runs">
          <div v-if="!(taskStore.runs[task.id] || []).length" class="empty small">暂无触发记录</div>
          <div v-for="rec in taskStore.runs[task.id] || []" :key="rec.runID" class="run-row">
            <span class="run-time">{{ fmt(rec.triggeredAt) }}</span>
            <span class="run-source">{{ rec.source }}</span>
            <span class="run-status" :class="rec.status">{{ rec.status }}</span>
            <span class="run-summary">
              {{ rec.error || rec.summary || '—' }}
              <em v-if="rec.notifyFallback">（{{ rec.notifyFallback }}）</em>
            </span>
          </div>
        </div>
      </div>
    </div>

    <TaskFormDialog
      v-if="showForm"
      :task="editing"
      @close="showForm = false"
      @saved="onFormSaved"
    />
  </div>
</template>

<style scoped lang="scss">
.tasks-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: $color-bg-primary;
}

.tasks-toolbar {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-sm $space-md;
  border-bottom: 1px solid $color-border;
}

.btn {
  padding: 4px $space-sm;
  font-size: $font-size-xs;
  color: $color-text-primary;
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: 4px;
  cursor: pointer;

  &:hover {
    border-color: $color-primary;
  }

  &.ghost {
    background-color: transparent;
  }

  &.warn {
    color: $color-warning;
    border-color: $color-warning;
  }
}

.mini {
  padding: 2px 6px;
  font-size: $font-size-xs;
  color: $color-text-muted;
  background-color: transparent;
  border: 1px solid $color-border;
  border-radius: 3px;
  cursor: pointer;

  &:hover {
    color: $color-text-primary;
    border-color: $color-primary;
  }

  &.danger:hover {
    color: $color-error;
    border-color: $color-error;
  }
}

.notice {
  padding: $space-md;
  margin: $space-md;
  background-color: $color-bg-secondary;
  border: 1px solid $color-warning;
  border-radius: 4px;

  .notice-title {
    font-size: $font-size-sm;
    color: $color-warning;
    margin-bottom: $space-xs;
  }

  .notice-body {
    font-size: $font-size-xs;
    color: $color-text-muted;
  }
}

.error-bar {
  padding: $space-xs $space-md;
  font-size: $font-size-xs;
  color: $color-error;
  background-color: $color-bg-secondary;
}

.inapp {
  border-bottom: 1px solid $color-border;

  .inapp-item {
    display: flex;
    align-items: center;
    gap: $space-sm;
    padding: $space-xs $space-md;
    font-size: $font-size-xs;
    background-color: $color-bg-secondary;

    b {
      color: $color-primary;
      font-weight: 500;
    }

    span {
      flex: 1;
      color: $color-text-muted;
    }
  }
}

.hint {
  font-size: $font-size-xs;
  color: $color-warning;
}

.tasks-body {
  flex: 1;
  padding: $space-md;
}

.empty {
  font-size: $font-size-xs;
  color: $color-text-muted;
  padding: $space-md 0;

  &.small {
    padding: $space-xs 0;
  }
}

.task-item {
  border-bottom: 1px solid $color-border;
  padding: $space-sm 0;

  &.off .task-title {
    color: $color-text-muted;
    text-decoration: line-through;
  }
}

.task-main {
  display: flex;
  align-items: flex-start;
  gap: $space-sm;
}

.checkbox {
  margin-top: 3px;
  cursor: pointer;
}

.task-info {
  flex: 1;
  min-width: 0;
}

.task-title-row {
  display: flex;
  align-items: center;
  gap: $space-xs;
  flex-wrap: wrap;
}

.task-title {
  font-size: $font-size-sm;
  color: $color-text-primary;
}

.tag {
  padding: 0 4px;
  font-size: 10px;
  line-height: 16px;
  color: $color-text-muted;
  border: 1px solid $color-border;
  border-radius: 3px;

  &.subtle {
    color: $color-text-muted;
  }

  &.done {
    color: $color-success;
    border-color: $color-success;
  }
}

.task-meta {
  display: flex;
  gap: $space-md;
  margin-top: 2px;
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.task-actions {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.runs {
  margin-top: $space-xs;
  padding-left: 24px;
}

.run-row {
  display: flex;
  gap: $space-sm;
  align-items: baseline;
  font-size: $font-size-xs;
  color: $color-text-muted;
  padding: 2px 0;
}

.run-time {
  min-width: 84px;
}

.run-source {
  min-width: 66px;
}

.run-status {
  min-width: 66px;

  &.failed,
  &.timeout,
  &.interrupted {
    color: $color-error;
  }

  &.succeeded,
  &.notified {
    color: $color-success;
  }
}

.run-summary {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;

  em {
    font-style: normal;
    color: $color-warning;
  }
}
</style>
