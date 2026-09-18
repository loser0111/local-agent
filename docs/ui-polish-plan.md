# 前端 UI 美化与可用性修复方案

> 文档版本：v1.1（P0 + P2a 已实施，见文末「七、实施记录」）
> 范围：`frontend/`（Vue 3 + Vite + Wails）
> 触发背景：用户反馈「界面不够好看、没有主题选择功能、部分按钮点了没反应（例如弹窗上的按钮）」

---

## 一、调研结论速览

| 类别 | 结论 | 严重度 |
|------|------|--------|
| 主题系统 | 设置页**有**「主题」下拉，但底层完全没有实现 → 是「假控件」，选浅色界面毫无变化 | 高（正是用户反馈的现象） |
| 同类假控件 | 语言、字体大小、自动保存、PR 合并后自动归档，共 4 个，绑定到 store 中不存在的字段 | 高 |
| 死按钮 | 7 处「点击无反应」的按钮（含**所有面板标题栏的 ✕ 关闭按钮**） | 高 |
| 演示壳面板 | Terminal / File Editor / Preview 三个面板是硬编码假数据，且**无任何入口可以打开** | 中 |
| 视觉一致性 | 两套色板混用（Dracula + 早期 Tailwind 残留），60+ 处硬编码颜色、遮罩透明度 3 种、z-index 无秩序 | 中 |
| 主题化阻塞点 | 颜色全部是 **编译期固化的 SCSS 变量**，运行时无法切换 —— 这是主题功能必须先在架构上解决的前置问题 | 高（P0 前置） |

---

## 二、现状调研（附证据）

### 2.1 样式体系现状

- `frontend/src/assets/styles/variables.scss`（79 行）：纯 SCSS 变量，注释标明「Dracula Dark 主题」。共约 40 个 token（背景/文本/主色/语义色/字号/间距/圆角/阴影/尺寸/过渡）。
- `frontend/src/assets/styles/global.scss`（251 行）：`@use './variables.scss' as *`，定义 reset、通用工具类、`.btn`（primary/default/ghost/danger）、`.input`/`.textarea`、`.status-dot` 等。
- **30+ 个组件**都在 `<style scoped lang="scss">` 里 `@use '@/assets/styles/variables.scss' as *`（Vite 配置了自动注入）。
- 结论：**颜色值在构建时就被内联成字面量**，`data-theme` / CSS 变量 / 媒体查询都影响不到它们 → 运行时切主题不可能，必须先把 token 层从 SCSS 变量改成 CSS 自定义属性。

### 2.2 「主题」控件是假的（用户反馈的直接原因）

`frontend/src/views/Settings.vue:286-291`：

```html
<label>主题</label>
<select v-model="settingStore.settings.theme" class="input">
  <option value="dark">深色</option>
  <option value="light">浅色</option>
</select>
```

但：

1. `frontend/src/stores/setting.js:8-16` 的 `defaultSettings()` 只有 3 个字段：
   `permissionMode` / `streamResponse` / `defaultModel` —— **没有 `theme`**（也没有 `language` / `fontSize` / `autoSave` / `autoArchive`）。
2. 全项目 grep `settings.theme` 只有这一处**写**，没有任何地方**读**它（无 `applyTheme`、无 `data-theme`、无 class 切换）。
3. 由于 `setting.js:51-61` 对整个 `settings` 做了 `deep watch` 并落 localStorage，**选中的值会被持久化**：用户选了浅色 → 重启后下拉仍显示「浅色」，但界面还是深色。这会让人感觉「主题功能是坏的 / 根本没这功能」。

> 补充：`docs/frontend-design.md:606` 与 `:654` 本来就规划了「主题（深色/浅色）」和色彩系统，属于**设计了但没落地**。

### 2.3 同类「假控件」清单（`Settings.vue` 通用设置 tab）

