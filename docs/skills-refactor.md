# Skills 标准对齐重构说明

> - 状态：已实施（已过一轮本地编译反馈并修复；仍有未核实项，见文末「验证状态」）
> - 取代：`docs/skills-implementation-plan.md`（第一版方案，已归档）
> - 对标：Agent Skills 开放标准（Claude Code / Codex 通用的 `SKILL.md` 形状）

## 一、重构范围

第一版把技能做成了「单根目录 + 手写 frontmatter + 模型按需 read_skill + 可选强制注入正文」。功能是通的，但有三处与标准不符：frontmatter 只支持单行 `key: value`（含冒号的 description 会写出非法 YAML）、没有安装通道（只能手工往目录里丢文件夹）、以及 `alwaysInject` 这个标准里不存在的字段——它让正文常驻 system prompt，恰好抵消了渐进式披露的意义。

这次重构做四件事：把 frontmatter 换成标准 YAML 并补齐校验；按标准字段实现调用控制；补齐 L3（技能自带资源）的受控读取；新增两条安装通道（本地文件夹/zip、Git 仓库）。

## 二、文件与职责

后端拆成四个文件，边界按「解析 / 存储 / 资源 / 安装」切分，避免再出现一个 `skills.go` 里既写 YAML 解析又跑 `git clone` 的局面。

```
skillparse.go      frontmatter 的解析、校验、序列化（唯一处理 YAML 的地方）
skills.go          SkillStore、状态持久化、L1 清单、显式调用解析、read_skill（L2）
skillresources.go  L3 资源枚举与受限读取、read_skill_file 工具
skillinstall.go    安装器：文件夹 / zip / Git，以及更新
```

前端相应新增 `components/business/SkillInstallDialog.vue`，并把 `SkillSettings.vue`、`SkillEditDialog.vue`、`api/skill.js`、`stores/skills.js` 改到新契约上。

## 三、SKILL.md 的标准字段

磁盘布局与标准一致，`SKILL.md` 是唯一必需文件：

```
~/.local-agent/skills/<id>/
├── SKILL.md            # frontmatter + 正文
├── scripts/            # 可选，脚本（L3，用 exec_shell 执行）
├── references/         # 可选，长文档（L3，用 read_skill_file 读取）
└── assets/             # 可选，模板素材（L3）
```

frontmatter 的字段分两类。`name` 与 `description` 是路由字段，只有它们会被注入系统提示；`description` 是模型判断「要不要用这个技能」的唯一依据，缺失即为阻断性错误。其余都是可选的展示与控制字段：`version`、`license`、`author`、`allowed-tools`、`disable-model-invocation`、`user-invocable`、`metadata`。

```yaml
---
name: pdf-report                        # 1–64 字符，建议 kebab-case
description: 从 CSV 生成 PDF 报表。当用户需要把表格数据导出为 PDF 时使用。   # ≤1024
version: 1.0.0
license: MIT
allowed-tools: bash, read_file          # 标量或序列都可
disable-model-invocation: false         # true=模型不得自动触发
user-invocable: true                    # 默认 true
metadata: { owner: team-reports }
---
```

解析用 `gopkg.in/yaml.v3`（该模块已在 `go.sum` 中，无需联网即可构建）。相比第一版的手写解析，现在能正确处理嵌套 `metadata`、块标量（`description: >` 多行）、序列形式的 `allowed-tools`、以及写成数字的 `version: 1.0`；序列化时 description 里含冒号或 `#` 也会被正确加引号——第一版会直接写出非法 YAML。

校验结果分两档，这个区分贯穿前后端：**阻断性错误**让技能不参与路由（界面标红），**告警**只提示（界面可展开、安装时可回传）。阻断的是缺 `description`、`description` 超 1024、`name` 超 64、frontmatter 不是合法 YAML、缺 `SKILL.md`。告警的有 `name` 与目录名不一致、`name` 不是 kebab-case、存在未识别的顶层字段、正文为空、正文超 256KB 会被截断、以及两个开关同时关闭导致技能不可达。

## 四、三级渐进式披露

L1 是常驻系统提示的清单，只有可自动触发技能的 ID 与 description。停用的、有阻断错误的、以及声明了 `disable-model-invocation` 的技能都不进清单。单条描述超过 400 字符会截断——标准上限是 1024，但常驻清单按技能数量放大，400 是 token 与路由准确度的折中。

L2 是模型判定相关后调 `read_skill(id)`，返回技能正文，并附上该技能自带资源的清单（路径、归类、大小）与技能目录绝对路径。这一条是本次披露机制里最实际的改进：第一版只给正文，模型得自己猜 `references/` 下有什么、或者先 `list_dir` 再猜路径；现在正文后直接跟着可用资源清单。

