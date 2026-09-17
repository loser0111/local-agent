package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ===== 权限管理：核心判定（纯函数为主，无 IO）=====
//
// 设计要点（详见 docs/permission-management-outline.md）：
//
//  1. Fail closed：Decision 的零值必须是 Ask。非法输入、解析不可信、超时、无人应答，
//     全部落到「询问」而不是「放行」。
//  2. 求值方向性：收紧方向（deny / ask）用「存在一段命中」，放宽方向（allow / 会话授权）
//     必须「每一段都被覆盖」。两者用不同函数实现，不允许共用——历史上把 allow 写成
//     「任一命中即放行」导致过静默放行的绕过漏洞。
//  3. 前缀规则只在逐段判定里生效，整行判定只接受精确匹配。
//  4. 判定主体归一化为 Subject，引擎不依赖具体工具实现。

// ===== 决策三态 =====

// Decision 权限判定结果。零值即 DecisionAsk（fail closed）。
type Decision int

const (
	// DecisionAsk 询问用户。**零值**：任何未显式赋值的路径都落到这里。
	DecisionAsk Decision = iota
	// DecisionAllow 放行。
	DecisionAllow
	// DecisionDeny 拒绝。deny 不可被模式、规则或授权覆盖。
	DecisionDeny
)

// String 返回决策的稳定名称（用于审计与前端协议）
func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionDeny:
		return "deny"
	default:
		return "ask"
	}
}

// ParseDecision 解析前端回传的决策字符串；无法识别时返回 DecisionAsk 与 false。
// 注意：识别失败返回的是询问而非放行——调用方应把 false 当"未授权"处理。
func ParseDecision(s string) (Decision, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "allow":
		return DecisionAllow, true
	case "deny":
		return DecisionDeny, true
	case "ask":
		return DecisionAsk, true
	default:
		return DecisionAsk, false
	}
}

// ===== 判定主体 =====

// SubjectKind 判定主体的类别
type SubjectKind string

const (
	SubjectTool    SubjectKind = "tool"    // 普通工具（MCP / HTTP API 等）：主体是工具名 + 参数摘要
	SubjectCommand SubjectKind = "command" // 命令类工具（exec_shell / 动态 CLI）：主体是展开后的命令，逐段判定
	SubjectPath    SubjectKind = "path"    // 路径类操作（read_file / write_file / edit_file 等）：主体是目标路径
	SubjectMeta    SubjectKind = "meta"    // 只读元数据操作（tool_router 的 list / describe）：不判定
)

// 路径类主体的动作。读操作走只读放行；写操作按模式判定（manual 询问 / acceptEdits 放行 / plan 拒绝）。
const (
	SubjectActionRead  = "read"
	SubjectActionWrite = "write"
)

// Subject 归一化后的判定主体。引擎只认这个结构，不认具体工具实现。
type Subject struct {
	Tool    string      // 工具名
	Kind    SubjectKind // 类别
	Action  string      // 路径类主体的动作：read / write（其余类别为空）
	Units   []string    // 待判定的段：命令类逐段，路径类为单个绝对路径，其余为参数摘要
	Raw     string      // 原始文本（展示与审计用）
	Trusted bool        // 解析是否可信；false 一律询问（fail closed）
}

// Summary 返回用于展示与审计的单行摘要
func (s Subject) Summary() string {
	if strings.TrimSpace(s.Raw) != "" {
		return strings.TrimSpace(s.Raw)
	}
	if len(s.Units) > 0 {
		return strings.Join(s.Units, " ; ")
	}
	return s.Tool
}

// SubjectProvider 由工具自行声明判定主体。模板类工具（动态 CLI）把参数展开成具体
// 命令后返回，使判定发生在真正要执行的命令上，而不是工具名上。
type SubjectProvider interface {
	PermissionSubject(args map[string]interface{}) Subject
}

// newCommandSubject 构造命令类主体：分解命令，解析不可信时标记 Trusted=false。
func newCommandSubject(tool, cmd string) Subject {
	units, trusted := AnalyzeCommand(cmd)
	return Subject{
		Tool:    tool,
		Kind:    SubjectCommand,
		Units:   units,
		Raw:     cmd,
		Trusted: trusted,
	}
}

// newToolSubject 构造普通工具主体（参数摘要作为单一段）
func newToolSubject(tool string, args map[string]interface{}) Subject {
	return newRawSubject(tool, compactArgs(args))
}

// newPathSubject 构造路径类主体（read_file / write_file / edit_file / glob / grep / list_dir）。
// path 必须是解析后的绝对路径，判定时不再做 cwd 推理。
func newPathSubject(tool, action, path string) Subject {
	return Subject{
		Tool:    tool,
		Kind:    SubjectPath,
		Action:  action,
		Units:   []string{path},
		Raw:     path,
		Trusted: true,
	}
}

// newRawSubject 用一段文本作为普通工具主体（适用于无法分解成命令的调用：MCP / HTTP API）
func newRawSubject(tool, raw string) Subject {
	return Subject{
		Tool:    tool,
		Kind:    SubjectTool,
		Units:   []string{raw},
		Raw:     raw,
		Trusted: true,
	}
}

