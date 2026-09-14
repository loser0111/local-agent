<script setup>
import { ref, computed, onMounted } from 'vue'
import {
  Plus,
  Pencil,
  Trash2,
  RefreshCw,
  Loader2,
  ChevronDown,
  ChevronRight,
  BookOpen,
  Pin,
  FileCode2,
  FolderOpen,
  AlertTriangle,
} from 'lucide-vue-next'
import SkillEditDialog from './SkillEditDialog.vue'
import { useSkillsStore } from '@/stores/skills'
import { skillsDir } from '@/api/skill'

const store = useSkillsStore()

const keyword = ref('')
const dialogVisible = ref(false)
const editingSkill = ref(null)
const dir = ref('')
const expanded = ref({})
const previewBody = ref({})
const previewLoading = ref({})

const filtered = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return store.skills
  return store.skills.filter(
    (s) =>
      (s.id || '').toLowerCase().includes(kw) ||
      (s.name || '').toLowerCase().includes(kw) ||
      (s.description || '').toLowerCase().includes(kw)
  )
})

onMounted(async () => {
  store.load()
  try {
    dir.value = await skillsDir()
  } catch {
    /* 忽略 */
  }
})

function openAdd() {
  editingSkill.value = null
  dialogVisible.value = true
}

function openEdit(s) {
  editingSkill.value = s
  dialogVisible.value = true
}

async function handleToggle(s) {
  try {
    await store.toggle(s.id, !s.enabled)
  } catch (e) {
    alert(`切换失败：${e.message || e}`)
  }
}

async function handleAlwaysInject(s) {
  try {
    await store.setAlwaysInject(s.id, !s.alwaysInject)
  } catch (e) {
    alert(`设置失败：${e.message || e}`)
  }
}

async function handleDelete(s) {
  if (!confirm(`确定删除技能「${s.name}」吗？将删除整个目录：\n${s.dir || s.id}`)) return
  try {
    await store.remove(s.id)
  } catch (e) {
    alert(`删除失败：${e.message || e}`)
  }
}

async function refresh() {
  await store.refresh()
}

async function toggleExpand(s) {
  const open = !expanded.value[s.id]
  expanded.value[s.id] = open
  if (open && previewBody.value[s.id] === undefined) {
    previewLoading.value[s.id] = true
    try {
      const detail = await store.detail(s.id)
      previewBody.value[s.id] = detail.body || '（空正文）'
    } catch (e) {
      previewBody.value[s.id] = `读取失败：${e.message || e}`
    } finally {
      previewLoading.value[s.id] = false
    }
  }
}
</script>

<template>
  <div class="skill-settings">
    <div class="toolbar">
      <input v-model="keyword" class="input search" placeholder="搜索技能名称、ID 或描述..." />
      <button class="btn btn-ghost btn-sm" @click="refresh">
        <Loader2 v-if="store.loading" :size="13" class="spin" />
        <RefreshCw v-else :size="13" />
        刷新
      </button>
      <button class="btn btn-primary btn-sm" @click="openAdd">
        <Plus :size="14" /> 添加技能
      </button>
    </div>

    <div v-if="dir" class="dir-tip">
      <FolderOpen :size="12" />
      技能目录：<span class="mono" :title="dir">{{ dir }}</span>
      <span class="dir-hint">（也可直接往该目录放入技能文件夹后点刷新）</span>
    </div>

    <div v-if="store.loading && filtered.length === 0" class="list-tip">加载中...</div>
    <div v-else-if="filtered.length === 0" class="list-tip">
      {{ keyword ? '没有匹配的技能' : '暂无技能，点击右上角「添加技能」创建' }}
    </div>

    <div v-else class="skill-list">
      <div v-for="s in filtered" :key="s.id" class="skill-card" :class="{ disabled: !s.enabled, invalid: !!s.error }">
        <div class="skill-main">
          <div class="skill-icon-wrap" :class="{ error: !!s.error }">
            <BookOpen :size="18" />
          </div>

          <div class="skill-info" @click="toggleExpand(s)">
            <div class="skill-title-row">
              <span class="skill-name">{{ s.name }}</span>
              <span class="skill-id mono">{{ s.id }}</span>
              <span v-if="s.builtin" class="tag tag-builtin">内置</span>
              <span v-if="s.hasScripts" class="tag tag-scripts">
                <FileCode2 :size="10" /> 脚本
              </span>
              <span v-if="s.alwaysInject" class="tag tag-inject">
                <Pin :size="10" /> 强制注入
              </span>
              <span v-if="s.error" class="tag tag-error" :title="s.error">
                <AlertTriangle :size="10" /> {{ s.error }}
              </span>
              <ChevronDown v-if="expanded[s.id]" :size="13" class="expand-icon" />
              <ChevronRight v-else :size="13" class="expand-icon" />
            </div>
            <div class="skill-desc">{{ s.description || '（无描述）' }}</div>
          </div>

          <div class="skill-actions">
            <button
              class="icon-btn"
              :class="{ active: s.alwaysInject }"
              :title="s.alwaysInject ? '取消强制注入（改为模型按需 read_skill 加载）' : '强制注入：正文直接进入每次对话的 system prompt'"
              @click="handleAlwaysInject(s)"
            >
              <Pin :size="14" />
            </button>
            <label class="switch" :title="s.enabled ? '点击停用' : '点击启用'">
              <input type="checkbox" :checked="s.enabled" @change="handleToggle(s)" />
              <span class="slider"></span>
            </label>
            <button class="icon-btn" title="编辑" @click="openEdit(s)"><Pencil :size="14" /></button>
            <button v-if="!s.builtin" class="icon-btn danger" title="删除" @click="handleDelete(s)">
              <Trash2 :size="14" />
            </button>
          </div>
        </div>

        <!-- 正文预览 -->
        <div v-if="expanded[s.id]" class="skill-preview">
          <Loader2 v-if="previewLoading[s.id]" :size="13" class="spin" />
          <pre v-else>{{ previewBody[s.id] }}</pre>
        </div>
      </div>
    </div>

    <SkillEditDialog
      :visible="dialogVisible"
      :skill="editingSkill"
      @close="dialogVisible = false"
      @saved="() => {}"
    />
  </div>
