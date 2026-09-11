<script setup>
import { ref, computed } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'

// 模拟差异数据
const diffFiles = ref([
  {
    path: 'app.go',
    additions: 12,
    deletions: 3,
    lines: [
      { type: 'context', oldLineNo: 1, newLineNo: 1, content: 'package main' },
      { type: 'context', oldLineNo: 2, newLineNo: 2, content: '' },
      { type: 'context', oldLineNo: 3, newLineNo: 3, content: 'import (' },
      { type: 'context', oldLineNo: 4, newLineNo: 4, content: '  "context"' },
      { type: 'del', oldLineNo: 5, newLineNo: 0, content: '  "fmt"' },
      { type: 'add', oldLineNo: 0, newLineNo: 5, content: '  "fmt" // 保留 fmt 用于格式化输出' },
      { type: 'add', oldLineNo: 0, newLineNo: 6, content: '  "strings"' },
      { type: 'context', oldLineNo: 6, newLineNo: 7, content: ')' },
      { type: 'context', oldLineNo: 7, newLineNo: 8, content: '' },
      { type: 'context', oldLineNo: 8, newLineNo: 9, content: '// App struct' },
      { type: 'del', oldLineNo: 9, newLineNo: 0, content: 'type App struct {' },
      { type: 'del', oldLineNo: 10, newLineNo: 0, content: '  ctx context.Context' },
      { type: 'del', oldLineNo: 11, newLineNo: 0, content: '}' },
      { type: 'add', oldLineNo: 0, newLineNo: 10, content: 'type App struct {' },
      { type: 'add', oldLineNo: 0, newLineNo: 11, content: '  ctx    context.Context' },
      { type: 'add', oldLineNo: 0, newLineNo: 12, content: '  config *Config' },
      { type: 'add', oldLineNo: 0, newLineNo: 13, content: '}' },
    ],
  },
  {
    path: 'main.go',
    additions: 7,
    deletions: 1,
    lines: [
      { type: 'context', oldLineNo: 14, newLineNo: 14, content: 'func main() {' },
      { type: 'context', oldLineNo: 15, newLineNo: 15, content: '  app := NewApp()' },
      { type: 'add', oldLineNo: 0, newLineNo: 16, content: '  // 加载配置' },
      { type: 'add', oldLineNo: 0, newLineNo: 17, content: '  cfg, err := LoadConfig()' },
      { type: 'add', oldLineNo: 0, newLineNo: 18, content: '  if err != nil {' },
      { type: 'add', oldLineNo: 0, newLineNo: 19, content: '    log.Fatal(err)' },
      { type: 'add', oldLineNo: 0, newLineNo: 20, content: '  }' },
      { type: 'context', oldLineNo: 16, newLineNo: 21, content: '' },
      { type: 'context', oldLineNo: 17, newLineNo: 22, content: '  err := wails.Run(&options.App{' },
    ],
  },
])

const selectedFile = ref(0)
const currentFile = computed(() => diffFiles.value[selectedFile.value])

const totalAdditions = computed(() =>
  diffFiles.value.reduce((sum, f) => sum + f.additions, 0)
)
const totalDeletions = computed(() =>
  diffFiles.value.reduce((sum, f) => sum + f.deletions, 0)
)

function reviewCode() {
  alert('AI 将审查当前代码差异...')
}
</script>

<template>
  <div class="diff-pane">
    <PaneHeader
      type="diff"
      :extra="`+${totalAdditions} -${totalDeletions}`"
    >
      <template #extra>
        <button class="btn btn-ghost btn-sm" @click="reviewCode">Review code</button>
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
          <span class="file-icon">📄</span>
          <span class="file-path">{{ f.path }}</span>
          <span class="file-stats">
            <span class="add">+{{ f.additions }}</span>
            <span class="del">-{{ f.deletions }}</span>
          </span>
        </div>
      </div>

      <div class="diff-content scroll-container">
        <table class="diff-table">
          <tbody>
            <tr
              v-for="(line, i) in currentFile.lines"
              :key="i"
              :class="`line-${line.type}`"
            >
              <td class="line-no old">{{ line.oldLineNo || '' }}</td>
              <td class="line-no new">{{ line.newLineNo || '' }}</td>
              <td class="line-sign">
                {{ line.type === 'add' ? '+' : line.type === 'del' ? '-' : ' ' }}
              </td>
              <td class="line-content">{{ line.content }}</td>
            </tr>
          </tbody>
        </table>
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

.btn-sm {
  padding: 2px $space-sm;
  font-size: $font-size-xs;
}
</style>