// compactArgs 把工具参数压成单行摘要（长度受限，避免审计与事件过大）
func compactArgs(args map[string]interface{}) string {
	if len(args) == 0 {
		return "（无参数）"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sortStrings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, args[k]))
	}
	out := strings.Join(parts, " ")
	if len(out) > 400 {
		out = out[:400] + "…"
	}
	return out
}

// sortStrings 简单插入排序：避免为几行摘要引入 sort 包的额外依赖面
func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// ===== 命令分解与解析可信度 =====

// AnalyzeCommand 把一个命令行拆成独立判定单元，并给出解析是否可信。
//
// 拆分规则：按 && || ; | & 与换行分段（引号内的分隔符不拆）；命令替换 $(...) 与
// 反引号的内容各自作为独立单元抽出（只看 `$(rm -rf /)` 时不能因为它在替换里就漏判）。
// 单元顺序与源码顺序一致：替换前的内容、替换内容、替换后的内容依次成段。
//
// 解析不可信（trusted=false）的情形：引号未闭合、命令替换未闭合。此时一律询问。
func AnalyzeCommand(cmd string) (units []string, trusted bool) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil, false
	}

	runes := []rune(cmd)
	var (
		cur      strings.Builder
		quote    rune // 0 = 无引号，否则为 ' 或 "
		escaped  bool
		badSubst bool // 看起来是命令替换但没闭合
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			units = append(units, s)
		}
		cur.Reset()
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}

		if quote != 0 {
			// 双引号内仍会展开命令替换，必须抽出；单引号内一切原样
			if quote == '"' && (r == '$' || r == '`') {
				if sub, next, ok := extractSubstitution(runes, i); ok {
					// 先把替换之前的部分收成一段（保持源码顺序，且不把 $ 带进单元），
					// 替换内容自成一段
					flush()
					units = append(units, sub)
					i = next
					continue
				}
				if r == '`' || (r == '$' && i+1 < len(runes) && runes[i+1] == '(') {
					badSubst = true
				}
			}
			if r == '\\' && quote == '"' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
			continue
		}

		switch r {
		case '\\':
			escaped = true
		case '\'', '"':
			quote = r
			cur.WriteRune(r)
		case '$', '`':
			if sub, next, ok := extractSubstitution(runes, i); ok {
				// 同上：替换前的内容先成段，替换内容自成一段
				flush()
				units = append(units, sub)
				i = next
				continue
			}
			if r == '`' || (r == '$' && i+1 < len(runes) && runes[i+1] == '(') {
				badSubst = true
			}
			cur.WriteRune(r)
		case ';', '\n':
			flush()
		case '&', '|':
			flush()
			if i+1 < len(runes) && runes[i+1] == r {
				i++ // 吃掉 && 或 ||
			}
		default:
			cur.WriteRune(r)
		}
	}
	flush()

	if quote != 0 || escaped || badSubst {
		// 引号未闭合或替换不完整：分解结果不可信（判定会落到询问）
		return units, false
	}
	if len(units) == 0 {
		return nil, false
	}
	return units, true
}

// extractSubstitution 尝试从 runes[i] 处抽出一个命令替换的内容。
// 支持 $( ... )（含一层以上括号配对）与反引号。ok=false 表示不是或不完整。
func extractSubstitution(runes []rune, i int) (string, int, bool) {
	if i < 0 || i >= len(runes) {
		return "", i, false
	}

	if runes[i] == '`' {
		for j := i + 1; j < len(runes); j++ {
			if runes[j] == '`' {
				inner := strings.TrimSpace(string(runes[i+1 : j]))
				if inner == "" {
					return "", i, false
				}
				return inner, j, true
			}
		}
		return "", i, false
	}

	if runes[i] != '$' || i+1 >= len(runes) || runes[i+1] != '(' {
		return "", i, false
	}
	depth := 0
	for j := i + 1; j < len(runes); j++ {
		switch runes[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				inner := strings.TrimSpace(string(runes[i+2 : j]))
				if inner == "" {
					return "", i, false
				}
				return inner, j, true
			}
		}
	}
	return "", i, false
}

// normalizeCommand 压平空白，供正则匹配使用
func normalizeCommand(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ===== 规则 =====

// Rule 一条权限规则，语法 `Tool(specifier)`：
//
//	exec_shell              工具级：该工具的任意调用
//	exec_shell(git status)  精确：命令段完全等于 "git status"
//	exec_shell(git:*)       前缀：命令段以 "git" 开头（**只在逐段判定里生效**）
//	*(git status)           工具名用 * 表示任意工具
type Rule struct {
	Raw      string `json:"raw"`                // 原始行（审计展示）
	Tool     string `json:"tool"`               // 工具名，* 表示任意
	Spec     string `json:"spec,omitempty"`     // 规格；空表示工具级
	IsPrefix bool   `json:"isPrefix,omitempty"` // true 表示 Spec 是前缀
	Source   string `json:"source,omitempty"`   // 规则来源（文件路径或 "会话授权"）
}

// IsToolLevel 是否为工具级规则（覆盖该工具的全部调用）
func (r Rule) IsToolLevel() bool { return r.Spec == "" }

// ParseRule 解析一行规则。ok=false 表示语法非法；调用方应把它当作"无法解析"处理，
// 不可静默忽略（fail closed：解析失败不得让规则消失）。
func ParseRule(line, source string) (Rule, bool) {
	raw := strings.TrimSpace(line)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return Rule{}, false
	}

	open := strings.Index(raw, "(")
	if open == -1 {
		if !validRuleTool(raw) {
			return Rule{}, false
		}
		return Rule{Raw: raw, Tool: raw, Source: source}, true
	}

	if !strings.HasSuffix(raw, ")") {
		return Rule{}, false
	}
	tool := strings.TrimSpace(raw[:open])
	spec := strings.TrimSpace(raw[open+1 : len(raw)-1])
	if !validRuleTool(tool) || spec == "" {
		return Rule{}, false
	}
	// 括号嵌套不支持（易写歧义）
	if strings.ContainsAny(spec, "()") {
		return Rule{}, false
	}

	isPrefix := strings.HasSuffix(spec, ":*")
	if isPrefix {
		spec = strings.TrimSpace(strings.TrimSuffix(spec, ":*"))
		if spec == "" {
			return Rule{}, false
		}
	}
	return Rule{Raw: raw, Tool: tool, Spec: spec, IsPrefix: isPrefix, Source: source}, true
}

