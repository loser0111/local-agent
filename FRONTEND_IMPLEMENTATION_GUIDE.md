# Extended Thinking 前端实现指南

## 📋 前端集成清单

### Phase 5：TypeScript 类型定义

#### 1. 扩展 Session 接口
在你的类型定义文件（如 `types/index.ts` 或 `models/Session.ts`）中：

```typescript
export interface Session {
  id: string
  title: string
  project: string
  model: string
  permissionMode: string
  viewMode: string
  environment: string
  status: string
  startAt: number
  endAt: number
  messages: Message[]
  // ... 其他已有字段 ...

  // [新增] Thinking 相关配置
  thinkingEnabled?: boolean    // 是否启用 extended thinking
  thinkingBudget?: number      // thinking token 预算
}
```

#### 2. 扩展 Message 接口
```typescript
export interface Message {
  id: string
  role: 'user' | 'assistant' | 'tool' | 'system'
  content: string
  toolCalls?: ToolCall[]
  attachments?: Attachment[]
  // ... 其他已有字段 ...

  // [新增] 模型生成的思考过程（只读）
  thinking?: string  // 仅当 role='assistant' 且启用 thinking 时有值
}
```

#### 3. 扩展 SessionConfig 接口
```typescript
export interface SessionConfig {
  title: string
  project: string
  model: string
  permissionMode: string
  viewMode: string
  environment: string
  enabledTools: string[]
  enabledSkills: string[]
  parentId?: string

  // [新增] Thinking 配置
  thinkingEnabled?: boolean
  thinkingBudget?: number
}
```

#### 4. 扩展 ChatResult 接口
```typescript
export interface ChatResult {
  reply: string
  toolCalls?: ToolCall[]
  messages?: Message[]
  diff?: DiffFile[]
  plan?: Plan
  error?: string
  cancelled?: boolean
  cancelKind?: string
  context?: ContextStat

  // [新增] 思考过程
  thinking?: string
}
```

---

### Phase 6：UI 组件实现

#### 1. NewSessionDialog.vue - 添加 Thinking 配置表单

在新建会话对话框中添加：

```vue
<template>
  <div class="session-config">
    <!-- 现有字段 -->
    <div class="config-group">
      <label>{{ $t('session.model') }}</label>
      <select v-model="config.model">
        <!-- 模型选项 -->
      </select>
    </div>

    <!-- [新增] Thinking 配置组 -->
    <div class="config-group thinking-group">
      <div class="thinking-header">
        <input
          type="checkbox"
          v-model="config.thinkingEnabled"
          id="thinking-toggle"
        />
        <label for="thinking-toggle" class="thinking-label">
          {{ $t('thinking.enable') }}
        </label>
        <span class="thinking-hint">(Extended Thinking)</span>
      </div>

      <!-- 预算配置 -->
      <div v-if="config.thinkingEnabled" class="thinking-budget">
        <label>{{ $t('thinking.budget') }}</label>
        <input
          type="number"
          v-model.number="config.thinkingBudget"
          min="1000"
          max="30000"
          step="1000"
          placeholder="5000"
        />
        <span class="budget-hint">
          推荐 5000-10000 token，越高推理越深入
        </span>
      </div>
    </div>

    <!-- 确认按钮 -->
    <button @click="createSession" class="btn btn-primary">
      {{ $t('common.create') }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { reactive } from 'vue'
import type { SessionConfig } from '@/types'

const config = reactive<SessionConfig>({
  title: '',
  project: '',
  model: '',
  permissionMode: 'standard',
  viewMode: 'normal',
  environment: 'prod',
  enabledTools: [],
  enabledSkills: [],
  // [新增] thinking 配置
  thinkingEnabled: false,
  thinkingBudget: 5000,
})

const createSession = async () => {
  // 调用后端 API 创建会话
  // API 会自动使用 config 中的 thinkingEnabled 和 thinkingBudget
}
</script>

<style scoped>
.thinking-group {
  border: 1px solid #e5e7eb;
  padding: 12px;
  border-radius: 6px;
  background-color: #f9fafb;
}

.thinking-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.thinking-label {
  font-weight: 500;
  cursor: pointer;
}

.thinking-hint {
  font-size: 12px;
  color: #6b7280;
}

.thinking-budget {
  margin-top: 12px;
  padding-left: 28px;
}

.budget-hint {
  display: block;
  font-size: 12px;
  color: #6b7280;
  margin-top: 4px;
}
</style>
```

#### 2. MessageBubble.vue - 显示 Thinking 块

在消息气泡组件中添加 thinking 显示：

