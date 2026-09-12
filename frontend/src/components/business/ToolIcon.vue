<script setup>
/**
 * 工具图标：按配置的 Lucide 图标名渲染，未知名称降级为 Zap
 */
import { computed } from 'vue'
import { TOOL_ICON_MAP } from './toolIcons'

const props = defineProps({
  name: { type: String, default: 'zap' },
  size: { type: Number, default: 16 },
})

const comp = computed(() => TOOL_ICON_MAP[props.name] || TOOL_ICON_MAP.zap)
</script>

<template>
  <component :is="comp" :size="size" :stroke-width="1.8" />
</template>
