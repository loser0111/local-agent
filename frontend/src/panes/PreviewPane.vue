<script setup>
import { ref } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'

const url = ref('http://localhost:5173')
</script>

<template>
  <div class="preview-pane">
    <PaneHeader type="preview" />
    <div class="preview-toolbar">
      <input v-model="url" class="url-input" readonly />
      <button class="btn btn-ghost btn-sm" title="刷新">
        <svg width="12" height="12" viewBox="0 0 12 12" fill="none">
          <path d="M1 6a5 5 0 019-3M11 6a5 5 0 01-9 3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
        </svg>
      </button>
    </div>
    <div class="preview-body">
      <div class="preview-placeholder">
        <div class="ph-icon">🌐</div>
        <div class="ph-text">应用预览区域</div>
        <div class="ph-sub">在此预览本地开发服务器或 HTML 文件</div>
        <div class="ph-url">{{ url }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.preview-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: #fff;
}

.preview-toolbar {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-xs $space-md;
  background-color: $color-bg-secondary;
  border-bottom: 1px solid $color-border;
}

.url-input {
  flex: 1;
  padding: $space-xs $space-sm;
  font-size: $font-size-xs;
  background-color: $color-bg-tertiary;
  border: 1px solid $color-border;
  border-radius: $radius-sm;
  color: $color-text-primary;
  font-family: $font-family-mono;
}

.preview-body {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  background-color: #f5f5f5;
}

.preview-placeholder {
  text-align: center;
  color: $color-text-muted;
}

.ph-icon {
  font-size: 48px;
  margin-bottom: $space-md;
}

.ph-text {
  font-size: $font-size-md;
  color: $color-text-secondary;
  margin-bottom: $space-xs;
}

.ph-sub {
  font-size: $font-size-xs;
  margin-bottom: $space-md;
}

.ph-url {
  font-family: $font-family-mono;
  font-size: $font-size-xs;
  color: $color-primary;
  background-color: rgba(124, 58, 237, 0.1);
  padding: $space-xs $space-md;
  border-radius: $radius-sm;
  display: inline-block;
}

.btn-sm {
  padding: 2px $space-sm;
  font-size: $font-size-xs;
}
</style>
