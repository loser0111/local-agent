<script setup>
import { reactive, ref, computed, watch } from 'vue'
import { X, Loader2 } from 'lucide-vue-next'
import { useSkillsStore } from '@/stores/skills'

const props = defineProps({
  visible: Boolean,
  skill: { type: Object, default: null }, // 编辑传入，null 为新增
})
const emit = defineEmits(['close', 'saved'])

const store = useSkillsStore()

const emptyForm = () => ({
  id: '',
  name: '',
  description: '',
  version: '',
  license: '',
  author: '',
  allowedTools: '',
  disableModelInvocation: false,
  userInvocable: true,
  body: '',
  builtin: false,
})

const form = reactive(emptyForm())
const saving = ref(false)
const loadingBody = ref(false)
const errorMsg = ref('')
const skillWarnings = ref([])

const isEdit = computed(() => !!form.id)
const idValid = computed(() => /^[A-Za-z0-9_-]{1,64}$/.test(form.id.trim()))
// 仅 git 来源的技能由安装器接管，编辑会被下次更新覆盖
const installManaged = computed(() => props.skill?.install?.sourceType === 'git')

watch(
  () => props.visible,
  async (v) => {
    if (!v) return
    errorMsg.value = ''
    skillWarnings.value = []
    Object.assign(form, emptyForm())
    if (props.skill) {
      Object.assign(form, {
        id: props.skill.id || '',
        name: props.skill.name || '',
        description: props.skill.description || '',
        version: props.skill.version || '',
        license: props.skill.license || '',
        author: props.skill.author || '',
        allowedTools: (props.skill.allowedTools || []).join(', '),
        disableModelInvocation: !!props.skill.disableModelInvocation,
        userInvocable: props.skill.userInvocable !== false,
        builtin: !!props.skill.builtin,
      })
      skillWarnings.value = props.skill.warnings || []
      // 拉取正文（列表接口不含 body）
      loadingBody.value = true
      try {
        const detail = await store.detail(props.skill.id)
        form.body = detail.body || ''
      } catch (e) {
        errorMsg.value = `读取正文失败：${e.message || e}`
      } finally {
        loadingBody.value = false
      }
    }
  }
)