// validRuleTool 校验工具名：字母数字下划线连字符，或通配 *
func validRuleTool(tool string) bool {
	if tool == "*" {
		return true
	}
	if tool == "" {
		return false
	}
	for _, r := range tool {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// FormatRule 把规则还原成文本形式
func FormatRule(r Rule) string {
	if r.Tool == "" {
		return r.Raw
	}
	if r.IsToolLevel() {
		return r.Tool
	}
	if r.IsPrefix {
		return fmt.Sprintf("%s(%s:*)", r.Tool, r.Spec)
	}
	return fmt.Sprintf("%s(%s)", r.Tool, r.Spec)
}

// matchesUnit 单条规则是否覆盖某一段。
// 注意：前缀比较只在这里（逐段）发生，整行判定不会走这条路径。
func (r Rule) matchesUnit(tool, unit string) bool {
	if r.Tool != "*" && r.Tool != tool {
		return false
	}
	if r.IsToolLevel() {
		return true // 工具级规则：覆盖该工具的全部段
	}
	if r.IsPrefix {
		return strings.HasPrefix(unit, r.Spec)
	}
	return unit == r.Spec
}

// RuleSet 三桶规则。deny 优先于 ask，ask 优先于 allow。
type RuleSet struct {
	Mode  string `json:"mode,omitempty"` // 配置层建议的默认模式（新建会话时采用；空表示不指定）
	Deny  []Rule `json:"deny,omitempty"`
	Ask   []Rule `json:"ask,omitempty"`
	Allow []Rule `json:"allow,omitempty"`
}

// Empty 是否没有任何规则
func (rs *RuleSet) Empty() bool {
	if rs == nil {
		return true
	}
	return len(rs.Deny) == 0 && len(rs.Ask) == 0 && len(rs.Allow) == 0
}

// AllRules 返回全部规则的扁平列表（供界面展示）
func (rs *RuleSet) AllRules() []Rule {
	if rs == nil {
		return nil
	}
	out := make([]Rule, 0, len(rs.Deny)+len(rs.Ask)+len(rs.Allow))
	out = append(out, rs.Deny...)
	out = append(out, rs.Ask...)
	out = append(out, rs.Allow...)
	return out
}

// matchAnyUnit 收紧方向（deny / ask）：**存在**一段命中即成立。
// 这个方向保守，可用于判定"整条命令里有没有危险的一段"。
func matchAnyUnit(rules []Rule, s Subject) (Rule, bool) {
	for _, r := range rules {
		for _, u := range s.Units {
			if r.matchesUnit(s.Tool, u) {
				return r, true
			}
		}
	}
	return Rule{}, false
}

// coversAllUnits 放宽方向（allow / 会话授权）：**每一段**都必须被覆盖才成立。
//
// 这是历史漏洞的修复点：放宽方向若沿用 matchAnyUnit，授权过 `grep:*` 之后
// `rm -rf /tmp/x && grep -n y file.go` 会因为 grep 段命中而整条放行。
func coversAllUnits(rules []Rule, s Subject) (Rule, bool) {
	if len(s.Units) == 0 || len(rules) == 0 {
		return Rule{}, false
	}
	var (
		first Rule
		set   bool
	)
	for _, u := range s.Units {
		covered := false
		for _, r := range rules {
			if r.matchesUnit(s.Tool, u) {
				if !set {
					first = r
					set = true
				}
				covered = true
				break
			}
		}
		if !covered {
			return Rule{}, false
		}
	}
	return first, true
}

// ===== 内置 deny：灾难性不可逆操作 =====
//
// 只放"一旦执行就无法挽回、且几乎不可能是正常开发流程"的操作。
// 「敏感但可恢复」的（.env、私钥、强制推送）放在 sensitive 里询问，不在这里拒绝——
// 防误读与防破坏是两件事。
var builtinDenyPatterns = []*regexp.Regexp{
	// rm 递归删除「根 / 家目录 / 当前目录整体」。
	// 这一条用「任意边界」形式：sudo rm -rf / 也必须拒绝（deny 优先于询问）。
	// 路径分支必须覆盖带斜杠与通配的写法——曾经漏掉 `~/` 导致 rm -rf ~/ 被放行。
	regexp.MustCompile(`(?i)(^|[\s;&|])rm\s+(-[a-zA-Z]+\s+)*-[a-zA-Z]*[rf][a-zA-Z]*\s+(-[a-zA-Z]+\s+)*` +
		`(/|~|\$HOME|\$\{HOME\}|/home|/usr|/etc|/var|/bin|/sbin|/lib|/lib64|/opt|/boot|/root|/System|/Applications|\.\.?|\*)` +
		`(?:/)?(?:\*)?\s*$`),
	// 覆盖块设备：重定向可能出现在命令中段（cat x > /dev/sda），故不锚定开头
	regexp.MustCompile(`(?i)>\s*/dev/(sd|nvme|hd|vd|disk)`),
	regexp.MustCompile(`(?i)(^|[\s;&|])dd\b[^;&|]*\bof=/dev/(sd|nvme|hd|vd|disk)`),
	// 格式化（不锚定开头：sudo mkfs 也要拦）
	regexp.MustCompile(`(?i)(^|[\s;&|])mkfs(\.[a-z0-9]+)?(\s|$)`),
	regexp.MustCompile(`(?i)(^|[\s;&|])(fdisk|parted|mkswap)\s`),
	// 关机 / 重启。锚定段首：避免 "echo shutdown" 这类文本被误判；
	// 带 sudo 的写法会落进敏感名单被询问，不会静默放行
	regexp.MustCompile(`(?i)^(shutdown|reboot|halt|poweroff)(\s|$)`),
	// fork 炸弹
	regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`),
	// 把根目录权限改烂（同上：锚定段首，sudo 前缀由敏感名单兜住）
	regexp.MustCompile(`(?i)^chmod\s+(-[a-zA-Z]+\s+)*[0-7]{3,4}\s+/\s*$`),
	regexp.MustCompile(`(?i)^chown\s+(-[a-zA-Z]+\s+)*[^\s]+\s+/\s*$`),
}

// matchBuiltinDeny 命中内置拒绝名单。收紧方向：整行或任意一段命中都算。
func matchBuiltinDeny(s Subject) (string, bool) {
	candidates := make([]string, 0, len(s.Units)+1)
	candidates = append(candidates, s.Raw)
	candidates = append(candidates, s.Units...)
	for _, c := range candidates {
		n := normalizeCommand(c)
		if n == "" {
			continue
		}
		for _, p := range builtinDenyPatterns {
			if p.MatchString(n) {
				return p.String(), true
			}
		}
	}
	return "", false
}

// ===== 敏感内容：需要显式询问，但不拒绝 =====
var sensitivePatterns = []*regexp.Regexp{
	// 敏感文件：凭据与环境变量
	regexp.MustCompile(`(^|[\s/='"])\.env(\.[a-zA-Z0-9]+)?($|[\s'";|&>)])`),
	regexp.MustCompile(`(^|[\s/=])\.ssh/`),
	regexp.MustCompile(`\bid_(rsa|dsa|ecdsa|ed25519)\b`),
	regexp.MustCompile(`\.aws/credentials`),
	regexp.MustCompile(`\.git-credentials`),
	regexp.MustCompile(`(^|[\s/])\.netrc($|\s)`),
	regexp.MustCompile(`\bcredentials\.json\b`),
	regexp.MustCompile(`\.pem($|[\s'";|&>)])`),
	// 管道给 shell：curl ... | sh 这类"下载即执行"
	regexp.MustCompile(`\|\s*(sudo\s+)?(sh|bash|zsh|dash)(\s|$)`),
	// 强制推送（可覆盖远端历史）
	regexp.MustCompile(`(^|[\s;&|])git\s+push\b[^;&|]*--force`),
	// 提权
	regexp.MustCompile(`(^|[\s;&|])(sudo|doas|su)(\s|$)`),
}

// matchSensitive 命中敏感内容。整行与逐段都判（管道给 shell 需要看整行）。
func matchSensitive(s Subject) (string, bool) {
	candidates := make([]string, 0, len(s.Units)+1)
	candidates = append(candidates, s.Raw)
	candidates = append(candidates, s.Units...)
	for _, c := range candidates {
		n := normalizeCommand(c)
		if n == "" {
			continue
		}
		for _, p := range sensitivePatterns {
			if p.MatchString(n) {
				return p.String(), true
			}
		}
	}
	return "", false
}

// ===== 只读白名单：只读不询问 =====
//
// 命名的命令必须有裸名（不含 / 或 .），避免 /bin/ls、./ls.sh 同名伪装。
var readOnlyCommands = map[string]bool{
	// 目录与元信息
	"cd": true, "pwd": true, "ls": true, "tree": true, "stat": true, "file": true,
	"du": true, "df": true, "basename": true, "dirname": true, "realpath": true, "readlink": true,
	// 内容查看与文本处理
	"cat": true, "head": true, "tail": true, "less": true, "more": true, "wc": true,
	"grep": true, "rg": true, "fgrep": true, "egrep": true, "sort": true, "uniq": true,
	"cut": true, "tr": true, "nl": true, "diff": true, "comm": true, "cmp": true,
	"column": true, "md5sum": true, "sha1sum": true, "sha256sum": true, "cksum": true,
	// 环境与查询
	"which": true, "whereis": true, "whoami": true, "id": true, "hostname": true,
	"uname": true, "date": true, "uptime": true, "printenv": true, "echo": true,
	"printf": true, "man": true, "type": true, "test": true, "true": true, "false": true,
	// 需额外判定
	"find": true, "git": true,
}

// readOnlyGitSubcommands 只认明确只读的 git 子命令。
// 刻意不含 branch / tag / remote / config / stash —— 它们都带写模式。
var readOnlyGitSubcommands = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "rev-parse": true,
	"describe": true, "shortlog": true, "blame": true, "ls-files": true, "ls-tree": true,
	"cat-file": true, "grep": true, "reflog": true, "name-rev": true, "merge-base": true,
	"cherry": true, "count-objects": true, "whatchanged": true, "annotate": true,
	"symbolic-ref": true, "show-ref": true, "for-each-ref": true,
	"verify-commit": true, "verify-tag": true, "version": true,
}

// riskyCommands auto 模式下必须询问的命令：副作用不可预测，或能拉取/执行任意代码。
// 刻意不收进只读白名单，因此它们在 manual 模式下也必然询问。
var riskyCommands = map[string]bool{
	// 网络
	"curl": true, "wget": true, "nc": true, "ncat": true, "netcat": true, "telnet": true,
	"ssh": true, "scp": true, "sftp": true, "rsync": true, "socat": true, "ftp": true,
	// 包管理与构建（可拉取并执行远程代码）
	"npm": true, "npx": true, "pnpm": true, "yarn": true, "pip": true, "pip3": true,
	"gem": true, "cargo": true, "go": true, "make": true, "cmake": true,
	"docker": true, "kubectl": true, "brew": true, "apt": true, "apt-get": true, "yum": true,
	// 解释器（等价于执行任意代码）
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"python": true, "python3": true, "node": true, "nodejs": true, "perl": true,
	"ruby": true, "php": true, "lua": true, "awk": true, "sed": true, "eval": true, "source": true,
	// 提权
	"sudo": true, "doas": true, "su": true,
	// 删除与破坏
	"rm": true, "rmdir": true, "shred": true, "truncate": true, "dd": true,
	"mkfs": true, "fdisk": true, "parted": true,
	// 包装器（把真实命令藏起来）
	"xargs": true, "env": true, "nice": true, "nohup": true, "timeout": true,
	"time": true, "watch": true, "stdbuf": true, "setsid": true, "command": true,
}

// isReadOnlyUnit 判定单个命令段是否只读
func isReadOnlyUnit(unit string) bool {
	u := strings.TrimSpace(unit)
	if u == "" {
		return false
	}
	// 重定向、未解析变量、反引号、反斜杠转义：一律不认（保守）
	if strings.ContainsAny(u, "><") || strings.Contains(u, "$") || strings.Contains(u, "`") || strings.Contains(u, "\\") {
		return false
	}
	fields := strings.Fields(u)
	if len(fields) == 0 {
		return false
	}
	name := fields[0]
	if strings.ContainsAny(name, "/.") {
		return false // 必须是裸名
	}
	if !readOnlyCommands[name] {
		return false
	}

	args := fields[1:]
	switch name {
	case "find":
		for _, f := range args {
			switch f {
			case "-exec", "-execdir", "-ok", "-okdir", "-delete",
				"-fprint", "-fprint0", "-fprintf", "-fls":
				return false
			}
		}
	case "git":
		if len(args) == 0 || !readOnlyGitSubcommands[args[0]] {
			return false
		}
	}
	// 选项级写副作用
	for _, f := range args {
		if f == "--output" || strings.HasPrefix(f, "--output=") {
			return false
		}
	}
	switch name {
	case "sort":
		if hasFlag(args, "-o") {
			return false
		}
	case "date":
		if hasFlag(args, "-s") || hasFlag(args, "--set") {
			return false
		}
	case "hostname":
		if len(args) > 0 {
			return false // 带参数会改主机名
		}
	case "uniq":
		if countNonFlag(args) > 1 {
			return false // 第二个位置参数是输出文件
		}
	}
	return true
}

// hasFlag 参数列表中是否存在该选项（支持 --opt 与 --opt=value 形式）
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// countNonFlag 统计非选项的位置参数个数
func countNonFlag(args []string) int {
	n := 0
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			n++
		}
	}
	return n
}

// isReadOnlyCommand 整条命令是否只读：每一段都必须只读
func isReadOnlyCommand(units []string) bool {
	if len(units) == 0 {
		return false
	}
	for _, u := range units {
		if !isReadOnlyUnit(u) {
			return false
		}
	}
	return true
}

// readOnlyTools 内置无副作用工具：直接放行（不弹窗）
var readOnlyTools = map[string]bool{
	"read_skill": true,
	// ask_user 只与用户交互，不碰文件系统也不执行命令；它本身就是"问用户"，
	// 再叠一层权限弹窗只会变成连续两个弹窗，且没有任何安全收益。
	toolAskUser: true,
}

// isReadOnlySubject 判定主体是否只读。
// 注意：只读「默认放行」排在 ask 规则与会话授权之后，因此显式的用户意图优先于它。
func isReadOnlySubject(s Subject) bool {
	switch s.Kind {
	case SubjectMeta:
		return true
	case SubjectCommand:
		return isReadOnlyCommand(s.Units)
	case SubjectPath:
		// 读文件/搜索不改变状态 → 只读；写操作不是
		return s.Action == SubjectActionRead
	case SubjectTool:
		return readOnlyTools[s.Tool]
	default:
		return false
	}
}

// ===== 模式 =====

// Mode 会话权限模式。只调节默认松紧，不能突破 deny。
//
// 两处需要留意的语义：
//   - ModeAcceptEdits：本项目当前没有独立的文件编辑工具（编辑经 exec_shell 完成），
//     因此该模式对命令的处理与 manual 一致，待 write_file / edit_file 落地后收敛。
//   - ModeAuto：本项目没有 AI 安全分类器，无法判断命令"想干什么"，
//     只能判断它"碰得到哪里"，故按作用域（是否在项目目录内）而非意图定义。
type Mode string

const (
	ModeManual      Mode = "manual"      // 一切写操作与命令都需确认（只读白名单除外）
	ModeAcceptEdits Mode = "acceptEdits" // 文件编辑类自动放行、命令仍需确认
	ModePlan        Mode = "plan"        // 只读探索：写操作直接拒绝
	ModeAuto        Mode = "auto"        // 仅项目目录内的写操作自动放行，越界一律询问
)

// ValidMode 校验模式取值
func ValidMode(m Mode) bool {
	switch m {
	case ModeManual, ModeAcceptEdits, ModePlan, ModeAuto:
		return true
	default:
		return false
	}
}

// NormalizeMode 归一化模式：空值与非法值一律回退到最严格的 manual（fail closed）
func NormalizeMode(s string) Mode {
	m := Mode(strings.TrimSpace(s))
	if ValidMode(m) {
		return m
	}
	return ModeManual
}

// ===== 管线阶段名（审计与展示）=====

const (
	StageInvalid     = "invalid-subject"  // 主体不可判定
	StageMeta        = "meta-allowed"     // 只读元数据，直接放行
	StageBuiltinDeny = "builtin-deny"     // 内置拒绝名单
	StageUntrusted   = "untrusted-parse"  // 命令解析不可信
	StagePlanMode    = "plan-mode"        // plan 模式只读约束
	StageDenyRule    = "deny-rule"        // 用户 deny 规则
	StageSensitive   = "sensitive"        // 敏感内容（凭据/管道给 shell/提权）
	StageAskRule     = "ask-rule"         // 用户 ask 规则
	StageGrant       = "session-grant"    // 会话授权
	StageAllowRule   = "allow-rule"       // 用户 allow 规则
	StageAutoMode    = "auto-mode"        // auto 模式作用域判定
	StageAcceptEdits = "accept-edits"     // acceptEdits 模式：项目内的文件编辑自动放行
	StageDefault     = "mode-default"     // 模式默认
	StageReadOnly    = "readonly-default" // 只读默认放行
	StageFallback    = "fallback-ask"     // 兜底询问
)

// Verdict 一次判定的结果
type Verdict struct {
	Decision Decision `json:"decision"`
	Stage    string   `json:"stage"`
	Reason   string   `json:"reason"`
	Rule     Rule     `json:"rule,omitempty"`
}

// AuthorizeInput 判定输入。全部显式传入，便于表驱动单测。
type AuthorizeInput struct {
	Subject    Subject
	Mode       Mode
	Rules      *RuleSet
	Grants     []Grant
	ProjectDir string
}

// Authorize 决策管线（纯函数：无 IO、无锁、无时间依赖）。
//
// 顺序即优先级：deny（内置 → 规则）> plan 模式约束 > 敏感内容 > ask 规则 >
// 会话授权 > allow 规则 > 模式默认 > 只读默认 > 兜底询问。
func Authorize(in AuthorizeInput) Verdict {
	s := in.Subject
	mode := in.Mode
	if !ValidMode(mode) {
		mode = ModeManual
	}

	// 1. 主体不可判定：保守询问（工具名为空说明解包失败）
	if strings.TrimSpace(s.Tool) == "" {
		return Verdict{Decision: DecisionAsk, Stage: StageInvalid, Reason: "无法确定工具名，保守询问"}
	}

	// 2. 只读元数据（tool_router 的 list / describe）：不判定，不产生审计噪音
	if s.Kind == SubjectMeta {
		return Verdict{Decision: DecisionAllow, Stage: StageMeta, Reason: "只读元数据操作"}
	}

	// 3. 内置 deny：不可被模式、规则、授权覆盖
	if pat, hit := matchBuiltinDeny(s); hit {
		return Verdict{
			Decision: DecisionDeny,
			Stage:    StageBuiltinDeny,
			Reason:   "命中内置拒绝名单（灾难性不可逆操作）",
			Rule:     Rule{Raw: pat, Source: "内置"},
		}
	}

	// 4. 解析不可信：命令分解失败（引号未闭合 / 替换未闭合）一律询问
	if !s.Trusted {
		return Verdict{Decision: DecisionAsk, Stage: StageUntrusted, Reason: "命令解析不可信，保守询问"}
	}

	// 5. plan 模式：非只读操作直接拒绝
	if mode == ModePlan && !isReadOnlySubject(s) {
		return Verdict{Decision: DecisionDeny, Stage: StagePlanMode, Reason: "plan 模式只允许只读操作"}
	}

	// 6. 用户 deny 规则（收紧方向：存在一段命中）
	if in.Rules != nil {
		if r, hit := matchAnyUnit(in.Rules.Deny, s); hit {
			return Verdict{Decision: DecisionDeny, Stage: StageDenyRule, Reason: "命中 deny 规则", Rule: r}
		}
	}

	// 7. 敏感内容：凭据文件、管道给 shell、提权、强制推送 → 询问。
	//    排在只读默认之前，因此 `cat .env` 不会因为 cat 只读而被静默放行。
	if pat, hit := matchSensitive(s); hit {
		return Verdict{Decision: DecisionAsk, Stage: StageSensitive, Reason: "涉及敏感内容，需确认", Rule: Rule{Raw: pat, Source: "内置"}}
	}

	// 8. 用户 ask 规则（收紧方向）
	if in.Rules != nil {
		if r, hit := matchAnyUnit(in.Rules.Ask, s); hit {
			return Verdict{Decision: DecisionAsk, Stage: StageAskRule, Reason: "命中 ask 规则", Rule: r}
		}
	}

	// 9. 会话授权（放宽方向：每一段都被覆盖）
	if r, hit := grantsCoverAll(in.Grants, s); hit {
		return Verdict{Decision: DecisionAllow, Stage: StageGrant, Reason: "本会话已授权", Rule: r}
	}

	// 10. 用户 allow 规则（放宽方向：每一段都被覆盖）
	if in.Rules != nil {
		if r, hit := coversAllUnits(in.Rules.Allow, s); hit {
			return Verdict{Decision: DecisionAllow, Stage: StageAllowRule, Reason: "命中 allow 规则", Rule: r}
		}
	}

	// 11. 模式默认
	if mode == ModeAuto && autoAllows(s, in.ProjectDir) {
		return Verdict{Decision: DecisionAllow, Stage: StageAutoMode, Reason: "auto 模式：操作范围在项目目录内"}
	}
	// acceptEdits：项目内的文件编辑自动放行——这正是这个模式存在的意义。
	// 排在敏感内容（第 7 步）之后，因此改 .env 之类仍会询问。
	if mode == ModeAcceptEdits && s.Kind == SubjectPath && s.Action == SubjectActionWrite && pathWithinProject(s, in.ProjectDir) {
		return Verdict{Decision: DecisionAllow, Stage: StageAcceptEdits, Reason: "acceptEdits 模式：项目内文件编辑自动放行"}
	}

	// 12. 只读默认放行（内置只读工具、命令级只读白名单）
	if isReadOnlySubject(s) {
		return Verdict{Decision: DecisionAllow, Stage: StageReadOnly, Reason: "只读操作，无需确认"}
	}

	// 13. 兜底：询问
	return Verdict{
		Decision: DecisionAsk,
		Stage:    StageFallback,
		Reason:   fmt.Sprintf("%s 模式下的写操作需要确认", string(mode)),
	}
}

// pathWithinProject 判定路径类主体是否落在项目目录内。
// 不能判定（目录未知、路径非绝对、含 .. 跳转、指向家目录）时一律返回 false（fail closed）。
func pathWithinProject(s Subject, projectDir string) bool {
	if len(s.Units) == 0 {
		return false
	}
	root := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(projectDir), "\\", "/"), "/")
	if root == "" {
		return false
	}
	p := strings.ReplaceAll(strings.TrimSpace(s.Units[0]), "\\", "/")
	if p == "" || !strings.HasPrefix(p, "/") {
		return false // 必须是绝对路径（路径类主体由工具解析后给出）
	}
	if p == "~" || strings.HasPrefix(p, "~/") || hasParentEscape(p) {
		return false
	}
	return p == root || strings.HasPrefix(p, root+"/")
}

