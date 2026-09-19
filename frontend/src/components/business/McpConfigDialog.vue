<script setup>
import { computed, ref, watch } from 'vue'
import { ClipboardCopy, Upload, X } from 'lucide-vue-next'
import { useToolsStore } from '@/stores/tools'
import { useUiStore } from '@/stores/ui'

/**
 * MCP 配置导入 / 导出弹窗
 *
 * 导入：粘贴官方 mcpServers 片段（Claude Desktop / Cline / MCP 文档通用形状）即可生效，
 * 不必手工对照本项目的内部结构。
 * 导出：把本项目的 MCP 配置还原成官方片段，方便复制给别人或粘到别的客户端。
 */
const props = defineProps({
  visible: { type: Boolean, default: false },
  mode: { type: String, default: 'import' }, // import | export
})
const emit = defineEmits(['close', 'imported'])

const toolsStore = useToolsStore()
const ui = useUiStore()

const raw = ref('')
const result = ref(null)
const errorMsg = ref('')
const busy = ref(false)
const exportText = ref('')

const isImport = computed(() => props.mode === 'import')

const PLACEHOLDER = `{
  "mcpServers": {
    "qconfig": {
      "type": "streamable-http",
      "url": "https://example.com/mcp",
      "headers": { "x-token": "..." }
    }
  }
}`

watch(
  () => props.visible,
  async (v) => {
    if (!v) return
    errorMsg.value = ''
    result.value = null
    exportText.value = ''
    if (!isImport.value) {
      busy.value = true
      try {
        exportText.value = await toolsStore.exportMCP([])
      } catch (e) {
        errorMsg.value = `导出失败：${e?.message || e}`
      } finally {
        busy.value = false
      }
    }
  }
)

async function doImport() {
  errorMsg.value = ''
  result.value = null
  if (!raw.value.trim()) {
    errorMsg.value = '请先粘贴要导入的配置'
    return
  }
  busy.value = true
  try {
    const res = await toolsStore.importMCP(raw.value)
    result.value = res
    if (res?.imported?.length) {
      raw.value = ''
      emit('imported', res)
    }
  } catch (e) {
    errorMsg.value = `导入失败：${e?.message || e}`
  } finally {
    busy.value = false
  }
}

async function copyExport() {
  try {
    await navigator.clipboard.writeText(exportText.value)
    errorMsg.value = ''
    ui.notify('已复制到剪贴板', 'success')
  } catch {
    // 剪贴板可能被 webview 限制：退化成让用户手动复制（文本框本身可全选）
    errorMsg.value = '复制失败，请手动全选文本框内容复制'
  }
}
</script>

<template>
  <div v-if="visible" class="mcp-overlay" @click.self="emit('close')">
    <div class="mcp-dialog" role="dialog" aria-modal="true">
      <div class="dialog-header">
        <span class="dialog-icon">{{ isImport ? '📥' : '📤' }}</span>
        <h3 class="dialog-title">{{ isImport ? '导入 MCP 配置' : '导出 MCP 配置' }}</h3>
        <button class="icon-btn" title="关闭" @click="emit('close')"><X :size="14" /></button>
      </div>

      <div class="dialog-body">
        <template v-if="isImport">
          <p class="hint">
            粘贴官方 <code>mcpServers</code> 片段（Claude Desktop / Cline / MCP 文档里的形状）。
            支持 <code>streamable-http</code> / <code>sse</code> / <code>stdio</code> 三种接入方式；
            同名条目会覆盖更新。
          </p>
          <textarea
            v-model="raw"
            class="input mono textarea"
            rows="12"
            :placeholder="PLACEHOLDER"
            :disabled="busy"
          ></textarea>

          <div v-if="result" class="result">
            <div v-if="result.imported?.length" class="result-ok">
              ✅ 已导入：{{ result.imported.join('、') }}
            </div>
            <div v-if="result.skipped?.length" class="result-skip">
              <div>⚠️ 跳过 {{ result.skipped.length }} 条：</div>
              <ul>
                <li v-for="(s, i) in result.skipped" :key="i">
                  <span class="mono">{{ s.name }}</span> — {{ s.reason }}
                </li>
              </ul>
            </div>
          </div>
        </template>

        <template v-else>
          <p class="hint">下面是可以直接粘贴到其它客户端（或分享给同事）的官方格式片段：</p>
          <textarea
            class="input mono textarea"
            rows="12"
            readonly
            :value="exportText"
            placeholder="没有可导出的 MCP 配置"
          ></textarea>
        </template>

        <div v-if="errorMsg" class="dialog-error">{{ errorMsg }}</div>
      </div>

      <div class="dialog-footer">
        <span class="spacer"></span>
        <button class="btn" :disabled="busy" @click="emit('close')">关闭</button>
        <button v-if="isImport" class="btn btn-primary" :disabled="busy" @click="doImport">
          <Upload :size="13" /> {{ busy ? '导入中...' : '导入' }}
        </button>
        <button v-else class="btn btn-primary" :disabled="busy || !exportText" @click="copyExport">
          <ClipboardCopy :size="13" /> 复制
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.mcp-overlay {
  position: fixed;
  inset: 0;
  background-color: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 900;
}

.mcp-dialog {
  width: min(640px, 92vw);
  max-height: 88vh;
  display: flex;
  flex-direction: column;
  background-color: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.4);
}

.dialog-header {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-md;
  border-bottom: 1px solid $color-border;

  .dialog-title {
    margin: 0;
    font-size: $font-size-sm;
    color: $color-text-primary;
  }

  .icon-btn {
    margin-left: auto;
  }
}

.dialog-body {
  padding: $space-md;
  overflow-y: auto;
}

.hint {
  margin: 0 0 $space-sm;
  font-size: $font-size-xs;
  color: $color-text-secondary;
  line-height: 1.6;

  code {
    font-family: $font-family-mono;
    background-color: rgba(68, 71, 90, 0.6);
    padding: 0 4px;
    border-radius: 3px;
  }
}

.mono {
  font-family: $font-family-mono;
}

.textarea {
  width: 100%;
  resize: vertical;
  font-size: $font-size-xs;
  line-height: 1.5;
}

.result {
  margin-top: $space-sm;
  font-size: $font-size-xs;
}

.result-ok {
  color: $color-success;
  margin-bottom: 4px;
}

.result-skip {
  color: $color-text-secondary;

  ul {
    margin: 4px 0 0;
    padding-left: 18px;
  }
}

.dialog-error {
  margin-top: $space-sm;
  font-size: $font-size-xs;
  color: $color-error;
}

.dialog-footer {
  display: flex;
  align-items: center;
  gap: $space-sm;
  padding: $space-md;
  border-top: 1px solid $color-border;

  .spacer {
    flex: 1;
  }

  .btn {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
}
</style>
