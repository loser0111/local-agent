# Proposal

## Why

聊天界面的**长文可读性**目前没有任何度量约束：`ChatPane.vue:1322` 的 `.messages` 与 `MessageBubble.vue:199` 的 `.message-body` 都只写了 `flex: 1`，全项目 `max-width` 只出现在 260px 的工具卡片和对话框上，正文一个都没有。于是在 1024px 以上的窗口里，正文行宽随窗口线性拉长——按截图实测约 1136px，13px 中文约合**每行 87 个汉字**，而舒适阅读区间是 30–40 字。同时正文用的是 `$font-size-sm`（13px）配 24px 行高（比值 1.85），形成"小字疏排"：字偏小、行偏松，两个方向同时偏离。

配色侧有一个可量化的合规缺陷：`--color-text-muted` 在浅色主题取 `#7c8296`，白底上对比度 **3.82:1**、落在 `bg-secondary` 上 **3.49:1**、落在 `bg-tertiary` 上 **3.08:1**，全部低于 WCAG AA 的 4.5:1；深色主题取 `#6272a4`，在 `#282a36` 上 **3.03:1**。而它承载的正是侧边栏每行的「活跃 · 昨天」、输入框占位符、`.empty-desc` 空状态说明这些**必须能读清**的信息。（以上比值均按 WCAG 相对亮度公式实算，非估算。）

界面文案上还有两处信息密度问题：侧边栏 45 条会话每行都重复一遍「活跃」（状态词出现 45 次，信息量为零），输入框占位符把 Enter/Shift+Enter、`/` 技能、`/vision`、拖入图片四条说明压在一行 3.82:1 的灰色文字里。

**为什么现在做**：这几项在 `docs/ui-polish-plan.md` 的 P1/P2b 里已有结论但从未落地（`text-muted` 未列入当时清单，行宽与字号问题当时未被发现）。它们是纯样式与文案改动、不触及任何业务逻辑，却能一次性改善整个界面的阅读体验，属于投入产出比最高的一批。

## What Changes

- **正文列宽自适应**：给消息列加自适应宽度——按容器宽度 92% 伸缩并设 1120px 上限，使列宽随面板宽度增长、在超宽窗口下封顶，两侧不再留出大片空白；同时保证侧边栏/右侧面板开合时列宽随之收窄而不溢出。代码块、表格等需要横向空间的元素不受此限制影响（沿用各自的溢出策略）。**不采用纯字符数度量**（如固定 `72ch`）：它在 1200px 面板里只占约 630px，两侧各留近 300px 空白、工具卡片一并变窄，实测观感不可接受，故改为「比例自适应 + px 上限」，取舍记录见 design.md §2.2。
- **BREAKING（视觉可见）**：列宽策略由「无约束」变为「比例 + 上限」，宽窗口下正文会比原先的满宽更窄，但在常规窗口里明显宽于初版 72ch 方案。
- **正文字号与行高**：聊天正文由 `$font-size-sm`（13px）升为 `$font-size-md`（14px），行高由 `$line-height-lg`（24px，比值 1.85）改为无单位 `1.7`，回到 1.5–1.75 的建议区间且随字号滑块等比缩放。
- **对比度修正**：`--color-text-muted` 换值——浅色主题 `#7c8296` → `#5f6678`（白底 5.74:1、`bg-secondary` 5.23:1、`bg-tertiary` 4.63:1，三档全部达标）；深色主题 `#6272a4` → `#a3aacf`（`#282a36` 上 6.24:1、`#21222c` 上 6.92:1）。同步更新两套主题的 `--color-text-muted-rgb` 分量（`95 102 120` / `163 170 207`）。**已知限制**：深色主题的 `#a3aacf` 落在 `bg-tertiary`（`#44475a`）上只有 4.01:1——再提亮到 4.5 就会与 `text-secondary` 的层级塌陷，因此本次明确「muted 不用在 bg-tertiary 上」，该场景改用 `text-secondary`（在 tertiary 上为 5.11:1）。**BREAKING（视觉可见）**：次要文字观感会比现在明显加深/提亮，这是达标所必需。
- **侧边栏元信息去重**：去掉每行重复的状态词（`SessionSidebar.vue:256`），状态改为仅由已有的 `status-dot` 圆点表达，文字只保留相对时间；「运行中 / 待应答」这类**非常态**信息继续保留为芯片。
- **占位符瘦身**：输入框 placeholder 只保留「输入消息…」，快捷键与 `/` 技能提示移到输入框下方的常驻说明行（用 `text-muted` 但字号不小于 12px，且不再与 placeholder 混为一段）。

**明确不在本次范围**：工具栏 10 个控件的分组/降级（原方案第 6 项，估 1–2 天）、弹窗骨架抽取、z-index token 化、emoji→lucide、`transition: all` 收敛、Wails `MinWidth/MinHeight`。这些留在 `docs/ui-polish-plan.md` 的 P2/P3 与后续变更处理。

## Capabilities

### New Capabilities

- `frontend-visual-system`: 桌面端前端的视觉呈现规范——文本对比度下限（WCAG AA）、排版度量（字号层级、行高比值、正文行宽区间）、以及界面信息密度（状态表达的冗余、占位符与说明文案的承载位置）。该能力为后续主题与排版类改动提供可验收的度量基线。

### Modified Capabilities

（无：`plan-execution-recovery` 与 `session-plan-mode` 均为计划模式的后端行为，本次不改变任何 spec 级行为。）

## Impact

- **前端样式 token**：`frontend/src/assets/styles/tokens.css`（`--color-text-muted` 深/浅两套取值，及其 `-rgb` 分量）。
- **前端样式度量**：`frontend/src/assets/styles/variables.scss`（新增可读性度量变量，供组件引用）、`frontend/src/components/business/MessageBubble.vue`（`.message-body` / `.message-content` 的字号、行高、行宽）、`frontend/src/panes/ChatPane.vue`（`.messages` 与 `.process-row` 的行宽居中、placeholder 文案）。
- **前端布局组件**：`frontend/src/components/layout/SessionSidebar.vue`（`.session-meta` 去掉状态词）。
- **不受影响**：Go 后端、Wails 绑定、API 契约、持久化格式。本次不新增/修改任何导出方法，`frontend/wailsjs/` 无需重新生成。
- **回归风险**：`.empty-state` 与 `.text-muted` 工具类共用 `--color-text-muted`，改色会同时影响空状态与工具类——这是预期内的统一收益；需在两套主题下目视确认次要文字与主文字的层级没被抹平（实测层级差：浅色 primary/secondary/muted = 16.37 / 7.99 / 5.74，深色 = 13.36 / 7.96 / 6.24，三档仍可区分）。
- **验证方式**：`npm run build`（项目约定前端改动必须过构建）+ 深/浅两主题下按对比度表复核 + 不同窗口宽度下检查行宽不随窗口拉长。
