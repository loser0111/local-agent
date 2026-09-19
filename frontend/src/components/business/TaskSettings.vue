<script setup>
import { ref, onMounted, watch } from 'vue'
import { useTaskStore } from '@/stores/task'

/**
 * 设置页「定时任务」面板：全局配置
 *
 * 这里只放**全局**级别的开关；逐任务的规则在任务面板里编辑。
 * 所有改动保存后立即生效（后端会重建定时器，无需重启）。
 */
const taskStore = useTaskStore()

const form = ref(null)
const saved = ref(false)
const errorMsg = ref('')

onMounted(async () => {
  await taskStore.load()
  form.value = normalize(taskStore.settings)
})

// 后端配置变化（例如从托盘暂停）时同步到表单。
watch(
  () => taskStore.settings,
  (val) => {
    if (val && !saved.value) form.value = normalize(val)
  },
)

/**
 * 规整配置：DND 是可选的嵌套对象，直接绑定会让「启用」复选框
 * 在对象与布尔之间反复横跳（v-model 会把它写成 true/false），
 * 所以统一补成 { enabled, start, end } 的形状。
 */
function normalize(cfg) {
  const out = { ...(cfg || {}) }
  out.dnd = { enabled: false, start: '22:00', end: '07:30', ...(cfg?.dnd || {}) }
  return out
}

async function save() {
  saved.value = false
  errorMsg.value = ''
  try {
    const payload = { ...form.value }
    // 未启用免打扰时不下发时段，避免把半成品配置写进数据文件。
    if (!payload.dnd?.enabled) payload.dnd = { enabled: false }
    await taskStore.saveSettings(payload)
    saved.value = true
    setTimeout(() => (saved.value = false), 2000)
  } catch (e) {
    errorMsg.value = e.message || String(e)
  }
}

async function togglePause() {
  errorMsg.value = ''
  try {
    await taskStore.setPaused(!taskStore.paused)
    form.value = { ...taskStore.settings }
  } catch (e) {
    errorMsg.value = e.message || String(e)
  }
}

const snoozeOptions = [5, 10, 30, 60]

/**
 * 启用免打扰时补一组默认起止时间：避免出现「启用了但起止为空」的半成品配置
 * （后端会拒绝起止非法的配置，那时用户只看到一个报错却不知道要填什么）。
 */
function onDndToggle() {
  if (!form.value) return
  if (form.value.dnd && form.value.dnd.enabled && !form.value.dnd.start) {
    form.value.dnd.start = '22:00'
    form.value.dnd.end = '07:30'
  }
}
</script>

