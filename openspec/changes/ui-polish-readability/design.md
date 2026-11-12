# Design

## 1. 现状实测（改前基线）

所有比值按 WCAG 相对亮度公式实算（`(L1+0.05)/(L2+0.05)`，sRGB 分段线性化），脚本未落到仓库，仅用于本设计的取值决策。

### 1.1 对比度（主题 token 原值）

| 角色 | 原取值 | `bg-primary` | `bg-secondary` | `bg-tertiary` | 判定 |
|---|---|---|---|---|---|
| 浅色 `text-muted` | `#7c8296` | `#ffffff` **3.82** | `#f4f4f8` **3.49** | `#e6e6ef` **3.08** | 三档全不达标 |
| 深色 `text-muted` | `#6272a4` | `#282a36` **3.03** | `#21222c` **3.36** | `#44475a` **1.94** | 三档全不达标 |
| 浅色 `text-secondary` | `#4b5063` | 7.99 | 7.29 | 6.44 | 达标 |
| 深色 `text-secondary` | `#bcc2cd` | 7.96 | 8.83 | 5.11 | 达标 |
| 浅色语义色（success/error） | `#15803d` / `#dc2626` | 5.02 / 4.83 | 4.57 / 4.40 | **4.04 / 3.89** | 凸起表面跌破 |

### 1.2 排版度量

| 项 | 原值 | 实测问题 |
|---|---|---|
| 聊天正文 `.message-content` | `$font-size-sm`(13px) / `$line-height-lg`(24px) | 比值 **1.85**（>1.75 偏松），字号低于正文档位 |
| 行高表达方式 | `calc(var(--font-size-base) + 6px)` | **固定 px 增量**：基准 18px 时比值降为 24/18 = 1.33 |
| 消息列行宽 | `.messages` / `.message-body` 均无 `max-width` | 随窗口线性拉长，截图实测 ≈1136px ≈ 每行 87 汉字（舒适区 30–40） |

### 1.3 信息密度

| 位置 | 原状 | 问题 |
|---|---|---|
| `SessionSidebar.vue:256-258` | 每行渲染「{状态} · {时间}」 | 45 条会话 = 状态词重复 45 次，信息量为零；状态圆点已表达同一信息 |
| `ChatPane.vue:1294` | placeholder 承载 4 条说明（Enter/Shift+Enter、`/` 技能、`/vision`、拖拽图片） | 说明与「输入什么」混在一行，且用 3.82:1 的灰色 → 双重不可读 |

## 2. 取值决策

### 2.1 `text-muted` 新取值

**浅色 → `#5f6678`**：`#ffffff` 5.74 / `#f4f4f8` 5.23 / `#e6e6ef` 4.63，**三档全部达标**，可以无条件替换，不需要额外的使用限制。

候选比较（为什么不是更浅的 `#6b7284`）：`#6b7284` 在白底 4.81 看似够，但 `bg-secondary` 只有 4.39、`bg-tertiary` 3.88——**只在最深的那一档背景达标，等于没解决**。这一条是本设计阶段被实测推翻的第一个直觉取值。

**深色 → `#a3aacf`**：`#282a36` 6.24 / `#21222c` 6.92 / `#44475a` **4.01**。

深色侧的取舍必须显式记录：若要把 `bg-tertiary` 也推到 4.5，需要 `#b0b7d8`（4.62），但它在 `bg-primary` 上达到 7.20，与 `text-secondary` 的 7.96 只差 1.11 倍——**次级与次要文本在视觉上会合并成一级**，等于用一个层级缺陷换一个对比度合规。因此选择保留层级、把「muted 不用于 `bg-tertiary`」写进 spec（场景：该表面改用 `text-secondary`，5.11 达标）。

配套修改 `--color-text-muted-rgb` 分量（拼透明度时用）：深色 `98 114 164` → `163 170 207`；浅色 `124 130 150` → `95 102 120`。

层级校验（改后三档仍单调可辨）：浅色 primary/secondary/muted = **16.37 / 7.99 / 5.74**；深色 = **13.36 / 7.96 / 6.24**。

### 2.2 行宽度量：用 `ch` 而不是 px

`ch` 是「0」字形的推进宽度，在多数 UI 字体约为 `0.55em`。14px 基准下：

- `72ch` ≈ 72 × 0.55 × 14 ≈ **555px**
- 拉丁文：每行 ≈ 72 字符 → 落在 65–75 区间
- 中文：每行 ≈ 555 / 14 ≈ **40 个汉字** → 落在 30–40 舒适区上沿

**同一个数值同时命中中英两种文字各自的舒适区**，这是选 `ch` 而非 `px` 的核心理由——若写死 `640px`，中英两侧就必然有一侧偏离。同时 `ch` 随字号基准缩放，与「字号滑块」保持一致。

**列宽与正文度量取同一值**（不采用「宽列 + 窄正文」的嵌套方案）：后者会让正文在宽列里左对齐、右侧留大片空白，观感不齐。代价是表格/代码块在 555px 内偏窄——但它们本就有横向溢出策略，且 `DiffPane` 是独立面板不受影响。

