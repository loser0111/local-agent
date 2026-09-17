import { readFileSync } from 'node:fs'
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc'

const files = [
  'src/components/layout/TopBar.vue',
  'src/panes/ChatPane.vue',
  'src/components/business/ToolProcess.vue',
  'src/components/business/ToolCallCard.vue',
]

let failed = 0
for (const f of files) {
  const source = readFileSync(f, 'utf8')
  const { descriptor, errors } = parse(source, { filename: f })
  if (errors.length) {
    failed++
    console.log(`[FAIL] ${f} parse:`, errors.map((e) => e.message))
    continue
  }
  try {
    const script = compileScript(descriptor, { id: f })
    const tpl = compileTemplate({
      source: descriptor.template.content,
      filename: f,
      id: f,
      compilerOptions: { bindingMetadata: script.bindings },
    })
    if (tpl.errors.length) {
      failed++
      console.log(`[FAIL] ${f} template:`, tpl.errors.map((e) => e.message || e))
      continue
    }
    // 汇总模板里引用到的标识符，便于人工核对是否已在 script 中定义
    const used = [...tpl.code.matchAll(/\$setup\.([A-Za-z_$][\w$]*)/g)].map((m) => m[1])
    const declared = Object.keys(script.bindings || {})
    const missing = [...new Set(used)].filter((u) => !declared.includes(u))
    console.log(`[ok] ${f}  script+template 编译通过；模板引用 ${new Set(used).size} 个标识符`)
    if (missing.length) console.log(`     未在 script setup 中声明: ${missing.join(', ')}`)
  } catch (e) {
    failed++
    console.log(`[FAIL] ${f} compileScript:`, e.message)
  }
}
process.exit(failed ? 1 : 0)