| 行号 | 控件 | 绑定字段 | 问题 |
|------|------|----------|------|
| 286-291 | 主题下拉 | `settings.theme` | store 无此字段、无消费方 → 无效 |
| 293-298 | 语言下拉 | `settings.language` | 同上；且全项目无 i18n 实现 |
| 300-307 | 字体大小滑块 | `settings.fontSize` | 同上；`{{ settings.fontSize }}px` 会渲染成 `px`（undefined），且无 CSS 变量承接 |
| 309-314 | 自动保存 | `settings.autoSave` | store 无此字段，无消费方 |
| 315-320 | PR 合并后自动归档 | `settings.autoArchive` | 同上 |
| 321-327 | 流式输出 | `settings.streamResponse` | ✅ **正常**（store 有、ChatPane 有消费） |
| 328-338 | 默认模型 | `settings.defaultModel` | ✅ **正常** |

### 2.4 死按钮清单（点击无任何反应）

| # | 位置 | 控件 | 原因 |
|---|------|------|------|
| 1 | `components/layout/TopBar.vue:107-112` | 「使用量」图标按钮 | **完全没有 `@click`**，但带 hover 态，强诱导点击 |
| 2 | `panes/ChatPane.vue:633-638` | 输入框工具栏「附件」图标 | 无 `@click` |
| 3 | `panes/ChatPane.vue:639` | 「@提及文件」 | 无 `@click` |
| 4 | `panes/ChatPane.vue:640-646` | 「更多」图标 | 无 `@click` |
| 5 | `panes/PreviewPane.vue:13-17` | 工具栏「刷新」 | 无 `@click` |
| 6 | `components/layout/PaneHeader.vue:26-30` | **面板标题栏的 ✕ 关闭按钮** | 组件 `emit('close')`，但 `DiffPane.vue:141`、`FileEditorPane.vue:46`、`PlanPane.vue:159`、`PreviewPane.vue:10`、`SubagentPane.vue:161`、`TasksPane.vue:164`、`TerminalPane.vue:35` **全都没有监听 `@close`** → 7 个面板的 ✕ 全是死的 |
| 7 | `panes/SubagentPane.vue:163` / `ChatPane` 其余按钮 | — | ✅ 已核对，其余按钮均有Handler；PaneContainer tab 上的 ✕（`PaneContainer.vue:91`）正常 |

> 说明：`PaneHeader` 只有 ChatPane 用了 `:closable="false"`（`ChatPane.vue:576`）显式隐藏，其余面板保留了默认 `closable=true`，所以 ✕ 看得见、点不动。
> 这一条最符合用户说的「对话框上的按钮不可用」——面板头 + 各类弹窗的关闭/取消类按钮在视觉上属于同一族。

### 2.5 演示壳（占位）面板

| 面板 | 现状 | 证据 |
|------|------|------|
| `TerminalPane.vue` | 假终端：`runCommand()` 只 `push('(模拟执行: xx)')`，初始输出是硬编码的 `npm run dev` 日志 | `TerminalPane.vue:7-24` |
| `FileEditorPane.vue` | 内容是硬编码的 Wails 模板 `app.go`，`save()` 只把 `modified=false`，不落盘 | `FileEditorPane.vue:5-41` |
| `PreviewPane.vue` | 连 iframe 都没有，纯占位文案；URL 输入框 `readonly` | `PreviewPane.vue:19-26` |

**并且这四个面板（`terminal` / `file-editor` / `preview` / `tasks`）没有任何入口能打开**：
`paneStore.openPane()` 全项目仅被以 `'plan'`（`ChatPane.vue:284`）、`'subagent'`（`ChatPane.vue:328`）、`'diff'`（`ChatPane.vue:394,569`）调用；`PaneContainer.vue:58-67` 却注册了 8 个面板。
→ 属于"看得见代码、用户到不了、真到了也是假的"三重浪费，且会误导评审者以为功能已完成。

其它死代码：`stores/pane.js:46-52` 的 `getLayout` / `setLayout` 定义后无人调用。
死资源：`assets/fonts/nunito-v16-latin-regular.woff2` + `OFL.txt`（全项目无 `@font-face`）、`assets/images/logo-universal.png`（无引用）。

