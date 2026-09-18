<script setup>
import { ref, computed, watch, onMounted } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'
import { useSubagentStore } from '@/stores/subagent'
import { useSessionStore } from '@/stores/session'
import { useDiffStore } from '@/stores/diff'
import { useUiStore } from '@/stores/ui'
import { listCheckpoints, undoDiffTurn } from '@/api/session'
import {
  Loader2, RefreshCw, ChevronDown, ChevronRight, AlertTriangle, Undo2, SquareTerminal, Wrench,
} from 'lucide-vue-next'

const subagentStore = useSubagentStore()
const sessionStore = useSessionStore()
const diffStore = useDiffStore()
const ui = useUiStore()

// 展开后每条消息最多显示多少字符（子代理的工具输出可能很长）
const MAX_MSG_CHARS = 600
// 一次回退最多在确认框里列多少个文件
const MAX_LIST_FILES = 20

const undoing = ref({}) // runId -> 是否正在回退

const runs = computed(() => subagentStore.runs)
const runningCount = computed(() => subagentStore.runningCount)

const STATUS_TEXT = {
  running: '运行中',
  completed: '已完成',
  failed: '失败',
  cancelled: '已取消',
  interrupted: '已中断',
}

onMounted(() => subagentStore.load(sessionStore.currentSessionId))

watch(
  () => sessionStore.currentSessionId,
  (id) => subagentStore.load(id)
)

function statusText(s) {
  return STATUS_TEXT[s] || s
}

/** 已跑时长：跑完的用 endedAt，运行中的算到当前 */
function duration(sa) {
  if (!sa.startedAt) return ''
  const end = sa.endedAt || Date.now()
  const sec = Math.max(0, Math.round((end - sa.startedAt) / 1000))
  if (sec < 60) return `${sec}s`
  return `${Math.floor(sec / 60)}m ${sec % 60}s`
}

function refresh() {
  return subagentStore.load(sessionStore.currentSessionId)
}

/** 消息内容截断（工具输出动辄上万字，全渲染会把面板卡住） */
function msgText(m) {
  const c = (m.content || '').trim()
  if (!c) return ''
  return c.length > MAX_MSG_CHARS ? c.slice(0, MAX_MSG_CHARS) + '…（已截断）' : c
}

/** 消息行的图标：工具结果 / 工具调用 / 纯文本（纯文本不显示图标） */
function msgIcon(m) {
  if (m.role === 'tool') return SquareTerminal
  if (m.toolCalls && m.toolCalls.length) return Wrench
  return null
}

function toggle(sa) {
  subagentStore.toggle(sa.runId)
}

/**
 * 回退这个子代理改过的文件。
 *
 * 复用 P2 的回退：子代理有自己的会话，diff 轮次与 checkpoint 都按它的会话 ID 键控，
 * 所以"回退某一轮"这套机制原样可用。这里把它的**所有**可回退轮次按从新到旧依次回退——
 * 用户点这个按钮的意图是"把子代理做的事撤掉"，而不是"具体撤某一轮"。
 */
async function undoFiles(sa) {
  if (undoing.value[sa.runId]) return
  let cps = []
  try {
    cps = (await listCheckpoints(sa.runId)) || []
  } catch (e) {
    ui.notify(`读取回退信息失败：${e.message || e}`, 'error')
    return
  }
  const turns = cps
    .filter((c) => c.available)
    .map((c) => c.turn)
    .sort((a, b) => b - a)
  if (!turns.length) {
    ui.notify('这个子代理没有可回退的轮次（非 git 仓库、或快照已过期）', 'info')
    return
  }
  const files = [...new Set(cps.flatMap((c) => c.files || []))]
  const listed = files.slice(0, MAX_LIST_FILES).join('\n')
  const more = files.length > MAX_LIST_FILES ? `\n…（共 ${files.length} 个文件）` : ''
  const ok = await ui.ask({
    title: `回退「${sa.title}」改过的文件`,
    message:
      `将依次回退它跑过的 ${turns.length} 个轮次，把文件恢复到它动手之前的状态：\n` +
      `${listed}${more}`,
    confirmText: '回退',
    danger: true,
  })
  if (!ok) return

  undoing.value[sa.runId] = true
  let failed = 0
  let forced = false
  for (const turn of turns) {
    try {
      await undoDiffTurn(sa.runId, turn, forced)
    } catch (e) {
      const msg = String(e?.message || e)
      // 这些文件在这一轮之后又被改过 → 后端按 P2 的约定拒绝并要求 force 重试。
      // 子代理的轮次在 DiffPane 里看不到（那是主会话的视图），所以必须在这里给用户
      // 一个确认的机会，否则他们就再也没有办法强制回退了。
      if (!forced && msg.includes('又被改动过')) {
        const okForce = await ui.ask({
          title: '这些文件之后又被改过',
          message: `${msg}\n\n继续会覆盖那些改动。确定吗？`,
          confirmText: '仍然回退',
          danger: true,
        })
        if (!okForce) break
        forced = true
        try {
          await undoDiffTurn(sa.runId, turn, true)
        } catch {
          failed++
        }
        continue
      }
      failed++
    }
  }
  undoing.value[sa.runId] = false

  // 回退不走事件推送（空 diff 会被前端丢弃，见 stores/diff.js），所以这里主动重取
  await refresh()
  subagentStore.invalidate(sa.runId)
  await subagentStore.loadMessages(sa.runId)
  await diffStore.load(sessionStore.currentSessionId)
  ui.notify(
    failed ? `回退完成，${failed} 个轮次失败` : '已回退这个子代理改过的文件',
    failed ? 'error' : 'success'
  )
}
</script>

