<script setup>
import { ref, computed, nextTick, watch, onMounted, onUnmounted } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useChatStore } from '@/stores/chat'
import { usePaneStore } from '@/stores/pane'
import { useDiffStore } from '@/stores/diff'
import { appendMessage, appendConversation, chat, onDiffUpdate } from '@/api/session'
import PaneHeader from '@/components/layout/PaneHeader.vue'
import MessageBubble from '@/components/business/MessageBubble.vue'
import ToolProcess from '@/components/business/ToolProcess.vue'

const sessionStore = useSessionStore()
const chatStore = useChatStore()
const paneStore = usePaneStore()
const diffStore = useDiffStore()

// 订阅后端 diff 实时推送（有文件改动时刷新差异数据）
let offDiff = null
onMounted(() => {
  offDiff = onDiffUpdate((payload) => diffStore.applyUpdate(payload))
})
onUnmounted(() => {
  if (offDiff) offDiff()
})

const input = ref('')
const messagesContainer = ref(null)
const autoScroll = ref(true)

const sessionId = computed(() => sessionStore.currentSessionId)
const messages = computed(() => chatStore.messages)

/**
 * 展示分组：把一次问答中连续的多轮工具调用消息（无正文的 assistant+toolCalls）
 * 合并为单个"思考过程"折叠块，用户消息和模型最终回复保持独立展示。
 *
 * 会话文件中的消息序列：
 *   user → assistant(toolCalls) → tool(result) → assistant(toolCalls) → tool → assistant(最终回复)
 * （role=tool 已在 chat store 加载时过滤）
 * 展示为：
 *   user → [一个折叠块，含全部工具调用] → assistant(最终回复)
 */
const displayItems = computed(() => {
  const items = []
  for (const msg of messages.value) {
    const hasText = !!(msg.content && msg.content.trim())
    const hasToolCalls = !!(msg.toolCalls && msg.toolCalls.length)
    const isAssistantProcess =
      msg.role === 'assistant' &&
      !hasText &&
      (hasToolCalls || msg.streaming) // 流式占位消息（首个工具调用尚未产生时）也归入过程组

    if (isAssistantProcess) {
      const last = items[items.length - 1]
      if (last && last.type === 'process') {
        // 合并到前一个过程组
        last.toolCalls.push(...(msg.toolCalls || []))
        last.streaming = last.streaming || !!msg.streaming
      } else {
        items.push({
          type: 'process',
          key: `process-${msg.id}`,
          toolCalls: [...(msg.toolCalls || [])],
          streaming: !!msg.streaming,
        })
      }
    } else if (msg.role === 'assistant' && hasText && hasToolCalls) {
      // 兜底：正文与工具调用共存时，工具调用并入相邻过程组，正文独立展示
      const last = items[items.length - 1]
      if (last && last.type === 'process') {
        last.toolCalls.push(...msg.toolCalls)
        last.streaming = last.streaming || !!msg.streaming
      } else {
        items.push({
          type: 'process',
          key: `process-${msg.id}`,
          toolCalls: [...msg.toolCalls],
          streaming: !!msg.streaming,
        })
      }
      items.push({ type: 'message', key: `message-${msg.id}`, message: msg })
    } else {
      items.push({ type: 'message', key: `message-${msg.id}`, message: msg })
    }
  }
  return items
})

// 自动滚动到底部
watch(
  () => messages.value.length,
  async () => {
    if (autoScroll.value) {
      await nextTick()
      scrollToBottom()
    }
  }
)

watch(
  () =>
    messages.value
      .map((m) => m.content + (m.toolCalls || []).map((t) => t.status).join(','))
      .join('|'),
  async () => {
    if (autoScroll.value) {
      await nextTick()
      scrollToBottom()
    }
  }
)

// 消费外部面板（如 DiffPane Review code）请求填入的提示词
watch(
  () => chatStore.pendingPrompt,
  (t) => {
    if (t) {
      input.value = chatStore.consumePrompt()
    }
  }
)

function scrollToBottom() {
  if (messagesContainer.value) {
    messagesContainer.value.scrollTop = messagesContainer.value.scrollHeight
  }
}

function handleScroll() {
  const el = messagesContainer.value
  if (!el) return
  const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 50
  autoScroll.value = nearBottom
}