### 2.6 视觉一致性

**a. 两套色板混用。** Dracula 是事实标准（`variables.scss`），但组件里残留大量早期 Tailwind 色板：

- Dracula：`#bd93f9` `#50fa7b` `#ffb86c` `#ff5555` `#8be9fd` `#ff79c6` `#f1fa8c` `#6272a4` `#f8f8f2` `#282a36` `#44475a`
- 残留 Tailwind：`#7c3aed` `#10b981` `#ef4444` `#f59e0b` `#94a3b8` `#38bdf8` `#22c55e` `#facc15` `#c084fc` `#a78bfa`

分布：**60+ 处硬编码 hex、约 38 处硬编码 `rgba()`，横跨 14 个文件**。最集中的是
`SkillSettings.vue`（11 处 rgba + 12 处 hex）、`RequestPreviewDialog.vue`（5 处 rgba + 6 处 hex）、`DiffPane.vue`、`SubagentPane.vue`、`ToolEditDialog.vue`、`PermissionDialog.vue`。
典型症状：`RequestPreviewDialog.vue:351-360` 用 `#94a3b8/#38bdf8/#a78bfa/#22c55e`（冷色系）画消息角色标签，而同一个对话框的 `:275` 又用 `#c084fc`（Dracula 紫系）——同一个弹窗里两套色。

**b. 焦点环颜色错误。** `global.scss:191`：`box-shadow: 0 0 0 2px rgba(124, 58, 237, 0.2)` —— 这是 Tailwind 的 `#7c3aed`，与主题主色 `#bd93f9` 不是同一个紫，全局输入框聚焦时色相跳变。

**c. 遮罩层透明度不统一。** `0.45`（AskUserDialog:130、McpConfigDialog:166）、`0.55`（RequestPreviewDialog:187、SkillEditDialog:234、PermissionDialog:154）、`0.6`（NewSessionDialog:306）三种并存。

**d. z-index 无秩序（纯手写魔数）：**
`PermissionDialog:150 = 200` → `AskUserDialog:134 = 900` → `NewSessionDialog:310 = 1000` → `UiDialogHost:106 = 1200` → `toast:156 = 1300`（另有 `Settings.vue` 的 `.modal-overlay`）。
没有常量表；权限弹窗（200）会被新建会话弹窗（1000）盖住。

**e. 弹窗结构四份重复。** `NewSessionDialog` / `SkillEditDialog` / `SkillInstallDialog` / `ToolEditDialog` / `McpConfigDialog` / `TaskFormDialog` / `AskUserDialog` / `PermissionDialog` / `RequestPreviewDialog` 各自复制了一套 `.dialog-mask / .dialog / .dialog-header / .dialog-body / .dialog-footer`，导致圆角（`$radius-md` vs `$radius-lg`）、头尾内边距、遮罩色、关闭按钮样式全都不一致。`UiDialogHost` 是唯一"集中式"的实现（且质量最好），却没有被复用。

**f. 图标风格混用三套：** 手写内联 SVG（TopBar / ChatPane / PaneHeader）、`lucide-vue-next`（SkillSettings / DiffPane / UiDialogHost）、emoji（`🔧` ToolCallCard:88、`🔒` PermissionDialog:65、`💬` AskUserDialog:64、`🌐` PreviewPane:21、`📄` DiffPane:185）。emoji 在不同平台的字形/尺寸差异会直接破坏对齐。

**g. 无障碍与细节缺失：**
- 无 `:focus-visible` 样式 → 键盘 Tab 时看不出焦点在哪（`button` 全局 `outline:none` 且未补偿）。
- 无 `prefers-reduced-motion` 兜底（`global.scss:231` 的 `pulse` 动画、各处 `transition`）。
- 无 `color-scheme` 声明 → 一旦做浅色主题，原生 `<select>` 下拉、滚动条、`range`、`checkbox` 会保持深色/白底黑字，与界面割裂。
- `index.html` 无 `data-theme` 且无内联背景色 → 冷启动时 CSS 未加载完会**白闪**一下。

