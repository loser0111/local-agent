<script setup>
import { onMounted, onUnmounted } from 'vue'
import { AlertTriangle, CheckCircle2, Info, X } from 'lucide-vue-next'
import { useUiStore } from '@/stores/ui'

/**
 * 应用内对话框宿主：确认框 + 提示条
 * 挂在应用根部（App.vue）渲染一次，任何页面/组件都能用（见 stores/ui.js 的说明）。
 */
const ui = useUiStore()

const ICONS = { info: Info, success: CheckCircle2, error: AlertTriangle }

function onKeydown(e) {
  if (ui.promptState) {
    if (e.key === 'Escape') {
      e.preventDefault()
      ui.answerText(false)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      ui.answerText(true)
    }
    return
  }
  if (!ui.confirmState) return
  if (e.key === 'Escape') {
    e.preventDefault()
    ui.answer(false)
  } else if (e.key === 'Enter') {
    e.preventDefault()
    ui.answer(true)
  }
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <!-- 确认框 -->
  <div v-if="ui.confirmState" class="ui-overlay" @click.self="ui.answer(false)">
    <div class="ui-dialog" role="alertdialog" aria-modal="true">
      <div class="ui-dialog-body">
        <div class="ui-dialog-title">{{ ui.confirmState.title }}</div>
        <div class="ui-dialog-message">{{ ui.confirmState.message }}</div>
      </div>
      <div class="ui-dialog-footer">
        <button class="btn" @click="ui.answer(false)">
          {{ ui.confirmState.cancelText }}
        </button>
        <button
          class="btn"
          :class="ui.confirmState.danger ? 'btn-danger' : 'btn-primary'"
          @click="ui.answer(true)"
        >
          {{ ui.confirmState.confirmText }}
        </button>
      </div>
    </div>
  </div>

  <!-- 输入框（替代原生 prompt） -->
  <div v-if="ui.promptState" class="ui-overlay" @click.self="ui.answerText(false)">
    <div class="ui-dialog" role="dialog" aria-modal="true">
      <div class="ui-dialog-body">
        <div class="ui-dialog-title">{{ ui.promptState.title }}</div>
        <div v-if="ui.promptState.message" class="ui-dialog-message">{{ ui.promptState.message }}</div>
        <input
          class="input ui-dialog-input"
          :value="ui.promptState.value"
          :placeholder="ui.promptState.placeholder"
          @input="ui.setPromptValue($event.target.value)"
        />
      </div>
      <div class="ui-dialog-footer">
        <button class="btn" @click="ui.answerText(false)">{{ ui.promptState.cancelText }}</button>
        <button class="btn btn-primary" @click="ui.answerText(true)">{{ ui.promptState.confirmText }}</button>
      </div>
    </div>
  </div>

  <!-- 提示条 -->
  <div v-if="ui.toasts.length" class="ui-toasts">
    <div
      v-for="t in ui.toasts"
      :key="t.id"
      class="ui-toast"
      :class="`ui-toast-${t.type}`"
      @click="ui.dismiss(t.id)"
    >
      <component :is="ICONS[t.type] || Info" :size="14" class="ui-toast-icon" />
      <span class="ui-toast-text">{{ t.message }}</span>
      <X :size="12" class="ui-toast-close" />
    </div>
  </div>
</template>

<style scoped lang="scss">
.ui-overlay {
  position: fixed;
  inset: 0;
  background-color: $color-overlay;
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1200; // 高于其它弹窗：确认框必须压在最上层
}

.ui-dialog {
  width: min(420px, 90vw);
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  box-shadow: $shadow-lg;
  overflow: hidden;
}

.ui-dialog-body {
  padding: $space-md;
}

.ui-dialog-title {
  font-size: $font-size-sm;
  font-weight: $font-weight-medium;
  color: $color-text-primary;
  margin-bottom: $space-xs;
}

.ui-dialog-message {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

.ui-dialog-input {
  width: 100%;
  margin-top: $space-sm;
}

.ui-dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: $space-sm;
  padding: $space-sm $space-md $space-md;
}

.ui-toasts {
  position: fixed;
  right: 16px;
  bottom: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  z-index: 1300;
  max-width: min(420px, 70vw);
}

.ui-toast {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 8px 10px;
  border-radius: $radius-sm;
  border: 1px solid $color-border;
  background-color: $color-bg-secondary;
  box-shadow: $shadow-lg;
  font-size: $font-size-xs;
  color: $color-text-primary;
  cursor: pointer;
}

.ui-toast-icon {
  flex-shrink: 0;
  margin-top: 1px;
}

.ui-toast-text {
  flex: 1;
  min-width: 0;
  line-height: 1.5;
  word-break: break-word;
}

.ui-toast-close {
  flex-shrink: 0;
  margin-top: 2px;
  color: $color-text-muted;
}

.ui-toast-success {
  border-color: rgb(var(--color-success-rgb) / 0.5);

  .ui-toast-icon {
    color: $color-success;
  }
}

.ui-toast-error {
  border-color: rgb(var(--color-error-rgb) / 0.55);

  .ui-toast-icon {
    color: $color-error;
  }
}

.ui-toast-info .ui-toast-icon {
  color: $color-text-secondary;
}
</style>