L3 新增 `read_skill_file(id, path)`，把「按需读取」从「让模型自己拼绝对路径调 read_file」收紧成只能读技能目录内的文件。三道闸分别拦绝对路径与 `..`、清理后的前缀越界、以及软链接指向目录外。二进制文件（前 8KB 含 NUL）与目录路径都会被拒绝，单个文件读取上限 256KB 并提示截断。`scripts/` 下的东西仍走 `exec_shell`——它是命令不是文本，`read_skill_file` 会对这个前缀直接报错并把模型引回 `exec_shell`。

## 五、安装

两条通道都先解到技能根目录下的临时目录（`.staging-<id>-*`，扫描时按 `.` 前缀跳过），校验通过后整目录 `rename` 到位——同盘内的 rename 是原子的，因此不会出现「技能目录里躺着半个技能」的状态。

本地文件夹安装时，如果指定目录自身含 `SKILL.md` 就装成一个技能；否则扫描它的直接子目录，装下其中所有含 `SKILL.md` 的技能。zip 走同一条路径，只是先解压，并在解压时逐条目校验：拒绝绝对路径、`..` 段、软链接条目，限制条目数（2000）与解压后总大小（64MB），跳过 `__MACOSX/`。

Git 安装用 `git clone --depth 1`（可指定 `--branch` 与仓库内子目录），失败信息原样回传。`GIT_TERMINAL_PROMPT=0` 是刻意设的：需要交互认证时宁可快速失败，也不要让界面挂住。安装后会记下来源（URL、ref、子目录）到 `skills_state.json`，界面上这类技能多一个「更新」按钮，重新拉取时先把旧目录挪到 `.backup-<id>`，替换成功再删备份，中途失败则回滚——旧版本不会因为一次失败的更新而丢失。

安装是「先校验全部、再落盘全部」的两段式：批量安装里只要有任一 ID 与既有技能冲突，就整体拒绝，不做半截安装。冲突判定有个例外——Git 来源可以覆盖同为 Git 来源的同名技能，这正是「更新」的语义。

## 六、显式调用与两个开关

`disable-model-invocation` 表示模型不得自动触发，只能由用户在消息开头写 `/技能名` 唤起。这种情况下正文直接进入本轮系统提示，不经模型路由——这正是该字段的配套语义：用不用这个技能由用户决定，模型不必再判断一次。前端输入框在输入 `/` 时会向上弹出可调用技能列表（Enter 选中、Esc 关闭）；补全只在「整段输入恰好是一个 `/命令`」时启用，一旦出现空格就认为用户在写正文，不再弹菜单。

`user-invocable: false` 则相反：不允许用户显式调用。两个开关同时关闭的技能无法被任何方式调用，扫描时给出告警。

会话级的技能白名单同样约束显式调用——本会话没开放的技能，不会被 `/名称` 绕过去。`read_skill` 排除 `disable-model-invocation` 的技能，但 `read_skill_file` 不排除：用户显式调用某技能后，正文里引用的资料仍要读得到。

## 七、与标准的三处有意偏离

`name` 与目录名不一致时只告警、不阻断。标准要求两者相同，但本项目的 ID 一律取目录名，`name` 充作展示名——这样既有的中文 `name` 技能（例如 `name: 示例技能`）不会被判死。界面上会提示不一致，ID 始终以目录名为准。

只保留用户级技能目录 `~/.local-agent/skills`，不做项目级与内置级。这是本轮的取舍：项目级需要与会话工作区绑定并按优先级覆盖，改动面比本轮目标大，等有实际需求再加。

`allowed-tools` 只解析与展示，尚未接入权限网关。要做成硬约束，需要把「当前生效的技能」传进工具循环并按声明过滤工具，这是一次跨 `runToolLoop` 与权限网关的改动。现在它只作为一行提示写进 `read_skill` 的返回值，界面上标注了这一点——不假装它已经生效。

## 八、接口变更

`App` 上技能相关的绑定变化如下，前端 `wailsjs` 已手工同步（下次 `wails dev`/`build` 会重新生成并覆盖）。

| 方法 | 变化 |
| --- | --- |
| `SaveSkill(id, draft)` | 签名改为接收 `SkillDraft`（frontmatter 字段 + 正文） |
| `SetSkillAlwaysInject` | 删除 |
| `InstallSkillFromFolder(path)` / `InstallSkillFromZip(path)` | 新增，返回 `[]SkillInstallResult` |
| `InstallSkillFromGit(url, ref, subdir)` | 新增 |
| `UpdateSkill(id)` | 新增，仅 Git 来源可用 |
| `ListSkillResources(id)` | 新增，界面展示 L3 内容 |
| `PickSkillFolder()` / `PickSkillZip()` | 新增，系统选择对话框 |

