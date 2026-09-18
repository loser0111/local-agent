<script setup>
import { ref, computed, watch, onMounted } from 'vue'
import { useTaskStore } from '@/stores/task'

/**
 * 创建 / 编辑定时任务
 *
 * 关键约束（F2.8）：保存前必须能看到「未来 3 次触发时间」。
 * 预览是把规则原样交给后端算的（后端是唯一的规则真源），
 * 界面不做任何本地推算 —— 本地推算出来的时间与调度器算的不一致时，
 * 用户会失去对整件事的信任。
 */
const props = defineProps({
  task: { type: Object, default: null },
})

const emit = defineEmits(['close', 'saved'])

const taskStore = useTaskStore()
const editing = computed(() => !!props.task)

const form = ref({
  title: '',
  note: '',
  kind: 'reminder',
  enabled: true,
  triggerKind: 'once',
  onceAt: '',
  everyMinutes: 60,
  period: 'daily',
  timeOfDay: '09:00',
  weekdays: [1],
  dayOfMonth: 1,
  cronExpr: '0 9 * * 1-5',
  prompt: '',
  model: '',
  workDir: '',
  timeoutSeconds: 600,
})

const preview = ref([])
const previewError = ref('')
const submitError = ref('')
const submitting = ref(false)

const weekdayNames = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

onMounted(() => {
  if (props.task) hydrate(props.task)
  else resetDefaults()
  refreshPreview()
})

/** 把已有任务灌进表单 */
function hydrate(task) {
  const tr = task.trigger || {}
  form.value.title = task.title || ''
  form.value.note = task.note || ''
  form.value.kind = task.kind || 'reminder'
  form.value.enabled = task.enabled !== false
  form.value.triggerKind = tr.kind || 'once'
  if (tr.once) form.value.onceAt = toLocalInput(tr.once.at)
  if (tr.interval) {
    form.value.everyMinutes = tr.interval.everyMinutes || 60
  }
  if (tr.recurring) {
    form.value.period = tr.recurring.period || 'daily'
    form.value.timeOfDay = tr.recurring.timeOfDay || '09:00'
    form.value.weekdays = (tr.recurring.weekdays || [1]).slice()
    form.value.dayOfMonth = tr.recurring.dayOfMonth || 1
  }
  if (tr.cron) form.value.cronExpr = tr.cron.expr || ''
  if (task.exec) {
    form.value.prompt = task.exec.prompt || ''
    form.value.model = task.exec.model || ''
    form.value.workDir = task.exec.workDir || ''
    form.value.timeoutSeconds = task.exec.timeoutSeconds || 600
  }
}

function resetDefaults() {
  const d = new Date(Date.now() + 60 * 60 * 1000)
  form.value.onceAt = toLocalInput(d.toISOString())
}