async function sendMessage() {
  const text = input.value.trim()
  const sid = sessionId.value
  if (!text || !sid || chatStore.isGenerating) return

  input.value = ''
  chatStore.isGenerating = true
  const startAt = Date.now()

  try {
    // 1. 持久化用户消息（后端补全 id/createdAt 后同步到本地）
    const savedUserMsg = await appendMessage(sid, { role: 'user', content: text })
    chatStore.addLocalMessage(savedUserMsg)
    // 本地同步会话标题与活跃时间
    const cur = sessionStore.currentSession
    if (cur && (!cur.title || cur.title === '新会话')) {
      const flat = text.replace(/\s+/g, ' ').trim()
      sessionStore.touchSession(sid, {
        title: flat.length > 30 ? flat.slice(0, 30) + '...' : flat,
      })
    } else {
      sessionStore.touchSession(sid)
    }

    // 2. 创建本地 AI 消息占位（流式显示用，展示工具调用中间状态）
    const localMsg = chatStore.addLocalMessage({
      id: `local-${Date.now()}`,
      role: 'assistant',
      content: '',
      toolCalls: [],
      createdAt: Date.now(),
      streaming: true,
    })
    streamingMessageId.value = localMsg.id

    // 3. 调用后端 Chat 方法（真实 LLM 调用 + 工具执行）
    // 后端会持久化所有中间消息（assistant+tool_calls, tool结果, 最终回复）
    const result = await chat(sid, text, {
      onToolCallStart: (tc) => {
        chatStore.addToolCall(localMsg.id, tc)
      },
      onToolCallEnd: (tc) => {
        chatStore.updateToolCall(localMsg.id, tc.id, {
          status: tc.status,
          duration: tc.duration,
          result: tc.result,
        })
      },
    })

    // 4. 移除流式占位消息，用后端持久化的消息替换
    const placeholderIdx = chatStore.messages.findIndex((m) => m.id === streamingMessageId.value)
    if (placeholderIdx > -1) {
      chatStore.messages.splice(placeholderIdx, 1)
    }

    // 5. 添加后端持久化的消息（过滤 role=tool，工具结果已显示在 toolCalls 卡片中）
    if (result.messages && result.messages.length > 0) {
      for (const msg of result.messages) {
        if (msg.role !== 'tool') {
          chatStore.addLocalMessage({ ...msg, streaming: false })
        }
      }
    }

    // 6. 如果有错误且没有回复，追加错误消息
    if (result.error && !result.reply) {
      chatStore.addLocalMessage({
        id: `error-${Date.now()}`,
        role: 'assistant',
        content: `调用失败：${result.error}`,
        createdAt: Date.now(),
        streaming: false,
      })
    }

    // 7. 记录本轮对话（借鉴 01agent 的 Conversations）
    await appendConversation(sid, {
      query: text,
      answer: result.reply || '',
      startTime: startAt,
      endTime: Date.now(),
    })

    sessionStore.touchSession(sid)

    // 8. 联动 DiffPane：重新加载差异，有改动则自动展开右侧面板
    try {
      const changed = await diffStore.load(sid)
      if (changed > 0) paneStore.openPane(sid, 'diff')
    } catch (diffErr) {
      console.warn('加载 diff 失败:', diffErr)
    }
  } catch (e) {
    console.error('发送消息失败:', e)
    if (streamingMessageId.value) {
      chatStore.updateLocalMessage(streamingMessageId.value, {
        content: `发送失败：${e.message || e}`,
        streaming: false,
      })
    } else {
      alert(`发送失败：${e.message || e}`)
    }
  } finally {
    chatStore.isGenerating = false
    streamingMessageId.value = null
  }
}

function stopGeneration() {
  chatStore.isGenerating = false
}

function handleKeydown(e) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    sendMessage()
  }
}

const streamingMessageId = ref(null)

function openDiff() {
  if (sessionId.value) {
    paneStore.openPane(sessionId.value, 'diff')
  }
}
</script>

