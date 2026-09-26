<script setup>
import { ref, computed, nextTick, watch, onMounted, onUnmounted } from 'vue'
import { useSessionStore } from '@/stores/session'
import { useChatStore } from '@/stores/chat'
import { usePaneStore } from '@/stores/pane'
import { useDiffStore } from '@/stores/diff'
import { useSettingStore } from '@/stores/setting'
import { usePlanStore } from '@/stores/plan'
import { appendMessage, appendConversation, chat, stopChat, getContextStat, onDiffUpdate, saveAttachment, fetchImageURL, getAttachmentDataURL } from '@/api/session'
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
//
// **按会话记**：两个会话可能同时在跑，"A 已进入停止中"不该让 B 的停止按钮
// 第一次点就变成硬取消（那是不可逆的：直接断开在途请求、kill 子进程）。
const stoppingBySession = ref({})
const stopping = computed(() => !!stoppingBySession.value[sessionId.value])

function setStopping(sid, v) {
  if (!sid) return
  const next = { ...stoppingBySession.value }
  if (v) next[sid] = true
  else delete next[sid]
  stoppingBySession.value = next
}

// 请求快照查看（排障用）：显示实际发出的 message 列表
const reqPreviewVisible = ref(false)

// 上下文用量（后端算的近似值）。用于显示占用比例；点它可直接触发压缩。
//
// **按会话存**（在 chat store 里）：它是某一次运行回传的数字，后台会话跑完回传的
// 用量不该显示在当前会话的指示器上。改造前它是本组件的一个 ref，于是切会话时
// 后台会话的用量会覆盖当前会话的显示。
const contextStat = computed(() => chatStore.contextStat)

function updateContextStat(sid, st) {
  if (sid && st) chatStore.setContextStat(sid, st)
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
  const sid = sessionId.value
  if (!sid || chatStore.isGeneratingIn(sid)) return
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
  // 还没发出去的图片预览用的是 object URL，组件销毁时要还回去，否则这一份内存不释放
  clearPendingFiles()
})
const input = ref('')
const messagesContainer = ref(null)
const autoScroll = ref(true)

// ===== 图片附件 =====
//
// 用户在本地挑好图、点发送时才上传。本地态只存"未落盘"的这一批：
// 贴错了直接删掉，后端不会留下任何垃圾文件。
//
// 三个入口都收敛到 addFiles：粘贴（Ctrl+V，截图最常用的一条）、拖拽、点按钮选文件。
// 这与项目里"宿主原生对话框不可用"的教训同一个方向——入口多没关系，
// 但出口必须只有一个，否则三处各写一份读文件/校验/限额逻辑，必然漂移。
const MAX_ATTACHMENTS = 6
const pendingFiles = ref([])
const fileInput = ref(null)
const dragging = ref(false)

function fileToBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const r = String(reader.result || '')
      const i = r.indexOf(',')
      resolve(i >= 0 ? r.slice(i + 1) : r)
    }
    reader.onerror = () => reject(new Error('读取文件失败'))
    reader.readAsDataURL(file)
  })
}

// imageFilesFrom 从一个 DataTransfer 里取出图片文件。
//
// **必须同时看 items 与 files。** Chromium 系粘贴图片时填 files；而 macOS 上 Wails 用的是
// WebKit（WKWebView），它经常**只填 items、files 是空的**。只读 files 的写法在浏览器里
// 好使、在真机上失效——实测表现是"粘贴之后图没进来，输入框里却多出一串图片 URL"：
// 因为没被 preventDefault，浏览器接着执行了默认粘贴，把剪贴板里的**文本表示**插了进来。
function imageFilesFrom(dt) {
  if (!dt) return []
  const out = []
  // hintType 是剪贴板项自己声明的 MIME：WebKit 交回来的 File 有时 type 为空，
  // 这时只能靠它（或文件名后缀）判断——否则会被当成"非图片"静默丢掉。
  const push = (f, hintType) => {
    if (!f || out.indexOf(f) >= 0) return
    const t = String(f.type || hintType || '').toLowerCase()
    if (t.startsWith('image/') || looksLikeImage(f.name)) out.push(f)
  }
  if (dt.items) {
    for (const item of dt.items) {
      if (item.kind === 'file') push(item.getAsFile(), item.type)
    }
  }
  for (const f of Array.from(dt.files || [])) push(f)
  return out
}