```vue
<template>
  <div class="message-bubble" :class="message.role">
    <!-- Thinking 块（仅当存在且角色为 assistant 时）-->
    <div v-if="message.role === 'assistant' && message.thinking" class="thinking-block">
      <div class="thinking-header" @click="thinkingExpanded = !thinkingExpanded">
        <span class="toggle-icon">{{ thinkingExpanded ? '▼' : '▶' }}</span>
        <span class="thinking-title">{{ $t('thinking.process') }}</span>
        <span class="thinking-indicator">(思考中...)</span>
      </div>
      <transition name="slide">
        <div v-if="thinkingExpanded" class="thinking-content">
          <div class="thinking-text">{{ message.thinking }}</div>
          <div class="thinking-divider"></div>
        </div>
      </transition>
    </div>

    <!-- 主消息内容 -->
    <div class="message-content">
      <div v-if="message.role === 'assistant'">
        <!-- 渲染 markdown 内容 -->
        <MarkdownRenderer :content="message.content" />
      </div>
      <div v-else>
        {{ message.content }}
      </div>
    </div>

    <!-- 工具调用记录 -->
    <div v-if="message.toolCalls?.length">
      <!-- 现有工具调用显示逻辑 -->
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { Message } from '@/types'

const props = defineProps<{
  message: Message
}>()

const thinkingExpanded = ref(false)
</script>

<style scoped>
.thinking-block {
  margin-bottom: 12px;
  background-color: #f3f4f6;
  border-left: 3px solid #8b5cf6;
  border-radius: 4px;
  overflow: hidden;
}

.thinking-header {
  padding: 10px 12px;
  cursor: pointer;
  display: flex;
  align-items: center;
  gap: 8px;
  user-select: none;
  transition: background-color 0.2s;
}

.thinking-header:hover {
  background-color: #e9d5ff;
}

.toggle-icon {
  display: inline-block;
  width: 16px;
  text-align: center;
  font-size: 12px;
  transition: transform 0.2s;
}

.thinking-title {
  font-weight: 600;
  font-size: 13px;
  color: #7c3aed;
}

.thinking-indicator {
  font-size: 12px;
  color: #a78bfa;
}

.thinking-content {
  padding: 12px;
  background-color: #faf5ff;
  border-top: 1px solid #ddd6fe;
}

.thinking-text {
  font-size: 13px;
  line-height: 1.6;
  color: #5b21b6;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: 'Monaco', 'Courier New', monospace;
}

.thinking-divider {
  height: 1px;
  background-color: #ddd6fe;
  margin-top: 12px;
}

.slide-enter-active,
.slide-leave-active {
  transition: max-height 0.3s ease;
  overflow: hidden;
}

.slide-enter-from,
.slide-leave-to {
  max-height: 0;
}
</style>
```

#### 3. 会话编辑面板 - 修改 Thinking 参数

在会话编辑/设置面板中添加：

```vue
<template>
  <div class="session-settings">
    <!-- 现有设置 -->

    <!-- [新增] Thinking 设置组 -->
    <div v-if="session" class="settings-group thinking-settings">
      <h3 class="group-title">{{ $t('thinking.title') }}</h3>

      <div class="setting-item">
        <div class="setting-label">
          <span>{{ $t('thinking.enable') }}</span>
          <span class="setting-desc">启用模型的深度推理能力</span>
        </div>
        <toggle-switch
          v-model="editSession.thinkingEnabled"
          @change="onThinkingToggle"
        />
      </div>

      <div v-if="editSession.thinkingEnabled" class="setting-item">
        <div class="setting-label">
          <span>{{ $t('thinking.budget') }}</span>
          <span class="setting-desc">分配给思考过程的 token 数量</span>
        </div>
        <div class="budget-input">
          <input
            type="range"
            v-model.number="editSession.thinkingBudget"
            min="1000"
            max="30000"
            step="1000"
            class="range-slider"
          />
          <input
            type="number"
            v-model.number="editSession.thinkingBudget"
            min="1000"
            max="30000"
            step="1000"
            class="number-input"
          />
          <span class="budget-unit">tokens</span>
        </div>
      </div>
    </div>

    <!-- 保存按钮 -->
    <div class="settings-actions">
      <button @click="saveSettings" class="btn btn-primary">
        {{ $t('common.save') }}
      </button>
      <button @click="resetSettings" class="btn btn-secondary">
        {{ $t('common.cancel') }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive } from 'vue'
import type { Session } from '@/types'

const props = defineProps<{
  session: Session
}>()

const editSession = reactive({ ...props.session })

const onThinkingToggle = () => {
  if (!editSession.thinkingEnabled) {
    editSession.thinkingBudget = undefined
  } else if (!editSession.thinkingBudget) {
    editSession.thinkingBudget = 5000
  }
}

const saveSettings = async () => {
  // 调用后端 API 更新会话
  // PATCH /api/sessions/{id}
}

const resetSettings = () => {
  Object.assign(editSession, props.session)
}
</script>

<style scoped>
.thinking-settings {
  background-color: #f0f4ff;
  border: 1px solid #c7d2fe;
  border-radius: 6px;
  padding: 16px;
  margin: 16px 0;
}

.group-title {
  font-size: 14px;
  font-weight: 600;
  margin-bottom: 16px;
  color: #1f2937;
}

.setting-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 0;
  border-bottom: 1px solid #dbeafe;
}

.setting-item:last-child {
  border-bottom: none;
}

.setting-label {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.setting-desc {
  font-size: 12px;
  color: #6b7280;
}

.budget-input {
  display: flex;
  align-items: center;
  gap: 8px;
}

.range-slider {
  flex: 1;
  min-width: 200px;
}

.number-input {
  width: 80px;
  padding: 6px;
  border: 1px solid #e5e7eb;
  border-radius: 4px;
  text-align: center;
}

.budget-unit {
  font-size: 12px;
  color: #6b7280;
}
</style>
```

