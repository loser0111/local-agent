<script setup>
import { ref, computed, watch } from 'vue'
import { X, Loader2, ChevronDown, ChevronRight, Info } from 'lucide-vue-next'
import { getUsageDetail } from '@/api/session'
import { formatTokens, formatPercent, formatClock } from '@/utils/format'

/**
 * 用量明细弹窗。
 *
 * 数据有**两个来源**，刻意分开：
 *   - 打开时向后端拉一次完整明细（getUsageDetail）：含请求快照、上下文构成、原始 usage，
 *     这些重、且只在打开这一刻有意义；
 *   - 运行期间由 `live` 属性喂进来的实时事件（chat 事件的 onUsage）：只有几个整数，
 *     用于让开着的弹窗跟着刷新，不必等下一轮结束。
 *
 * 两者字段同源（后端同一套 cacheViewOf 算出），所以可以直接按轮次取新，不存在口径不一致。
 */
const props = defineProps({
  visible: Boolean,
  sessionId: { type: String, default: '' },
  // 实时用量事件（会话累计 + 本轮 + 缓存状态）；无数据时传 null
  live: { type: Object, default: null },
})
const emit = defineEmits(['close', 'compact'])

const detail = ref(null)
const loading = ref(false)
const errorMsg = ref('')
const showRaw = ref(false)

function reload() {
  errorMsg.value = ''
  showRaw.value = false
  detail.value = null
  if (!props.sessionId) return
  loading.value = true
  getUsageDetail(props.sessionId)
    .then((d) => {
      detail.value = d
    })
    .catch((e) => {
      errorMsg.value = `读取失败：${e?.message || e}`
    })
    .finally(() => {
      loading.value = false
    })
}

watch(() => props.visible, (v) => { if (v) reload() })
// 打开状态下切会话也要重拉，否则会显示上一个会话的数字
watch(() => props.sessionId, () => { if (props.visible) reload() })

/**
 * 合并后的视图：以拉取到的明细为底，实时事件更新时按**轮次**取新。
 * 用轮次而不是时间戳比较——前端与后端时钟不同源，时间戳比较会在跨时区/睡眠后失真。
 */
const view = computed(() => {
  const d = detail.value
  const lv = props.live
  if (!d) return null
  if (lv && (lv.turn || 0) > (d.totals?.turns || 0)) {
    return { ...d, last: lv.last, totals: lv.totals, cache: lv.cache, hasData: true }
  }
  return d
})

const last = computed(() => view.value?.last || {})
const totals = computed(() => view.value?.totals || {})
const cache = computed(() => view.value?.cache || {})
const ctx = computed(() => view.value?.context || null)
const req = computed(() => view.value?.request || null)

/** 本轮是否有数据。全零时显示空态而不是一排 0——0 和"没有"是两件事。 */
const lastHasData = computed(() => {
  const l = last.value
  return (l.inputUncached || 0) + (l.cacheRead || 0) + (l.cacheWrite || 0) + (l.output || 0) > 0
})

// ===== ② 缓存 =====

/**
 * 生效的 TTL 文案。
 *
 * 优先看**实际发生了哪种写入**（cacheWrite1h / cacheWrite5m 只有一个非零，来自 Anthropic 的
 * cache_creation 拆分），而不是看配置——服务端会单方面调整默认 TTL，
 * 只有实际写入档位能回答"我配的 1h 到底生效没有"。
 * 没有写入时退回显示配置值，并标注这是配置而非实测。
 */
const ttlText = computed(() => {
  if ((last.value.cacheWrite1h || 0) > 0) return '1 小时'
  if ((last.value.cacheWrite5m || 0) > 0) return '5 分钟'
  return cache.value.ttl === '1h' ? '1 小时（配置）' : '5 分钟（默认）'
})

/** 相对全价的成本倍数 → 人话。倍数由后端算，前端不重复实现。 */
const savingsText = computed(() => {
  const m = cache.value.costMultiple
  if (!Number.isFinite(m) || m >= 0.999) return '与全价持平'
  return `约为全价的 ${m.toFixed(2)}×（省 ${Math.round((1 - m) * 100)}%）`
})