### 2.7 弹窗挂载位置与事件收口不一致

`App.vue:14-23` 在应用根部订阅了 `permission_request` / `ask_user` 事件并写入 store（注释明确写「必须在应用根部收口」），
但真正渲染弹窗的 `PermissionDialog` / `AskUserDialog` / `RequestPreviewDialog` 却挂在 `ChatPane.vue:579-588`。

目前恰好能工作（`PaneContainer` 常驻渲染 ChatPane），但：
- 用户在**设置页**（`/settings`）时，ChatPane 未渲染 → 权限请求弹不出来（后端在工具循环里阻塞等待，只能等超时按拒绝处理）。
- 层级也不对：设置页里弹出的权限框（z-index 200）可能被其它层盖住。

**结论：应把这三个弹窗迁到 `App.vue` / `UiDialogHost`**，与事件收口位置对齐。

---

## 三、优化方案

### P0 · 主题系统落地（前置：token 层 CSS 变量化）

**目标**：`设置 → 通用设置 → 主题` 能真正切换深/浅色，且能跟随系统。

1. **新建 token 层** `frontend/src/assets/styles/tokens.css`（或 `.scss`）：

   ```css
   :root,
   :root[data-theme='dark'] {
     --color-bg-primary: #282a36;
     --color-bg-secondary: #21222c;
     /* ... 全量迁移 variables.scss 的 40 个 token ... */
     --color-primary-rgb: 189 147 249;   /* 供 color-mix / rgb() 拼透明用 */
     --shadow-md: 0 4px 12px rgb(0 0 0 / 0.4);
     color-scheme: dark;
   }

   :root[data-theme='light'] {
     --color-bg-primary: #ffffff;
     --color-bg-secondary: #f6f6f9;
     /* ... 浅色对齐 ... */
     --color-primary-rgb: 124 58 237;
     color-scheme: light;
   }
   ```

2. **`variables.scss` 改成薄映射层**，保持 30+ 个组件的 `@use` 语句与 `$color-xxx` 写法**一个字都不用改**：

   ```scss
   $color-bg-primary: var(--color-bg-primary);
   $color-primary: var(--color-primary);
   /* ... */
   ```

   注意两处**特例**（不能直接 `var()` 化）：
   - `PlanPane.vue:293,297,301,305,428,482` 用了 SCSS 颜色函数 `rgba($color-warning, 0.12)` 等，共 6 处。
     改为 `color-mix(in srgb, var(--color-warning) 12%, transparent)`（WebView2 / WKWebView 均支持），或提供 `--color-warning-rgb` 后用 `rgb(var(--color-warning-rgb) / 0.12)`。
     全项目仅这 6 处，迁移量可控（已核实：`rgba($` / `lighten(` / `darken(` / `mix(` 无其它命中）。

3. **`stores/setting.js` 补齐字段**并在字段变更时应用：
   ```js
   function defaultSettings() {
     return { ...,
       theme: 'system',        // 'dark' | 'light' | 'system'
       fontSize: 14,
     }
   }
   ```
   `main.js` 在 `app.mount()` **之前**调用 `applyTheme()`（避免白闪）；`applyTheme()` 把 `document.documentElement.dataset.theme` 设为 `dark|light`（`system` 时按 `matchMedia('(prefers-color-scheme: light)')` 求值），并监听该 media query 的变化。

4. **`index.html`** `<html lang="zh-CN" data-theme="dark">` + 内联 `style="background:#282a36"`，消除冷启动白闪。

5. **同步 Wails 原生窗口主题**（`frontend/wailsjs/runtime/runtime.d.ts:100-110` 已导出）：
   `WindowSetDarkTheme()` / `WindowSetLightTheme()`，让窗口标题栏、原生菜单跟界面一致。

6. **设置页主题选择器升级**为三选一（深色 / 浅色 / 跟随系统）。
7. （可选）**TopBar 加主题快捷切换按钮** —— 顺手把 `TopBar.vue:107` 那个死的「使用量」按钮位用起来。

