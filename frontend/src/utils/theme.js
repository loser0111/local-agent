/**
 * 主题与字号的应用逻辑（与 Pinia 解耦，方便单独测试/复用）
 *
 * 主题通过 `<html data-theme="dark|light">` 生效，真实配色见 assets/styles/tokens.css。
 * 「跟随系统」用 matchMedia 实时求值，而不是交给 CSS 媒体查询 —— 这样用户手动
 * 选了深色/浅色时能覆盖系统偏好，且 JS 侧（例如 Wails 原生窗口）拿到的是同一个结果。
 */
import * as runtime from '@/../wailsjs/runtime/runtime'

const LIGHT_MEDIA = '(prefers-color-scheme: light)'

/** 设置页「主题」下拉的候选项 */
export const THEME_OPTIONS = [
  { value: 'dark', label: '深色' },
  { value: 'light', label: '浅色' },
  { value: 'system', label: '跟随系统' },
]

/** 默认保持暗色优先（与 docs/frontend-design.md 的设计原则一致） */
export const DEFAULT_THEME = 'dark'

export const FONT_SIZE_MIN = 12
export const FONT_SIZE_MAX = 20
export const FONT_SIZE_DEFAULT = 14

let mediaQuery = null
let mediaHandler = null

export function normalizeTheme(value) {
  return value === 'dark' || value === 'light' || value === 'system' ? value : DEFAULT_THEME
}

export function normalizeFontSize(value) {
  const n = Number(value)
  if (!Number.isFinite(n)) return FONT_SIZE_DEFAULT
  return Math.min(FONT_SIZE_MAX, Math.max(FONT_SIZE_MIN, Math.round(n)))
}

function systemPrefersLight() {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia(LIGHT_MEDIA).matches
  )
}

/** 把 dark/light/system 解析成实际生效的 dark | light */
export function resolveTheme(theme) {
  const normalized = normalizeTheme(theme)
  if (normalized === 'system') return systemPrefersLight() ? 'light' : 'dark'
  return normalized
}

/** 同步宿主窗口（标题栏、原生菜单）的主题；非 Wails 环境静默跳过 */
function syncNativeWindow(resolved) {
  try {
    const inWails = typeof window !== 'undefined' && !!window.runtime
    if (!inWails) return
    if (resolved === 'light') runtime.WindowSetLightTheme()
    else runtime.WindowSetDarkTheme()
  } catch (e) {
    // 主题同步失败不影响界面本身，仅记录
    console.warn('同步原生窗口主题失败:', e)
  }
}

/**
 * 应用主题
 * @param {'dark'|'light'|'system'} theme
 * @returns {'dark'|'light'} 实际生效的主题
 */
export function applyTheme(theme) {
  const resolved = resolveTheme(theme)
  document.documentElement.setAttribute('data-theme', resolved)
  syncNativeWindow(resolved)
  return resolved
}

/**
 * 应用基础字号（驱动 --font-size-base，进而派生所有 $font-size-* / $line-height-*）
 * @param {number} size 像素值，会被夹到 [FONT_SIZE_MIN, FONT_SIZE_MAX]
 * @returns {number} 实际生效的像素值
 */
export function applyFontSize(size) {
  const px = normalizeFontSize(size)
  document.documentElement.style.setProperty('--font-size-base', `${px}px`)
  return px
}

/**
 * 仅在「跟随系统」时监听系统主题变化；切换到固定主题时自动注销。
 * @param {'dark'|'light'|'system'} theme
 * @param {(resolved: string) => void} onChange
 */
export function watchSystemTheme(theme, onChange) {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return

  if (mediaQuery && mediaHandler) {
    mediaQuery.removeEventListener('change', mediaHandler)
    mediaQuery = null
    mediaHandler = null
  }
  if (normalizeTheme(theme) !== 'system') return

  mediaQuery = window.matchMedia(LIGHT_MEDIA)
  mediaHandler = () => onChange(applyTheme('system'))
  mediaQuery.addEventListener('change', mediaHandler)
}