const badgeClass = computed(() => `badge-${cache.value.state || 'idle'}`)

/** 状态徽标文案。与后端 CacheState 一一对应，不在这里推断状态本身。 */
const stateLabel = computed(() => {
  switch (cache.value.state) {
    case 'hit':
      return '已命中'
    case 'miss':
      return '未命中'
    case 'off':
      return '未启用'
    case 'unsupported':
      return '疑似不支持'
    default:
      return '无数据'
  }
})

// ===== ④ 上下文构成 =====

const ctxUsed = computed(() => ctx.value?.usedTokens || 0)
const ctxWindow = computed(() => ctx.value?.windowTokens || 0)

/**
 * 窗口占比只算**消息部分**，不含工具定义——这是后端两条路径的口径差异，不是本组件的选择：
 * 运行中那次统计把工具定义算进去了（`runToolLoop` 传了 toolTokens），
 * 而按会话查询走的 `contextStatForSession` 传的是 0（查询路径拿不到已装配的工具视图）。
 * 所以指示器上的百分比与"相加后的总数"天然对不上，必须分别标注，不能互相换算。
 */
const ctxPct = computed(() => (ctxWindow.value > 0 ? ctxUsed.value / ctxWindow.value : 0))

const toolTokens = computed(() => req.value?.toolSchemaTokens || 0)

/**
 * 本轮输入的两块之和。
 *
 * ⚠️ **不能**用 `usedTokens − toolTokens` 去推消息部分：`usedTokens` 里本来就不含工具定义，
 * 相减会得到负数。真机上就是这么显示成「消息历史 0」的——而那一轮的消息明明有 674 token。
 * 两个数各说各的、再给一个求和，比强行拼一个会算错的减法要诚实。
 */
const inputTotal = computed(() => ctxUsed.value + toolTokens.value)
const toolShare = computed(() => (inputTotal.value > 0 ? toolTokens.value / inputTotal.value : 0))

const summaryText = computed(() => {
  const c = ctx.value
  if (!c || !c.coveredMsgs) return '未压缩'
  return `摘要覆盖前 ${c.coveredMsgs} 条（${c.summaryChars} 字，省约 ${formatTokens(c.savedTokens)} token）`
})

/** 估算可信度：有锚点时是真值，没有时是纯字符估算（误差可达两位数百分比）。 */
const anchorText = computed(() => {
  const c = ctx.value
  if (!c) return ''
  return c.hasAnchor
    ? '建立在真实用量锚点上，可信'
    : `纯字符估算（校准系数 ${(c.calibRatio || 1).toFixed(2)}），误差可能较大`
})

function timeText(ms) {
  if (!ms) return '—'
  const d = new Date(ms)
  return `${d.getMonth() + 1}/${d.getDate()} ${formatClock(ms)}`
}

/** 原始 usage 的展示文本。没有时返回空串，模板据此隐藏整块。 */
const rawUsage = computed(() => String(view.value?.rawUsage || '').trim())
</script>

