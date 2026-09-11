/**
 * Markdown 渲染工具
 * - marked：Markdown → HTML
 * - highlight.js：代码块语法高亮（常用语言按需注册，控制打包体积）
 * - DOMPurify：HTML 消毒，防止 XSS
 */
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/core'

// 按需注册常用语言
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import python from 'highlight.js/lib/languages/python'
import go from 'highlight.js/lib/languages/go'
import java from 'highlight.js/lib/languages/java'
import bash from 'highlight.js/lib/languages/bash'
import powershell from 'highlight.js/lib/languages/powershell'
import json from 'highlight.js/lib/languages/json'
import yaml from 'highlight.js/lib/languages/yaml'
import xml from 'highlight.js/lib/languages/xml'
import css from 'highlight.js/lib/languages/css'
import sql from 'highlight.js/lib/languages/sql'
import markdownLang from 'highlight.js/lib/languages/markdown'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import cpp from 'highlight.js/lib/languages/cpp'
import csharp from 'highlight.js/lib/languages/csharp'
import rust from 'highlight.js/lib/languages/rust'
import plaintext from 'highlight.js/lib/languages/plaintext'

const languages = {
  javascript,
  js: javascript,
  typescript,
  ts: typescript,
  python,
  py: python,
  go,
  golang: go,
  java,
  bash,
  sh: bash,
  shell: bash,
  powershell,
  ps1: powershell,
  json,
  yaml,
  yml: yaml,
  xml,
  html: xml,
  vue: xml,
  css,
  scss: css,
  sql,
  markdown: markdownLang,
  md: markdownLang,
  dockerfile,
  docker: dockerfile,
  cpp,
  c: cpp,
  csharp,
  cs: csharp,
  rust,
  rs: rust,
  plaintext,
  text: plaintext,
  txt: plaintext,
}

Object.entries(languages).forEach(([name, lang]) => {
  hljs.registerLanguage(name, lang)
})

// 兼容别名（normalizeLang 时 highlight.js 只认注册名，这里做一层映射）
const langAliases = {
  js: 'javascript',
  ts: 'typescript',
  py: 'python',
  golang: 'go',
  sh: 'bash',
  shell: 'bash',
  ps1: 'powershell',
  yml: 'yaml',
  html: 'xml',
  scss: 'css',
  md: 'markdown',
  docker: 'dockerfile',
  c: 'cpp',
  cs: 'csharp',
  rs: 'rust',
  text: 'plaintext',
  txt: 'plaintext',
}

/**
 * 转义 HTML（用于 highlight 失败时的兜底）
 */
function escapeHtml(str) {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

/**
 * 代码块高亮
 * @param {string} code 代码内容
 * @param {string} lang 语言标识
 */
function highlightCode(code, lang) {
  const normalizedLang = langAliases[(lang || '').toLowerCase()] || (lang || '').toLowerCase()
  if (normalizedLang && hljs.getLanguage(normalizedLang)) {
    try {
      return hljs.highlight(code, { language: normalizedLang }).value
    } catch {
      // 高亮失败时降级为纯文本
    }
  }
  // 未指定语言：自动检测（可能不准时退回纯文本）
  try {
    return hljs.highlightAuto(code).value
  } catch {
    return escapeHtml(code)
  }
}

// 配置 marked 渲染器
marked.use({
  gfm: true, // GitHub 风格 Markdown（表格、删除线等）
  breaks: true, // 单换行转 <br>
  renderer: {
    // marked v14+：code 接收 token 对象，兼容旧版字符串参数
    code(token) {
      const code = typeof token === 'string' ? token : token.text
      const lang = typeof token === 'string' ? arguments[1] : token.lang
      const highlighted = highlightCode(code, lang || '')
      const langLabel = lang ? ` data-lang="${escapeHtml(lang)}"` : ''
      return `<pre class="md-code-block"${langLabel}><code class="hljs language-${escapeHtml(lang || 'plaintext')}">${highlighted}</code></pre>`
    },
    // 外链新窗口打开
    link({ href, title, tokens }) {
      const text = this.parser.parseInline(tokens)
      const titleAttr = title ? ` title="${escapeHtml(title)}"` : ''
      return `<a href="${href}"${titleAttr} target="_blank" rel="noopener noreferrer">${text}</a>`
    },
  },
})

/**
 * 将 Markdown 渲染为经过消毒的 HTML
 * @param {string} content Markdown 原文
 * @returns {string} 安全的 HTML
 */
export function renderMarkdown(content) {
  if (!content) return ''
  const rawHtml = marked.parse(content, { async: false })
  return DOMPurify.sanitize(rawHtml, {
    // 允许语法高亮的 class（hljs-* 及语言类名）
    ADD_ATTR: ['target', 'rel', 'data-lang'],
  })
}
