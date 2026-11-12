# Tasks

## 1. Token 与度量层

- [x] 1.1 `frontend/src/assets/styles/tokens.css`：深色主题 `--color-text-muted: #6272a4 → #a3aacf`、`--color-text-muted-rgb: 98 114 164 → 163 170 207`。验证：复算 `#282a36` 为 6.24:1、`#21222c` 为 6.92:1
- [x] 1.2 `frontend/src/assets/styles/tokens.css`：浅色主题 `--color-text-muted: #7c8296 → #5f6678`、`--color-text-muted-rgb: 124 130 150 → 95 102 120`。验证：复算三档背景 5.74 / 5.23 / 4.63，均 ≥4.5
- [x] 1.3 `frontend/src/assets/styles/tokens.css`：在 `:root` 基础块新增 `--measure-prose: 72ch`、`--measure-column: calc(72ch + 76px)`（主题无关，与 `--font-size-base` 同处）。列宽中的 76px = 头像 32 + 间隙 12 + 左右内边距 32
- [x] 1.4 `frontend/src/assets/styles/variables.scss`：新增 `$measure-prose` / `$measure-column` 映射与 `$line-height-prose: 1.7`。验证：`npm run build` 无 Sass 未定义变量告警

## 2. 聊天正文排版度量

- [x] 2.1 `components/business/MessageBubble.vue`：正文改为 `$font-size-md`（14px）+ `$line-height-prose`（1.7）。实现方式为文件末尾统一维护的「正文排版度量」规则，覆盖上文旧值（构建产物已确认后者生效）
- [x] 2.2 `components/business/MessageBubble.vue`：`.message` 增加 `max-width: $measure-column` 与 `margin-inline: auto`。构建产物出现 `max-width:var(--measure-column)`
- [x] 2.3 `panes/ChatPane.vue`：`.process-row` 施加同样的 `max-width` + `margin-inline: auto`，与消息行左边缘对齐（构建产物第 2 处 `max-width:var(--measure-column)`）
- [x] 2.4 未改动 `pre` / `table` 的溢出策略，代码块按自身横向滚动，不被行宽上限裁切

## 3. 界面信息密度

- [x] 3.1 `components/layout/SessionSidebar.vue`：删除 `.session-meta` 中渲染状态词的 `<span>` 与三处分隔符 `·`；`运行中` / `待应答` 芯片保留；同时把 `run-dot` / `wait-dot` 改为仅折叠态显示，避免与芯片重复表达同一状态
- [x] 3.2 `panes/ChatPane.vue`：placeholder 收敛为「输入消息…」；`prompt-footer` 新增 `.prompt-hint` 常驻说明行（12px、`text-muted`、落在 `$glass-bg` 上：浅色 5.74:1 / 深色 6.24:1）

## 4. 验证与收尾

- [x] 4.1 `npm run build` 通过（vite 7.3.6，3.17s，1880 modules，无 Sass 告警）
- [x] 4.2 对比度已按公式复算：浅色 5.74 / 5.23 / 4.63，深色 6.24 / 6.92（`bg-tertiary` 4.01 按 design §2.1 改用 `text-secondary`）
- [ ] 4.3 目视项：窗口 1024 / 1280 / 1600px 三档确认行宽恒定、消息列居中、窄窗口无横向滚动（需运行应用）
- [ ] 4.4 目视项：设置页字号基准调到极小/极大，确认正文行高比值恒为 1.7（需运行应用）
- [ ] 4.5 目视项：深/浅两主题下核对空状态说明、输入区说明行、侧边栏元信息，确认 `text-muted` 与 `text-secondary` 层级仍可辨（需运行应用）
- [x] 4.6 `openspec validate ui-polish-readability --strict --json` 通过

## 5. 已知残留（待后续清理）

- [ ] 5.1 `MessageBubble.vue` 原 `.message-content` 块内仍留有 `font-size: $font-size-sm` 与 `line-height: $line-height-lg` 两条声明，被文件末尾的同名规则覆盖。因该文件为 CRLF 换行、无法用精确多行替换删除，暂以「后置覆盖」实现；后续可随手清理这两行（不影响构建与运行）
- [ ] 5.2 `SessionSidebar.vue` 模板中因删除分隔符残留 3 处空白行（不影响渲染），可在后续格式化时一并清理
