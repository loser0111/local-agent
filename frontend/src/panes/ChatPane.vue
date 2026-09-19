<script setup>
import { ref, computed, nextTick, watch, onMounted, onUnmounted } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useChatStore } from '@/stores/chat'
import { usePaneStore } from '@/stores/pane'
import { useDiffStore } from '@/stores/diff'
import { useSettingStore } from '@/stores/setting'
import { usePlanStore } from '@/stores/plan'
import { appendMessage, appendConversation, chat, stopChat, getContextStat, onDiffUpdate } from '@/api/session'
import { fetchPendingInteraction } from '@/api/interaction'
import { DEFAULT_VIEW_MODE } from '@/types'
import { usePermissionStore } from '@/stores/permissions'
import { useAskStore } from '@/stores/asks'
import { useSkillsStore } from '@/stores/skills'
import { useUiStore } from '@/stores/ui'
import RequestPreviewDialog from '@/components/business/RequestPreviewDialog.vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'
import MessageBubble from '@/components/business/MessageBubble.vue'
import ToolProcess from '@/components/business/ToolProcess.vue'

const sessionStore = useSessionStore()
const chatStore = useChatStore()
const paneStore = usePaneStore()
const diffStore = useDiffStore()
const settingStore = useSettingStore()
const planStore = usePlanStore()
const permissionStore = usePermissionStore()
const askStore = useAskStore()
const skillsStore = useSkillsStore()
const ui = useUiStore()

// 计划模式（单次意图，非全局偏好）：开启后下一条消息走规划流程
const planMode = ref(false)
// 停止请求已发出、正在等后端收尾。此时再点「停止」升级为硬取消。
const stopping = ref(false)

// 请求快照查看（排障用）：显示实际发出的 message 列表
const reqPreviewVisible = ref(false)

// 上下文用量（后端算的近似值）。用于显示占用比例；点它可直接触发压缩。
const contextStat = ref(null)

function updateContextStat(st) {
  if (st) contextStat.value = st
}

const contextPercent = computed(() => {
  const st = contextStat.value
  if (!st || !st.windowTokens) return 0
  return Math.round((st.usedTokens / st.windowTokens) * 100)
})

const contextTitle = computed(() => {
  const st = contextStat.value
  if (!st) return ''
  const lines = [
    `上下文用量约 ${contextPercent.value}%（≈${st.usedTokens} / ${st.windowTokens} token，${st.messageCount} 条消息）`,
  ]
  if (st.coveredMsgs > 0) {
    lines.push(`已压缩：摘要覆盖前 ${st.coveredMsgs} 条消息（${st.summaryChars} 字），原文仍保留`)
  }
  lines.push('点击立即压缩（等价于输入 /compact）')
  return lines.join('\n')
})

// 点用量指示 = 手动压缩：复用发送路径，后端把 /compact 当上下文维护操作拦截
function requestCompact() {
  if (!sessionId.value || chatStore.isGenerating) return
  input.value = '/compact'
  sendMessage()
}
const plan = computed(() => planStore.plan)

// 订阅后端 diff 实时推送（有文件改动时刷新差异数据）
let offDiff = null
onMounted(() => {
  offDiff = onDiffUpdate((payload) => diffStore.applyUpdate(payload))
  // 「/技能名」补全需要技能清单；加载失败不影响正常聊天
  skillsStore.load().catch(() => {})
})
onUnmounted(() => {
  if (offDiff) offDiff()
  stopInteractionPolling()
})
const input = ref('')
const messagesContainer = ref(null)
const autoScroll = ref(true)

const sessionId = computed(() => sessionStore.currentSessionId)
const messages = computed(() => chatStore.messages)
// 视图模式（会话级）：控制工具调用过程块的展示粒度；旧会话无该字段时兜底为默认值
const viewMode = computed(() => sessionStore.currentSession?.viewMode || DEFAULT_VIEW_MODE)

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