// autoAllows auto 模式的放行判定：所有段都不是高风险命令，且命令中出现的绝对路径
// 都在项目目录内、不含上级跳转、不含 ~ 与变量引用。
//
// 之所以按「作用域」而不是「意图」定义：本项目没有 AI 安全分类器，
// 无法判断一条陌生命令"想干什么"，只能判断它"碰得到哪里"。
func autoAllows(s Subject, projectDir string) bool {
	// 路径类：只有「项目内的写操作」自动放行（读走只读默认；项目外一律询问）
	if s.Kind == SubjectPath {
		if s.Action != SubjectActionWrite {
			return false
		}
		return pathWithinProject(s, projectDir)
	}
	if s.Kind != SubjectCommand {
		return false
	}
	root := strings.TrimSpace(projectDir)
	if root == "" {
		return false // 目录未知则无法界定作用域
	}
	if len(s.Units) == 0 {
		return false
	}
	for _, u := range s.Units {
		fields := strings.Fields(u)
		if len(fields) == 0 {
			return false
		}
		name := fields[0]
		if strings.ContainsAny(name, "/.") {
			return false // 非裸名：/bin/rm、./script.sh 这类不计入放行
		}
		if riskyCommands[name] {
			return false
		}
		// git 的写子命令（push / fetch / clone / remote ...）会出网或改仓库状态，
		// 只读子命令之外的都不自动放行
		if name == "git" {
			args := fields[1:]
			if len(args) == 0 || !readOnlyGitSubcommands[args[0]] {
				return false
			}
		}
	}
	return allPathsWithinProject(s.Units, root)
}