<template>
  <div class="chat-pane">
    <PaneHeader type="chat" :closable="false" />

    <div class="messages scroll-container" ref="messagesContainer" @scroll="handleScroll">
      <div v-if="chatStore.loadingHistory" class="loading-history">历史消息加载中...</div>

      <div v-else-if="messages.length === 0" class="welcome">
        <div class="welcome-icon">🤖</div>
        <h2>有什么可以帮你的？</h2>
        <p>输入你的需求，我将帮你编写、修改和审查代码。</p>
        <div class="quick-actions">
          <button class="quick-btn" @click="input = '帮我分析这个项目的结构'">
            帮我分析这个项目的结构
          </button>
          <button class="quick-btn" @click="input = '实现一个用户登录功能'">
            实现一个用户登录功能
          </button>
        </div>
      </div>

      <template v-for="item in displayItems" :key="item.key">
        <!-- 普通消息：用户消息 / 模型最终回复 -->
        <MessageBubble
          v-if="item.type === 'message'"
          :message="item.message"
        />

        <!-- 多轮工具调用合并后的单个折叠块 -->
        <div v-else class="process-row" :class="{ streaming: item.streaming }">
          <div class="avatar avatar-assistant">
            <span>AI</span>
          </div>
          <div class="process-row-body">
            <ToolProcess
              :tool-calls="item.toolCalls"
              :streaming="item.streaming"
            />
          </div>
        </div>
      </template>
    </div>

    <div class="prompt-box">
      <div class="prompt-toolbar">
        <button class="tool-btn" title="附件">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M12 4L4 12M4 4l8 8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" transform="rotate(45 8 8)" />
            <path d="M5 11l-2 2 2 2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
        <button class="tool-btn" title="@提及文件">@</button>
        <button class="tool-btn" title="更多">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <circle cx="3" cy="8" r="1.2" fill="currentColor" />
            <circle cx="8" cy="8" r="1.2" fill="currentColor" />
            <circle cx="13" cy="8" r="1.2" fill="currentColor" />
          </svg>
        </button>
        <button class="tool-btn diff-btn" title="查看文件差异" @click="openDiff">
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none">
            <path d="M8 2v12M2 8h12" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
          </svg>
          Diff
        </button>
        <button v-if="chatStore.isGenerating" class="tool-btn stop-btn" @click="stopGeneration">
          <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor">
            <rect x="2" y="2" width="10" height="10" rx="1" />
          </svg>
          停止
        </button>
      </div>

      <textarea
        v-model="input"
        class="prompt-input"
        placeholder="输入消息... (Enter 发送，Shift+Enter 换行)"
        rows="1"
        @keydown="handleKeydown"
      />

      <div class="prompt-footer">
        <button
          class="btn btn-primary send-btn"
          :disabled="!input.trim() || chatStore.isGenerating || !sessionId"
          @click="sendMessage"
        >
          发送
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.chat-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: $color-bg-primary;
}

.messages {
  flex: 1;
  overflow-y: auto;
  padding: $space-lg 0;
}

// 多轮工具调用合并行：与消息行布局对齐（头像 + 折叠块）
.process-row {
  display: flex;
  gap: $space-md;
  padding: 6px $space-lg;
}

.process-row-body {
  flex: 1;
  min-width: 0;
}

.process-row .avatar {
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
  background-color: $color-info;
}

// 执行过程中头像轻微高亮
.process-row.streaming .avatar {
  box-shadow: 0 0 0 2px rgba(189, 147, 249, 0.25);
}

.loading-history {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: $color-text-muted;
  font-size: $font-size-sm;
}

.welcome {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  padding: $space-2xl;
  text-align: center;

  .welcome-icon {
    font-size: 48px;
    margin-bottom: $space-lg;
  }

  h2 {
    font-size: $font-size-xl;
    font-weight: $font-weight-bold;
    margin-bottom: $space-sm;
  }

  p {
    font-size: $font-size-sm;
    color: $color-text-secondary;
    margin-bottom: $space-xl;
  }
}

.quick-actions {
  display: flex;
  flex-wrap: wrap;
  gap: $space-sm;
  justify-content: center;
}

.quick-btn {
  padding: $space-sm $space-lg;
  font-size: $font-size-sm;
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  color: $color-text-primary;
  transition: all $transition-fast;

  &:hover {
    border-color: $color-primary;
    background-color: rgba(189, 147, 249, 0.1);
  }
}

.prompt-box {
  border-top: 1px solid $color-border;
  background-color: $color-bg-secondary;
  padding: $space-md;
}

.prompt-toolbar {
  display: flex;
  align-items: center;
  gap: $space-xs;
  margin-bottom: $space-sm;
}

.tool-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: $space-xs;
  padding: $space-xs $space-sm;
  font-size: $font-size-sm;
  border-radius: $radius-sm;
  color: $color-text-secondary;
  transition: all $transition-fast;

  &:hover {
    background-color: $color-bg-tertiary;
    color: $color-text-primary;
  }

  &.stop-btn {
    color: $color-error;
    &:hover {
      background-color: rgba(255, 85, 85, 0.1);
    }
  }
}

.prompt-input {
  width: 100%;
  min-height: 40px;
  max-height: 200px;
  padding: $space-sm $space-md;
  font-size: $font-size-sm;
  line-height: $line-height-md;
  background-color: $color-bg-primary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  color: $color-text-primary;
  resize: none;
  outline: none;
  transition: border-color $transition-fast;

  &:focus {
    border-color: $color-primary;
    box-shadow: 0 0 0 2px rgba(189, 147, 249, 0.2);
  }

  &::placeholder {
    color: $color-text-muted;
  }
}

.prompt-footer {
  display: flex;
  justify-content: flex-end;
  margin-top: $space-sm;
}

.send-btn {
  min-width: 64px;
}
</style>