// looksLikeImage 文件名后缀兜底。前端只是做体验过滤，**真伪由后端按内容嗅探判定**
// （imageproc.go 的 sniffImageMediaType），所以这里宁可放宽：拦错了是把真图丢掉，
// 放过了最多由后端回一句"不支持的图片格式"。
function looksLikeImage(name) {
  return /\.(png|jpe?g|gif|webp|bmp|tiff?)$/i.test(String(name || ''))
}

// clipboardClaimsImage 剪贴板是否**声称**有图片（items 里有 image/* 类型）。
// 用于"拿不到字节但必须拦住默认粘贴"的场景。
function clipboardClaimsImage(dt) {
  if (!dt || !dt.items) return false
  for (const item of dt.items) {
    if (item.type && item.type.startsWith('image/')) return true
  }
  return false
}

// safeGetData 取剪贴板里的某一种表示；部分平台/类型会抛错，不该让它冒出来
function safeGetData(dt, type) {
  if (!dt || typeof dt.getData !== 'function') return ''
  try {
    return String(dt.getData(type) || '')
  } catch {
    return ''
  }
}

// clipboardIsBareLink 剪贴板里**只有一串链接**（没有图片字节）。
//
// 这正是"以为贴了图、其实只发了链接"的那个场景：不少截图工具与内部平台复制图片时
// 放进剪贴板的是一条 URL。这时前端拿不到任何 File，"粘贴图片"从源头就不成立。
// 刻意**不** preventDefault——用户可能就是想发这个链接；只把话说清楚，
// 并指向显式的「链接」入口（那条路要用户点一下，因为会从本机发起出站请求）。
function clipboardIsBareLink(dt) {
  const text = safeGetData(dt, 'text/plain').trim()
  return /^https?:\/\/\S+$/i.test(text)
}

// clipboardSummary 把"剪贴板里到底有什么"说清楚。
//
// 这是排障用的：用户看到的现象（"我明明贴了图"）与剪贴板的真实内容经常对不上
// ——有的工具复制的是链接、有的是 HTML、有的干脆是别的协议的载荷。
// 真机上开控制台看 `clipboardData.items` 成本很高，不如让界面自己报出来。
function clipboardSummary(dt) {
  if (!dt) return '剪贴板为空'
  const kinds = []
  if (dt.items) {
    for (const item of dt.items) kinds.push(`${item.kind}/${item.type || '未知类型'}`)
  }
  const text = safeGetData(dt, 'text/plain').trim().replace(/\s+/g, ' ')
  const head = text ? `；文本开头：「${text.slice(0, 100)}${text.length > 100 ? '…' : ''}」` : ''
  const parts = kinds.length ? `${kinds.join('、')}` : '没有可用的内容项'
  return `剪贴板内容是 ${parts}${head}`
}

// addFromURL 从链接添加图片：**用户显式点按**才会走这条路。
//
// 为什么不做成"粘贴链接就自动下载"：那等于让任何一次粘贴都能触发本机的出站请求，
// 用户既没看到目标地址、也没同意。这里先弹输入框收 URL（能看到完整地址），
// 再交给后端下载；后端限定 http/https、重定向上限、超时、限长、内容必须是图片。
async function addFromURL() {
  const sid = sessionId.value
  if (!sid || chatStore.isGenerating) return
  const raw = await ui.askText({
    title: '从链接添加图片',
    message: '粘贴图片直链（http / https）。会从本机下载一次，内容必须是图片。',
    placeholder: 'https://example.com/screenshot.png',
    confirmText: '下载并添加',
  })
  const url = String(raw || '').trim()
  if (!url) return
  if (!/^https?:\/\//i.test(url)) {
    ui.notify('只支持 http / https 链接', 'warn')
    return
  }
  const tip = ui.notify('正在下载图片…', 'info', 0) // 0 = 不自动消失，结束时手动关掉
  try {
    const att = await fetchImageURL(sid, url)
    // 预览用后端存下来的那一份（与模型看到的完全同一张图），不另存一份原始数据
    const preview = await getAttachmentDataURL(sid, att.id)
    pendingFiles.value.push({
      key: `att-${Date.now()}-${Math.random().toString(16).slice(2, 6)}`,
      name: att.name,
      size: att.bytes,
      attachment: att, // 已经落盘，发送时直接引用，不再上传一次
      preview,
    })
    ui.notify(`已添加 ${att.name}（${att.width}×${att.height}）`, 'success')
  } catch (e) {
    ui.notify(`未能添加：${e.message || e}`, 'error')
  } finally {
    ui.dismiss(tip)
  }
}