<template>
  <div class="subagent-pane">
    <PaneHeader type="subagent" :extra="`运行中 ${runningCount} / 共 ${runs.length}`">
      <template #extra>
        <button class="btn btn-ghost btn-sm" :disabled="subagentStore.loading" @click="refresh">
          <RefreshCw :size="12" :class="{ spin: subagentStore.loading }" />
          刷新
        </button>
      </template>
    </PaneHeader>

    <div class="subagent-body scroll-container">
      <div v-if="!runs.length" class="empty">
        <div>本会话还没有派生子代理</div>
        <div class="empty-hint">
          模型遇到"过程很长、且与主线关系不大"的任务时可以用 spawn_agent 派一个子代理去做，
          它会用自己的上下文独立完成，只把结论交回来
        </div>
      </div>

      <div
        v-for="sa in runs"
        :key="sa.runId"
        class="subagent-card"
        :class="sa.status"
      >
        <div class="card-header" @click="toggle(sa)">
          <span class="status-dot" :class="sa.status" />
          <span class="sa-name" :title="sa.task">{{ sa.title }}</span>
          <span class="sa-duration">{{ duration(sa) }}</span>
          <component :is="subagentStore.expanded[sa.runId] ? ChevronDown : ChevronRight" :size="13" />
        </div>

        <div class="card-meta">
          <span class="status-text" :class="sa.status">{{ statusText(sa.status) }}</span>
          <span v-if="sa.model">· {{ sa.model }}</span>
          <span v-if="sa.step">· {{ sa.step }} 次工具调用</span>
          <span v-if="sa.status === 'running'">
            · {{ sa.currentTool ? `正在执行 ${sa.currentTool}` : '正在思考' }}
          </span>
        </div>

        <div v-if="sa.error" class="card-error">
          <AlertTriangle :size="12" />
          <span>{{ sa.error }}</span>
        </div>

        <div v-if="subagentStore.expanded[sa.runId]" class="card-detail">
          <div class="section">
            <div class="section-label">任务</div>
            <div class="section-body task-text">{{ sa.task }}</div>
          </div>

          <div v-if="sa.summary" class="section">
            <div class="section-label">结论（只有这部分回到了主会话的上下文）</div>
            <div class="section-body pre-wrap">{{ sa.summary }}</div>
          </div>

          <div v-if="sa.files && sa.files.length" class="section">
            <div class="section-label">
              改动的文件（{{ sa.files.length }}）
              <button
                class="btn btn-ghost btn-sm undo-btn"
                :disabled="!!undoing[sa.runId]"
                title="把这个子代理改过的文件恢复到它动手之前的状态"
                @click="undoFiles(sa)"
              >
                <Loader2 v-if="undoing[sa.runId]" :size="12" class="spin" />
                <Undo2 v-else :size="12" />
                回退它改的文件
              </button>
            </div>
            <div class="file-list">
              <div v-for="f in sa.files" :key="f" class="file-line">{{ f }}</div>
            </div>
          </div>

          <div v-if="sa.declined && sa.declined.length" class="section">
            <div class="section-label">因需要授权被挡下的操作（{{ sa.declined.length }}）</div>
            <div class="section-body">
              <div v-for="(d, i) in sa.declined" :key="i" class="declined-line">{{ d }}</div>
              <div class="declined-hint">
                子代理无法代你确认授权，它应当把这些写进结论。需要放行的操作请在主会话里做，
                或在权限设置里加规则。
              </div>
            </div>
          </div>

          <div class="section">
            <div class="section-label">
              过程消息（{{ (subagentStore.messages[sa.runId] || []).length }}）
              <Loader2 v-if="subagentStore.messagesLoading[sa.runId]" :size="12" class="spin" />
            </div>
            <div v-if="!(subagentStore.messages[sa.runId] || []).length" class="section-body muted">
              暂无消息
            </div>
            <div
              v-for="(m, i) in subagentStore.messages[sa.runId] || []"
              :key="m.id || i"
              class="msg-line"
              :class="`role-${m.role}`"
            >
              <component :is="msgIcon(m)" v-if="msgIcon(m)" :size="11" class="msg-icon" />
              <div class="msg-body">
                <div v-if="m.role === 'tool'" class="msg-role">工具结果</div>
                <div v-else-if="m.role === 'user'" class="msg-role">任务</div>
                <div v-else-if="m.toolCalls && m.toolCalls.length" class="msg-role">
                  调用工具：{{ m.toolCalls.map((t) => t.name).join('、') }}
                </div>
                <div v-if="msgText(m)" class="msg-text pre-wrap">{{ msgText(m) }}</div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.subagent-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: $color-bg-primary;
}