// 切换会话时恢复该会话挂起的提问（后端仍在阻塞等待，弹窗必须补回来）
watch(
  sessionId,
  (id) => {
    askStore.loadPending(id)
    // 上下文用量是「打开会话就该看到」的信息，跟着会话一起加载。
    // 它在后端是近似值，取不到就不显示，不打扰用户。
    contextStat.value = null
    if (id) getContextStat(id).then(updateContextStat)
  },
  { immediate: true }
)

// 运行期间轮询“是否有人在等我应答”作为兜底：事件万一没送达（前后端产物版本错配、
// 运行时时序），弹窗仍然会补出来，而不是让工具卡片一直卡在“运行中”直到超时。
let interactionTimer = null

function stopInteractionPolling() {
  if (interactionTimer) {
    clearInterval(interactionTimer)
    interactionTimer = null
  }
}

async function syncPendingInteraction() {
  const sid = sessionId.value
  if (!sid) return
  const p = await fetchPendingInteraction(sid)
  if (!p) return
  if (p.kind === 'permission' && p.permission && permissionStore.pending?.id !== p.permission.id) {
    permissionStore.setPending(p.permission)
  } else if (p.kind === 'ask' && p.ask && askStore.pending?.id !== p.ask.id) {
    askStore.setPending(p.ask)
  }
}