async function addFiles(files) {
  // 与 imageFilesFrom 用同一套判据（MIME 或后缀），避免"入口放行、这里又拦掉"
  const list = Array.from(files || []).filter(
    (f) => f && (String(f.type || '').toLowerCase().startsWith('image/') || looksLikeImage(f.name))
  )
  if (!list.length) {
    if (files && files.length) ui.notify('只支持图片（PNG / JPEG / GIF）', 'warn')
    return
  }
  const room = MAX_ATTACHMENTS - pendingFiles.value.length
  if (list.length > room) ui.notify(`单条消息最多 ${MAX_ATTACHMENTS} 张图片`, 'warn')
  for (const f of list.slice(0, Math.max(0, room))) {
    try {
      const payload = await fileToBase64(f)
      pendingFiles.value.push({
        key: `att-${Date.now()}-${Math.random().toString(16).slice(2, 6)}`,
        name: f.name || '粘贴的图片.png',
        size: f.size,
        payload,
        // 本地预览用 object URL：发送前图还没落盘，没有 ID 可以去向后端要。
        // 组件卸载/清空时要 revoke，否则这一份内存不会释放。
        preview: URL.createObjectURL(f),
      })
    } catch (e) {
      ui.notify(`读取图片失败：${e.message || e}`, 'error')
    }
  }
}

function removePending(key) {
  const i = pendingFiles.value.findIndex((f) => f.key === key)
  if (i < 0) return
  const [removed] = pendingFiles.value.splice(i, 1)
  if (removed && removed.preview) URL.revokeObjectURL(removed.preview)
}

function clearPendingFiles() {
  for (const f of pendingFiles.value) {
    if (f.preview) URL.revokeObjectURL(f.preview)
  }
  pendingFiles.value = []
}

function pickFiles() {
  fileInput.value?.click()
}

function handleFilePicked(e) {
  addFiles(e.target.files)
  e.target.value = '' // 同一个文件连选两次也要能触发 change
}

function handlePaste(e) {
  const dt = e.clipboardData
  const files = imageFilesFrom(dt)
  if (files.length) {
    // 拦掉默认粘贴：否则剪贴板里的文本表示（往往就是那张图的 URL）会一起插进输入框，
    // 用户以为图发出去了，模型却只看到一串链接。
    e.preventDefault()
    addFiles(files)
    return
  }
  if (clipboardClaimsImage(dt)) {
    // 有图却拿不到字节：同样不能放行默认粘贴（理由同上），并明确告诉用户怎么办
    e.preventDefault()
    ui.notify('这张图片读不出来，请先另存为文件再拖进来，或重新截一次图', 'warn')
    return
  }
  if (clipboardIsBareLink(dt)) {
    // 只带了链接：不拦默认粘贴（用户可能就是想发这个链接），但要说清楚它不是图片，
    // 并指向「链接」入口——否则用户会以为图已经发出去了。
    ui.notify('只检测到链接，不是图片内容。若是图片直链，请用上方「链接」按钮添加', 'warn', 7000)
    return
  }
  // 既没有图片文件、也不是一条链接：把剪贴板里究竟有什么报出来。
  // 与其猜"你贴的是什么"，不如让界面直接说明——这类问题用户与开发者看到的
  // 现象往往一致（"我明明贴了图"），但对不上的正是剪贴板的真实内容。
  if (safeGetData(dt, 'text/plain').trim() || safeGetData(dt, 'text/html').trim()) {
    ui.notify(`粘贴进来的不是图片。${clipboardSummary(dt)}`, 'warn', 12000)
  }
}

