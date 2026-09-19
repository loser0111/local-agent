<script setup>
import { ref, computed } from 'vue'
import { X, FolderOpen, FileArchive, GitBranch, Loader2, CheckCircle2, AlertTriangle } from 'lucide-vue-next'
import { useSkillsStore } from '@/stores/skills'
import { pickSkillFolder, pickSkillZip } from '@/api/skill'

const props = defineProps({
  visible: Boolean,
})
const emit = defineEmits(['close', 'installed'])

const store = useSkillsStore()

const tab = ref('folder') // folder | zip | git
const folderPath = ref('')
const zipPath = ref('')
const gitUrl = ref('')
const gitRef = ref('')
const gitSubdir = ref('')
const errorMsg = ref('')
const results = ref([])

const busy = computed(() => store.installing)

function reset() {
  errorMsg.value = ''
  results.value = []
  folderPath.value = ''
  zipPath.value = ''
  gitUrl.value = ''
  gitRef.value = ''
  gitSubdir.value = ''
}

function close() {
  reset()
  emit('close')
}

async function browseFolder() {
  try {
    const p = await pickSkillFolder()
    if (p) folderPath.value = p
  } catch (e) {
    errorMsg.value = `打开目录选择失败：${e.message || e}`
  }
}

async function browseZip() {
  try {
    const p = await pickSkillZip()
    if (p) zipPath.value = p
  } catch (e) {
    errorMsg.value = `打开文件选择失败：${e.message || e}`
  }
}

async function run(kind) {
  errorMsg.value = ''
  results.value = []
  try {
    if (kind === 'folder') {
      if (!folderPath.value.trim()) return (errorMsg.value = '请先选择或填写技能文件夹路径')
      results.value = await store.installFromFolder(folderPath.value.trim())
    } else if (kind === 'zip') {
      if (!zipPath.value.trim()) return (errorMsg.value = '请先选择或填写压缩包路径')
      results.value = await store.installFromZip(zipPath.value.trim())
    } else {
      if (!gitUrl.value.trim()) return (errorMsg.value = '请填写 Git 仓库地址')
      results.value = await store.installFromGit(gitUrl.value.trim(), gitRef.value.trim(), gitSubdir.value.trim())
    }
    if (results.value.length > 0) emit('installed', results.value)
  } catch (e) {
    errorMsg.value = `安装失败：${e.message || e}`
  }
}
</script>