// 模型运行（含计划执行）期间才轮询：闲置时不做任何额外请求
watch(
  () => chatStore.isGenerating,
  (generating) => {
    if (generating) {
      if (!interactionTimer) interactionTimer = setInterval(syncPendingInteraction, 1200)
      syncPendingInteraction()
    } else {
      stopInteractionPolling()
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
  stopping.value = false
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

    // 计划模式：走规划流程产出可审核的计划（平凡请求由后端直接回复）
    if (planMode.value) {
      const result = await chat(sid, text, {
        stream: false, // 规划器固定非流式；免计划直答取完整回复
        plan: true,
        onPlanUpdate: (p) => planStore.applyUpdate(p),
      })
      updateContextStat(result?.context)
      if (result?.plan) {
        planStore.applyUpdate(result.plan)
        planMode.value = false // 计划是单次意图，生成后自动复位
        paneStore.openPane(sid, 'plan')
      } else if (result?.reply) {
        chatStore.addLocalMessage({
          id: `assistant-${Date.now()}`,
          role: 'assistant',
          content: result.reply,
          createdAt: Date.now(),
          streaming: false,
        })
      } else if (result?.error) {
        chatStore.addLocalMessage({
          id: `plan-gen-error-${Date.now()}`,
          role: 'assistant',
          content: `生成计划失败：${result.error}`,
          createdAt: Date.now(),
          streaming: false,
        })
      }
      sessionStore.touchSession(sid)
      return
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
      stream: settingStore.settings.streamResponse,
      onReplyDelta: (chunk) => {
        chatStore.appendStreamContent(localMsg.id, chunk)
      },
      onToolCallStart: (tc) => {
        chatStore.addToolCall(localMsg.id, tc)
        // 派生子代理时自动打开子代理面板：子代理的中间过程不在聊天流里（它跑在自己
        // 独立的上下文里，只有结论会回到聊天），不打开面板用户就完全看不到它在做什么
        if (tc?.name === 'spawn_agent') paneStore.openPane(sid, 'subagent')
      },
      onToolCallEnd: (tc) => {
        chatStore.updateToolCall(localMsg.id, tc.id, {
          status: tc.status,
          duration: tc.duration,
          result: tc.result,
        })
      },
      // 被用户停止：立刻收尾流式气泡，否则它会一直停在"正在输入"
      onCancelled: () => {
        if (streamingMessageId.value) {
          chatStore.updateLocalMessage(streamingMessageId.value, { streaming: false })
        }
      },
    })
    updateContextStat(result?.context)

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

    // 6. 被取消 / 出错。取消是用户的主动行为，不能显示成"调用失败"——
    //    这也是后端把取消从错误路径里摘出来的原因（见 App.Chat）。
    if (result.cancelled) {
      chatStore.addLocalMessage({
        id: `cancelled-${Date.now()}`,
        role: 'assistant',
        content: result.cancelKind === 'hard' ? '已强制停止。' : '已停止（当前步骤执行完即停）。',
        createdAt: Date.now(),
        streaming: false,
      })
    } else if (result.error && !result.reply) {
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
    stopping.value = false
    streamingMessageId.value = null
  }
}

/**
 * 把「正在运行」的工具卡片标为等待授权（后端阻塞在授权/提问上）。
 * 工具循环是串行执行的，所以匹配最后一个同名 running 卡片是准确的
 * （事件里没有工具调用 ID）；从后往前找也天然适配计划执行期间的多条消息。
 */
function markPendingToolCard(tool) {
  if (!tool) return
  const msgs = chatStore.messages
  for (let mi = msgs.length - 1; mi >= 0; mi--) {
    const calls = msgs[mi].toolCalls || []
    for (let i = calls.length - 1; i >= 0; i--) {
      if (calls[i].status === 'running' && calls[i].name === tool) {
        chatStore.updateToolCall(msgs[mi].id, calls[i].id, { status: 'pending' })
        return
      }
    }
  }
}

// 两级停止：
//   第一次点 → 软取消。后端在当前 LLM 请求与工具跑完后于下一轮边界停下。
//   已在停止中再点 → 硬取消。后端 cancel 运行 ctx：在途请求立即断开、
//   正在跑的子进程被 kill、等待中的提问立即以"取消"结束。
// 改造前这里只把 chatStore.isGenerating 置 false——纯客户端标志位，
// 后端那一轮循环仍在跑、仍在写文件、仍在起子进程。
async function stopGeneration() {
  // 挂起的提问 / 授权：必须先结掉，否则后端还卡在等待里，"停止"看起来毫无反应。
  // 这两条是"用户在弹窗上的选择"，与"停止运行"是两件事，所以要前置处理。
  if (askStore.hasPending) {
    await askStore.skip(sessionId.value)
  }
  if (permissionStore.hasPending) {
    await permissionStore.cancel(sessionId.value)
  }

  // 计划执行中：交给计划取消（后端是同一套取消机制，走软取消）
  if (planStore.executing && plan.value) {
    await planStore.cancel(plan.value.id)
    stopping.value = true
    return
  }

  const hard = stopping.value
  try {
    const hit = await stopChat(sessionId.value, hard)
    if (!hit) {
      // 后端已经没有在跑的运行：直接复位，避免按钮卡在"停止中"
      chatStore.isGenerating = false
      stopping.value = false
      return
    }
    stopping.value = true
    if (hard) ui.notify('已强制停止', 'info')
  } catch (e) {
    ui.notify(`停止失败：${e.message || e}`, 'error')
  }
}

// 授权请求被应答后，把对应卡片从「等待授权」还原为「运行中」，
// 终态（成功/失败）随后由 tool_call_end 事件覆盖
watch(
  () => permissionStore.pending,
  (val, old) => {
    // 新的等待授权请求：把对应工具卡片标成"等待授权"（从 store 派生，不再订阅事件——
    // 事件通道的监听是进程级常驻的，组件卸载时不许去动它）
    if (val && !old) {
      markPendingToolCard(val.tool)
      return
    }
    if (val || !old) return
    // 应答后把卡片还原为「运行中」，终态随后由 tool_call_end 覆盖。
    // 不依赖 streamingMessageId：计划执行期间它是空的，但卡片同样需要还原。
    for (const msg of chatStore.messages) {
      if (!msg.toolCalls) continue
      for (const tc of msg.toolCalls) {
        if (tc.status === 'pending') {
          chatStore.updateToolCall(msg.id, tc.id, { status: 'running' })
        }
      }
    }
  }
)

// ===== 「/技能名」显式调用补全 =====
//
// 只在「整段输入恰好是一个 /命令」时启用：一旦出现空格就认为用户已经在写正文，
// 不再弹菜单——否则正文里的斜杠或路径都会触发误补全。
const skillMenuIndex = ref(0)
const skillMenuDismissed = ref(false)

const skillQuery = computed(() => {
  const m = /^\s*\/([A-Za-z0-9_-]*)$/.exec(input.value || '')
  return m ? m[1].toLowerCase() : null
})

const skillMenu = computed(() => {
  const kw = skillQuery.value
  if (kw === null) return []
  const list = skillsStore.invocableSkills
  const matched = kw
    ? list.filter(
        (s) => s.id.toLowerCase().includes(kw) || (s.name || '').toLowerCase().includes(kw)
      )
    : list
  return matched.slice(0, 8)
})

const skillMenuOpen = computed(() => skillMenu.value.length > 0 && !skillMenuDismissed.value)

// 输入一变就复位，避免上一次的选中项与「已关闭」状态残留
watch(input, () => {
  skillMenuDismissed.value = false
  skillMenuIndex.value = 0
})

function applySkillCommand(s) {
  if (!s) return
  input.value = `/${s.id} `
}

function handleKeydown(e) {
  if (skillMenuOpen.value) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      skillMenuIndex.value = (skillMenuIndex.value + 1) % skillMenu.value.length
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      skillMenuIndex.value =
        (skillMenuIndex.value - 1 + skillMenu.value.length) % skillMenu.value.length
      return
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      skillMenuDismissed.value = true
      return
    }
    if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
      e.preventDefault()
      applySkillCommand(skillMenu.value[skillMenuIndex.value] || skillMenu.value[0])
      return
    }
  }
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

    <!-- 授权弹窗 / 模型提问弹窗已上移到 App.vue：它们由进程级事件驱动，
         必须和事件收口在同一层，否则在设置页（本组件未渲染）时弹不出来。
         见 components/business/PermissionDialog.vue 顶部说明。 -->

    <!-- 实际发出的请求快照（排障用，按需拉取，不随每轮推送） -->
    <RequestPreviewDialog
      :visible="reqPreviewVisible"
      :session-id="sessionId || ''"
      @close="reqPreviewVisible = false"
    />

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
              :view-mode="viewMode"
            />
          </div>
        </div>
      </template>
    </div>

    <div class="prompt-box">
      <div class="prompt-toolbar">
        <!-- 原先这里的「附件 / @提及文件 / 更多」三个图标按钮没有绑定任何事件，
             点了没反应；后端也还没有对应能力，先移除，避免诱导点击。 -->
        <button class="tool-btn diff-btn" title="查看文件差异" @click="openDiff">
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none">
            <path d="M8 2v12M2 8h12" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
          </svg>
          Diff
        </button>
        <button
          class="tool-btn stream-btn"
          :class="{ active: settingStore.settings.streamResponse }"
          :title="settingStore.settings.streamResponse ? '流式输出已开启（点击关闭）' : '流式输出已关闭（点击开启）'"
          @click="settingStore.setStreamResponse(!settingStore.settings.streamResponse)"
        >
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none">
            <path d="M2 4h12M2 8h8M2 12h10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
          </svg>
          流式
        </button>
        <button
          class="tool-btn plan-btn"
          :class="{ active: planMode }"
          :disabled="chatStore.isGenerating"
          :title="planMode ? '计划模式已开启：本条消息将先生成可审核的计划' : '计划模式：复杂任务先生成可审核的计划'"
          @click="planMode = !planMode"
        >
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none">
            <path d="M3 2.5h7l3 3v8a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1v-10a1 1 0 0 1 1-1z" stroke="currentColor" stroke-width="1.3" />
            <path d="M5 7h6M5 10h4" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
          </svg>
          计划
        </button>
        <button
          class="tool-btn"
          :disabled="!sessionId"
          title="查看实际发给模型的 message 列表（压缩与工具结果预算之后的那一份）"
          @click="reqPreviewVisible = true"
        >
          请求
        </button>
        <button
          v-if="contextStat"
          class="tool-btn ctx-btn"
          :class="{ warn: contextPercent >= 70 }"
          :title="contextTitle"
          @click="requestCompact"
        >
          上下文 {{ contextPercent }}%
        </button>
        <button
          v-if="chatStore.isGenerating"
          class="tool-btn stop-btn"
          :class="{ active: stopping }"
          :title="stopping ? '再点一次强制停止（立即中断在途请求与子进程）' : '停止（当前步骤跑完后停下）'"
          @click="stopGeneration"
        >
          <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor">
            <rect x="2" y="2" width="10" height="10" rx="1" />
          </svg>
          {{ stopping ? '强制停止' : '停止' }}
        </button>
      </div>

      <div class="prompt-input-wrap">
        <!-- 「/技能名」补全：向上弹出，避免遮住下方的发送按钮 -->
        <div v-if="skillMenuOpen" class="skill-menu">
          <div class="skill-menu-head">调用技能 · Enter 选中 / Esc 关闭</div>
          <div
            v-for="(s, i) in skillMenu"
            :key="s.id"
            class="skill-menu-item"
            :class="{ active: i === skillMenuIndex }"
            @mousedown.prevent="applySkillCommand(s)"
          >
            <span class="skill-menu-id">/{{ s.id }}</span>
            <span class="skill-menu-desc">{{ s.description }}</span>
          </div>
        </div>
        <textarea
          v-model="input"
          class="prompt-input"
          placeholder="输入消息... (Enter 发送，Shift+Enter 换行；输入 / 调用技能)"
          rows="1"
          @keydown="handleKeydown"
        />
      </div>

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
  box-shadow: 0 0 0 2px rgb(var(--color-primary-rgb) / 0.25);
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
    background-color: rgb(var(--color-primary-rgb) / 0.1);
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

  // 上下文用量指示：点击即压缩。等宽数字避免百分比变化时按钮宽度抖动。
  &.ctx-btn {
    font-variant-numeric: tabular-nums;

    &.warn {
      color: $color-yellow;
    }
  }

  &.stop-btn {
    color: $color-error;
    &:hover {
      background-color: rgb(var(--color-error-rgb) / 0.1);
    }
    // 已在"停止中"：按钮文案变成"强制停止"，用实底强调再点一次会立即中断
    &.active {
      color: #fff;
      background-color: $color-error;
      &:hover {
        background-color: $color-error;
      }
    }
  }

  &.stream-btn.active,
  &.plan-btn.active {
    color: $color-primary;
    background-color: rgb(var(--color-primary-rgb) / 0.12);
  }

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
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
    box-shadow: 0 0 0 2px rgb(var(--color-primary-rgb) / 0.2);
  }

  &::placeholder {
    color: $color-text-muted;
  }
}

// 「/技能名」补全下拉：向上弹出
.prompt-input-wrap {
  position: relative;
}

.skill-menu {
  position: absolute;
  left: 0;
  right: 0;
  bottom: calc(100% + 4px);
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  box-shadow: 0 -6px 24px rgba(0, 0, 0, 0.35);
  overflow: hidden;
  z-index: 20;
  max-height: 260px;
  overflow-y: auto;
}

.skill-menu-head {
  padding: 5px $space-md;
  font-size: $font-size-xs;
  color: $color-text-muted;
  border-bottom: 1px solid $color-border;
}

.skill-menu-item {
  display: flex;
  align-items: baseline;
  gap: $space-sm;
  padding: 6px $space-md;
  cursor: pointer;

  &.active {
    background-color: $color-bg-tertiary;
  }
}

.skill-menu-id {
  font-family: monospace;
  font-size: $font-size-xs;
  color: $color-primary;
  flex-shrink: 0;
}

.skill-menu-desc {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
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
