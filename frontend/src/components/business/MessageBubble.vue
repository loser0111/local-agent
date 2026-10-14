<script setup>
import { computed, ref, watch } from 'vue'
import { formatClock } from '@/utils/format'
import { renderMarkdown } from '@/utils/markdown'
import { useSessionStore } from '@/stores/session'
import { useChatStore } from '@/stores/chat'

const props = defineProps({
  message: { type: Object, required: true },
})

const sessionStore = useSessionStore()
const chatStore = useChatStore()

const isUser = computed(() => props.message.role === 'user')
const isSystem = computed(() => props.message.role === 'system')
const hasContent = computed(() => !!(props.message.content && props.message.content.trim()))
const hasThinking = computed(() => !!(props.message.thinking && props.message.thinking.trim()))

// 思考块展开/折叠。
//
// 策略与工具过程块（ToolProcess.vue）一致：**流式期间默认展开、结束后自动折叠**，
// 用户手动点过之后就不再自动跟随。理由：
//   - 生成中展开：模型"想"的这段时间里界面上必须有东西在动，否则用户以为卡住了；
//   - 结束后折叠：思考通常比正文长好几倍，留着展开会把回答顶到屏幕外。
const thinkingUserToggled = ref(false)
const thinkingExpanded = ref(!!props.message.streaming)

watch(
  () => props.message.streaming,
  (streaming) => {
    if (!thinkingUserToggled.value) thinkingExpanded.value = !!streaming
  }
)

function toggleThinking() {
  thinkingUserToggled.value = true
  thinkingExpanded.value = !thinkingExpanded.value
}

// AI 消息按 Markdown 渲染；用户消息保持纯文本
const renderedHtml = computed(() => renderMarkdown(props.message.content || ''))

// ===== 附件（图片）=====
//
// 图**不在**消息里（那会让会话 JSON 每次追加消息都重写几十 MB），消息只存引用；
// 字节按需向后端要，由 chat store 按 `${会话ID}:${附件ID}` 缓存。
//
// urls 的三种取值必须有区分，否则会闪一下"图片已丢失"：
//   undefined → 还在加载（显示占位）
//   ''        → 确实取不到（附件目录被清理过），显示"图片已丢失"
//   非空串    → data URL
const attachments = computed(() => props.message.attachments || [])
const urls = ref({})
const previewUrl = ref('')

function urlFor(id) {
  return urls.value[id] || ''
}

function isLoaded(id) {
  return urls.value[id] !== undefined
}

async function loadImages() {
  const sid = sessionStore.currentSessionId
  if (!sid) return
  let changed = false
  const next = { ...urls.value }
  for (const a of attachments.value) {
    if (next[a.id] !== undefined) continue
    next[a.id] = await chatStore.loadAttachmentUrl(sid, a.id)
    changed = true
  }
  if (changed) urls.value = next
}

watch(
  () => [sessionStore.currentSessionId, attachments.value.map((a) => a.id).join(',')],
  loadImages,
  { immediate: true }
)

// 点击放大：截图里的报错信息往往在缩略图里看不清
function openPreview(id) {
  const u = urlFor(id)
  if (u) previewUrl.value = u
}
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

      <!-- 图片附件（用户贴的图挂在自己的消息上） -->
      <div v-if="attachments.length" class="attachment-list">
        <div v-for="a in attachments" :key="a.id" class="attachment">
          <img
            v-if="urlFor(a.id)"
            :src="urlFor(a.id)"
            :alt="a.name"
            :title="`${a.name}（点击放大）`"
            @click="openPreview(a.id)"
          />
          <div v-else-if="isLoaded(a.id)" class="attachment-missing" :title="a.name">
            图片已丢失
          </div>
          <div v-else class="attachment-loading">图片加载中…</div>
        </div>
      </div>

      <!-- Thinking 块（仅 AI 消息且存在 thinking 时显示）。
           必须排在正文**之前**：思考是"怎么得出答案"的过程，阅读顺序上先于答案；
           放在下方会让用户先读到结论、再回头看过程，与 Claude / DeepSeek 的惯例相反。 -->
      <div v-if="!isUser && hasThinking" class="thinking-block" :class="{ 'thinking-live': message.streaming }">
        <div class="thinking-header" @click="toggleThinking">
          <span class="thinking-toggle">{{ thinkingExpanded ? '▼' : '▶' }}</span>
          <span class="thinking-label">💭 思考过程</span>
          <span v-if="message.streaming" class="thinking-live-hint">思考中…</span>
        </div>
        <div v-if="thinkingExpanded" class="thinking-content">
          <div class="thinking-text">{{ message.thinking }}</div>
        </div>
      </div>

      <!-- 用户消息：纯文本展示 -->
      <div v-if="isUser && hasContent" class="message-content">
        <pre class="content-text">{{ message.content }}</pre>
      </div>

      <!-- AI 消息：Markdown 渲染 -->
      <div v-else-if="!isUser && hasContent" class="message-content markdown-body" v-html="renderedHtml"></div>

      <!-- 流式等待光标 -->
      <span v-if="message.streaming && !hasContent" class="cursor">▊</span>
      <span v-else-if="message.streaming" class="cursor cursor-inline">▊</span>
    </div>
  </div>

  <!-- 点击放大：fixed 定位，脱离聊天流的滚动容器 -->
  <div v-if="previewUrl" class="image-preview" @click="previewUrl = ''">
    <img :src="previewUrl" alt="预览" @click.stop />
    <button class="preview-close" title="关闭" @click="previewUrl = ''">×</button>
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
  background-color: $color-surface-tint;
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
  box-shadow: 0 0 0 1px rgb(var(--color-info-rgb) / 0.2);
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

