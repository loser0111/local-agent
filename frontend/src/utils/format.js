/**
 * 时间格式化工具
 */

export function formatTime(timestamp) {
  if (!timestamp) return ''
  const now = Date.now()
  const diff = now - timestamp
  const minute = 60 * 1000
  const hour = 60 * minute
  const day = 24 * hour

  if (diff < minute) return '刚刚'
  if (diff < hour) return `${Math.floor(diff / minute)} 分钟前`
  if (diff < day) return `${Math.floor(diff / hour)} 小时前`
  if (diff < 7 * day) return `${Math.floor(diff / day)} 天前`

  const date = new Date(timestamp)
  return `${date.getMonth() + 1}/${date.getDate()}`
}

export function formatClock(timestamp) {
  if (!timestamp) return ''
  const date = new Date(timestamp)
  const h = String(date.getHours()).padStart(2, '0')
  const m = String(date.getMinutes()).padStart(2, '0')
  return `${h}:${m}`
}

export function formatFileSize(bytes) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/**
 * token 数的概览写法：1234 → "1.2k"，1234567 → "3.4M"。
 *
 * 只用于概览。**精确值必须另外给**（放在 title 里）：
 * token 数在排障时经常要逐位核对，四舍五入掉之后用户没法判断
 * "缓存写入是 2051 还是 2100" 这类问题。
 *
 * 千以下不做缩写 —— "512" 比 "0.5k" 好读得多。
 */
export function formatTokens(n) {
  const v = Number(n) || 0
  if (v < 1000) return String(v)
  if (v < 1000000) {
    const k = v / 1000
    // 10k 以上不再留小数：1.2k 有位意义，123.4k 没有
    return k < 10 ? `${k.toFixed(1)}k` : `${Math.round(k)}k`
  }
  return `${(v / 1000000).toFixed(2)}M`
}

/**
 * 比率 → 百分比文本（0.8734 → "87.3%"）。
 *
 * hasData 为 false 时返回 "—"，而不是 "0%"：前者表示"没有数据"，
 * 后者表示"有数据但一次都没命中"——这两件事在缓存统计里含义完全不同，
 * 混用会让用户把"还没跑过"误读成"缓存失效了"。所以由调用方显式告诉它有没有数据，
 * 而不是在这里靠 0 去猜（0 本身是合法数据）。
 */
export function formatPercent(ratio, hasData = true) {
  if (!hasData) return '—'
  const v = Number(ratio)
  if (!Number.isFinite(v) || v <= 0) return '0%'
  return `${(v * 100).toFixed(1)}%`
}