**验收**：切换主题后**不刷新页面**即全局变色；刷新/重启后保持；<kbd>Tab</kbd> 焦点环、滚动条、下拉框、range 在浅色下均正常；深/浅两套文本对比度 ≥ WCAG AA（正文 ≥ 4.5:1）。

### P1 · 消除硬编码颜色，收敛到 token

- 新增语义 token：`--color-diff-add-bg` / `--color-diff-del-bg` / `--color-role-system` / `--color-role-tool` / `--color-warn-bg` 等。
- 把 2.6(a) 的 60+ 处 hex、38 处 rgba 全部替换为 token 或 `color-mix()`。优先级：`SkillSettings` > `RequestPreviewDialog` > `DiffPane` > `SubagentPane` > `ToolEditDialog` > `Permission*`。
- 修 `global.scss:191` 焦点环 → `rgb(var(--color-primary-rgb) / 0.25)`。
- 加一条 **stylelint 规则**（或 `scripts/check-tokens.mjs`）禁止在 `.vue` 里出现裸 `#hex` / `rgba(`，防回归。

**验收**：grep `.vue` 中裸色值命中数为 0；深浅两主题下无「颜色突兀」的区块。

### P2 · 按钮与控件修复（用户可感知最强）

**a. 死按钮：要么接线，要么移除**（原则：不留诱导点击的装饰）

| 位置 | 建议 |
|------|------|
| `TopBar.vue:107` 使用量 | 优先实现（接 usage 数据）；短期改为 `disabled` + `title="暂未开放"`。**推荐与 P0 的同位置主题切换按钮合并**：`TopBar` 右区改为 [主题切换] [使用量(禁用)] [设置] |
| `ChatPane.vue:633` 附件 | 后端无附件能力 → **移除**，或 `disabled` + tooltip |
| `ChatPane.vue:639` @提及文件 | 同上 |
| `ChatPane.vue:640` 更多 | 同上 |
| `PreviewPane.vue:13` 刷新 | 接线（`iframe.contentWindow.location.reload()`）；若 P4 决定移除该面板则一并删掉 |
| `PaneHeader` ✕ ×7 | 给每个面板补 `@close="paneStore.closePane(sessionId, '<type>')"`；或统一在 `PaneContainer` 传 `@close`。**建议后者**：`<component :is="..." :key="activePane" @close="closePane(activePane)" />`，一处修复 7 个 |

**b. 假控件：要么实现，要么删掉**

- `theme` → P0 实现。
- `fontSize` → 接入 CSS 变量 `--font-size-base`（`html { font-size: var(--font-size-base) }`，把 `$font-size-md` 等改成 `em/rem` 派生），实现成本低、收益直观，建议实现。
- `autoSave` / `autoArchive` → 后端目前无对应语义，**建议先移除控件**（或加「未实现」标记），避免假功能。
- `language` → 无 i18n 基建，**建议先移除**，等有 i18n 需求再做。

**c. 死代码清理**：`stores/pane.js` 的 `getLayout`/`setLayout`；`assets/fonts/nunito-*.woff2` + `OFL.txt`；确认无用后删 `assets/images/logo-universal.png`。

**验收**：`ChatPane` / `TopBar` / 各面板工具栏上**不存在点了没反应的按钮**；每个按钮的 `title` 与实际行为一致。

### P3 · 视觉一致性打磨

1. **抽公共弹窗基础样式**（新增 `assets/styles/dialog.scss`，与既有 `global.scss` 的 `.btn` / `.input` 风格一致）：
   `.ui-overlay` / `.ui-dialog` / `.ui-dialog__header` / `__body` / `__footer` / `__close`，
   并用 `UiDialogHost.vue` 的实现作为基准（它是目前最规范的一份）。
   把 9 个弹窗的重复样式替换掉 —— 这会**同时解决**遮罩透明度、圆角、内边距、关闭按钮四处不一致。