---

### Phase 7：集成到对话流

#### 1. 修改消息发送逻辑

在你的 Chat API 调用中：

```typescript
// composables/useChat.ts
export const useChat = () => {
  const sendMessage = async (sessionId: string, content: string) => {
    const response = await chatAPI.sendMessage({
      sessionId,
      content,
      // thinking 配置由后端从 session 读取，无需前端传递
    })

    // 响应中会包含 thinking 字段
    if (response.thinking) {
      console.log('模型思考过程:', response.thinking)
    }

    return response
  }

  return { sendMessage }
}
```

#### 2. 修改消息存储

在你的状态管理中（如 Pinia store）：

```typescript
// stores/chatStore.ts
import { defineStore } from 'pinia'
import type { Message, Session } from '@/types'

export const useChatStore = defineStore('chat', () => {
  const messages = ref<Message[]>([])
  const currentSession = ref<Session | null>(null)

  const addMessage = (message: Message) => {
    // thinking 字段会自动保存在 message 中
    messages.value.push(message)
  }

  const updateSessionThinking = (thinkingEnabled: boolean, budget: number) => {
    if (currentSession.value) {
      currentSession.value.thinkingEnabled = thinkingEnabled
      currentSession.value.thinkingBudget = budget
    }
  }

  return {
    messages,
    currentSession,
    addMessage,
    updateSessionThinking,
  }
})
```

---

## 🎨 样式建议

### Thinking 块样式变量（可自定义）

```css
:root {
  /* Thinking 块颜色 */
  --color-thinking-border: #8b5cf6;
  --color-thinking-bg: #f3f4f6;
  --color-thinking-text: #5b21b6;
  --color-thinking-hover: #e9d5ff;
  --color-thinking-divider: #ddd6fe;

  /* Thinking 指示器 */
  --color-thinking-indicator: #a78bfa;
  --font-thinking: 'Monaco', 'Courier New', monospace;
}
```

---

## 📝 国际化（i18n）

在你的翻译文件中添加：

```json
{
  "thinking": {
    "title": "Extended Thinking",
    "enable": "启用 Extended Thinking",
    "budget": "思考预算",
    "process": "思考过程",
    "hint": "模型会使用更多计算资源进行深度推理"
  }
}
```

---

## ✅ 前端验收清单

- [ ] TypeScript 类型定义已扩展
- [ ] Session/Message/SessionConfig 类型已更新
- [ ] NewSessionDialog 显示 thinking 配置
- [ ] 可在新建会话时启用/禁用 thinking
- [ ] 可配置 thinking 预算
- [ ] MessageBubble 正确显示 thinking 块
- [ ] Thinking 块可折叠/展开
- [ ] 会话编辑面板可修改 thinking 参数
- [ ] 后端返回的 thinking 内容正确显示
- [ ] 禁用 thinking 后正常工作
- [ ] 编译通过：`npm run build`

---

## 🚀 快速检查清单

1. **类型安全**
   - [ ] 所有 thinking 相关字段都在 TypeScript 接口中定义
   - [ ] 无类型错误

2. **UI 完整**
   - [ ] 新建会话对话框有 thinking 选项
   - [ ] 消息气泡显示 thinking 块
   - [ ] 编辑会话能修改 thinking 参数

3. **交互正常**
   - [ ] 启用/禁用 thinking 能切换对应 UI
   - [ ] thinking 块能折叠/展开
   - [ ] 修改预算立即生效

4. **数据一致**
   - [ ] 前端发送的配置与后端使用一致
   - [ ] 后端返回的 thinking 正确显示
   - [ ] 刷新后配置保持不变

---

## 📚 参考

### 后端接口

**创建会话：** `POST /api/sessions`
```json
{
  "title": "session-title",
  "model": "claude-3-5-sonnet-20241022",
  "thinkingEnabled": true,
  "thinkingBudget": 5000
}
```

**更新会话：** `PATCH /api/sessions/{id}`
```json
{
  "thinkingEnabled": true,
  "thinkingBudget": 8000
}
```

**消息响应：** 包含 thinking 字段
```json
{
  "reply": "回复内容",
  "thinking": "思考过程内容...",
  "messages": [...]
}
```

---

## 💡 最佳实践

1. **thinking 块应该可折叠** - 减少界面混乱
2. **显示 thinking 长度提示** - 让用户了解推理深度
3. **thinking 与内容分离** - 清晰的视觉层级
4. **支持复制 thinking** - 用户可能想保存推理过程
5. **提供预设预算** - 常用值（3000/5000/10000）

---

**前端实现完全可选** - 后端已实现所有必需功能。
前端实现主要是为了提升用户体验，让 thinking 过程可见。

推荐完成后端验收后再进行前端实现。