</template>

<style scoped lang="scss">
.skill-settings {
  max-width: 760px;
}

.toolbar {
  display: flex;
  gap: $space-sm;
  margin-bottom: $space-sm;

  .search {
    flex: 1;
  }
}

.btn-sm {
  padding: 5px 12px;
  font-size: $font-size-xs;
  gap: 5px;
  display: inline-flex;
  align-items: center;
  white-space: nowrap;
}

.dir-tip {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin-bottom: $space-lg;

  .mono {
    font-family: monospace;
    color: $color-text-secondary;
  }

  .dir-hint {
    color: $color-text-muted;
    opacity: 0.75;
  }
}

.list-tip {
  text-align: center;
  color: $color-text-muted;
  font-size: $font-size-sm;
  padding: $space-xl 0;
}

.skill-list {
  display: flex;
  flex-direction: column;
  gap: $space-sm;
}

.skill-card {
  background: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  transition: border-color $transition-fast;

  &:hover {
    border-color: $color-border-light;
  }

  &.disabled {
    opacity: 0.55;
  }

  &.invalid {
    border-color: rgba(255, 85, 85, 0.45);
  }
}

.skill-main {
  display: flex;
  align-items: center;
  gap: $space-md;
  padding: $space-md;
}

.skill-icon-wrap {
  width: 38px;
  height: 38px;
  border-radius: $radius-sm;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  background: rgba(124, 58, 237, 0.12);
  color: #a78bfa;

  &.error {
    background: rgba(255, 85, 85, 0.12);
    color: $color-error;
  }
}

.skill-info {
  flex: 1;
  min-width: 0;
  cursor: pointer;
}

.skill-title-row {
  display: flex;
  align-items: center;
  gap: $space-sm;
}

.expand-icon {
  margin-left: auto;
  color: $color-text-muted;
  flex-shrink: 0;
}

.skill-name {
  font-size: $font-size-sm;
  font-weight: $font-weight-semibold;
  color: $color-text-primary;
}

.skill-id {
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.tag {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 8px;
  font-weight: $font-weight-medium;

  &.tag-builtin {
    background: rgba(250, 204, 21, 0.15);
    color: #facc15;
  }
  &.tag-scripts {
    background: rgba(34, 197, 94, 0.15);
    color: #22c55e;
  }
  &.tag-inject {
    background: rgba(124, 58, 237, 0.18);
    color: #c084fc;
  }
  &.tag-error {
    background: rgba(255, 85, 85, 0.15);
    color: $color-error;
    max-width: 280px;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
}

.skill-desc {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  margin-top: 4px;
  display: -webkit-box;
  -webkit-line-clamp: 1;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.skill-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}

.icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  background: transparent;
  border: none;
  border-radius: 4px;
  color: $color-text-secondary;
  cursor: pointer;

  &:hover {
    background: $color-bg-tertiary;
    color: $color-text-primary;
  }

  &.active {
    color: #c084fc;
  }

  &.danger:hover {
    color: $color-error;
  }
}

// 开关
.switch {
  position: relative;
  display: inline-block;
  width: 36px;
  height: 20px;
  margin-right: 4px;

  input {
    opacity: 0;
    width: 0;
    height: 0;
  }

  .slider {
    position: absolute;
    inset: 0;
    background: $color-bg-tertiary;
    border: 1px solid $color-border;
    border-radius: 20px;
    cursor: pointer;
    transition: $transition-fast;

    &::before {
      content: '';
      position: absolute;
      width: 14px;
      height: 14px;
      left: 2px;
      top: 2px;
      background: $color-text-muted;
      border-radius: 50%;
      transition: $transition-fast;
    }
  }

  input:checked + .slider {
    background: rgba(124, 58, 237, 0.35);
    border-color: $color-primary;

    &::before {
      transform: translateX(16px);
      background: $color-primary;
    }
  }
}

// 正文预览
.skill-preview {
  border-top: 1px solid $color-border;
  padding: $space-sm $space-md;
  max-height: 280px;
  overflow-y: auto;

  pre {
    margin: 0;
    font-family: monospace;
    font-size: $font-size-xs;
    line-height: 1.6;
    color: $color-text-secondary;
    white-space: pre-wrap;
    word-break: break-word;
  }
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