// allPathsWithinProject 命令里出现的路径是否都在项目目录内。
// 只检查"看起来像路径"的 token：以 / 开头、以 ~ 开头、含 .. 跳转，以及 --flag=value 的值部分。
func allPathsWithinProject(units []string, projectDir string) bool {
	root := strings.TrimRight(strings.ReplaceAll(projectDir, "\\", "/"), "/")
	if root == "" {
		return false
	}
	prefix := root + "/"

	for _, u := range units {
		for _, f := range strings.Fields(u) {
			tok := strings.Trim(f, `"'`)
			tok = strings.TrimRight(tok, "|&;,")
			if tok == "" {
				continue
			}
			for _, cand := range pathCandidates(tok) {
				c := strings.ReplaceAll(cand, "\\", "/")
				switch {
				case c == "~" || strings.HasPrefix(c, "~/"):
					return false // 家目录在项目外
				case strings.HasPrefix(c, "/"):
					if c != root && !strings.HasPrefix(c, prefix) {
						return false
					}
				}
				if hasParentEscape(c) {
					return false // .. 跳出项目
				}
			}
		}
	}
	return true
}

// pathCandidates 从一个 token 里取出可能的路径值（兼容 --flag=/path 写法）
func pathCandidates(tok string) []string {
	if i := strings.Index(tok, "="); i >= 0 && i+1 < len(tok) {
		return []string{tok[i+1:], tok}
	}
	return []string{tok}
}

