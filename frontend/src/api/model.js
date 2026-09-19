import {
  GetModels,
  GetModelNames,
  AddModel,
  DeleteModel,
  GetModel,
  GetModelFull,
  UpdateModel,
  TestModelConnection,
} from '@/../wailsjs/go/main/App'

/**
 * 判断是否运行在 Wails 桌面环境（window.go 存在）
 */
function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

/**
 * 浏览器开发模式下的本地 mock（使用 localStorage 模拟后端 JSON 持久化）
 * 仅在非 Wails 环境下生效，保证 npm run dev 时前端可独立调试
 */
const MOCK_KEY = 'local-agent:models'

function getMockModels() {
  try {
    const raw = localStorage.getItem(MOCK_KEY)
    return raw ? JSON.parse(raw) : []
  } catch {
    return []
  }
}

function saveMockModels(models) {
  localStorage.setItem(MOCK_KEY, JSON.stringify(models))
}

/** mock 脱敏：仅保留后 4 位 */
function maskKey(key) {
  if (!key || key.length <= 4) return '*'.repeat(key ? key.length : 0)
  return '*'.repeat(key.length - 4) + key.slice(-4)
}

/**
 * 获取所有模型（APIKey 脱敏）
 * @returns {Promise<Array<{name:string, alias:string, modelId:string, apiKey:string, url:string, protocol?:string}>>}
 */
export async function fetchModels() {
  if (isWails()) {
    return await GetModels()
  }
  // dev mock（脱敏）
  return getMockModels().map((m) => ({ ...m, apiKey: maskKey(m.apiKey) }))
}

/**
 * 获取所有模型名称
 * @returns {Promise<string[]>}
 */
export async function fetchModelNames() {
  if (isWails()) {
    return await GetModelNames()
  }
  return getMockModels().map((m) => m.name)
}

/**
 * 添加模型
 * @param {{name:string, alias:string, modelId:string, apiKey:string, url:string, protocol?:string}} model
 * @returns {Promise<void>}
 */
export async function addModel(model) {
  if (isWails()) {
    return await AddModel(model)
  }
  // dev mock
  const models = getMockModels()
  if (models.some((m) => m.name === model.name)) {
    throw new Error(`模型名称 "${model.name}" 已存在`)
  }
  models.push(model)
  saveMockModels(models)
}

/**
 * 更新模型配置（支持重命名：若 model.name 与旧 name 不同则级联更新会话引用）
 * @param {string} name 原模型名称
 * @param {{name?:string, alias:string, modelId:string, apiKey:string, url:string, protocol?:string}} model
 * @returns {Promise<void>}
 */
export async function updateModel(name, model) {
  if (isWails()) {
    return await UpdateModel(name, model)
  }
  // dev mock
  const models = getMockModels()
  const idx = models.findIndex((m) => m.name === name)
  if (idx === -1) {
    throw new Error(`模型 "${name}" 不存在`)
  }
  const newName = model.name || name
  if (newName !== name && models.some((m) => m.name === newName)) {
    throw new Error(`模型名称 "${newName}" 已存在`)
  }
  // APIKey 为空时保留旧值（脱敏后未修改场景）
  const apiKey = model.apiKey || models[idx].apiKey
  models[idx] = { ...models[idx], ...model, name: newName, apiKey }
  saveMockModels(models)
}

/**
 * 删除模型（后端会检查是否有会话引用）
 * @param {string} name
 * @returns {Promise<void>}
 */
export async function deleteModel(name) {
  if (isWails()) {
    return await DeleteModel(name)
  }
  // dev mock
  const models = getMockModels()
  const idx = models.findIndex((m) => m.name === name)
  if (idx === -1) {
    throw new Error(`模型 "${name}" 不存在`)
  }
  models.splice(idx, 1)
  saveMockModels(models)
}

/**
 * 根据名称获取模型（APIKey 脱敏）
 * @param {string} name
 * @returns {Promise<{name:string, alias:string, modelId:string, apiKey:string, url:string, protocol?:string}>}
 */
export async function getModel(name) {
  if (isWails()) {
    return await GetModel(name)
  }
  // dev mock
  const models = getMockModels()
  const found = models.find((m) => m.name === name)
  if (!found) {
    throw new Error(`模型 "${name}" 不存在`)
  }
  return { ...found, apiKey: maskKey(found.apiKey) }
}

/**
 * 根据名称获取模型完整信息（不脱敏，供编辑时回填 API Key）
 * @param {string} name
 * @returns {Promise<{name:string, alias:string, modelId:string, apiKey:string, url:string, protocol?:string}>}
 */
export async function getModelFull(name) {
  if (isWails()) {
    return await GetModelFull(name)
  }
  // dev mock
  const models = getMockModels()
  const found = models.find((m) => m.name === name)
  if (!found) {
    throw new Error(`模型 "${name}" 不存在`)
  }
  return { ...found }
}

/**
 * 测试模型连通性：发送一条 ping 消息验证 API 配置
 * @param {{name:string, modelId?:string, apiKey:string, url:string, protocol?:string}} model
 * @returns {Promise<void>} 成功无返回，失败抛异常
 */
export async function testModelConnection(model) {
  if (isWails()) {
    return await TestModelConnection(model)
  }
  // dev mock：模拟延迟
  await new Promise((r) => setTimeout(r, 800))
  if (!model.url) {
    throw new Error('模型 API URL 不能为空')
  }
  // 模拟成功
}
