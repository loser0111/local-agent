import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { fetchTools, saveTool, deleteTool, toggleTool, testToolConnection } from '@/api/tool'

/**
 * 工具配置 Store
 * 维护工具中心列表（builtin/cli/mcp/api），供设置页和新建会话工具选择器使用
 */
export const useToolsStore = defineStore('tools', () => {
  const tools = ref([])
  const loading = ref(false)
  const loaded = ref(false)

  // 已启用的顶层工具（会话白名单选择器用）
  const enabledTools = computed(() => tools.value.filter((t) => t.enabled))

  async function load(force = false) {
    if (loaded.value && !force) return
    loading.value = true
    try {
      tools.value = (await fetchTools()) || []
      loaded.value = true
    } catch (e) {
      console.error('加载工具列表失败:', e)
      tools.value = []
    } finally {
      loading.value = false
    }
  }

  /**
   * 新增/更新，返回保存后的工具
   */
  async function save(tool) {
    const saved = await saveTool(tool)
    const idx = tools.value.findIndex((t) => t.id === saved.id)
    if (idx > -1) {
      tools.value[idx] = { ...tools.value[idx], ...saved }
    } else {
      tools.value.push({ ...saved, status: saved.status || { connected: saved.type !== 'mcp', toolCount: 0, error: '' } })
    }
    return saved
  }

  async function remove(id) {
    await deleteTool(id)
    tools.value = tools.value.filter((t) => t.id !== id)
  }

  async function toggle(id, enabled) {
    await toggleTool(id, enabled)
    const t = tools.value.find((x) => x.id === id)
    if (t) t.enabled = enabled
  }

  /**
   * 测试 MCP 连接；成功后把发现结果合并进列表项（已保存的工具）
   * @returns {Promise<Array>} 发现的工具摘要
   */
  async function testConnection(tool) {
    const discovered = await testToolConnection(tool)
    if (tool.id) {
      const t = tools.value.find((x) => x.id === tool.id)
      if (t) {
        t.discovered = discovered
        t.status = { ...(t.status || {}), connected: true, toolCount: discovered.length, error: '' }
      }
    }
    return discovered
  }

  function clear() {
    tools.value = []
    loaded.value = false
  }

  return {
    tools,
    loading,
    loaded,
    enabledTools,
    load,
    save,
    remove,
    toggle,
    testConnection,
    clear,
  }
})