.subagent-body {
  flex: 1;
  padding: $space-md;
}

.empty {
  color: $color-text-muted;
  font-size: $font-size-sm;
  padding: $space-lg $space-md;
  text-align: center;
}

.empty-hint {
  margin-top: $space-sm;
  font-size: $font-size-xs;
  color: $color-text-muted;
  line-height: $line-height-md;
}

.subagent-card {
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  padding: $space-md;
  margin-bottom: $space-md;

  &.running {
    border-color: rgba(245, 158, 11, 0.3);
  }

  &.failed,
  &.interrupted {
    border-color: rgba(239, 68, 68, 0.3);
  }
}

.card-header {
  display: flex;
  align-items: center;
  gap: $space-sm;
  cursor: pointer;
  color: $color-text-muted;
}

.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
  background-color: $color-text-muted;

  &.running {
    background-color: $color-warning;
  }

  &.completed {
    background-color: $color-success;
  }

  &.failed,
  &.interrupted {
    background-color: $color-error;
  }
}

.sa-name {
  flex: 1;
  font-size: $font-size-sm;
  font-weight: $font-weight-medium;
  color: $color-text-primary;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sa-duration {
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.card-meta {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  margin-top: $space-xs;
  padding-left: 15px;
}

.status-text {
  &.running {
    color: $color-warning;
  }

  &.failed,
  &.interrupted {
    color: $color-error;
  }
}

.card-error {
  display: flex;
  align-items: flex-start;
  gap: $space-xs;
  margin-top: $space-sm;
  padding: $space-sm;
  border-radius: $radius-sm;
  background-color: rgba(239, 68, 68, 0.08);
  color: $color-error;
  font-size: $font-size-xs;
  line-height: $line-height-sm;
}

.card-detail {
  margin-top: $space-md;
  padding-top: $space-sm;
  border-top: 1px solid $color-border;
}

.section {
  margin-bottom: $space-md;

  &:last-child {
    margin-bottom: 0;
  }
}

.section-label {
  display: flex;
  align-items: center;
  gap: $space-sm;
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin-bottom: $space-xs;
}

.section-body {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  line-height: $line-height-md;

  &.muted {
    color: $color-text-muted;
  }
}

.task-text {
  color: $color-text-primary;
}

.pre-wrap {
  white-space: pre-wrap;
  word-break: break-word;
}

.file-list {
  font-family: $font-family-mono;
}

.file-line,
.declined-line,
.msg-text {
  font-family: $font-family-mono;
  font-size: $font-size-xs;
  color: $color-text-secondary;
  padding: 1px 0;
}

.declined-line {
  color: $color-warning;
}

.declined-hint {
  margin-top: $space-xs;
  font-size: $font-size-xs;
  color: $color-text-muted;
  line-height: $line-height-md;
}

.undo-btn {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.msg-line {
  display: flex;
  gap: $space-xs;
  padding: $space-xs 0;
  border-top: 1px dashed $color-border-light;

  &.role-user {
    border-top: none;
  }
}

.msg-icon {
  flex-shrink: 0;
  margin-top: 2px;
  color: $color-text-muted;
}

.msg-body {
  min-width: 0;
  flex: 1;
}

.msg-role {
  font-size: $font-size-xs;
  color: $color-text-muted;
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
