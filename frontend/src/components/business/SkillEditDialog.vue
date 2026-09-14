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
  body: '',
  builtin: false,
})

const form = reactive(emptyForm())
const saving = ref(false)
const loadingBody = ref(false)
const errorMsg = ref('')

const isEdit = computed(() => !!form.id)
const idValid = computed(() => /^[A-Za-z0-9_-]{1,64}$/.test(form.id.trim()))

watch(
  () => props.visible,
  async (v) => {
    if (!v) return
    errorMsg.value = ''
    Object.assign(form, emptyForm())
    if (props.skill) {
      Object.assign(form, {
        id: props.skill.id || '',
        name: props.skill.name || '',
        description: props.skill.description || '',
        builtin: !!props.skill.builtin,
      })
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
  if (!form.name.trim()) return (errorMsg.value = '技能名称（name）不能为空')
  if (!form.description.trim()) return (errorMsg.value = '技能描述（description）不能为空——模型靠它判断何时使用该技能')

  saving.value = true
  try {
    await store.save({
      id: form.id.trim(),
      name: form.name.trim(),
      description: form.description.trim(),
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

        <div class="form-row">
          <div class="form-group">
            <label>技能 ID <span class="required">*</span></label>
            <input
              v-model="form.id"
              class="input mono"
              placeholder="如：pdf-report（即技能目录名）"
              :disabled="isEdit"
            />
            <div class="hint">仅支持字母、数字、下划线、短横线；创建后不可修改</div>
          </div>
          <div class="form-group">
            <label>名称 name <span class="required">*</span></label>
            <input v-model="form.name" class="input" placeholder="如：PDF 报告生成" />
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
            placeholder="# 操作手册&#10;&#10;模型调用 read_skill 后会看到这里的全部内容；可指引它用 exec_shell 运行 scripts/ 下的脚本"
          ></textarea>
          <div class="hint">正文仅在模型需要该技能时经 read_skill 加载，不占用常驻上下文</div>
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
  background: rgba(0, 0, 0, 0.55);
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
  width: 640px;
  max-width: 100%;
  max-height: 86vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.5);
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
  min-height: 260px;
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
  background-color: rgba(255, 85, 85, 0.12);
  border: 1px solid rgba(255, 85, 85, 0.4);
  border-radius: $radius-sm;
  color: $color-error;
  font-size: $font-size-sm;
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
