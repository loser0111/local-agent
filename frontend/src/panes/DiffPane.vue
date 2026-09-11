<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'
import { useDiffStore } from '@/stores/diff'
import { useSessionStore } from '@/stores/session'
import { useChatStore } from '@/stores/chat'
import { usePaneStore } from '@/stores/pane'

const diffStore = useDiffStore()
const sessionStore = useSessionStore()
const chatStore = useChatStore()
const paneStore = usePaneStore()

const selectedFile = ref(0)

const diffFiles = computed(() => diffStore.files)
const currentFile = computed(() => diffFiles.value[selectedFile.value] || null)

// 单文件最多渲染行数，防止超大 diff 卡顿
const MAX_RENDER_LINES = 2000

// 把 hunks 扁平成可渲染行：hunk 头作为独立行 + 内容行
const flatLines = computed(() => {
  const f = currentFile.value
  if (!f) return []
  const rows = []
  for (const h of f.hunks || []) {
    rows.push({ type: 'hunk', content: h.header })
    rows.push(...h.lines)
  }
  return rows.slice(0, MAX_RENDER_LINES)
})

const truncated = computed(() => {
  const f = currentFile.value
  if (!f) return false
  const total = (f.hunks || []).reduce((n, h) => n + h.lines.length + 1, 0)
  return total > MAX_RENDER_LINES
})

onMounted(() => diffStore.load(sessionStore.currentSessionId))

watch(
  () => sessionStore.currentSessionId,
  (id) => {
    selectedFile.value = 0
    diffStore.load(id)
  }
)

watch(
  () => diffFiles.value.length,
  () => {
    if (selectedFile.value >= diffFiles.value.length) selectedFile.value = 0
  }
)

function statusBadge(s) {
  return { added: 'A', deleted: 'D', renamed: 'R' }[s] || ''
}

function reviewCode() {
  const f = currentFile.value
  if (!f) return
  chatStore.requestPrompt(
    `请审查以下改动：${f.path}（+${f.additions} -${f.deletions}）`
  )
  // 切回对话面板，方便用户在输入框基础上补充并发送
  paneStore.setActivePane(sessionStore.currentSessionId, 'chat')
}
</script>

<template>
  <div class="diff-pane">
    <PaneHeader type="diff" :extra="`+${diffStore.additions} -${diffStore.deletions}`">
      <template #extra>
        <select
          v-if="diffStore.turns.length"
          class="turn-select"
          :value="diffStore.activeTurn"
          @change="diffStore.setActiveTurn(Number($event.target.value))"
        >
          <option v-for="t in diffStore.turns" :key="t.turn" :value="t.turn">
            {{ t.label || (t.turn === 0 ? '累计' : `第 ${t.turn} 轮`) }}
          </option>
        </select>
        <button
          class="btn btn-ghost btn-sm review-btn"
          :disabled="!currentFile"
          @click="reviewCode"
        >
          Review code
        </button>
      </template>
    </PaneHeader>

    <div class="diff-body">
      <div class="file-list">
        <div
          v-for="(f, idx) in diffFiles"
          :key="f.path"
          class="file-item"
          :class="{ active: idx === selectedFile }"
          @click="selectedFile = idx"
        >
          <span v-if="statusBadge(f.status)" class="file-badge" :class="`badge-${f.status}`">
            {{ statusBadge(f.status) }}
          </span>
          <span v-else class="file-icon">📄</span>
          <span class="file-path" :title="f.path">{{ f.path }}</span>
          <span class="file-stats">
            <span class="add">+{{ f.additions }}</span>
            <span class="del">-{{ f.deletions }}</span>
          </span>
        </div>
      </div>

      <div v-if="currentFile" class="diff-content scroll-container">
        <table class="diff-table">
          <tbody>
            <tr
              v-for="(line, i) in flatLines"
              :key="i"
              :class="`line-${line.type}`"
            >
              <td class="line-no old">{{ line.oldLineNo || '' }}</td>
              <td class="line-no new">{{ line.newLineNo || '' }}</td>
              <td class="line-sign">
                {{ line.type === 'add' ? '+' : line.type === 'del' ? '-' : line.type === 'hunk' ? '' : ' ' }}
              </td>
              <td class="line-content">{{ line.content }}</td>
            </tr>
          </tbody>
        </table>
        <div v-if="truncated" class="diff-truncated">
          差异过大，仅展示前 {{ MAX_RENDER_LINES }} 行
        </div>
      </div>

      <div v-else class="diff-empty">
        {{ diffStore.loading ? '正在计算差异…' : '本轮对话暂无文件改动' }}
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.diff-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: $color-bg-primary;
}

