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
  FileCode2,
  FolderOpen,
  AlertTriangle,
  Download,
  GitBranch,
  EyeOff,
  ArrowUpCircle,
} from 'lucide-vue-next'
import SkillEditDialog from './SkillEditDialog.vue'
import SkillInstallDialog from './SkillInstallDialog.vue'
import { useSkillsStore } from '@/stores/skills'
import { useUiStore } from '@/stores/ui'
import { skillsDir } from '@/api/skill'

const store = useSkillsStore()
const ui = useUiStore()

const keyword = ref('')
const dialogVisible = ref(false)
const installVisible = ref(false)
const editingSkill = ref(null)
const dir = ref('')
const expanded = ref({})
const previewBody = ref({})
const previewResources = ref({})
const previewLoading = ref({})
const updatingId = ref('')

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
    ui.notify(`切换失败：${e.message || e}`, 'error')
  }
}

async function handleUpdate(s) {
  const ok = await ui.ask({
    title: '更新技能',
    message: `将从 ${s.install?.source || '原仓库'} 重新拉取技能「${s.name}」。\n失败时旧版本会保留。`,
    confirmText: '更新',
  })
  if (!ok) return
  updatingId.value = s.id
  try {
    const r = await store.update(s.id)
    const warns = (r?.warnings || []).length
    ui.notify(`技能 ${r?.id || s.id} 已更新到最新版本${warns ? `（${warns} 项告警，展开可见）` : ''}`, 'success')
  } catch (e) {
    ui.notify(`更新失败：${e.message || e}`, 'error')
  } finally {
    updatingId.value = ''
  }
}

async function handleDelete(s) {
  const fromGit = s.install?.sourceType === 'git'
  const ok = await ui.ask({
    title: fromGit ? '卸载技能' : '删除技能',
    message: `确定${fromGit ? '卸载' : '删除'}技能「${s.name}」吗？将删除整个目录：\n${s.dir || s.id}`,
    confirmText: fromGit ? '卸载' : '删除',
  })
  if (!ok) return
  try {
    await store.remove(s.id)
  } catch (e) {
    ui.notify(`删除失败：${e.message || e}`, 'error')
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
      previewResources.value[s.id] = detail.resources || []
    } catch (e) {
      previewBody.value[s.id] = `读取失败：${e.message || e}`
    } finally {
      previewLoading.value[s.id] = false
    }
  }
}