<template>
  <div v-if="visible" class="dialog-mask" @click.self="emit('close')">
    <div class="dialog">
      <div class="dialog-header">
        <h3>用量明细</h3>
        <button class="icon-btn" title="关闭" @click="emit('close')">
          <X :size="16" />
        </button>
      </div>

      <div class="dialog-body">
        <div v-if="!sessionId" class="tip">
          <Info :size="14" />
          <span>请先打开一个会话。</span>
        </div>

        <div v-else-if="loading" class="tip">
          <Loader2 :size="14" class="spin" />
          <span>读取中…</span>
        </div>

        <div v-else-if="errorMsg" class="tip err">
          <Info :size="14" />
          <span>{{ errorMsg }}</span>
        </div>

        <div v-else-if="view && !view.hasData" class="tip">
          <Info :size="14" />
          <span>
            这个会话还没有用量数据。它可能创建于用量统计上线之前，或者还没真正发过一条消息
            （统计只记录真实的模型调用，纯本地操作不计）。
          </span>
        </div>

        <template v-else-if="view">
          <!-- ① 本轮 -->
          <section class="sec">
            <div class="sec-title">
              本轮
              <span class="sec-sub">第 {{ totals.turns }} 次模型请求</span>
            </div>
            <div v-if="lastHasData" class="grid">
              <div class="cell">
                <span class="k">未命中输入</span>
                <span class="v" :title="String(last.inputUncached)">{{ formatTokens(last.inputUncached) }}</span>
              </div>
              <div class="cell">
                <span class="k">缓存读取</span>
                <span class="v" :title="String(last.cacheRead)">{{ formatTokens(last.cacheRead) }}</span>
              </div>
              <div class="cell">
                <span class="k">缓存写入</span>
                <span class="v" :title="String(last.cacheWrite)">{{ formatTokens(last.cacheWrite) }}</span>
                <!-- 写入按 TTL 分档计价（5 分钟 1.25×、1 小时 2×），
                     用户配了 1h 却看到挂在 5 分钟档，就说明配置没生效 -->
                <span v-if="last.cacheWrite > 0" class="sub">按 {{ ttlText }} 计价</span>
              </div>
              <div class="cell">
                <span class="k">输出</span>
                <span class="v" :title="String(last.output)">{{ formatTokens(last.output) }}</span>
                <!-- 推理 token 计费算输出、但不进正文：对用户是完全看不见的成本 -->
                <span v-if="last.outputReasoning > 0" class="sub">
                  其中推理 {{ formatTokens(last.outputReasoning) }}
                </span>
              </div>
            </div>
            <div v-else class="tip">
              <Info :size="14" />
              <span>本次响应没有带回用量明细。部分网关的流式响应不带 usage 字段。</span>
            </div>
          </section>

          <!-- ② 缓存 -->
          <section class="sec">
            <div class="sec-title">
              缓存
              <span class="badge" :class="badgeClass">{{ stateLabel }}</span>
            </div>

            <div class="cache-head">
              <div class="hit">
                <span class="hit-num">
                  {{ formatPercent(cache.hitRate, lastHasData) }}
                </span>
                <span class="hit-label">本轮命中率</span>
              </div>
              <div class="hit">
                <span class="hit-num">
                  {{ formatPercent(cache.totalHitRate, totals.turns > 0) }}
                </span>
                <span class="hit-label">会话累计命中率</span>
              </div>
            </div>

            <div class="bar">
              <div class="bar-fill" :style="{ width: `${Math.min(100, (cache.hitRate || 0) * 100)}%` }" />
            </div>

            <div class="kv">
              <span class="k">成本</span><span class="v">{{ savingsText }}</span>
            </div>
            <div class="kv">
              <span class="k">写入计价</span><span class="v">{{ ttlText }}（{{ (cache.writeRate || 1.25).toFixed(2) }}×）</span>
            </div>
            <div class="kv">
              <span class="k">开关</span>
              <span class="v">{{ cache.enabled ? '已启用' : '未启用（设置页可开）' }}</span>
            </div>

            <!-- note 由后端给出：文案要同时覆盖"未启用/未命中/未达最小长度/疑似网关不支持"
                 四种成因，放在前端拼必然散落成多个分支 -->
            <div class="note" :class="badgeClass">{{ cache.note }}</div>
          </section>

          <!-- ③ 会话累计 -->
          <section class="sec">
            <div class="sec-title">会话累计</div>
            <div class="grid">
              <div class="cell">
                <span class="k">请求轮数</span>
                <span class="v">{{ totals.turns || 0 }}</span>
              </div>
              <div class="cell">
                <span class="k">输入总量</span>
                <span class="v" :title="String((totals.inputUncached || 0) + (totals.cacheRead || 0) + (totals.cacheWrite || 0))">
                  {{
                    formatTokens(
                      (totals.inputUncached || 0) + (totals.cacheRead || 0) + (totals.cacheWrite || 0)
                    )
                  }}
                </span>
              </div>
              <div class="cell">
                <span class="k">输出总量</span>
                <span class="v" :title="String(totals.output)">{{ formatTokens(totals.output) }}</span>
                <span v-if="totals.outputReasoning > 0" class="sub">
                  其中推理 {{ formatTokens(totals.outputReasoning) }}
                </span>
              </div>
              <div class="cell">
                <span class="k">时间跨度</span>
                <span class="v small">{{ timeText(totals.firstAt) }} → {{ timeText(totals.lastAt) }}</span>
              </div>
            </div>
            <div class="footnote">
              累计只增不减：压缩与撤销改变的是「上下文里还剩多少」，不改变「已经花掉了多少」。
            </div>
          </section>

          <!-- ④ 上下文构成 -->
          <section v-if="ctx" class="sec">
            <div class="sec-title">
              上下文构成
              <span class="sec-sub">
                消息 {{ formatTokens(ctxUsed) }} + 工具定义 {{ formatTokens(toolTokens) }}
              </span>
            </div>

            <div class="stack" :title="`消息历史 ${ctxUsed}，工具定义 ${toolTokens}`">
              <div class="stack-tool" :style="{ width: `${Math.min(100, toolShare * 100)}%` }" />
            </div>
            <div class="legend">
              <span class="dot hist" /> 消息历史 {{ formatTokens(ctxUsed) }}
              <span class="dot tool" /> 工具定义 {{ formatTokens(toolTokens) }}
            </div>

            <div class="kv">
              <span class="k">消息条数</span><span class="v">{{ ctx.messageCount }}</span>
            </div>
            <div class="kv">
              <span class="k">窗口占比</span>
              <span class="v">
                消息部分占 {{ formatPercent(ctxPct, ctxWindow > 0) }}（{{ ctxUsed }} / {{ ctxWindow }} token）
              </span>
            </div>
            <div class="kv">
              <span class="k">摘要</span><span class="v">{{ summaryText }}</span>
            </div>
            <div class="kv">
              <span class="k">可信度</span><span class="v">{{ anchorText }}</span>
            </div>
            <div class="footnote">
              这两块**不能相加后就当作总量**：窗口占比只统计消息部分（后端的查询路径拿不到工具视图），
              而工具定义是每次请求都发的固定开销。所以指示器上的百分比会小于「两块之和 / 窗口」，
              两者不是同一个口径。
            </div>
          </section>

          <!-- ⑤ 原始 usage -->
          <section v-if="rawUsage" class="sec">
            <button class="raw-toggle" @click="showRaw = !showRaw">
              <component :is="showRaw ? ChevronDown : ChevronRight" :size="14" />
              原始 usage（排障用）
            </button>
            <pre v-if="showRaw" class="raw">{{ rawUsage }}</pre>
            <div v-if="showRaw" class="footnote">
              这是网关原样返回的 usage。解析出的字段可能在归一化时被我方改名，
              对不上时以这里为准。
            </div>
          </section>
        </template>
      </div>

      <div class="dialog-footer">
        <button class="btn btn-ghost" :disabled="!sessionId" @click="emit('compact')">
          立即压缩上下文
        </button>
        <button class="btn btn-ghost" @click="reload">刷新</button>
        <button class="btn" @click="emit('close')">关闭</button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.dialog-mask {
  position: fixed;
  inset: 0;
  background: $color-overlay;
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  padding: $space-lg;
}

