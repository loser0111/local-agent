<script setup>
import { PANE_TITLES } from '@/types'

const props = defineProps({
  type: { type: String, required: true },
  title: { type: String, default: '' },
  closable: { type: Boolean, default: true },
  extra: { type: String, default: '' },
})

const emit = defineEmits(['close'])

function handleClose() {
  emit('close')
}
</script>

<template>
  <div class="pane-header">
    <div class="pane-title">
      <span class="pane-name">{{ title || PANE_TITLES[type] || type }}</span>
      <span v-if="extra" class="pane-extra">{{ extra }}</span>
    </div>
    <div class="pane-actions">
      <button v-if="closable" class="pane-btn" title="关闭" @click="handleClose">
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
          <path d="M1 1L13 13M13 1L1 13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped lang="scss">
.pane-header {
  height: $pane-header-height;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 $space-md;
  background-color: $color-bg-secondary;
  border-bottom: 1px solid $color-border;
  flex-shrink: 0;
  cursor: default;
}

.pane-title {
  display: flex;
  align-items: center;
  gap: $space-sm;
  font-size: $font-size-sm;
  font-weight: $font-weight-semibold;
}

.pane-name {
  color: $color-text-primary;
}

.pane-extra {
  font-size: $font-size-xs;
  color: $color-text-secondary;
  font-weight: $font-weight-normal;
}

.pane-actions {
  display: flex;
  align-items: center;
  gap: $space-xs;
}

.pane-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: $radius-sm;
  color: $color-text-secondary;
  transition: all $transition-fast;

  &:hover {
    background-color: $color-bg-tertiary;
    color: $color-text-primary;
  }
}
</style>