留 `--measure-prose` 变量是为了未来单点可调（例如用户觉得太窄，改一个值即可）。

### 2.3 行高改用无单位倍数

`line-height: 1.7` 而非 `calc(...)`。理由：现有 `calc(var(--font-size-base) + 6px)` 是**加性**的，字号基准一变比值就漂（18px 时 1.33），直接违反「行高随字号等比缩放」。无单位倍数由浏览器按当前字号计算，天然等比。

正文取 **1.7**（区间 1.5–1.75 内偏松一侧）：中文正文在 1.7 时更易分辨行，且与现有 24px 视觉效果接近（14×1.7 = 23.8px）。

只改聊天正文，**不动 `variables.scss` 里全局的 `$line-height-*` 派生**——那两个用于按钮、输入框等紧凑控件，改成 1.5 会让工具栏变高，属于本变更范围外的连锁改动。

### 2.4 字号：13px → 14px，但不提高全局基准

正文用 `$font-size-md`（14px，即基准值）。**不把 `--font-size-base` 提到 16px**：这是桌面 IDE 类应用（VS Code 13px、JetBrains 13–14px），且 `$topbar-height: 48px` / `$sidebar-width: 240px` / `$pane-header-height: 36px` 都是按 14px 调的固定尺寸，提到 16 会连带挤压。技能库的 16px 是 Web/移动端默认假设，在此不适用。

## 3. 实现落点

| 文件 | 改动 |
|---|---|
| `assets/styles/tokens.css` | 深/浅两套 `--color-text-muted` 与 `--color-text-muted-rgb`；新增 `--measure-prose: 72ch` 与 `--measure-column: 72ch` |
| `assets/styles/variables.scss` | 映射 `$measure-prose` / `$measure-column`；新增 `$line-height-prose: 1.7` |
| `components/business/MessageBubble.vue` | `.message-content`：字号 `$font-size-md`、行高 `$line-height-prose`；`.message-body` 加行宽上限 |
| `panes/ChatPane.vue` | `.messages` 内容居中；`.message-row` / `.process-row` 加 `max-width` + `margin-inline: auto`；placeholder 瘦身 + 新增说明行 |
| `components/layout/SessionSidebar.vue` | `.session-meta` 去掉状态词 `<span>`，保留芯片与时间 |

## 4. 验证方法

1. **构建**：`npm run build`（项目约定前端改动必须过构建；CI 的 go 作业依赖真实 `dist` 产物）。
2. **对比度**：按 §1.1 表格逐项复算改后值，浅色三档全部 ≥4.5、深色 `bg-primary`/`bg-secondary` ≥4.5、`bg-tertiary` 按 §2.1 限制改用 `text-secondary`。
3. **行宽**：窗口宽度分别取 1024 / 1280 / 1600px，确认段落行宽不变、且消息列水平居中；窗口收窄到小于度量值时确认无横向滚动。
4. **行高等比**：设置页把字体大小调到最大与最小，确认行高比值不变（不出现字小行高巨大或反之）。
5. **冗余状态**：侧边栏常态行只剩「标题 + 时间」；运行中会话仍显示芯片。
6. **双主题**：`data-theme` 深/浅各看一遍空状态、placeholder、侧边栏元信息三处，确认层级未塌陷。

## 5. 风险与已规避项

| 风险 | 处置 |
|---|---|
| `--color-text-muted` 被 `.empty-state`、`.text-muted` 工具类、`::placeholder` 共用 | 预期内统一收益；已在 §2.1 校验层级差仍可辨 |
| 555px 列宽在宽屏上可能显得偏窄 | 收敛到 `--measure-prose` 单点；改一个值即可调整，不影响结构 |
| 行高只改正文不改全局 | 显式记录：全局行高涉及控件高度，属范围外 |
| 深色 `bg-tertiary` 上 muted 仍不达标 | 不掩盖：写入 spec 的使用限制 + 该场景改用 `text-secondary` |
| `ChatPane.vue` 1731 行，改样式有误伤相邻规则的风险 | 只改 `.messages` / `.message-row` / `.process-row` / 输入区四处选择器，不动逻辑与模板结构 |

## 6. 未采纳的替代方案

- **跑 `--design-system` 重新出配色**：会重设整套 palette，而 `variables.scss` 61 处映射、9 个弹窗、双主题全挂在现有 Dracula 上，等于推翻重来；且本变更的目标是达标而非换风格。
- **`--persist` 写 `design-system/MASTER.md`**：会与 `docs/ui-polish-plan.md` 形成两份冲突的事实来源。
- **引入 stylelint 自动拦截裸色值**：收益明确但需引入新依赖与 CI 步骤，属独立变更。
- **一次性做完全部 P2/P3（弹窗骨架、z-index、emoji、`transition: all`）**：触及 12 个弹窗与 21 处过渡，风险与本次「度量修正」不同量级，拆分处理。