2. **z-index 常量化**：`--z-pane: 10` / `--z-dialog: 1000` / `--z-dialog-critical: 1200` / `--z-toast: 1300`，全部引用变量，禁止再写魔数。
3. **图标统一为 `lucide-vue-next`**（已在依赖里），移除 5 处 emoji（`🔧🔒💬🌐📄`）。
4. **交互三态统一**：`hover` / `active` / `disabled` 在 `.btn` 基类里一次写全；`.btn-ghost` 目前缺 `active` 态。
5. **无障碍补齐**：全局 `:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 1px }`；`@media (prefers-reduced-motion: reduce)` 关闭 `pulse` 与过渡。
6. **统一空状态 / 加载态 / 错误态**：抽 `EmptyState.vue`（图标 + 主文案 + 次文案），替换各 pane 里 DIY 的（`DiffPane.vue:216-224`、`SkillSettings`、`ToolSettings`、`SubagentPane` 目前各写一套）。加载统一用 `Loader2 + spin`（现在有的是"加载中..."纯文字，有的是 spinner）。
7. **滚动条浅色适配**：`:root[data-theme='light'] ::-webkit-scrollbar-thumb` 用浅色 token。

**验收**：同一类元素（按钮/输入/弹窗/空状态）在任意两个页面出现时外观一致；键盘遍历全界面焦点可见。

### P4 · 演示壳面板：接线或摘除

对 `TerminalPane` / `FileEditorPane` / `PreviewPane` / `TasksPane` 三选一：

- **A. 接线**（工作量大）：终端接后端 `exec_shell` 流式输出、文件编辑器接 `read_file`/`write_file`、预览接本地 dev server URL。
- **B. 摘除**（推荐先做）：从 `PaneContainer.vue:58-67` 的 `paneComponents` 与 `types/index.js:304` 的 `PANE_TITLES` 中移除这 3 个，删掉文件。反正**没有任何入口能打开它们**，留着只会让评审者误判完成度。
- **C. 保留但降级**：加明确的「预览版·未接通后端」角标 + 在面板内说明。

**建议**：先执行 B（清理），等 P4 有明确需求再按 A 重新实现。

---

## 四、实施顺序与工作量估算

| 阶段 | 内容 | 预估 | 依赖 |
|------|------|------|------|
| P0 | token 层 CSS 变量化 + theme store + 设置页主题选择 + Wails 原生主题 | 1.5 天 | 无 |
| P2a | 死按钮接线/移除（含 PaneHeader 一处修 7 个） | 0.5 天 | 无 |
| P2b | 假控件收敛（fontSize 实现；autoSave/autoArchive/language 移除） | 0.5 天 | P0 |
| P1 | 60+ 硬编码色 → token + 回归检查脚本 | 1.5 天 | P0 |
| P3 | 弹窗基础样式抽取 + z-index 常量 + 图标/状态统一 + a11y | 2 天 | P1 |
| P4 | 演示壳面板摘除 | 0.5 天 | 无 |

建议 **P0 + P2a 一起先合入**（约 2 天），能一次性消除用户提出的两个问题（没主题、按钮没反应），且改动内聚、风险低。

---

## 五、风险与注意事项

1. **CSS 变量在 Wails WebView 的支持**：WebView2 / WKWebView / WebKitGTK 均为现代内核，CSS 变量与 `color-mix()` 支持没有问题；`color-mix()` 若担心旧内核，可退回 `--color-xxx-rgb` + `rgb(var(--x) / a)` 写法。
2. **SCSS 变量 → CSS 变量的语义变化**：SCSS 变量可用于编译期计算（颜色函数、数学运算），CSS 变量不行。已定位受影响处只有 6 个（`PlanPane`），其余都是直接赋值，迁移安全。
3. **浅色主题的对比度需要单独设计**，不能简单反相：Dracula 的 `#ffb86c`（橙）、`#f1fa8c`（黄）在白底上对比度不足，浅色套需要重新取值（建议浅色用 `#B45309` / `#A16207` 一类的深色调）。
4. **`Settings.vue` 是 892 行的单文件**，通用设置与模型配置混在一起。P1/P3 期间如果要动它，建议顺手把「模型配置」拆成 `ModelSettings.vue`（与 `ToolSettings` / `SkillSettings` / `PermissionSettings` / `TaskSettings` 保持一致的结构）。
5. **改动前请先确认 `frontend/dist/` 是否纳入版本管理**：该目录里有构建产物（`settings.theme` 的老代码也在其中），若纳入仓库，每次构建都会产生噪声 diff。