.dialog {
  background: $color-bg-secondary;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  width: 720px;
  max-width: 100%;
  max-height: 88vh;
  display: flex;
  flex-direction: column;
  box-shadow: $shadow-lg;
}

.dialog-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: $space-md $space-lg;
  border-bottom: 1px solid $color-border;

  h3 {
    font-size: $font-size-md;
    font-weight: $font-weight-semibold;
    color: $color-text-primary;
  }
}

.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border: none;
  background: transparent;
  color: $color-text-muted;
  border-radius: $radius-sm;
  cursor: pointer;

  &:hover {
    background: $color-surface-tint;
    color: $color-text-primary;
  }
}

.dialog-body {
  padding: $space-lg;
  overflow-y: auto;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: $space-sm;
  padding: $space-md $space-lg;
  border-top: 1px solid $color-border;
}

.btn {
  padding: 6px 14px;
  border-radius: $radius-sm;
  border: 1px solid $color-border;
  background: $color-bg-tertiary;
  color: $color-text-primary;
  font-size: $font-size-sm;
  cursor: pointer;

  &:hover:not(:disabled) {
    border-color: $color-primary;
  }

  &:disabled {
    opacity: 0.5;
    cursor: default;
  }
}

.btn-ghost {
  background: transparent;
}

.sec {
  margin-bottom: $space-xl;

  &:last-child {
    margin-bottom: 0;
  }
}

