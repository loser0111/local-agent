<script setup>
import { computed, onMounted, watch } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'
import { useSessionStore } from '@/stores/session'
import { useTodoStore } from '@/stores/todo'

const sessionStore = useSessionStore()
const todoStore = useTodoStore()

const sessionId = computed(() => sessionStore.currentSessionId)
const todos = computed(() => todoStore.todos)
const doneCount = computed(() => todoStore.doneCount)

// 与 SubagentPane / PlanPane 一致：挂载与切会话各拉一次。
// 事件只在"改动发生时"推，面板打开之前的改动要靠这次拉取补上。
function load() {
  todoStore.loadForSession(sessionId.value)
}

onMounted(load)
watch(sessionId, load)

const STATUS_META = {
  pending: { label: '待办', cls: 'pending' },
  in_progress: { label: '进行中', cls: 'running' },
  done: { label: '已完成', cls: 'done' },
}
</script>

<template>
  <div class="todo-pane">
    <PaneHeader
      type="todo"
      :extra="todos.length ? `已完成 ${doneCount} / 共 ${todos.length}` : ''"
    />

    <div class="todo-body scroll-container">
      <div v-if="!todos.length" class="empty">
        <div>本会话还没有待办清单</div>
        <div class="empty-hint">
          模型遇到多步骤、跨多轮的任务时，可以用 todo 工具把步骤列成清单，
          做完一项就勾掉一项——进度会实时显示在这里，上下文被压缩后它也是模型重建进度的依据
        </div>
      </div>

      <ul v-else class="todo-list">
        <li v-for="item in todos" :key="item.id" class="todo-item" :class="STATUS_META[item.status]?.cls">
          <span class="todo-mark">
            {{ item.status === 'done' ? '✓' : item.status === 'in_progress' ? '◐' : '○' }}
          </span>
          <span class="todo-text">{{ item.text }}</span>
          <span class="todo-status">{{ STATUS_META[item.status]?.label || item.status }}</span>
        </li>
      </ul>
    </div>
  </div>
</template>

<style scoped lang="scss">
.todo-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.todo-body {
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

.todo-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.todo-item {
  display: flex;
  align-items: flex-start;
  gap: $space-sm;
  padding: $space-sm $space-md;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  background-color: $color-bg-secondary;
  margin-bottom: $space-sm;

  &.done {
    opacity: 0.6;

    .todo-text {
      text-decoration: line-through;
    }
  }

  &.running {
    border-color: $color-primary;
  }
}

.todo-mark {
  flex: 0 0 auto;
  font-size: $font-size-sm;
  color: $color-text-muted;
  line-height: $line-height-md;
}

.todo-item.done .todo-mark {
  color: $color-success;
}

.todo-item.running .todo-mark {
  color: $color-primary;
}

.todo-text {
  flex: 1;
  min-width: 0;
  font-size: $font-size-sm;
  line-height: $line-height-md;
  word-break: break-word;
}

.todo-status {
  flex: 0 0 auto;
  font-size: $font-size-xs;
  color: $color-text-muted;
}
</style>