// hasParentEscape 路径是否含 .. 跳转
func hasParentEscape(tok string) bool {
	for _, seg := range strings.Split(tok, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// ===== 会话授权（仅内存，不落盘）=====

// Grant 一条会话授权。由用户在授权弹窗里选择「本会话允许」时写入。
type Grant struct {
	Tool      string `json:"tool"`
	Spec      string `json:"spec,omitempty"`
	IsPrefix  bool   `json:"isPrefix,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// Rule 把授权转换成规则（复用同一套匹配逻辑，方向由调用方决定）
func (g Grant) Rule() Rule {
	return Rule{Tool: g.Tool, Spec: g.Spec, IsPrefix: g.IsPrefix, Source: "会话授权"}
}

// grantsCoverAll 会话授权走**放宽方向**：每一段都必须被某条授权覆盖。
func grantsCoverAll(grants []Grant, s Subject) (Rule, bool) {
	if len(grants) == 0 {
		return Rule{}, false
	}
	rules := make([]Rule, 0, len(grants))
	for _, g := range grants {
		rules = append(rules, g.Rule())
	}
	return coversAllUnits(rules, s)
}

// GrantStore 会话授权存储（内存，进程退出即失效）
type GrantStore struct {
	mu     sync.RWMutex
	grants map[string][]Grant
}

// NewGrantStore 创建授权存储
func NewGrantStore() *GrantStore {
	return &GrantStore{grants: map[string][]Grant{}}
}

// Add 追加一条授权（同一规则不重复追加）
func (g *GrantStore) Add(sessionID string, grant Grant) {
	if g == nil || sessionID == "" {
		return
	}
	if grant.CreatedAt == 0 {
		grant.CreatedAt = time.Now().UnixMilli()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.grants == nil {
		g.grants = map[string][]Grant{}
	}
	for _, e := range g.grants[sessionID] {
		if e.Tool == grant.Tool && e.Spec == grant.Spec && e.IsPrefix == grant.IsPrefix {
			return
		}
	}
	g.grants[sessionID] = append(g.grants[sessionID], grant)
}

// List 返回某会话的全部授权（副本）
func (g *GrantStore) List(sessionID string) []Grant {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	src := g.grants[sessionID]
	out := make([]Grant, len(src))
	copy(out, src)
	return out
}

// Clear 清空某会话的授权
func (g *GrantStore) Clear(sessionID string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.grants, sessionID)
}

// ===== 审计 =====

// AuditEntry 一条判定记录
type AuditEntry struct {
	Time      int64  `json:"time"`
	SessionID string `json:"sessionId"`
	Tool      string `json:"tool"`
	Subject   string `json:"subject"`
	Decision  string `json:"decision"`
	Stage     string `json:"stage"`
	Reason    string `json:"reason"`
	Rule      string `json:"rule,omitempty"`
	Source    string `json:"source,omitempty"`
}

// AuditLog 会话级审计日志（环形缓冲，避免长会话无限增长）
type AuditLog struct {
	mu      sync.Mutex
	limit   int
	entries map[string][]AuditEntry
}

// NewAuditLog 创建审计日志；limit <= 0 时取默认值
func NewAuditLog(limit int) *AuditLog {
	if limit <= 0 {
		limit = 500
	}
	return &AuditLog{limit: limit, entries: map[string][]AuditEntry{}}
}

// Append 追加一条记录
func (l *AuditLog) Append(e AuditEntry) {
	if l == nil {
		return
	}
	if e.Time == 0 {
		e.Time = time.Now().UnixMilli()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = map[string][]AuditEntry{}
	}
	list := append(l.entries[e.SessionID], e)
	if len(list) > l.limit {
		list = list[len(list)-l.limit:]
	}
	l.entries[e.SessionID] = list
}

// List 返回某会话的审计记录（按时间正序）
func (l *AuditLog) List(sessionID string) []AuditEntry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	src := l.entries[sessionID]
	out := make([]AuditEntry, len(src))
	copy(out, src)
	return out
}

// Clear 清空某会话的审计记录
func (l *AuditLog) Clear(sessionID string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, sessionID)
}
