<script setup>
/**
 * 可拖拽分隔条 —— 用于横向两栏之间的宽度调整
 * 拖拽时给 body 加 is-resizing class（全局 CSS 禁用 transition 避免滞后感）
 */
const emit = defineEmits(['drag'])

function onMousedown(e) {
  e.preventDefault()
  let lastX = e.clientX
  document.body.classList.add('is-resizing')

  function onMove(ev) {
    const dx = ev.clientX - lastX // 增量位移，不是从起点的总位移
    lastX = ev.clientX
    emit('drag', dx)
  }
  function onUp() {
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onUp)
    document.body.classList.remove('is-resizing')
  }
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onUp)
}
</script>

<template>
  <div class="resize-divider" @mousedown="onMousedown" />
</template>

<style scoped lang="scss">
.resize-divider {
  width: 4px;
  flex-shrink: 0;
  cursor: col-resize;
  background-color: transparent;
  position: relative;
  z-index: 5;

  &:hover,
  &:active {
    background-color: $color-primary;
    opacity: 0.3;
  }
}
</style>