`SkillMeta` 去掉了 `alwaysInject` 与 `error`，改为 `errors[]` / `warnings[]`，并新增 `version`、`license`、`author`、`allowedTools`、`disableModelInvocation`、`userInvocable`、`resources[]`、`install`。旧的 `skills_state.json` 无需迁移：`alwaysInject` 字段会被忽略，`enabled` 照旧生效，残留条目的清理由扫描时自动完成。

## 九、验证状态

开发沙箱里没有 Go 工具链（`apt` 无 root、模块代理被 allowlist 拦掉），`go build` / `go test` / `gofmt` 都跑不了，因此本轮改动是靠静态手段加一轮用户本地编译反馈过的。**第一轮编译暴露了三个错误，均已修复**：

`skillparse.go` 与 `skills_test.go` 里各有一处**字符串字面量中混入了真实 BOM 字节**（`strings.TrimPrefix(text, "<BOM>")`）——Go 只允许 BOM 出现在文件开头，否则报 `invalid BOM in the middle of the file`。已改为 `"\ufeff"` 转义。

`SkillStore.Refresh()` 在重写 `skills.go` 时被漏掉，而 `app.go` 与 `skillinstall.go` 仍在调用它。已补回。

三个错误里有两点值得记下来，因为它们暴露了静态检查的盲区。一是**通过接收者调用的方法名不检查**：我原来的检查器只匹配裸函数调用 `name(`，`a.skillStore.Refresh()` 这种形式从缝里漏了过去；后来按接收者类型做了收紧版本，误报太多（`store` 在不同测试文件里是 `ModelStore`/`ToolStore`/`SkillStore`），最终改用**有界人工核对**：把我新写的 `SkillStore`、`SkillInstaller`、`ReadSkillTool`、`ReadSkillFileTool` 的方法集列出来，与它们上面的全部调用点逐一比对。二是「历史上调用过」不能替代「现在有声明」——按 git HEAD 的调用名建白名单是抓不到这类错误的。

同时补了一个**精确**的新检查：**结构体字面量的字段名**（`T{Field: ...}` 里的 `Field` 必须是 `T` 的字段，含内嵌提升）。这个检查只针对包内类型，误报极少，且拼错字段名是编译错误、静态检查里最容易漏的一类。本次覆盖 125 个包内结构体类型、1024 个 keyed 字段，全过。

其余静态检查（括号配平、未使用 import、跨文件重复声明、包内函数调用可解析）全过；其中 58 处「调用未找到定义」经逐条确认均为局部函数变量（`flush`、`cancel`、`markExposure` 等），非遗漏。

前端做了更实的验证：5 个改动过的 `.vue` 文件用 `@vue/compiler-sfc` 跑通了 parse、`compileScript`、`compileTemplate`，并用项目自带的 `sass` 编译了各自的 `<style lang="scss">`；`api/skill.js`、`stores/skills.js`、`types/index.js`、`wailsjs/go/main/App.js` 经 `node --check`（ESM）通过；`wailsjs/go/models.ts` 去除 TS 外壳后语法通过。

需要在本地补跑：

```bash
go mod tidy            # 把 gopkg.in/yaml.v3 固化为直接依赖（go.sum 已有哈希）
gofmt -w .
go build ./...
go test ./...
cd frontend && npm run dev
```

`gofmt -w .` 是必需的：沙箱里没有 gofmt，新写的结构体与复合字面量的列对齐是手工推算的，可能与 gofmt 有几列偏差（纯格式问题，不影响编译）。

`PickSkillZip()` 刻意**没有设置 `Filters`**。原计划加 `*.zip` 过滤，但 `wailsRuntime.FileFilter` 这个类型名在开发环境无法核实（本地模块缓存读不到 Wails 源码），而过滤只是体验优化——安装器自己会校验是不是合法 zip。第一轮编译已经失败过一次，不值得为这点收益再冒一次编译风险。确认该类型可用后可自行补上，代码就写在 `app.go` 的注释里。

剩下一个未核实项是 `gopkg.in/yaml.v3`：它在 `go.sum` 中有完整哈希（`h1:` 与 `/go.mod` 两行都在，说明原本就在依赖图里），但模块 zip 是否已在本地缓存无法确认；若 `go build` 报拉取失败，`go mod download gopkg.in/yaml.v3` 一次即可。