/* ===== 图片附件 ===== */
.attachment-list {
  display: flex;
  flex-wrap: wrap;
  gap: $space-sm;
  margin-bottom: $space-sm;
}

.attachment img {
  display: block;
  max-width: 260px;
  max-height: 200px;
  border-radius: $radius-md;
  border: 1px solid $color-border;
  cursor: zoom-in;
  background-color: $color-bg-tertiary;
}

.attachment-missing,
.attachment-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 120px;
  height: 72px;
  padding: 0 $space-md;
  font-size: $font-size-xs;
  border: 1px dashed $color-border;
  border-radius: $radius-md;
  color: $color-text-muted;
  background-color: $color-bg-secondary;
}

/* 点击放大的遮罩：fixed 定位，不受聊天流滚动容器裁剪 */
.image-preview {
  position: fixed;
  inset: 0;
  z-index: 200;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: $space-xl;
  background-color: rgba(0, 0, 0, 0.72);
  cursor: zoom-out;

  img {
    max-width: 100%;
    max-height: 100%;
    border-radius: $radius-md;
    box-shadow: 0 8px 40px rgba(0, 0, 0, 0.5);
    cursor: default;
  }
}

.preview-close {
  position: absolute;
  top: $space-lg;
  right: $space-lg;
  width: 32px;
  height: 32px;
  font-size: 20px;
  line-height: 1;
  color: #fff;
  background-color: rgba(255, 255, 255, 0.14);
  border-radius: 50%;

  &:hover {
    background-color: rgba(255, 255, 255, 0.26);
  }
}

.cursor {
  color: $color-primary;
  animation: blink 1s step-end infinite;
  text-shadow: $glow-primary;
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

/* ===== Thinking 块样式 [新增] ===== */
.thinking-block {
  margin-bottom: $space-md;
  background-color: rgba(139, 92, 246, 0.05);
  border: 1px solid rgba(139, 92, 246, 0.2);
  border-left: 3px solid rgb(139, 92, 246);
  border-radius: $radius-md;
  overflow: hidden;
}

.thinking-header {
  padding: $space-md;
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: $space-sm;
  user-select: none;
  transition: background-color 0.2s ease;

  &:hover {
    background-color: rgba(139, 92, 246, 0.08);
  }
}

.thinking-toggle {
  display: inline-block;
  width: 16px;
  text-align: center;
  font-size: 12px;
  color: rgb(139, 92, 246);
  transition: transform 0.2s ease;
}

.thinking-label {
  font-weight: $font-weight-semibold;
  font-size: $font-size-sm;
  color: rgb(139, 92, 246);
}

/* 流式进行中的提示：让"模型正在想"这件事看得见 */
.thinking-live-hint {
  font-size: $font-size-xs;
  color: $color-text-muted;
  animation: thinkingPulse 1.4s ease-in-out infinite;
}

.thinking-live {
  border-left-color: rgb(139, 92, 246);
  box-shadow: 0 0 0 1px rgba(139, 92, 246, 0.15);
}

@keyframes thinkingPulse {
  0%,
  100% {
    opacity: 0.45;
  }
  50% {
    opacity: 1;
  }
}

.thinking-content {
  padding: $space-md;
  padding-top: 0;
  background-color: rgba(139, 92, 246, 0.02);
  border-top: 1px solid rgba(139, 92, 246, 0.1);
  animation: slideDown 0.3s ease;
}

.thinking-text {
  font-size: $font-size-sm;
  line-height: $line-height-lg;
  color: $color-text-primary;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: 'JetBrains Mono', 'Consolas', 'Courier New', monospace;
  background-color: rgba(139, 92, 246, 0.03);
  padding: $space-md;
  border-radius: $radius-sm;
}

@keyframes slideDown {
  from {
    opacity: 0;
    max-height: 0;
  }
  to {
    opacity: 1;
    max-height: 1000px;
  }
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
      background-color: $color-code-inline-bg;
      color: $color-code-inline-text;
      padding: 2px 6px;
      border-radius: 4px;
      white-space: nowrap;
    }

    // 代码块
    pre.md-code-block {
      position: relative;
      background-color: $color-code-bg;
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
        color: $color-code-text;
        white-space: pre;
      }
    }

    // highlight.js Dracula 配色
    .hljs {
      color: $color-code-text;

      &-comment,
      &-quote {
        color: $color-code-comment;
        font-style: italic;
      }

      &-keyword,
      &-selector-tag,
      &-literal,
      &-section,
      &-link {
        color: $color-code-keyword;
      }

      &-function,
      &-title.function_ {
        color: $color-code-function;
      }

      &-string,
      &-attr,
      &-template-string,
      &-regexp,
      &-addition {
        color: $color-code-string;
      }

      &-number,
      &-meta {
        color: $color-code-number;
      }

      &-title,
      &-name,
      &-type,
      &-built_in,
      &-class .hljs-title {
        color: $color-code-type;
      }

      &-attr {
        color: $color-code-function;
      }

      &-symbol,
      &-bullet,
      &-variable,
      &-template-variable {
        color: $color-code-text;
      }

      &-comment {
        color: $color-code-comment;
      }

      &-deletion {
        color: $color-code-deletion;
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
      background-color: rgb(var(--color-primary-rgb) / 0.08);
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
      background-color: $color-bg-tertiary;
      color: $color-text-primary;
      font-weight: $font-weight-semibold;
    }

    td {
      color: $color-text-secondary;
    }

    tr:nth-child(even) td {
      background-color: $color-surface-tint;
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
