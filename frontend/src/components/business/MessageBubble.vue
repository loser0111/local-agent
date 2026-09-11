<script setup>
import { computed } from 'vue'
import { formatClock } from '@/utils/format'
import { renderMarkdown } from '@/utils/markdown'

const props = defineProps({
  message: { type: Object, required: true },
})

const isUser = computed(() => props.message.role === 'user')
const isSystem = computed(() => props.message.role === 'system')
const hasContent = computed(() => !!(props.message.content && props.message.content.trim()))

// AI 消息按 Markdown 渲染；用户消息保持纯文本
const renderedHtml = computed(() => renderMarkdown(props.message.content || ''))
</script>

<template>
  <div v-if="isSystem" class="message-system">
    <span class="system-text">{{ message.content }}</span>
  </div>

  <div v-else class="message" :class="{ 'message-user': isUser, 'message-assistant': !isUser }">
    <div class="avatar" :class="isUser ? 'avatar-user' : 'avatar-assistant'">
      <span v-if="isUser">我</span>
      <span v-else>AI</span>
    </div>

    <div class="message-body">
      <div class="message-meta">
        <span class="sender">{{ isUser ? '你' : 'Assistant' }}</span>
        <span class="time">{{ formatClock(message.createdAt) }}</span>
      </div>

      <!-- 用户消息：纯文本展示 -->
      <div v-if="isUser && message.content" class="message-content">
        <pre class="content-text">{{ message.content }}</pre>
      </div>

      <!-- AI 消息：Markdown 渲染 -->
      <div v-else-if="hasContent" class="message-content markdown-body" v-html="renderedHtml"></div>

      <!-- 流式等待光标 -->
      <span v-if="message.streaming && !hasContent" class="cursor">▊</span>
      <span v-else-if="message.streaming" class="cursor cursor-inline">▊</span>
    </div>
  </div>
</template>

<style scoped lang="scss">
.message {
  display: flex;
  gap: $space-md;
  padding: $space-md $space-lg;
}

.message-user {
  .avatar-user {
    background-color: $color-primary;
  }
}

.message-assistant {
  background-color: rgba(255, 255, 255, 0.02);
}

.avatar {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: $font-size-xs;
  font-weight: $font-weight-bold;
  color: #fff;
  flex-shrink: 0;
}

.avatar-assistant {
  background-color: $color-info;
}

.message-body {
  flex: 1;
  min-width: 0;
}

.message-meta {
  display: flex;
  align-items: center;
  gap: $space-sm;
  margin-bottom: $space-xs;
}

.sender {
  font-size: $font-size-sm;
  font-weight: $font-weight-semibold;
  color: $color-text-primary;
}