<template>
  <div class="settings-panel">
    <div v-if="!taskStore.available" class="plugin-missing">
      定时任务插件未启用，配置不可用。
    </div>

    <template v-else-if="form">
      <div class="form-group">
        <label>插件状态</label>
        <div class="row">
          <span class="status">{{ taskStore.info?.state }}</span>
          <span class="muted">v{{ taskStore.info?.version }}（宿主接口 {{ taskStore.info?.hostAPIVersion }}）</span>
        </div>
      </div>

      <div class="form-group">
        <label>总开关</label>
        <label class="check"><input type="checkbox" v-model="form.enabled" /> 启用定时任务调度</label>
      </div>

      <div class="form-group">
        <label>全局暂停</label>
        <div class="row">
          <button class="btn ghost" :class="{ warn: taskStore.paused }" @click="togglePause">
            {{ taskStore.paused ? '已暂停，点击恢复' : '暂停全部任务' }}
          </button>
          <span class="muted">暂停期间任务仍会记录，但不会打扰你</span>
        </div>
      </div>

      <div class="form-group">
        <label>默认「稍后提醒」时长</label>
        <select v-model.number="form.defaultSnoozeMinutes" class="input narrow">
          <option v-for="m in snoozeOptions" :key="m" :value="m">{{ m }} 分钟</option>
        </select>
      </div>

      <div class="form-group">
        <label>错过的提醒如何处理</label>
        <select v-model="form.missedPolicy" class="input narrow">
          <option value="catchup">补发（提醒一次）</option>
          <option value="skip">跳过</option>
          <option value="defer">顺延到下一个周期</option>
        </select>
        <div class="muted small">执行型任务错过时一律不补跑。</div>
      </div>

      <div class="form-group">
        <label>免打扰时段</label>
        <label class="check"><input type="checkbox" v-model="form.dnd.enabled" @change="onDndToggle" /> 启用</label>
        <div v-if="form.dnd.enabled" class="row">
          <input v-model="form.dnd.start" type="time" class="input narrow" />
          <span class="muted">至</span>
          <input v-model="form.dnd.end" type="time" class="input narrow" />
        </div>
        <div class="muted small">免打扰期间只做应用内提醒，不弹系统通知。</div>
      </div>

      <div class="form-group">
        <label>系统通知</label>
        <label class="check"><input type="checkbox" v-model="form.notifyEnabled" /> 允许弹出系统通知</label>
        <div class="muted small">关闭后仍会记录触发历史，只是不打扰。</div>
      </div>

      <div class="form-group">
        <label>前台时只做应用内提醒</label>
        <label class="check"><input type="checkbox" v-model="form.foregroundOnlyInApp" /> 启用</label>
      </div>

      <div class="form-group">
        <label>通知频率上限</label>
        <div class="row">
          <span class="muted">同一任务每分钟</span>
          <input v-model.number="form.rateLimitPerTaskPerMinute" type="number" min="1" class="input tiny" />
          <span class="muted">条；全局每 5 分钟</span>
          <input v-model.number="form.rateLimitGlobalPer5Minutes" type="number" min="1" class="input tiny" />
          <span class="muted">条</span>
        </div>
      </div>

      <div class="form-group">
        <label>历史保留</label>
        <div class="row">
          <input v-model.number="form.keepHistoryDays" type="number" min="0" class="input tiny" />
          <span class="muted">天（0 表示不自动清理）</span>
        </div>
        <label class="check"><input type="checkbox" v-model="form.deleteHistoryWithTask" /> 删除任务时一并删除历史</label>
        <div class="muted small">默认保留历史，便于事后排查「当时到底怎么了」。</div>
      </div>

      <div class="form-group">
        <label>数据目录</label>
        <div class="muted small">{{ taskStore.info?.dataDir }}</div>
      </div>

      <div class="row">
        <button class="btn" @click="save">保存</button>
        <span v-if="saved" class="ok">已保存</span>
        <span v-if="errorMsg" class="err">{{ errorMsg }}</span>
      </div>
    </template>
  </div>
</template>

<style scoped lang="scss">
.form-group {
  margin-bottom: $space-md;

  > label {
    display: block;
    font-size: $font-size-sm;
    color: $color-text-primary;
    margin-bottom: 4px;
  }
}

.input {
  padding: 4px 6px;
  font-size: $font-size-sm;
  color: $color-text-primary;
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: 4px;

  &.narrow {
    width: 200px;
  }

  &.tiny {
    width: 64px;
  }
}

.check {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: $font-size-sm;
  color: $color-text-primary;
}

.row {
  display: flex;
  align-items: center;
  gap: $space-sm;
  flex-wrap: wrap;
}

.muted {
  font-size: $font-size-xs;
  color: $color-text-muted;

  &.small {
    margin-top: 4px;
  }
}

.status {
  font-size: $font-size-sm;
  color: $color-success;
}

.btn {
  padding: 4px $space-md;
  font-size: $font-size-sm;
  color: $color-text-primary;
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: 4px;
  cursor: pointer;

  &:hover {
    border-color: $color-primary;
  }

  &.ghost {
    background-color: transparent;
  }

  &.warn {
    color: $color-warning;
    border-color: $color-warning;
  }
}

.ok {
  font-size: $font-size-xs;
  color: $color-success;
}

.err {
  font-size: $font-size-xs;
  color: $color-error;
}

.plugin-missing {
  font-size: $font-size-sm;
  color: $color-warning;
}
</style>
