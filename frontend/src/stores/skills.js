import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  fetchSkills,
  getSkill,
  saveSkill,
  deleteSkill,
  toggleSkill,
  setSkillAlwaysInject,
  refreshSkills,
} from '@/api/skill'

/**
 * 技能（Skills）Store
 * 维护技能中心列表（文件夹 + SKILL.md），供设置页和新建会话技能选择器使用
 */
export const useSkillsStore = defineStore('skills', () => {
  const skills = ref([])
  const loading = ref(false)
  const loaded = ref(false)

  // 已启用且无解析错误的技能（会话白名单选择器用）
  const enabledSkills = computed(() => skills.value.filter((s) => s.enabled && !s.error))

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

  /** 新增/更新技能，随后重扫列表 */
  async function save(skill) {
    await saveSkill(skill)
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

  async function setAlwaysInject(id, v) {
    await setSkillAlwaysInject(id, v)
    const s = skills.value.find((x) => x.id === id)
    if (s) s.alwaysInject = v
  }

  async function refresh() {
    skills.value = (await refreshSkills()) || []
    loaded.value = true
  }

  function clear() {
    skills.value = []
    loaded.value = false
  }

  return {
    skills,
    loading,
    loaded,
    enabledSkills,
    load,
    detail,
    save,
    remove,
    toggle,
    setAlwaysInject,
    refresh,
    clear,
  }
})
