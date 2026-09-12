import {
  ListTools,
  SaveTool,
  DeleteTool,
  ToggleTool,
  TestToolConnection,
} from '@/../wailsjs/go/main/App'

/**
 * 判断是否运行在 Wails 桌面环境
 */
function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

// ===== 浏览器 dev mock（localStorage 持久化）=====

const MOCK_KEY = 'local-agent:tools'

function defaultMockTools() {
  return [
    {
      id: 'exec_shell',
      name: 'exec_shell',
      label: '执行终端命令',
      description: '在本机终端执行shell/终端命令，用于查看文件、查询目录、执行系统指令',
      type: 'builtin',
      icon: 'terminal',
      enabled: true,
      builtin: true,
      parameters: [
        { name: 'cmd', description: '要执行的终端命令，linux/mac用bash指令，windows用cmd/powershell指令', required: true },
      ],
      config: {},
      disabledTools: [],
      discovered: [],
      status: { connected: true, toolCount: 0, error: '' },
    },
  ]
}

function getMockTools() {
  try {
    const raw = localStorage.getItem(MOCK_KEY)
    if (!raw) {
      const d = defaultMockTools()
      localStorage.setItem(MOCK_KEY, JSON.stringify(d))
      return d
    }
    return JSON.parse(raw)
  } catch {
    return defaultMockTools()
  }
}

function saveMockTools(tools) {
  localStorage.setItem(MOCK_KEY, JSON.stringify(tools))
}

/**
 * 获取全部工具（含运行时状态）
 * @returns {Promise<Array>}
 */
export async function fetchTools() {
  if (isWails()) {
    return await ListTools()
  }
  return getMockTools().map((t) => ({
    ...t,
    status: t.status || { connected: true, toolCount: t.discovered?.length || 0, error: '' },
  }))
}

/**
 * 新增或更新工具
 * @param {object} tool 无 id 时新增
 * @returns {Promise<object>} 保存后的工具（含生成的 id）
 */
export async function saveTool(tool) {
  if (isWails()) {
    return await SaveTool(tool)
  }
  const tools = getMockTools()
  let saved = tool
  if (tool.id) {
    const idx = tools.findIndex((t) => t.id === tool.id)
    if (idx > -1) {
      tools[idx] = { ...tools[idx], ...tool }
      saved = tools[idx]
    }
  } else {
    saved = {
      ...tool,
      id: `tool_${Date.now()}`,
      discovered: [],
      disabledTools: tool.disabledTools || [],
      status: { connected: tool.type !== 'mcp', toolCount: 0, error: '' },
    }
    tools.push(saved)
  }
  saveMockTools(tools)
  return saved
}

/**
 * 删除工具
 * @param {string} id
 */
export async function deleteTool(id) {
  if (isWails()) {
    return await DeleteTool(id)
  }
  const tools = getMockTools()
  const idx = tools.findIndex((t) => t.id === id)
  if (idx === -1) throw new Error('工具不存在')
  if (tools[idx].builtin) throw new Error('内置工具不可删除')
  tools.splice(idx, 1)
  saveMockTools(tools)
}

/**
 * 启用/停用工具
 * @param {string} id
 * @param {boolean} enabled
 */
export async function toggleTool(id, enabled) {
  if (isWails()) {
    return await ToggleTool(id, enabled)
  }
  const tools = getMockTools()
  const t = tools.find((x) => x.id === id)
  if (!t) throw new Error('工具不存在')
  t.enabled = enabled
  saveMockTools(tools)
}

/**
 * 测试 MCP 连接并发现工具
 * @param {object} tool MCP 工具配置
 * @returns {Promise<Array<{name:string, description:string}>>}
 */
export async function testToolConnection(tool) {
  if (isWails()) {
    return await TestToolConnection(tool)
  }
  // dev mock：模拟一次发现
  await new Promise((r) => setTimeout(r, 600))
  if (tool.type !== 'mcp') throw new Error('仅 MCP 类型工具支持连接测试')
  const cfg = tool.config || {}
  if (cfg.transport !== 'stdio' && !cfg.url) {
    throw new Error('模拟连接失败：请填写 MCP server 地址（浏览器 mock 模式不会真实连接）')
  }
  return [
    { name: 'example_read', description: '读取资源（mock 发现）' },
    { name: 'example_write', description: '写入资源（mock 发现）' },
    { name: 'example_list', description: '列出资源（mock 发现）' },
  ]
}