function handleDragOver(e) {
  const dt = e.dataTransfer
  if (!dt) return
  // 必须 preventDefault，否则浏览器的默认行为是"用这个文件替换当前页面"
  const types = Array.from(dt.types || [])
  if (types.includes('Files') || imageFilesFrom(dt).length) {
    e.preventDefault()
    dragging.value = true
  }
}

function handleDragLeave() {
  dragging.value = false
}

function handleDrop(e) {
  dragging.value = false
  const files = imageFilesFrom(e.dataTransfer)
  if (files.length) {
    e.preventDefault()
    addFiles(files)
  }
}

function formatFileSize(n) {
  if (!n) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

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
    if (id) getContextStat(id).then((st) => updateContextStat(id, st))
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
  const pending = pendingFiles.value.slice()
  // 纯图片消息（不写一个字就发一张图）必须允许——"这张报错图怎么回事"是常见开场
  //
  // 守卫只看**本会话**：别的会话在跑不该拦住这条会话（这正是多会话并行的用法）。
  // 同一会话的重复发送由后端 beginExclusive 兜底——前端状态永远只是提示，不是保证。
  if ((!text && !pending.length) || !sid || chatStore.isGeneratingIn(sid)) return

  // 计划模式走的是无工具的文本规划器（见 ChatPlan），图片到不了它那里：
  // 与其发出一份"看不见图"的计划，不如说明白并改走普通对话。
  let usePlan = planMode.value
  if (usePlan && pending.length) {
    ui.notify('计划模式暂不支持图片，本条已按普通对话发送', 'info')
    usePlan = false
  }

  input.value = ''
  // 运行状态按会话落库：切到别的会话时，这条会话仍显示"运行中"，
  // 而别的会话不会被它带成"生成中"。
  chatStore.markRunStarted(sid)
  setStopping(sid, false)
  const startAt = Date.now()
  // 本次运行的流式占位消息 id。**局部变量而不是组件级的 ref**：
  // 用户可以在本次运行还没结束时就去另一条会话发消息，组件的 ref 会被覆盖，
  // 之后"替换占位"就会替换错人（或者谁也找不到）。
  let placeholderId = null

  try {
    // 1. 先落附件、再落消息：消息里挂的引用因此一定是完整的，
    //    不会出现"消息已入库、图还在路上"这种读了会报错的中间态。
    const attachments = []
    for (const f of pending) {
      // 从链接抓来的图在"添加"那一刻就已经落盘，这里直接引用，不再上传一次
      if (f.attachment) {
        attachments.push(f.attachment)
        continue
      }
      try {
        attachments.push(await saveAttachment(sid, f.name, f.payload, 'user'))
      } catch (e) {
        // 单张失败不阻断整条消息：剩下的照发，失败的在这里点名
        ui.notify(`图片「${f.name}」未能添加：${e.message || e}`, 'error')
      }
    }
    clearPendingFiles()

    // 2. 持久化用户消息（后端补全 id/createdAt 后同步到本地）
    const savedUserMsg = await appendMessage(sid, {
      role: 'user',
      content: text,
      attachments: attachments.length ? attachments : undefined,
    })
    // 消息一律写进 **sid 的桶**：await 期间用户完全可能切走，
    // 写"当前会话"就会把这条会话的内容塞进另一条（改造前最典型的串台）。
    chatStore.addLocalMessage(sid, savedUserMsg)
    // 本地同步会话标题与活跃时间（注意取的是 sid 那条会话，不是当前显示的）
    const target = sessionStore.sessions.find((s) => s.id === sid)
    if (target && (!target.title || target.title === '新会话')) {
      // 纯图片消息没有正文可作标题，用图片文件名兜底（否则会话列表里会出现一行空白）
      const src = text || (attachments[0] && attachments[0].name) || '（图片）'
      const flat = src.replace(/\s+/g, ' ').trim()
      sessionStore.touchSession(sid, {
        title: flat.length > 30 ? flat.slice(0, 30) + '...' : flat,
      })
    } else {
      sessionStore.touchSession(sid)
    }

    // 计划模式：走规划流程产出可审核的计划（平凡请求由后端直接回复）
    if (usePlan) {
      const result = await chat(sid, text, {
        stream: false, // 规划器固定非流式；免计划直答取完整回复
        plan: true,
        onPlanUpdate: (p) => planStore.applyUpdate(sid, p),
      })
      updateContextStat(sid, result?.context)
      if (result?.plan) {
        planStore.applyUpdate(sid, result.plan)
        // 计划是单次意图，生成后自动复位（只复位当前正在看的这条会话的开关）
        if (sessionId.value === sid) planMode.value = false
        // 后台自动打开：不动用户的右侧收起状态（收起时计划以图标出现在窄条上）
        paneStore.openPaneBackground(sid, 'plan')
      } else if (result?.reply) {
        chatStore.addLocalMessage(sid, {
          id: `assistant-${Date.now()}`,
          role: 'assistant',
          content: result.reply,
          createdAt: Date.now(),
          streaming: false,
        })
      } else if (result?.error) {
        chatStore.addLocalMessage(sid, {
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
    //
    // 占位消息活在 **sid 的桶**里（不是组件的一个 ref）：它既是流式文本的落点，
    // 也是"切走再切回来仍能看到进度"的载体。用一个 ref 记住它的 id 是不够的——
    // 用户可以在本次运行还没结束时就去另一条会话发消息，那个 ref 会被覆盖。
    const localMsg = chatStore.addLocalMessage(sid, {
      id: `local-${sid}-${Date.now()}`,
      role: 'assistant',
      content: '',
      toolCalls: [],
      createdAt: Date.now(),
      streaming: true,
    })
    placeholderId = localMsg.id

    // 3. 调用后端 Chat 方法（真实 LLM 调用 + 工具执行）
    // 后端会持久化所有中间消息（assistant+tool_calls, tool结果, 最终回复）
    const result = await chat(sid, text, {
      stream: settingStore.settings.streamResponse,
      onReplyDelta: (chunk) => {
        chatStore.appendStreamContent(sid, localMsg.id, chunk)
      },
      onToolCallStart: (tc) => {
        chatStore.addToolCall(sid, localMsg.id, tc)
        // 派生子代理时自动打开子代理面板：子代理的中间过程不在聊天流里（它跑在自己
        // 独立的上下文里，只有结论会回到聊天），不打开面板用户就完全看不到它在做什么。
        // 同样走后台打开：右侧收起时不强行铺开，图标会出现在窄条上。
        if (tc?.name === 'spawn_agent') paneStore.openPaneBackground(sid, 'subagent')
      },
      onToolCallEnd: (tc) => {
        chatStore.updateToolCall(sid, localMsg.id, tc.id, {
          status: tc.status,
          duration: tc.duration,
          result: tc.result,
        })
      },
      onContextCompacted: (ev) => {
        // 上下文被自动压缩：用量会骤降，不解释一句用户会以为对话被截断了。
        // 走事件而不是落库消息——它是解释，不是对话内容（/compact 才是用户主动、落库的那条）。
        updateContextStat(sid, ev?.context)
        const c = ev?.compact || {}
        const pct = ev?.context?.windowTokens
          ? Math.round((ev.context.usedTokens / ev.context.windowTokens) * 100)
          : null
        // 后台会话的压缩提示带上会话标题：否则用户会以为当前这条会话被压缩了
        const tail = sessionId.value === sid ? '' : `（会话「${sessionTitleOf(sid)}」）`
        ui.notify(
          `上下文已自动压缩${tail}：前 ${c.coveredMsgs ?? 0} 条消息合并为摘要` +
            (pct === null ? '' : `，用量降至 ${pct}%`) +
            '，原文仍完整保留（输入 /context-stat 查看明细）',
          'info'
        )
      },
      // 被用户停止：立刻收尾流式气泡，否则它会一直停在"正在输入"
      onCancelled: () => {
        chatStore.updateLocalMessage(sid, localMsg.id, { streaming: false })
      },
    })
    updateContextStat(sid, result?.context)

    // 4. 移除流式占位消息，用后端持久化的消息替换
    // 4. 移除流式占位消息，用后端持久化的消息替换（都写在 sid 的桶里）
    //
    // 改造前这里用 findIndex 在"当前会话"的消息里找占位、再往"当前会话"追加结果，
    // 于是 A 跑完的回复被追加进了 B——本次修复要挡住的正是这一条。
    // replacePlaceholder 找不到占位时返回 false（用户在运行期间刷新过），
    // 这时退回"直接追加"，至少不让这段回复丢掉。
    const persisted = (result.messages || []).filter((m) => m.role !== 'tool')
    const replaced = chatStore.replacePlaceholder(sid, localMsg.id, persisted)
    if (!replaced) {
      for (const msg of persisted) {
        chatStore.addLocalMessage(sid, { ...msg, streaming: false })
      }
    }

    // 6. 被取消 / 出错。取消是用户的主动行为，不能显示成"调用失败"——
    //    这也是后端把取消从错误路径里摘出来的原因（见 App.Chat）。
    if (result.cancelled) {
      chatStore.addLocalMessage(sid, {
        id: `cancelled-${sid}-${Date.now()}`,
        role: 'assistant',
        content: result.cancelKind === 'hard' ? '已强制停止。' : '已停止（当前步骤执行完即停）。',
        createdAt: Date.now(),
        streaming: false,
      })
    } else if (result.error && !result.reply) {
      chatStore.addLocalMessage(sid, {
        id: `error-${sid}-${Date.now()}`,
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

    // 8. 联动 DiffPane：重新加载差异，有改动则自动展开右侧面板（都以 sid 为准）
    try {
      const changed = await diffStore.load(sid)
      // 后台自动打开：收起状态下不强行铺开（图标出现在窄条上）
      if (changed > 0) paneStore.openPaneBackground(sid, 'diff')
    } catch (diffErr) {
      console.warn('加载 diff 失败:', diffErr)
    }
  } catch (e) {
    console.error('发送消息失败:', e)
    // placeholderId 可能还是 null（落附件/落用户消息阶段就失败了）
    if (placeholderId) {
      chatStore.updateLocalMessage(sid, placeholderId, {
        content: `发送失败：${e.message || e}`,
        streaming: false,
      })
    } else if (sessionId.value === sid) {
      // 只在用户正看着这条会话时用提示条：后台会话失败不该打断他手上的事
      ui.notify(`发送失败：${e.message || e}`, 'error')
    } else {
      chatStore.addLocalMessage(sid, {
        id: `error-${sid}-${Date.now()}`,
        role: 'assistant',
        content: `发送失败：${e.message || e}`,
        createdAt: Date.now(),
        streaming: false,
      })
    }
  } finally {
    // 只结束 sid 那一条的运行状态：别的会话该跑还在跑
    chatStore.markRunEnded(sid)
    setStopping(sid, false)
  }
}

/** 取会话标题（提示文案里点名"是哪条会话"用的；找不到就退回占位文案） */
function sessionTitleOf(sid) {
  const s = sessionStore.sessions.find((x) => x.id === sid)
  return s?.title || '未命名会话'
}

/**
 * 把「正在运行」的工具卡片标为等待授权（后端阻塞在授权/提问上）。
 *
 * 工具循环是串行执行的，所以匹配该会话里最后一个同名 running 卡片是准确的
 * （事件里没有工具调用 ID）；从后往前找也天然适配计划执行期间的多条消息。
 *
 * **按请求自带的会话找**：授权可能来自后台会话，而用户此刻看的是另一条——
 * 扫"当前会话"会标错卡片（改造前的行为）。
 */
function markPendingToolCard(tool, sid) {
  if (!tool || !sid) return
  const msgs = chatStore.messagesOf(sid)
  for (let mi = msgs.length - 1; mi >= 0; mi--) {
    const calls = msgs[mi].toolCalls || []
    for (let i = calls.length - 1; i >= 0; i--) {
      if (calls[i].status === 'running' && calls[i].name === tool) {
        chatStore.updateToolCall(sid, msgs[mi].id, calls[i].id, { status: 'pending' })
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
  const sid = sessionId.value
  if (!sid) return
  // 挂起的提问 / 授权：必须先结掉，否则后端还卡在等待里，"停止"看起来毫无反应。
  // 这两条是"用户在弹窗上的选择"，与"停止运行"是两件事，所以要前置处理。
  // 都按当前会话结：结掉的是"这条会话正在等的那一条"。
  if (askStore.hasPending) {
    await askStore.skip(sid)
  }
  if (permissionStore.hasPending) {
    await permissionStore.cancel(sid)
  }

  // 计划执行中：交给计划取消（后端是同一套取消机制，走软取消）
  if (planStore.executing && plan.value) {
    await planStore.cancel(plan.value.id)
    setStopping(sid, true)
    return
  }

  const hard = stopping.value
  try {
    const hit = await stopChat(sid, hard)
    if (!hit) {
      // 后端已经没有在跑的运行：直接复位，避免按钮卡在"停止中"
      chatStore.markRunEnded(sid)
      setStopping(sid, false)
      return
    }
    setStopping(sid, true)
    if (hard) ui.notify('已强制停止', 'info')
  } catch (e) {
    ui.notify(`停止失败：${e.message || e}`, 'error')
  }
}

// 授权请求状态变化时，把该会话对应的工具卡片标成 / 还原成对应状态。
//
// **按会话监听**（而不是监听"当前会话的 pending"）：授权可能来自后台会话，
// 用户此刻看的是另一条——那样标记会落在错误的会话上；而且切会话本身也会让
// "当前会话的 pending" 变化，会被误当成"授权已结束"。
// 终态（成功/失败）随后由 tool_call_end 事件覆盖。
watch(
  () => permissionStore.pendingSessionIds.join(','),
  (now, before) => {
    const nowIds = now ? now.split(',') : []
    const beforeIds = before ? before.split(',') : []
    for (const sid of nowIds) {
      const req = permissionStore.pendingBySession[sid]
      if (req) markPendingToolCard(req.tool, sid)
    }
    for (const sid of beforeIds) {
      if (nowIds.includes(sid)) continue
      restoreRunningCards(sid)
    }
  }
)

/** 把某会话里"等待授权"的卡片还原为"运行中"（授权已应答/超时/被停止） */
function restoreRunningCards(sid) {
  if (!sid) return
  for (const msg of chatStore.messagesOf(sid)) {
    if (!msg.toolCalls) continue
    for (const tc of msg.toolCalls) {
      if (tc.status === 'pending') {
        chatStore.updateToolCall(sid, msg.id, tc.id, { status: 'running' })
      }
    }
  }
}

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

// 流式占位消息的 id 不再是组件级状态：它按运行存在局部变量里、消息本身活在
// chat store 的会话桶中（见 sendMessage 里的说明）。

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

    <div
      class="prompt-box"
      :class="{ dragging }"
      @dragover="handleDragOver"
      @dragleave="handleDragLeave"
      @drop="handleDrop"
    >
      <!-- 拖拽遮罩：明确告诉用户"松手就添加"，而不是让它猜为什么拖不进来 -->
      <div v-if="dragging" class="drop-hint">松开鼠标添加图片</div>

      <div class="prompt-toolbar">
        <!-- 「附件 / @提及文件 / 更多」三个图标按钮当年没有绑定任何事件，点了没反应，
             已移除；这里只放真正有实现的入口。 -->
        <button
          class="tool-btn"
          :disabled="chatStore.isGenerating"
          title="添加图片（也可以直接粘贴 Ctrl+V 或把图拖进来）"
          @click="pickFiles"
        >
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none">
            <rect x="2" y="3" width="12" height="10" rx="1.4" stroke="currentColor" stroke-width="1.3" />
            <circle cx="5.8" cy="6.6" r="1.1" fill="currentColor" />
            <path d="M2.6 11.6l3.2-3.1 2.2 2.1 2-1.9 3.4 3.2" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          图片
        </button>
        <input
          ref="fileInput"
          type="file"
          accept="image/png,image/jpeg,image/gif"
          multiple
          class="file-input"
          @change="handleFilePicked"
        />
        <button
          class="tool-btn"
          :disabled="chatStore.isGenerating"
          title="从图片链接添加：会从本机下载一次，内容必须是图片（用户点按才会发起）"
          @click="addFromURL"
        >
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none">
            <path d="M6.6 9.4l2.8-2.8" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
            <path d="M7.3 4.7l.8-.8a2.5 2.5 0 0 1 3.5 3.5l-.8.8" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
            <path d="M8.7 11.3l-.8.8a2.5 2.5 0 0 1-3.5-3.5l.8-.8" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
          </svg>
          链接
        </button>
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
          <!-- 压缩标记：用量骤降时，用户第一眼要能看出这是摘要压缩造成的，
               而不是对话被截断了。常驻显示——它会一直影响后续每一轮。 -->
          <span v-if="contextStat.coveredMsgs > 0" class="ctx-badge">已压缩</span>
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

      <!-- 待发送的图片：本地预览 + 删除。发送成功后才上传，贴错了直接删掉 -->
      <div v-if="pendingFiles.length" class="pending-files">
        <div v-for="f in pendingFiles" :key="f.key" class="pending-file" :title="f.name">
          <img :src="f.preview" :alt="f.name" />
          <button class="pending-remove" title="移除这张图" @click="removePending(f.key)">×</button>
          <span class="pending-meta">{{ formatFileSize(f.size) }}</span>
        </div>
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
          placeholder="输入消息... (Enter 发送，Shift+Enter 换行；输入 / 调用技能，/vision 自检看图能力；可粘贴或拖入图片)"
          rows="1"
          @keydown="handleKeydown"
          @paste="handlePaste"
        />
      </div>

      <div class="prompt-footer">
        <button
          class="btn btn-primary send-btn"
          :disabled="(!input.trim() && !pendingFiles.length) || chatStore.isGenerating || !sessionId"
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
  position: relative;
  border-top: 1px solid $color-border;
  background-color: $color-bg-secondary;
  padding: $space-md;
}

// 拖拽悬停：给一点边框反馈，明确"这里可以放"
.prompt-box.dragging {
  outline: 1px dashed $color-primary;
  outline-offset: -4px;
}

.drop-hint {
  position: absolute;
  inset: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: $font-size-sm;
  color: $color-primary;
  background-color: rgb(var(--color-primary-rgb) / 0.06);
  pointer-events: none; // 让 dragover/drop 继续落在 .prompt-box 上
}

// 隐藏的原生文件选择框：入口是工具栏那个「图片」按钮
.file-input {
  display: none;
}

// 待发送的图片预览条
.pending-files {
  display: flex;
  flex-wrap: wrap;
  gap: $space-sm;
  margin-bottom: $space-sm;
}

.pending-file {
  position: relative;
  width: 72px;
  height: 72px;
  border-radius: $radius-md;
  border: 1px solid $color-border;
  overflow: hidden;
  background-color: $color-bg-tertiary;

  img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
  }

  .pending-remove {
    position: absolute;
    top: 2px;
    right: 2px;
    width: 18px;
    height: 18px;
    font-size: 13px;
    line-height: 1;
    color: #fff;
    background-color: rgba(0, 0, 0, 0.55);
    border-radius: 50%;

    &:hover {
      background-color: $color-error;
    }
  }

  .pending-meta {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    padding: 1px 4px;
    font-size: 10px;
    color: #fff;
    background-color: rgba(0, 0, 0, 0.55);
    text-align: center;
  }
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

    // "已压缩"标记：常驻小徽标，说明当前发出去的上下文里有摘要替换
    .ctx-badge {
      margin-left: 4px;
      padding: 0 4px;
      border-radius: 3px;
      font-size: 10px;
      line-height: 15px;
      color: var(--color-text-muted);
      background-color: var(--color-bg-tertiary);
    }

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
