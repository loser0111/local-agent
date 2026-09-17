import {
  ListSkills,
  GetSkill,
  SaveSkill,
  DeleteSkill,
  ToggleSkill,
  RefreshSkills,
  SkillsDir,
  InstallSkillFromFolder,
  InstallSkillFromZip,
  InstallSkillFromGit,
  UpdateSkill,
  ListSkillResources,
  PickSkillFolder,
  PickSkillZip,
} from '@/../wailsjs/go/main/App'

/**
 * 判断是否运行在 Wails 桌面环境
 */
function isWails() {
  return typeof window !== 'undefined' && window.go && window.go.main && window.go.main.App
}

// ===== 浏览器 dev mock（localStorage 持久化）=====
//
// 只用于 `npm run dev` 下的界面预览：不落盘、不执行安装。
// 字段与后端 SkillMeta 保持一致，避免 mock 与真实环境形状漂移。

const MOCK_KEY = 'local-agent:skills'

function defaultMockSkills() {
  return [
    {
      id: 'hello-skill',
      name: 'hello-skill',
      description: '演示用技能：当用户请求打招呼、问好，或想了解技能机制如何运作时使用。',
      dir: '~/.local-agent/skills/hello-skill',
      enabled: true,
      builtin: false,
      version: '1.0.0',
      license: 'MIT',
      author: '',
      allowedTools: [],
      disableModelInvocation: false,
      userInvocable: true,
      hasScripts: true,
      resources: [
        { path: 'scripts/greet.sh', size: 48, kind: 'scripts' },
      ],
      errors: [],
      warnings: [],
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

function findMock(id) {
  const m = getMockSkills().find((s) => s.id === id)
  if (!m) throw new Error('技能不存在')
  return m
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
 * 获取技能详情（含 SKILL.md 正文与完整 frontmatter）
 * @param {string} id
 * @returns {Promise<import('@/types').SkillDetail>}
 */
export async function getSkill(id) {
  if (isWails()) return await GetSkill(id)
  const m = findMock(id)
  return { ...m, body: '# ' + m.name + '\n\n（mock 模式：正文不持久化，请在桌面端编辑）' }
}

/**
 * 新建或更新技能
 * @param {string} id 技能目录名
 * @param {import('@/types').SkillDraft} draft frontmatter 字段 + 正文
 */
export async function saveSkill(id, draft) {
  if (isWails()) return await SaveSkill(id, draft)
  const list = getMockSkills()
  const idx = list.findIndex((s) => s.id === id)
  const base = {
    id,
    name: draft.name || id,
    description: draft.description || '',
    dir: '~/.local-agent/skills/' + id,
    enabled: true,
    builtin: false,
    version: draft.version || '',
    license: draft.license || '',
    author: draft.author || '',
    allowedTools: draft.allowedTools || [],
    disableModelInvocation: !!draft.disableModelInvocation,
    userInvocable: draft.userInvocable !== false,
    hasScripts: false,
    resources: [],
    errors: [],
    warnings: [],
  }
  if (idx > -1) list[idx] = { ...list[idx], ...base }
  else list.push(base)
  saveMockSkills(list)
}

/**
 * 删除技能（内置拒绝）
 * @param {string} id
 */
export async function deleteSkill(id) {
  if (isWails()) return await DeleteSkill(id)
  const target = findMock(id)
  if (target.builtin) throw new Error('内置技能不可删除')
  saveMockSkills(getMockSkills().filter((s) => s.id !== id))
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

/**
 * 列出技能目录内的可读资源（L3）
 * @param {string} id
 */
export async function listSkillResources(id) {
  if (isWails()) return (await ListSkillResources(id)) || []
  return findMock(id).resources || []
}

// ===== 安装 / 更新 =====

/**
 * 从本地文件夹安装（文件夹自身是技能，或装着若干技能的父目录）
 * @param {string} path
 * @returns {Promise<Array<import('@/types').SkillInstallResult>>}
 */
export async function installSkillFromFolder(path) {
  if (isWails()) return await InstallSkillFromFolder(path)
  throw new Error('mock 模式不支持安装，请在桌面端使用')
}

/**
 * 从 zip 压缩包安装
 * @param {string} path
 * @returns {Promise<Array<import('@/types').SkillInstallResult>>}
 */
export async function installSkillFromZip(path) {
  if (isWails()) return await InstallSkillFromZip(path)
  throw new Error('mock 模式不支持安装，请在桌面端使用')
}

/**
 * 从 Git 仓库安装
 * @param {string} url
 * @param {string} ref 分支或标签，可为空
 * @param {string} subdir 仓库内子目录，可为空
 * @returns {Promise<Array<import('@/types').SkillInstallResult>>}
 */
export async function installSkillFromGit(url, ref, subdir) {
  if (isWails()) return await InstallSkillFromGit(url, ref || '', subdir || '')
  throw new Error('mock 模式不支持安装，请在桌面端使用')
}

/**
 * 重新拉取 git 来源的技能
 * @param {string} id
 * @returns {Promise<import('@/types').SkillInstallResult>}
 */
export async function updateSkill(id) {
  if (isWails()) return await UpdateSkill(id)
  throw new Error('mock 模式不支持更新，请在桌面端使用')
}

/**
 * 打开系统目录选择对话框，返回所选路径（取消时为空串）
 * @returns {Promise<string>}
 */
export async function pickSkillFolder() {
  if (isWails()) return await PickSkillFolder()
  return ''
}

/**
 * 打开系统文件选择对话框，返回所选 zip 路径（取消时为空串）
 * @returns {Promise<string>}
 */
export async function pickSkillZip() {
  if (isWails()) return await PickSkillZip()
  return ''
}