async function handleSave() {
  errorMsg.value = ''
  if (!form.id.trim()) return (errorMsg.value = '技能 ID 不能为空')
  if (!idValid.value) return (errorMsg.value = 'ID 只能包含字母、数字、下划线、短横线（≤64 字符）')
  if (!form.description.trim())
    return (errorMsg.value = 'description 不能为空——模型靠它判断何时使用该技能')
  if (form.description.trim().length > 1024)
    return (errorMsg.value = 'description 超过 1024 字符（标准上限）')
  if (form.name.trim().length > 64) return (errorMsg.value = 'name 超过 64 字符（标准上限）')

  saving.value = true
  try {
    await store.save(form.id.trim(), {
      name: form.name.trim() || form.id.trim(),
      description: form.description.trim(),
      version: form.version.trim(),
      license: form.license.trim(),
      author: form.author.trim(),
      allowedTools: form.allowedTools
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean),
      disableModelInvocation: form.disableModelInvocation,
      userInvocable: form.userInvocable,
      body: form.body,
    })
    emit('saved')
    emit('close')
  } catch (e) {
    errorMsg.value = `保存失败：${e.message || e}`
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div v-if="visible" class="dialog-mask" @click.self="emit('close')">
    <div class="dialog">
      <div class="dialog-header">
        <h3>{{ isEdit ? '编辑技能' : '添加技能' }}</h3>
        <button class="icon-btn" @click="emit('close')"><X :size="16" /></button>
      </div>

      <div class="dialog-body">
        <div v-if="errorMsg" class="error-banner">{{ errorMsg }}</div>

        <div v-if="installManaged" class="warn-banner">
          该技能由 Git 仓库安装，本地修改会在下次「更新」时被覆盖。请改上游仓库后重新拉取。
        </div>

        <div v-for="(w, i) in skillWarnings" :key="i" class="warn-banner">告警：{{ w }}</div>

        <div class="form-row">
          <div class="form-group">
            <label>技能 ID <span class="required">*</span></label>
            <input
              v-model="form.id"
              class="input mono"
              placeholder="如：pdf-report（即技能目录名）"
              :disabled="isEdit"
            />
            <div class="hint">字母、数字、下划线、短横线；创建后不可修改</div>
          </div>
          <div class="form-group">
            <label>名称 name <span class="required">*</span></label>
            <input v-model="form.name" class="input" placeholder="如：pdf-report（建议与 ID 一致）" />
            <div class="hint">标准建议 kebab-case；与 ID 不一致时会告警但不影响使用</div>
          </div>
        </div>

        <div class="form-group">
          <label>描述 description <span class="required">*</span></label>
          <textarea
            v-model="form.description"
            class="input textarea-sm"
            rows="2"
            placeholder="模型依据此描述判断何时使用该技能，请写清「做什么 + 什么时候用」"
          ></textarea>
          <div class="hint">
            {{ form.description.trim().length }} / 1024 字符 —— 这是模型自动触发技能的唯一依据
          </div>
        </div>

        <div class="form-row">
          <div class="form-group">
            <label>version</label>
            <input v-model="form.version" class="input mono" placeholder="1.0.0" />
          </div>
          <div class="form-group">
            <label>license</label>
            <input v-model="form.license" class="input mono" placeholder="MIT" />
          </div>
          <div class="form-group">
            <label>author</label>
            <input v-model="form.author" class="input" placeholder="可选" />
          </div>
        </div>

        <div class="form-group">
          <label>allowed-tools</label>
          <input
            v-model="form.allowedTools"
            class="input mono"
            placeholder="bash, read_file（逗号分隔，可留空）"
          />
          <div class="hint">
            标准字段，本版本只作提示写入技能说明，尚未接入权限网关做硬约束
          </div>
        </div>

        <div class="form-group">
          <label>调用方式</label>
          <label class="check-row">
            <input v-model="form.disableModelInvocation" type="checkbox" />
            <span>
              disable-model-invocation
              <span class="hint-inline">开启后模型不会自动触发，只能由用户 /{{ form.id || '技能名' }} 调用</span>
            </span>
          </label>
          <label class="check-row">
            <input v-model="form.userInvocable" type="checkbox" />
            <span>
              user-invocable
              <span class="hint-inline">关闭后不允许用户显式调用（默认开启）</span>
            </span>
          </label>
        </div>

        <div class="form-group">
          <label>
            正文（SKILL.md 内容，Markdown）
            <Loader2 v-if="loadingBody" :size="12" class="spin inline-spin" />
          </label>
          <textarea
            v-model="form.body"
            class="input textarea-body"
            rows="14"
            placeholder="# 操作手册&#10;&#10;模型调用 read_skill 后会看到这里的全部内容；可指引它用 read_skill_file 读 references/，用 exec_shell 运行 scripts/ 下的脚本"
          ></textarea>
          <div class="hint">
            正文仅在需要时加载（L2），不占用常驻上下文；正文里引用的 references/、assets/ 文件按需读取（L3）
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <button class="btn btn-ghost" @click="emit('close')">取消</button>
        <button class="btn btn-primary" :disabled="saving || loadingBody" @click="handleSave">
          {{ saving ? '保存中...' : '保存' }}
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
  width: 720px;
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

.form-row {
  display: flex;
  gap: $space-md;

  .form-group {
    flex: 1;
  }
}

.form-group {
  margin-bottom: $space-md;

  > label {
    display: block;
    font-size: $font-size-sm;
    font-weight: $font-weight-medium;
    margin-bottom: 6px;
    color: $color-text-primary;
  }
}

.check-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  font-size: $font-size-sm;
  color: $color-text-secondary;
  margin-bottom: 6px;
  cursor: pointer;

  input {
    margin-top: 2px;
    flex-shrink: 0;
  }
}

.hint-inline {
  color: $color-text-muted;
  font-size: $font-size-xs;
  margin-left: 4px;
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

  &:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  &.mono {
    font-family: monospace;
  }
}

.textarea-sm {
  resize: vertical;
  min-height: 48px;
  line-height: 1.5;
}

.textarea-body {
  resize: vertical;
  min-height: 240px;
  font-family: monospace;
  font-size: $font-size-xs;
  line-height: 1.6;
}

.hint {
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin-top: 4px;
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
  font-size: $font-size-sm;
}

.warn-banner {
  padding: $space-sm $space-md;
  margin-bottom: $space-sm;
  background-color: rgb(var(--color-warning-rgb) / 0.1);
  border: 1px solid rgb(var(--color-warning-rgb) / 0.35);
  border-radius: $radius-sm;
  color: $color-yellow;
  font-size: $font-size-xs;
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

.inline-spin {
  vertical-align: -2px;
  margin-left: 4px;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