/** RFC3339 → <input type="datetime-local"> 需要的本地格式 */
function toLocalInput(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 本地格式 → RFC3339 */
function toRFC3339(local) {
  if (!local) return ''
  const d = new Date(local)
  if (Number.isNaN(d.getTime())) return ''
  return d.toISOString()
}

/** 组装后端要的 Trigger */
const trigger = computed(() => {
  const t = { kind: form.value.triggerKind }
  switch (form.value.triggerKind) {
    case 'once':
      t.once = { at: toRFC3339(form.value.onceAt) }
      break
    case 'interval':
      t.interval = { everyMinutes: Number(form.value.everyMinutes) || 0 }
      break
    case 'recurring':
      if (form.value.period === 'weekly') {
        t.recurring = {
          period: 'weekly',
          timeOfDay: form.value.timeOfDay,
          weekdays: form.value.weekdays.slice().sort((a, b) => a - b),
        }
      } else if (form.value.period === 'monthly') {
        t.recurring = {
          period: 'monthly',
          timeOfDay: form.value.timeOfDay,
          dayOfMonth: Number(form.value.dayOfMonth) || 1,
        }
      } else {
        t.recurring = { period: 'daily', timeOfDay: form.value.timeOfDay }
      }
      break
    case 'cron':
      t.cron = { expr: form.value.cronExpr }
      break
    default:
      break
  }
  return t
})

// 规则一变就重新预览（后端算，不本地推）
watch(trigger, refreshPreview, { deep: true })

async function refreshPreview() {
  previewError.value = ''
  try {
    const list = await taskStore.preview(trigger.value, 3)
    preview.value = list || []
  } catch (e) {
    preview.value = []
    previewError.value = e.message || String(e)
  }
}

function toggleWeekday(w) {
  const list = form.value.weekdays
  const idx = list.indexOf(w)
  if (idx >= 0) list.splice(idx, 1)
  else list.push(w)
}

/** 预览为空说明规则不成立，此时不允许保存 */
const canSave = computed(() => {
  if (!form.value.title.trim()) return false
  if (form.value.kind === 'exec' && !form.value.prompt.trim()) return false
  if (previewError.value) return false
  return preview.value.length > 0
})

async function submit() {
  submitError.value = ''
  if (!canSave.value) {
    submitError.value = previewError.value || '请先补全信息，并确认能看到未来的触发时间'
    return
  }
  submitting.value = true
  try {
    const payload = {
      title: form.value.title.trim(),
      note: form.value.note,
      kind: form.value.kind,
      enabled: form.value.enabled,
      trigger: trigger.value,
    }
    if (form.value.kind === 'exec') {
      payload.exec = {
        prompt: form.value.prompt,
        model: form.value.model,
        workDir: form.value.workDir,
        timeoutSeconds: Number(form.value.timeoutSeconds) || 0,
      }
    }
    if (editing.value) await taskStore.update(props.task.id, payload)
    else await taskStore.create(payload)
    emit('saved')
  } catch (e) {
    // 后端的校验错误必须原样展示：它比任何前端校验都权威。
    submitError.value = e.message || String(e)
  } finally {
    submitting.value = false
  }
}

function fmt(iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
</script>

<template>
  <div class="overlay" @click.self="emit('close')">
    <div class="dialog">
      <div class="dialog-header">
        <h3>{{ editing ? '编辑任务' : '新增任务' }}</h3>
        <button class="close" @click="emit('close')">×</button>
      </div>

      <div class="dialog-body">
        <div class="form-group">
          <label>名称</label>
          <input v-model="form.title" class="input" maxlength="50" placeholder="例如：站会提醒" />
        </div>

        <div class="form-group">
          <label>类型</label>
          <div class="radio-row">
            <label class="radio"><input type="radio" value="reminder" v-model="form.kind" /> 提醒型</label>
            <label class="radio"><input type="radio" value="exec" v-model="form.kind" /> 执行型</label>
          </div>
          <div class="hint-line">两种类型不可直接互转（需要转换请新建一条）。</div>
        </div>

        <template v-if="form.kind === 'reminder'">
          <div class="form-group">
            <label>提醒内容</label>
            <textarea v-model="form.note" class="input" rows="2" placeholder="留空则使用默认文案" />
          </div>
        </template>

        <template v-else>
          <div class="form-group">
            <label>Prompt</label>
            <textarea v-model="form.prompt" class="input" rows="3" placeholder="到点自动执行的指令" />
          </div>
          <div class="form-group">
            <label>模型（留空则用默认模型）</label>
            <input v-model="form.model" class="input" />
          </div>
          <div class="form-group">
            <label>工作区目录（留空则用当前项目）</label>
            <input v-model="form.workDir" class="input" />
          </div>
          <div class="form-group">
            <label>超时（秒）</label>
            <input v-model.number="form.timeoutSeconds" type="number" min="1" class="input" />
          </div>
        </template>

        <div class="form-group">
          <label>时间规则</label>
          <select v-model="form.triggerKind" class="input">
            <option value="once">一次性</option>
            <option value="interval">固定间隔</option>
            <option value="recurring">每天 / 每周 / 每月</option>
            <option value="cron">cron 表达式</option>
          </select>
        </div>

        <div v-if="form.triggerKind === 'once'" class="form-group">
          <label>触发时间</label>
          <input v-model="form.onceAt" type="datetime-local" class="input" />
        </div>

        <div v-if="form.triggerKind === 'interval'" class="form-group">
          <label>间隔（分钟，最小 1）</label>
          <input v-model.number="form.everyMinutes" type="number" min="1" class="input" />
        </div>

        <template v-if="form.triggerKind === 'recurring'">
          <div class="form-group">
            <label>周期</label>
            <select v-model="form.period" class="input">
              <option value="daily">每天</option>
              <option value="weekly">每周</option>
              <option value="monthly">每月</option>
            </select>
          </div>
          <div v-if="form.period === 'weekly'" class="form-group">
            <label>星期（可多选）</label>
            <div class="weekday-row">
              <button
                v-for="(name, w) in weekdayNames"
                :key="w"
                class="weekday"
                :class="{ on: form.weekdays.includes(w) }"
                @click="toggleWeekday(w)"
              >
                {{ name }}
              </button>
            </div>
          </div>
          <div v-if="form.period === 'monthly'" class="form-group">
            <label>每月第几天（1-31，遇到小月自动取当月最后一天）</label>
            <input v-model.number="form.dayOfMonth" type="number" min="1" max="31" class="input" />
          </div>
          <div class="form-group">
            <label>时刻</label>
            <input v-model="form.timeOfDay" type="time" class="input" />
          </div>
        </template>

        <div v-if="form.triggerKind === 'cron'" class="form-group">
          <label>cron 表达式（五段：分 时 日 月 周）</label>
          <input v-model="form.cronExpr" class="input" placeholder="0 9 * * 1-5" />
          <div class="hint-line">仅支持 * 单值 区间 列表 与 /n 步长，不支持名称写法。</div>
        </div>

        <div class="form-group">
          <label>启用</label>
          <label class="radio"><input type="checkbox" v-model="form.enabled" /> 保存后立即参与调度</label>
        </div>

        <!-- 保存前必须能看到未来 3 次触发时间（F2.8） -->
        <div class="preview">
          <div class="preview-title">未来 3 次触发</div>
          <ul v-if="preview.length">
            <li v-for="(p, i) in preview" :key="i">{{ fmt(p) }}</li>
          </ul>
          <div v-else class="preview-error">
            {{ previewError || '暂无法计算触发时间，请检查规则' }}
          </div>
        </div>
      </div>

      <div class="dialog-footer">
        <span v-if="submitError" class="error">{{ submitError }}</span>
        <button class="btn ghost" @click="emit('close')">取消</button>
        <button class="btn" :disabled="submitting || !canSave" @click="submit">
          {{ submitting ? '保存中…' : '保存' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.overlay {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background-color: rgba(0, 0, 0, 0.45);
  z-index: 100;
}

.dialog {
  width: 520px;
  max-height: 86vh;
  display: flex;
  flex-direction: column;
  background-color: $color-bg-primary;
  border: 1px solid $color-border;
  border-radius: 6px;
}

.dialog-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: $space-sm $space-md;
  border-bottom: 1px solid $color-border;

  h3 {
    font-size: $font-size-sm;
    font-weight: 500;
  }

  .close {
    background: none;
    border: none;
    color: $color-text-muted;
    font-size: 18px;
    cursor: pointer;
  }
}

.dialog-body {
  flex: 1;
  overflow-y: auto;
  padding: $space-md;
}

.form-group {
  margin-bottom: $space-sm;

  > label {
    display: block;
    font-size: $font-size-xs;
    color: $color-text-muted;
    margin-bottom: 4px;
  }
}

.input {
  width: 100%;
  padding: 4px 6px;
  font-size: $font-size-xs;
  color: $color-text-primary;
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: 4px;
}

.radio-row {
  display: flex;
  gap: $space-md;
}

.radio {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: $font-size-xs;
  color: $color-text-primary;
}

.hint-line {
  margin-top: 4px;
  font-size: 10px;
  color: $color-text-muted;
}

.weekday-row {
  display: flex;
  gap: 4px;
}

.weekday {
  padding: 2px 8px;
  font-size: $font-size-xs;
  color: $color-text-muted;
  background-color: transparent;
  border: 1px solid $color-border;
  border-radius: 3px;
  cursor: pointer;

  &.on {
    color: $color-primary;
    border-color: $color-primary;
  }
}

.preview {
  margin-top: $space-sm;
  padding: $space-sm;
  background-color: $color-bg-secondary;
  border-radius: 4px;

  .preview-title {
    font-size: $font-size-xs;
    color: $color-text-muted;
    margin-bottom: 4px;
  }

  ul {
    margin: 0;
    padding-left: 18px;
    font-size: $font-size-xs;
    color: $color-text-primary;
  }

  .preview-error {
    font-size: $font-size-xs;
    color: $color-warning;
  }
}

.dialog-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: $space-sm;
  padding: $space-sm $space-md;
  border-top: 1px solid $color-border;

  .error {
    flex: 1;
    font-size: $font-size-xs;
    color: $color-error;
  }
}

.btn {
  padding: 4px $space-md;
  font-size: $font-size-xs;
  color: $color-text-primary;
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: 4px;
  cursor: pointer;

  &:hover:not(:disabled) {
    border-color: $color-primary;
  }

  &:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  &.ghost {
    background-color: transparent;
  }
}
</style>