function sourceLabel(s) {
  const t = s.install?.sourceType
  if (t === 'git') return 'Git'
  if (t === 'zip') return 'Zip'
  if (t === 'folder') return '文件夹'
  return ''
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
      <button class="btn btn-ghost btn-sm" @click="installVisible = true">
        <Download :size="13" /> 安装
      </button>
      <button class="btn btn-primary btn-sm" @click="openAdd">
        <Plus :size="14" /> 新建技能
      </button>
    </div>

    <div v-if="dir" class="dir-tip">
      <FolderOpen :size="12" />
      技能目录：<span class="mono" :title="dir">{{ dir }}</span>
      <span class="dir-hint">（也可直接往该目录放入技能文件夹后点刷新）</span>
    </div>

    <div v-if="store.invalidCount > 0" class="notice">
      <AlertTriangle :size="12" />
      有 {{ store.invalidCount }} 个技能未通过校验，不会参与模型的技能路由。
    </div>

    <div v-if="store.loading && filtered.length === 0" class="list-tip">加载中...</div>
    <div v-else-if="filtered.length === 0" class="list-tip">
      {{ keyword ? '没有匹配的技能' : '暂无技能，点击右上角「新建技能」或「安装」' }}
    </div>

    <div v-else class="skill-list">
      <div v-for="s in filtered" :key="s.id" class="skill-card" :class="{ disabled: !s.enabled, invalid: (s.errors || []).length > 0 }">
        <div class="skill-main">
          <div class="skill-icon-wrap" :class="{ error: (s.errors || []).length > 0 }">
            <BookOpen :size="18" />
          </div>

          <div class="skill-info" @click="toggleExpand(s)">
            <div class="skill-title-row">
              <span class="skill-name">{{ s.name }}</span>
              <span class="skill-id mono">{{ s.id }}</span>
              <span v-if="s.builtin" class="tag tag-builtin">内置</span>
              <span v-if="s.version" class="tag tag-version">v{{ s.version }}</span>
              <span v-if="sourceLabel(s)" class="tag tag-source">
                <GitBranch v-if="s.install?.sourceType === 'git'" :size="10" />
                {{ sourceLabel(s) }}
              </span>
              <span v-if="s.hasScripts" class="tag tag-scripts">
                <FileCode2 :size="10" /> 脚本
              </span>
              <span
                v-if="s.disableModelInvocation"
                class="tag tag-manual"
                title="disable-model-invocation：模型不会自动触发，只能由用户 /技能名 调用"
              >
                <EyeOff :size="10" /> 仅手动
              </span>
              <span
                v-if="s.userInvocable === false"
                class="tag tag-nouser"
                title="user-invocable: false：不允许用户显式调用"
              >
                禁手动
              </span>
              <span v-if="(s.errors || []).length > 0" class="tag tag-error" :title="(s.errors || []).join('\n')">
                <AlertTriangle :size="10" /> {{ (s.errors || []).length }} 项错误
              </span>
              <span
                v-else-if="(s.warnings || []).length > 0"
                class="tag tag-warn"
                :title="(s.warnings || []).join('\n')"
              >
                {{ (s.warnings || []).length }} 项告警
              </span>
              <ChevronDown v-if="expanded[s.id]" :size="13" class="expand-icon" />
              <ChevronRight v-else :size="13" class="expand-icon" />
            </div>
            <div class="skill-desc">{{ s.description || '（无描述 —— 缺少 description 时技能不会被模型触发）' }}</div>
          </div>

          <div class="skill-actions">
            <button
              v-if="s.install?.canUpdate"
              class="icon-btn"
              title="从原仓库重新拉取更新"
              :disabled="updatingId === s.id"
              @click="handleUpdate(s)"
            >
              <Loader2 v-if="updatingId === s.id" :size="14" class="spin" />
              <ArrowUpCircle v-else :size="14" />
            </button>
            <label class="switch" :title="s.enabled ? '点击停用' : '点击启用'">
              <input type="checkbox" :checked="s.enabled" @change="handleToggle(s)" />
              <span class="slider"></span>
            </label>
            <button class="icon-btn" title="编辑" @click="openEdit(s)"><Pencil :size="14" /></button>
            <button v-if="!s.builtin" class="icon-btn danger" title="删除 / 卸载" @click="handleDelete(s)">
              <Trash2 :size="14" />
            </button>
          </div>
        </div>

        <!-- 正文与资源预览（L2 / L3） -->
        <div v-if="expanded[s.id]" class="skill-preview">
          <Loader2 v-if="previewLoading[s.id]" :size="13" class="spin" />
          <template v-else>
            <div v-if="(s.errors || []).length > 0" class="issue-list error">
              <div v-for="(e, i) in s.errors" :key="i">{{ e }}</div>
            </div>
            <div v-if="(s.warnings || []).length > 0" class="issue-list warn">
              <div v-for="(w, i) in s.warnings" :key="i">{{ w }}</div>
            </div>
            <div v-if="s.install" class="meta-row">
              安装来源：{{ s.install.sourceType }}
              <span v-if="s.install.source" class="mono"> · {{ s.install.source }}</span>
              <span v-if="s.install.ref" class="mono"> · {{ s.install.ref }}</span>
              <span v-if="s.install.subdir" class="mono"> · {{ s.install.subdir }}</span>
            </div>
            <div v-if="(s.allowedTools || []).length > 0" class="meta-row">
              allowed-tools：{{ (s.allowedTools || []).join(', ') }}
            </div>
            <div v-if="(previewResources[s.id] || []).length > 0" class="resource-block">
              <div class="resource-title">技能自带资源（L3，用 read_skill_file 读取）</div>
              <div v-for="r in previewResources[s.id]" :key="r.path" class="resource-row">
                <span class="mono">{{ r.path }}</span>
                <span class="resource-kind">{{ r.kind }}</span>
                <span class="resource-size">{{ r.size }} B</span>
              </div>
            </div>
            <pre>{{ previewBody[s.id] }}</pre>
          </template>
        </div>
      </div>
    </div>

    <SkillEditDialog
      :visible="dialogVisible"
      :skill="editingSkill"
      @close="dialogVisible = false"
      @saved="() => {}"
    />
    <SkillInstallDialog
      :visible="installVisible"
      @close="installVisible = false"
      @installed="() => {}"
    />
  </div>
</template>

<style scoped lang="scss">
.skill-settings {
  max-width: 780px;
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
  margin-bottom: $space-sm;

  .mono {
    font-family: monospace;
    color: $color-text-secondary;
  }

  .dir-hint {
    color: $color-text-muted;
    opacity: 0.75;
  }
}

.notice {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: $font-size-xs;
  color: $color-yellow;
  background: rgb(var(--color-warning-rgb) / 0.08);
  border: 1px solid rgb(var(--color-warning-rgb) / 0.3);
  border-radius: $radius-sm;
  padding: 6px 10px;
  margin-bottom: $space-lg;
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
    border-color: rgb(var(--color-error-rgb) / 0.45);
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
  background: rgb(var(--color-primary-rgb) / 0.12);
  color: $color-primary;

  &.error {
    background: rgb(var(--color-error-rgb) / 0.12);
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
  gap: 6px;
  flex-wrap: wrap;
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
    background: rgb(var(--color-warning-rgb) / 0.15);
    color: $color-yellow;
  }
  &.tag-version {
    background: rgb(var(--color-text-secondary-rgb) / 0.15);
    color: $color-text-secondary;
  }
  &.tag-source {
    background: rgb(var(--color-info-rgb) / 0.15);
    color: $color-info;
  }
  &.tag-scripts {
    background: rgb(var(--color-success-rgb) / 0.15);
    color: $color-success;
  }
  &.tag-manual {
    background: rgb(var(--color-primary-rgb) / 0.18);
    color: $color-primary;
  }
  &.tag-nouser {
    background: rgb(var(--color-text-secondary-rgb) / 0.18);
    color: $color-text-secondary;
  }
  &.tag-warn {
    background: rgb(var(--color-warning-rgb) / 0.15);
    color: $color-yellow;
  }
  &.tag-error {
    background: rgb(var(--color-error-rgb) / 0.15);
    color: $color-error;
    max-width: 200px;
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
  -webkit-line-clamp: 2;
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

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
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
    background: rgb(var(--color-primary-rgb) / 0.35);
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
  max-height: 360px;
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

.issue-list {
  font-size: $font-size-xs;
  line-height: 1.7;
  margin-bottom: 6px;

  &.error {
    color: $color-error;
  }

  &.warn {
    color: $color-yellow;
  }
}

.meta-row {
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin-bottom: 4px;
  word-break: break-all;
}

.resource-block {
  margin: 6px 0 8px;
}

.resource-title {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  margin-bottom: 3px;
}

.resource-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: $font-size-xs;
  color: $color-text-muted;
  padding: 1px 0;

  .mono {
    font-family: monospace;
    color: $color-text-secondary;
  }
}

.resource-kind {
  color: $color-info;
}

.resource-size {
  margin-left: auto;
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