---

## 六、待确认问题（需产品/用户拍板）

1. 「弹窗上不可用的按钮」具体指哪一个？（本方案已覆盖"面板标题栏 ✕ 全失效"这一族最可能的问题，但需确认是否另有其处）
2. 主题范围：只做「深色 / 浅色」两套，还是「+ 跟随系统」，还是「多套预置主题（Dracula / One Dark / GitHub）+ 主色可调」？
3. `autoSave` / `autoArchive` / `language` 三个无后端语义的控件：**移除**还是**保留占位**？
4. Terminal / File Editor / Preview 三个演示壳面板：**摘除**还是**保留并标注未完成**？
5. 是否需要在顶部栏提供主题快捷切换入口？

### 已确认的答复（2026-09-19）

| 问题 | 结论 |
|------|------|
| 「弹窗上不可用的按钮」 | 聊天输入框工具栏的「附件 / @提及文件 / 更多」；**以及模型的删除按钮** |
| 主题范围 | 深色 + 浅色 + **跟随系统** |
| 假控件 | 主题、字体大小**实现**；语言、自动保存、PR 合并后自动归档**移除** |
| 推进方式 | 先做 **P0（主题系统）+ P2a（按钮修复）** |

> ⚠️ 关于「模型的删除按钮」：已核查，`Settings.vue:450` 的删除按钮 → `handleDelete()`
> （`Settings.vue:107`）→ `ui.ask()` 应用内确认框 → `deleteModel()` → Go 侧 `App.DeleteModel`
> （`app.go:154`），链路完整，且 `ui.ask()` 的实现是正确的（`stores/ui.js:49`，会 resolve）。
> 因此**未在代码中发现它失效的原因**，需要用户补充复现信息（见第七节待办）。

---

## 七、实施记录（P0 + P2a，2026-09-19）

### 已完成

**P0 主题系统（真正可用）**

| 文件 | 改动 |
|------|------|
| `assets/styles/tokens.css` | **新增**。主题 token 层，`dark` / `light` 两套完整配色（含 `color-scheme`、语义软色、代码高亮色、阴影、遮罩、`*-rgb` 分量） |
| `assets/styles/variables.scss` | 由「颜色常量」改写为「`$scss变量 → var(--token)` 映射壳」。颜色 + 字号/行高全部 token 化，30+ 个组件与 `global.scss` **零改动** |
| `utils/theme.js` | **新增**。`applyTheme` / `applyFontSize` / `watchSystemTheme` / `resolveTheme`，并同步 Wails 原生窗口主题 |
| `stores/setting.js` | 补齐 `theme`、`fontSize` 字段（含脏数据归一化）；新增 `setTheme` / `setFontSize` / `applyAppearance`；对二者加了**真正生效**的 watch |
| `main.js` | 引入 `tokens.css`（须在 `global.scss` 前）；mount 前调用 `applyAppearance()` |
| `index.html` | `<html data-theme="dark">` + 内联启动脚本（首帧前应用已保存主题，避免白闪）+ 兜底背景色 |
| `views/Settings.vue` | 主题改为三选一（深色 / 浅色 / 跟随系统）+ 显示当前实际生效值；移除 3 个假控件（语言 / 自动保存 / PR 自动归档）；字号滑块真正生效 |
| `components/layout/TopBar.vue` | 顶部栏新增主题快捷切换（深↔浅），占用原先「使用量」死按钮的位置 |

**P2a 按钮修复**

