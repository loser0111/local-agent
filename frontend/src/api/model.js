import {
  GetModels,
  GetModelNames,
  AddModel,
  DeleteModel,
  GetModel,
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

/**
 * 获取所有模型
 * @returns {Promise<Array<{name:string, alias:string, apiKey:string, url:string}>>}
 */
export async function fetchModels() {
  if (isWails()) {
    return await GetModels()
  }
  // dev mock
  return getMockModels()
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
 * @param {{name:string, alias:string, apiKey:string, url:string}} model
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
 * 删除模型
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
 * 根据名称获取模型
 * @param {string} name
 * @returns {Promise<{name:string, alias:string, apiKey:string, url:string}>}
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
  return found
}