<template>
  <div v-if="props.visible" class="dialog-mask" @click.self="close">
    <div class="dialog">
      <div class="dialog-header">
        <h3>安装技能</h3>
        <button class="icon-btn" @click="close"><X :size="16" /></button>
      </div>

      <div class="tabs">
        <button class="tab" :class="{ active: tab === 'folder' }" @click="tab = 'folder'">
          <FolderOpen :size="14" /> 本地文件夹
        </button>
        <button class="tab" :class="{ active: tab === 'zip' }" @click="tab = 'zip'">
          <FileArchive :size="14" /> Zip 压缩包
        </button>
        <button class="tab" :class="{ active: tab === 'git' }" @click="tab = 'git'">
          <GitBranch :size="14" /> Git 仓库
        </button>
      </div>

      <div class="dialog-body">
        <div v-if="errorMsg" class="error-banner">{{ errorMsg }}</div>

        <!-- 本地文件夹 -->
        <div v-if="tab === 'folder'" class="pane">
          <label class="field-label">技能文件夹</label>
          <div class="path-row">
            <input
              v-model="folderPath"
              class="input mono"
              placeholder="选择技能目录，或装着若干技能目录的父目录"
            />
            <button class="btn btn-ghost btn-sm" :disabled="busy" @click="browseFolder">浏览…</button>
          </div>
          <div class="hint">
            文件夹自身含 SKILL.md 即装为单个技能；否则会扫描它的直接子目录，装下周边的多个技能。
          </div>
        </div>

        <!-- Zip -->
        <div v-else-if="tab === 'zip'" class="pane">
          <label class="field-label">技能压缩包</label>
          <div class="path-row">
            <input v-model="zipPath" class="input mono" placeholder="选择 .zip 文件" />
            <button class="btn btn-ghost btn-sm" :disabled="busy" @click="browseZip">浏览…</button>
          </div>
          <div class="hint">
            解压到临时目录后校验再落盘；含绝对路径、.. 或软链接条目的压缩包会被拒绝。
          </div>
        </div>

        <!-- Git -->
        <div v-else class="pane">
          <label class="field-label">仓库地址 <span class="required">*</span></label>
          <input
            v-model="gitUrl"
            class="input mono"
            placeholder="https://github.com/user/my-skills.git"
          />
          <div class="row-2">
            <div>
              <label class="field-label">分支 / 标签</label>
              <input v-model="gitRef" class="input mono" placeholder="可留空（默认分支）" />
            </div>
            <div>
              <label class="field-label">仓库内子目录</label>
              <input v-model="gitSubdir" class="input mono" placeholder="可留空（如 skills/pdf）" />
            </div>
          </div>
          <div class="hint">
            以 --depth 1 浅克隆；安装后会记下来源，之后可在列表里对该技能点「更新」重新拉取。
            需要本地已安装 git 且能访问该仓库。
          </div>
        </div>

        <!-- 结果 -->
        <div v-if="results.length > 0" class="results">
          <div v-for="r in results" :key="r.id" class="result-row">
            <CheckCircle2 :size="14" class="ok-icon" />
            <div class="result-body">
              <div class="result-title">
                {{ r.action === 'updated' ? '已更新' : '已安装' }}：{{ r.name }}
                <span class="mono result-id">{{ r.id }}</span>
              </div>
              <div class="result-dir mono">{{ r.dir }}</div>
              <div v-for="(w, i) in r.warnings || []" :key="i" class="result-warn">
                <AlertTriangle :size="11" /> {{ w }}
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn btn-ghost" :disabled="busy" @click="close">关闭</button>
        <button
          class="btn btn-primary"
          :disabled="busy"
          @click="run(tab)"
        >
          <Loader2 v-if="busy" :size="13" class="spin" />
          {{ busy ? '安装中…' : '安装' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.dialog-mask {
  position: fixed;
  inset: 0;
  background: $color-overlay;
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  padding: $space-lg;
}

.dialog {
  background: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  width: 620px;
  max-width: 100%;
  max-height: 86vh;
  display: flex;
  flex-direction: column;
  box-shadow: $shadow-lg;
}

.dialog-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: $space-md $space-lg;
  border-bottom: 1px solid $color-border;

  h3 {
    font-size: $font-size-md;
    font-weight: $font-weight-semibold;
    color: $color-text-primary;
  }
}

.tabs {
  display: flex;
  gap: 2px;
  padding: $space-sm $space-lg 0;
  border-bottom: 1px solid $color-border;
}

.tab {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 7px 12px;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: $color-text-secondary;
  font-size: $font-size-xs;
  cursor: pointer;

  &:hover {
    color: $color-text-primary;
  }

  &.active {
    color: $color-primary;
    border-bottom-color: $color-primary;
  }
}

.dialog-body {
  padding: $space-lg;
  overflow-y: auto;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: $space-sm;
  padding: $space-md $space-lg;
  border-top: 1px solid $color-border;
}

.pane {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.field-label {
  display: block;
  font-size: $font-size-sm;
  font-weight: $font-weight-medium;
  color: $color-text-primary;
  margin-top: $space-sm;
}

.path-row {
  display: flex;
  gap: $space-sm;

  .input {
    flex: 1;
  }
}

.row-2 {
  display: flex;
  gap: $space-md;

  > div {
    flex: 1;
  }
}

.btn-sm {
  padding: 5px 12px;
  font-size: $font-size-xs;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  white-space: nowrap;
}

.input {
  width: 100%;
  background: $color-bg-primary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  padding: 7px 10px;
  font-size: $font-size-sm;
  color: $color-text-primary;
  outline: none;

  &:focus {
    border-color: $color-primary;
  }

  &.mono {
    font-family: monospace;
  }
}

.hint {
  font-size: $font-size-xs;
  color: $color-text-muted;
  line-height: 1.6;
}

.required {
  color: $color-error;
}

.error-banner {
  padding: $space-sm $space-md;
  margin-bottom: $space-md;
  background-color: rgb(var(--color-error-rgb) / 0.12);
  border: 1px solid rgb(var(--color-error-rgb) / 0.4);
  border-radius: $radius-sm;
  color: $color-error;
  font-size: $font-size-xs;
  line-height: 1.6;
  word-break: break-word;
}

.results {
  margin-top: $space-lg;
  border-top: 1px solid $color-border;
  padding-top: $space-md;
  display: flex;
  flex-direction: column;
  gap: $space-sm;
}

.result-row {
  display: flex;
  gap: 8px;
  align-items: flex-start;
}

.ok-icon {
  color: $color-success;
  margin-top: 2px;
  flex-shrink: 0;
}

.result-body {
  min-width: 0;
  flex: 1;
}

.result-title {
  font-size: $font-size-sm;
  color: $color-text-primary;
}

.result-id {
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin-left: 6px;
}

.result-dir {
  font-size: $font-size-xs;
  color: $color-text-muted;
  word-break: break-all;
}

.result-warn {
  display: flex;
  align-items: center;
  gap: 4px;
  font-size: $font-size-xs;
  color: $color-yellow;
  margin-top: 2px;
}

.icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  background: transparent;
  border: none;
  border-radius: 4px;
  color: $color-text-secondary;
  cursor: pointer;

  &:hover {
    background: $color-bg-tertiary;
    color: $color-text-primary;
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
