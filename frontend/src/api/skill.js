import {
  ListSkills,
  GetSkill,
  SaveSkill,
  DeleteSkill,
  ToggleSkill,
  SetSkillAlwaysInject,
  RefreshSkills,
  SkillsDir,
} from '@/../wailsjs/go/main/App'

/**
 * 判断是否运行在 Wails 桌面环境
 */
function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

// ===== 浏览器 dev mock（localStorage 持久化）=====

const MOCK_KEY = 'local-agent:skills'

function defaultMockSkills() {
  return [
    {
      id: 'hello-skill',
      name: '示例技能',
      description: '演示用技能：当用户请求打招呼时给出固定问候。',
      dir: '~/.local-agent/skills/hello-skill',
      enabled: true,
      alwaysInject: false,
      builtin: false,
      hasScripts: false,
      error: '',
    },
  ]
}

function getMockSkills() {
  try {
    const raw = localStorage.getItem(MOCK_KEY)
    if (!raw) {
      const d = defaultMockSkills()
      localStorage.setItem(MOCK_KEY, JSON.stringify(d))
      return d
    }
    return JSON.parse(raw)
  } catch {
    return defaultMockSkills()
  }
}

function saveMockSkills(list) {
  localStorage.setItem(MOCK_KEY, JSON.stringify(list))
}

/**
 * 获取全部技能元数据（不含正文）
 * @returns {Promise<Array<import('@/types').SkillMeta>>}
 */
export async function fetchSkills() {
  if (isWails()) return (await ListSkills()) || []
  return getMockSkills()
}

/**
 * 获取技能详情（含 SKILL.md 正文）
 * @param {string} id
 * @returns {Promise<import('@/types').SkillDetail>}
 */
export async function getSkill(id) {
  if (isWails()) return await GetSkill(id)
  const m = getMockSkills().find((s) => s.id === id)
  if (!m) throw new Error('技能不存在')
  return { ...m, body: '# ' + m.name + '\n\n（mock 模式：正文不持久化，请在桌面端编辑）' }
}

/**
 * 新建或更新技能
 * @param {{id:string, name:string, description:string, body?:string}} skill
 */
export async function saveSkill(skill) {
  if (isWails()) return await SaveSkill(skill.id, skill.name, skill.description, skill.body || '')
  const list = getMockSkills()
  const idx = list.findIndex((s) => s.id === skill.id)
  if (idx > -1) {
    list[idx] = { ...list[idx], ...skill }
  } else {
    list.push({
      id: skill.id,
      name: skill.name,
      description: skill.description,
      dir: '~/.local-agent/skills/' + skill.id,
      enabled: true,
      alwaysInject: false,
      builtin: false,
      hasScripts: false,
      error: '',
    })
  }
  saveMockSkills(list)
}

/**
 * 删除技能（内置拒绝）
 * @param {string} id
 */
export async function deleteSkill(id) {
  if (isWails()) return await DeleteSkill(id)
  const list = getMockSkills()
  const target = list.find((s) => s.id === id)
  if (!target) throw new Error('技能不存在')
  if (target.builtin) throw new Error('内置技能不可删除')
  saveMockSkills(list.filter((s) => s.id !== id))
}

/**
 * 启用/停用技能
 * @param {string} id
 * @param {boolean} enabled
 */
export async function toggleSkill(id, enabled) {
  if (isWails()) return await ToggleSkill(id, enabled)
  const list = getMockSkills()
  const t = list.find((s) => s.id === id)
  if (!t) throw new Error('技能不存在')
  t.enabled = enabled
  saveMockSkills(list)
}

/**
 * 设置「强制注入正文」
 * @param {string} id
 * @param {boolean} v
 */
export async function setSkillAlwaysInject(id, v) {
  if (isWails()) return await SetSkillAlwaysInject(id, v)
  const list = getMockSkills()
  const t = list.find((s) => s.id === id)
  if (!t) throw new Error('技能不存在')
  t.alwaysInject = v
  saveMockSkills(list)
}

/**
 * 重新扫描技能目录
 * @returns {Promise<Array>}
 */
export async function refreshSkills() {
  if (isWails()) return (await RefreshSkills()) || []
  return getMockSkills()
}

/**
 * 技能目录绝对路径
 * @returns {Promise<string>}
 */
export async function skillsDir() {
  if (isWails()) return await SkillsDir()
  return '~/.local-agent/skills'
}