.sec-title {
  display: flex;
  align-items: baseline;
  gap: $space-sm;
  font-size: $font-size-sm;
  font-weight: $font-weight-semibold;
  color: $color-text-primary;
  margin-bottom: $space-md;
  padding-bottom: $space-xs;
  border-bottom: 1px solid $color-border-light;
}

.sec-sub {
  font-weight: $font-weight-normal;
  color: $color-text-muted;
  font-size: $font-size-xs;
}

.grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: $space-md;
}

.cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.k {
  color: $color-text-muted;
  font-size: $font-size-xs;
}

.v {
  color: $color-text-primary;
  font-size: $font-size-lg;
  font-family: $font-family-mono;

  &.small {
    font-size: $font-size-xs;
    font-family: inherit;
  }
}

.sub {
  color: $color-text-muted;
  font-size: $font-size-xs;
}

.kv {
  display: flex;
  gap: $space-sm;
  font-size: $font-size-sm;
  line-height: $line-height-md;

  .k {
    min-width: 72px;
  }

  .v {
    font-size: $font-size-sm;
    color: $color-text-secondary;
  }
}

.cache-head {
  display: flex;
  gap: $space-2xl;
  margin-bottom: $space-sm;
}

.hit {
  display: flex;
  flex-direction: column;
}

.hit-num {
  font-size: $font-size-xl;
  font-family: $font-family-mono;
  color: $color-text-primary;
}

.hit-label {
  font-size: $font-size-xs;
  color: $color-text-muted;
}

.bar {
  height: 6px;
  border-radius: 3px;
  background: $color-bg-tertiary;
  overflow: hidden;
  margin-bottom: $space-md;
}

.bar-fill {
  height: 100%;
  background: $color-success;
  transition: width $transition-fast;
}

.stack {
  display: flex;
  height: 10px;
  border-radius: 3px;
  overflow: hidden;
  background: $color-bg-tertiary;
  margin-bottom: $space-xs;
}

.stack-tool {
  background: $color-warning;
  height: 100%;
}

.legend {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: $font-size-xs;
  color: $color-text-muted;
  margin-bottom: $space-md;
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  display: inline-block;

  &.tool {
    background: $color-warning;
  }

  &.hist {
    background: $color-border;
  }
}

.badge {
  margin-left: auto;
  font-size: $font-size-xs;
  font-weight: $font-weight-normal;
  padding: 1px 8px;
  border-radius: 10px;
  color: $color-text-muted;
  background: $color-surface-tint;
}

.note {
  margin-top: $space-sm;
  padding: $space-sm $space-md;
  border-radius: $radius-sm;
  font-size: $font-size-sm;
  line-height: $line-height-md;
  color: $color-text-secondary;
  background: $color-surface-tint;
  border-left: 3px solid $color-border;

  &.badge-hit {
    border-left-color: $color-success;
    background: $color-success-soft;
  }

  &.badge-miss {
    border-left-color: $color-warning;
    background: $color-warning-soft;
  }

  &.badge-unsupported {
    border-left-color: $color-error;
    background: $color-error-soft;
  }
}

.tip {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  color: $color-text-muted;
  font-size: $font-size-sm;
  line-height: $line-height-md;
  padding: $space-md 0;

  &.err {
    color: $color-error;
  }
}

.footnote {
  margin-top: $space-xs;
  color: $color-text-muted;
  font-size: $font-size-xs;
  line-height: $line-height-md;
}

.raw-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: none;
  background: transparent;
  color: $color-text-muted;
  font-size: $font-size-sm;
  cursor: pointer;
  padding: 0;

  &:hover {
    color: $color-text-primary;
  }
}

.raw {
  margin-top: $space-sm;
  padding: $space-md;
  background: $color-code-bg;
  color: $color-code-text;
  border-radius: $radius-sm;
  font-family: $font-family-mono;
  font-size: $font-size-xs;
  line-height: $line-height-md;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 240px;
  overflow-y: auto;
}

.spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