.time {
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.message-content {
  font-size: $font-size-sm;
  line-height: $line-height-lg;
  color: $color-text-primary;
  word-break: break-word;
}

.content-text {
  font-family: inherit;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

.cursor {
  color: $color-primary;
  animation: blink 1s step-end infinite;
}

.cursor-inline {
  margin-left: 2px;
}

@keyframes blink {
  50% {
    opacity: 0;
  }
}

.message-system {
  padding: $space-md $space-lg;
  text-align: center;
}

.system-text {
  font-size: $font-size-xs;
  color: $color-text-muted;
}

/* ===== Markdown 正文样式（IDEA Dracula Dark 配色）===== */
.markdown-body {
  // v-html 内容需要 :deep 穿透 scoped 样式
  :deep() {
    font-size: $font-size-sm;
    line-height: 1.7;
    color: $color-text-primary;

    > *:first-child {
      margin-top: 0;
    }

    > *:last-child {
      margin-bottom: 0;
    }

    p {
      margin: 8px 0;
    }

    h1, h2, h3, h4, h5, h6 {
      color: $color-text-primary;
      font-weight: $font-weight-semibold;
      line-height: 1.3;
      margin: 20px 0 10px;

      &:first-child {
        margin-top: 0;
      }
    }

    h1 {
      font-size: 1.5em;
      padding-bottom: 6px;
      border-bottom: 1px solid $color-border;
    }

    h2 {
      font-size: 1.3em;
      padding-bottom: 5px;
      border-bottom: 1px solid $color-border;
    }

    h3 {
      font-size: 1.15em;
    }

    h4 {
      font-size: 1em;
    }

    a {
      color: $color-info;
      text-decoration: none;

      &:hover {
        text-decoration: underline;
      }
    }

    strong {
      color: $color-warning;
      font-weight: $font-weight-semibold;
    }

    em {
      color: $color-text-primary;
    }

    del {
      color: $color-text-muted;
    }

    // 行内代码
    code:not(.hljs) {
      font-family: 'JetBrains Mono', 'Consolas', 'Courier New', monospace;
      font-size: 0.875em;
      background-color: #44475a;
      color: #ff79c6;
      padding: 2px 6px;
      border-radius: 4px;
      white-space: nowrap;
    }

    // 代码块
    pre.md-code-block {
      position: relative;
      background-color: #282a36;
      border: 1px solid $color-border;
      border-radius: 6px;
      padding: 12px 14px;
      margin: 10px 0;
      overflow-x: auto;

      // 右上角语言标签
      &[data-lang]::after {
        content: attr(data-lang);
        position: absolute;
        top: 6px;
        right: 10px;
        font-size: 11px;
        color: $color-text-muted;
        text-transform: uppercase;
        letter-spacing: 0.5px;
      }

      code.hljs {
        font-family: 'JetBrains Mono', 'Consolas', 'Courier New', monospace;
        font-size: 13px;
        line-height: 1.6;
        background: transparent;
        padding: 0;
        color: #f8f8f2;
        white-space: pre;
      }
    }

    // highlight.js Dracula 配色
    .hljs {
      color: #f8f8f2;

      &-comment,
      &-quote {
        color: #6272a4;
        font-style: italic;
      }

      &-keyword,
      &-selector-tag,
      &-literal,
      &-section,
      &-link {
        color: #ff79c6;
      }

      &-function,
      &-title.function_ {
        color: #50fa7b;
      }

      &-string,
      &-attr,
      &-template-string,
      &-regexp,
      &-addition {
        color: #f1fa8c;
      }

      &-number,
      &-meta {
        color: #bd93f9;
      }

      &-title,
      &-name,
      &-type,
      &-built_in,
      &-class .hljs-title {
        color: #8be9fd;
      }

      &-attr {
        color: #50fa7b;
      }

      &-symbol,
      &-bullet,
      &-variable,
      &-template-variable {
        color: #f8f8f2;
      }

      &-comment {
        color: #6272a4;
      }

      &-deletion {
        color: #ff5555;
      }

      &-emphasis {
        font-style: italic;
      }

      &-strong {
        font-weight: bold;
      }
    }

    // 引用块
    blockquote {
      margin: 10px 0;
      padding: 6px 14px;
      border-left: 3px solid $color-primary;
      background-color: rgba(189, 147, 249, 0.08);
      color: $color-text-secondary;

      p {
        margin: 4px 0;
      }
    }

    // 无序列表 / 有序列表
    ul, ol {
      margin: 8px 0;
      padding-left: 24px;

      li {
        margin: 4px 0;

        &::marker {
          color: $color-text-muted;
        }
      }
    }

    ul {
      list-style: disc;
    }

    ol {
      list-style: decimal;
    }

    // 任务列表
    li:has(> input[type='checkbox']) {
      list-style: none;
      margin-left: -18px;

      input[type='checkbox'] {
        margin-right: 6px;
        accent-color: $color-primary;
      }
    }

    // 表格
    table {
      width: 100%;
      border-collapse: collapse;
      margin: 12px 0;
      font-size: 13px;
      display: block;
      overflow-x: auto;
    }

    th, td {
      border: 1px solid $color-border;
      padding: 8px 12px;
      text-align: left;
    }

    th {
      background-color: #44475a;
      color: $color-text-primary;
      font-weight: $font-weight-semibold;
    }

    td {
      color: $color-text-secondary;
    }

    tr:nth-child(even) td {
      background-color: rgba(255, 255, 255, 0.02);
    }

    // 分隔线
    hr {
      border: none;
      border-top: 1px solid $color-border;
      margin: 16px 0;
    }

    // 图片
    img {
      max-width: 100%;
      border-radius: 6px;
    }
  }
}
</style>