| 位置 | 问题 | 处理 |
|------|------|------|
| `PaneHeader.vue` | ✕ 关闭只 `emit('close')`，7 个面板都没监听 → **7 个面板的 ✕ 全是死的** | 改为由 PaneHeader 自己从 pane store 关闭（一处修好全部，且新增面板不会再漏） |
| `TopBar.vue` | 「使用量」按钮无 `@click` | 替换为可用的主题切换按钮 |
| `ChatPane.vue` | 「附件 / @提及文件 / 更多」三个按钮无 `@click` | 移除（后端尚无对应能力），避免诱导点击 |
| `PreviewPane.vue` | 「刷新」按钮无 `@click` | 移除（预览区尚未接入 iframe，无可刷新内容） |

**顺手修的架构问题**

| 位置 | 问题 | 处理 |
|------|------|------|
| `App.vue` / `ChatPane.vue` | `PermissionDialog` / `AskUserDialog` 挂在 ChatPane 上，但事件在 `App.vue` 根部收口 → 在**设置页**时权限请求弹不出来，后端只能等超时按拒绝处理 | 两个弹窗上移到 `App.vue`，与事件收口同层 |

**P1/P3 中顺手完成的部分**

- 60+ 处硬编码 hex、38 处 `rgba()` 全部替换为 token（`ToolProcess` / `ToolSettings` / `UiDialogHost` / `SessionSidebar` / `ChatPane` / `DiffPane` / `PlanPane` / `SubagentPane` / `Settings`，以及协作完成的 `SkillSettings` / `RequestPreviewDialog` / `PermissionSettings` / `PermissionDialog` / `MessageBubble` / `ToolEditDialog` / `ToolCallDialog` / `NewSessionDialog` / `SkillEditDialog` / `SkillInstallDialog` / `AskUserDialog` / `McpConfigDialog` / `TaskFormDialog` 等）。
  现在只剩 3 处有意保留：`TerminalPane` 的 `#0d0d14`（终端永远深色）、`PreviewPane` 的画布色、`ChatPane` 的一处黑色投影。
- 弹窗遮罩（原先 3 种透明度）统一为 `$color-overlay`；弹窗阴影统一为 `$shadow-lg` / `$shadow-md`。
- 修掉 `global.scss` 全局输入框焦点环的错误颜色（原先用的是 Tailwind 的 `#7c3aed`，与主题主色 `#bd93f9` 不是同一个紫）。
- 新增 `:focus-visible` 焦点环（原先全局 `outline:none` 且无补偿，键盘用户看不出焦点）。
- 新增 `prefers-reduced-motion` 兜底。
- 滚动条滑块改用 `$color-border-light` + hover `$color-text-muted`，浅色下可见。

### 验证

- `npx vite build` 通过；主题两套配色均进入产物（基础 CSS 9.07 kB → 15.22 kB）。
- 写了临时脚本扫描全部 `.vue` 模板，确认**不存在无事件绑定的 `<button>`**（脚本已删除）。

### 仍未做（P1 收尾 / P3 / P4）

- 弹窗基础样式抽取（9 份重复的 `.dialog-mask/.dialog/...`）、z-index 常量化、emoji 图标统一为 lucide、空态/加载态组件化。
- Terminal / File Editor / Preview 三个演示壳面板的处理（目前仍无入口可打开）。
- `stores/pane.js` 的 `getLayout` / `setLayout` 死代码；`assets/fonts/nunito-*.woff2`、`assets/images/logo-universal.png` 未引用资源。
- 回归防护脚本（禁止 `.vue` 里出现裸 `#hex` / `rgba()`）。

### 待补充信息

「模型删除按钮」失效 —— 代码链路已核查完整，未复现。若仍失败，请提供：
1. 是否有会话正在引用该模型（后端会拒绝删除并返回「有 N 个会话正在使用该模型」，这是一条 error toast）；
2. 删除确认框是否弹出、点「删除」后是否有任何提示；
3. 是否只发生在浏览器 `npm run dev` 的 mock 模式（`api/model.js` 的 mock 分支）。