.diff-body {
  display: flex;
  flex: 1;
  min-height: 0;
}

.file-list {
  width: 200px;
  border-right: 1px solid $color-border;
  overflow-y: auto;
  flex-shrink: 0;
}

.file-item {
  display: flex;
  align-items: center;
  gap: $space-xs;
  padding: $space-sm $space-md;
  cursor: pointer;
  border-left: 2px solid transparent;
  transition: all $transition-fast;

  &:hover {
    background-color: $color-bg-tertiary;
  }

  &.active {
    background-color: rgba(124, 58, 237, 0.1);
    border-left-color: $color-primary;
  }
}

.file-icon {
  font-size: $font-size-sm;
}

.file-badge {
  flex-shrink: 0;
  width: 16px;
  height: 16px;
  border-radius: 3px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 10px;
  font-weight: $font-weight-bold;

  &.badge-added {
    color: $color-success;
    background-color: rgba(16, 185, 129, 0.15);
  }
  &.badge-deleted {
    color: $color-error;
    background-color: rgba(239, 68, 68, 0.15);
  }
  &.badge-renamed {
    color: $color-warning;
    background-color: rgba(245, 158, 11, 0.15);
  }
}

.file-path {
  flex: 1;
  font-size: $font-size-xs;
  color: $color-text-primary;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-family: $font-family-mono;
}

.file-stats {
  display: flex;
  gap: $space-xs;
  font-size: $font-size-xs;
  flex-shrink: 0;
}

.add {
  color: $color-success;
}
.del {
  color: $color-error;
}

.diff-content {
  flex: 1;
  overflow: auto;
}

.diff-table {
  width: 100%;
  border-collapse: collapse;
  font-family: $font-family-mono;
  font-size: $font-size-xs;
}

.line-no {
  width: 40px;
  padding: 1px $space-sm;
  text-align: right;
  color: $color-text-muted;
  user-select: none;
  white-space: nowrap;
}

.line-sign {
  width: 20px;
  padding: 1px $space-xs;
  text-align: center;
  user-select: none;
}

.line-content {
  padding: 1px $space-sm;
  white-space: pre;
}

.line-add {
  background-color: $color-diff-add;
  .line-content, .line-sign {
    color: $color-diff-add-text;
  }
}

.line-del {
  background-color: $color-diff-del;
  .line-content, .line-sign {
    color: $color-diff-del-text;
  }
}

.line-context {
  .line-content {
    color: $color-text-secondary;
  }
}

.line-hunk {
  background-color: rgba(189, 147, 249, 0.1);

  .line-content {
    color: $color-primary;
    font-weight: $font-weight-medium;
  }

  .line-sign,
  .line-no {
    color: $color-text-muted;
  }
}

.diff-truncated {
  padding: $space-md;
  text-align: center;
  font-size: $font-size-xs;
  color: $color-warning;
}

.diff-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: $color-text-muted;
  font-size: $font-size-sm;
}

.btn-sm {
  padding: 2px $space-sm;
  font-size: $font-size-xs;
}

.turn-select {
  font-size: $font-size-xs;
  background-color: $color-bg-tertiary;
  color: $color-text-primary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  padding: 1px 4px;
  outline: none;
  cursor: pointer;

  &:hover {
    border-color: $color-primary;
  }
}

.review-btn {
  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
}
</style>
