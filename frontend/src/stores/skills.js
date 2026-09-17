import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  fetchSkills,
  getSkill,
  saveSkill,
  deleteSkill,
  toggleSkill,
  refreshSkills,
  installSkillFromFolder,
  installSkillFromZip,
  installSkillFromGit,
  updateSkill,
  listSkillResources,
} from '@/api/skill'

/**
 * 技能（Skills）Store
 * 维护技能中心列表（目录 + SKILL.md），供设置页、新建会话技能白名单与
 * 输入框的「/技能名」补全使用。
 *
 * 三级渐进式披露在前端只体现为列表与详情：L1 描述、L2 正文、L3 资源清单。
 */
export const useSkillsStore = defineStore('skills', () => {
  const skills = ref([])
  const loading = ref(false)
  const loaded = ref(false)
  const installing = ref(false)

  /** 可用技能（已启用且无阻断性错误）——会话白名单选择器用 */
  const enabledSkills = computed(() => skills.value.filter((s) => s.enabled && !hasErrors(s)))

  /** 可由模型自动触发的技能（排除 disable-model-invocation） */
  const autoTriggerSkills = computed(() => enabledSkills.value.filter((s) => !s.disableModelInvocation))

  /** 允许用户 /技能名 显式调用的技能——输入框补全用 */
  const invocableSkills = computed(() => enabledSkills.value.filter((s) => s.userInvocable !== false))

  /** 是否存在解析出错的技能（设置页顶部提示用） */
  const invalidCount = computed(() => skills.value.filter(hasErrors).length)

  function hasErrors(s) {
    return Array.isArray(s.errors) && s.errors.length > 0
  }

  async function load(force = false) {
    if (loaded.value && !force) return
    loading.value = true
    try {
      skills.value = (await fetchSkills()) || []
      loaded.value = true
    } catch (e) {
      console.error('加载技能列表失败:', e)
      skills.value = []
    } finally {
      loading.value = false
    }
  }

  /** 获取含正文的技能详情 */
  async function detail(id) {
    return await getSkill(id)
  }

  /** 列出技能内可读资源（L3） */
  async function resources(id) {
    return await listSkillResources(id)
  }

  /** 新增/更新技能，随后重扫列表 */
  async function save(id, draft) {
    await saveSkill(id, draft)
    await load(true)
  }

  async function remove(id) {
    await deleteSkill(id)
    skills.value = skills.value.filter((s) => s.id !== id)
  }

  async function toggle(id, enabled) {
    await toggleSkill(id, enabled)
    const s = skills.value.find((x) => x.id === id)
    if (s) s.enabled = enabled
  }

  async function refresh() {
    skills.value = (await refreshSkills()) || []
    loaded.value = true
  }

  /**
   * 统一包一层安装中状态：安装要跑 git/解压，属于慢操作，
   * 界面需要在期间禁用按钮而不是让用户重复点击。
   */
  async function runInstall(fn) {
    installing.value = true
    try {
      const results = (await fn()) || []
      await load(true)
      return results
    } finally {
      installing.value = false
    }
  }

  function installFromFolder(path) {
    return runInstall(() => installSkillFromFolder(path))
  }

  function installFromZip(path) {
    return runInstall(() => installSkillFromZip(path))
  }

  function installFromGit(url, ref, subdir) {
    return runInstall(() => installSkillFromGit(url, ref, subdir))
  }

  async function update(id) {
    return await runInstall(() => updateSkill(id))
  }

  function clear() {
    skills.value = []
    loaded.value = false
  }

  return {
    skills,
    loading,
    loaded,
    installing,
    enabledSkills,
    autoTriggerSkills,
    invocableSkills,
    invalidCount,
    load,
    detail,
    resources,
    save,
    remove,
    toggle,
    refresh,
    installFromFolder,
    installFromZip,
    installFromGit,
    update,
    clear,
  }
})
