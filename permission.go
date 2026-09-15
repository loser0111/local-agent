package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ===== 权限管理核心（对标 Claude Code 的 allow/ask/deny 三层规则）=====
//
// 设计要点（详细论证见 docs/permission-management-plan.md）：
//  1. fail closed —— 任何不确定都降级为询问或拒绝，绝不静默放行
//  2. deny 绝对优先 —— 排在权限模式与会话授权之前，任何模式都无法覆盖
//  3. 决策与实现解耦 —— 规则只依赖 PermissionSubject，新增工具类型不改引擎
//
// 拦截点在 ToolManager.BuildView 装配期：把每个 ToolInterface 包一层 guardedTool。
// 之所以不在 chat.go 主循环插桩，是因为暴露给 LLM 的只有 tool_router，主循环那层
// 拿到的工具名恒为 "tool_router"，真实工具名藏在 args["tool_name"] 里。
// 装饰器包在真实工具上，天然拿到真实工具名与参数。

// ===== 决策三态 =====

// Decision 授权决策。
//
// 零值刻意等于 DecisionAsk：任何「忘记赋值」的代码路径都会走向询问而不是放行，
// 这是 fail closed 在类型层面的兜底。
type Decision int

const (
	DecisionAsk Decision = iota
	DecisionAllow
	DecisionDeny
)

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

// ===== 风险级别 =====

// RiskClass 操作风险分类，决定默认策略与权限模式的语义映射。
type RiskClass string

const (
	RiskRead    RiskClass = "read"    // 只读，默认放行（read_skill）
	RiskWrite   RiskClass = "write"   // 修改本机状态，默认询问（exec_shell、cli）
	RiskNetwork RiskClass = "network" // 出网，默认询问（api、远端 mcp）
	RiskProcess RiskClass = "process" // 拉起进程，默认询问（stdio mcp）
)

// ===== 权限模式 =====

type PermissionMode string

const (
	ModeDefault     PermissionMode = "default"
	ModeAcceptEdits PermissionMode = "acceptEdits"
	ModePlan        PermissionMode = "plan"
	ModeBypass      PermissionMode = "bypassPermissions"
)

// ValidPermissionMode 校验模式取值；非法值一律回退到最严格的 default
func ValidPermissionMode(m PermissionMode) bool {
	switch m {
	case ModeDefault, ModeAcceptEdits, ModePlan, ModeBypass:
		return true
	}
	return false
}

// ===== 判定对象 =====

// PermissionSubject 归一化后的待判定操作，是规则匹配的唯一输入。
// 引擎不认识任何具体工具，只认识这个结构。
type PermissionSubject struct {
	ToolID    string                 `json:"toolId"`
	ToolName  string                 `json:"toolName"` // 真实工具名（非 tool_router）
	ToolType  string                 `json:"toolType"` // builtin/cli/mcp/api
	ToolLabel string                 `json:"toolLabel"`
	Risk      RiskClass              `json:"risk"`
	Command   string                 `json:"command,omitempty"`   // builtin/cli：最终要执行的命令行
	Domain    string                 `json:"domain,omitempty"`    // api：目标 host
	SpecValue string                 `json:"specValue,omitempty"` // 通用匹配值（如技能 id）
	Raw       map[string]interface{} `json:"raw,omitempty"`       // 原始参数，供界面展示
}

// Specifiable 由「需要按内容匹配的工具」实现：把原始参数归一化为判定对象。
//
// 为什么必须有这个接口：装饰器只能拿到**未渲染的原始参数**，而 DynamicCLITool 的
// 命令行是 {{参数}} 模板在 Execute 内部才渲染出来的。如果由装饰器自己拼命令行，
// 就必须复刻一遍模板渲染逻辑 —— 一旦两边不一致，被规则匹配的字符串和实际执行的
// 命令就会分叉（规则放行了 A、实际执行 B），这是最危险的一类漏洞。
// 因此把「描述自己要执行什么」交回工具自己实现，保证 match 与 exec 同源。
type Specifiable interface {
	DescribeOperation(args map[string]interface{}) PermissionSubject
}

// ===== 规则 =====

// SpecKind 限定符的类型
type SpecKind string

const (
	SpecKindAny     SpecKind = ""        // 工具级：匹配该工具的全部调用
	SpecKindCommand SpecKind = "command" // 命令前缀 / 精确命令
	SpecKindDomain  SpecKind = "domain"  // 域名
)

// Rule 一条权限规则。
type Rule struct {
	Raw      string   `json:"raw"`      // 原文，用于界面回显
	Tool     string   `json:"tool"`     // 工具名部分
	Kind     SpecKind `json:"kind"`     // 限定符类型
	Spec     string   `json:"spec"`     // 限定符值（Kind 为空时忽略）
	IsPrefix bool     `json:"isPrefix"` // 仅命令类有效：前缀匹配还是精确匹配
	Source   string   `json:"source"`   // 来源：builtin/user/project/local
}

// ParseRule 解析规则文本。
//
// 语法：
//
//	exec_shell                     工具级，匹配该工具全部调用
//	exec_shell(git:*)              命令前缀，匹配以 "git" 开头（按词边界）
//	exec_shell(git status)         精确命令
//	my_api(domain:example.com)     域名
//	my_api(domain:*.example.com)   域名通配
func ParseRule(raw, source string) (Rule, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return Rule{}, fmt.Errorf("规则不能为空")
	}
	r := Rule{Raw: text, Source: source}

	open := strings.Index(text, "(")
	if open < 0 {
		// 工具级
		r.Tool = text
		if !validToolRef(r.Tool) {
			return Rule{}, fmt.Errorf("非法的工具名: %s", r.Tool)
		}
		return r, nil
	}
	if !strings.HasSuffix(text, ")") {
		return Rule{}, fmt.Errorf("规则括号未闭合: %s", text)
	}
	r.Tool = strings.TrimSpace(text[:open])
	inner := strings.TrimSpace(text[open+1 : len(text)-1])
	if !validToolRef(r.Tool) {
		return Rule{}, fmt.Errorf("非法的工具名: %s", r.Tool)
	}
	if inner == "" {
		return Rule{}, fmt.Errorf("规则的限定符为空: %s", text)
	}

	if rest, ok := strings.CutPrefix(inner, "domain:"); ok {
		r.Kind = SpecKindDomain
		r.Spec = strings.TrimSpace(rest)
		if r.Spec == "" {
			return Rule{}, fmt.Errorf("域名限定符为空: %s", text)
		}
		return r, nil
	}

	r.Kind = SpecKindCommand
	// ":*" 是「该前缀及其后任意内容」的惯用写法
	if rest, ok := strings.CutSuffix(inner, ":*"); ok {
		r.Spec = strings.TrimSpace(rest)
		r.IsPrefix = true
	} else if rest, ok := strings.CutSuffix(inner, "*"); ok && !strings.Contains(rest, " ") {
		// 形如 exec_shell(git*) 也按前缀处理；但保留命令里既有的 * 通配符
		r.Spec = strings.TrimSpace(rest)
		r.IsPrefix = true
	} else {
		r.Spec = inner
	}
	if r.Spec == "" {
		return Rule{}, fmt.Errorf("命令限定符为空: %s", text)
	}
	return r, nil
}

// validToolRef 校验工具名（MCP 子工具名含双下划线，这里只挡明显非法的字符）
func validToolRef(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '-' || r == '*' {
			continue
		}
		return false
	}
	return true
}

// RuleSet 三态规则集合
type RuleSet struct {
	Allow []Rule `json:"allow"`
	Ask   []Rule `json:"ask"`
	Deny  []Rule `json:"deny"`
}

// ParseRules 批量解析；返回成功解析的规则与错误描述列表（错误不阻断启动）
func ParseRules(raws []string, source string) ([]Rule, []string) {
	rules := make([]Rule, 0, len(raws))
	var errs []string
	for _, raw := range raws {
		r, err := ParseRule(raw, source)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		rules = append(rules, r)
	}
	return rules, errs
}

// mergeRules 跨层合并（列表取并集，保持来源标记）
func mergeRules(sets ...[]Rule) []Rule {
	var out []Rule
	seen := map[string]bool{}
	for _, set := range sets {
		for _, r := range set {
			key := r.Raw + "\x00" + r.Source
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, r)
		}
	}
	return out
}

// matchGenericSpec 非命令类工具的限定符匹配（如 read_skill(pdf-report)）
func matchGenericSpec(r Rule, value string) bool {
	if value == "" {
		return false
	}
	if r.IsPrefix {
		return strings.HasPrefix(value, r.Spec)
	}
	return value == r.Spec
}

// matchDomain 域名匹配。支持 *.example.com 通配（有意修复 Claude Code 不支持
// 域名通配的缺陷），且通配要求存在至少一级子域，避免 *.example.com 命中裸域。
func matchDomain(pattern, domain string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	domain = strings.ToLower(strings.TrimSpace(domain))
	if pattern == "" || domain == "" {
		return false
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		if !strings.HasSuffix(domain, suffix) {
			return false
		}
		// 必须真的多一级子域：a.example.com 命中，example.com 不命中
		return len(domain) > len(suffix)
	}
	return pattern == domain
}

// matchCommandPrefix 命令前缀匹配，且要求前缀结束于「名字边界」。
//
// 这是防止范围过宽的关键：规则 exec_shell(git:*) 应当匹配 "git status" 与 "git"，
// 但**不应**匹配 "github-cli x" 或 "git-cli x"。若只做 strings.HasPrefix，
// 后者会被 "git" 命中，等于把用户没批准的命令放进来了。
//
// 边界判定用「后续字符是否为命令名的继续字符」，而不是「是否为空白」：
//   - 继续字符（字母/数字/下划线/连字符/点）表示前缀只是一个更长名字的开头，不匹配
//   - 其余（空白、/、:、; 等）视为分隔符，匹配
//
// 这样 rm:* 不会命中 rmdir，git:* 不会命中 github-cli；
// 同时 rm -rf /tmp/keep:* 能命中 rm -rf /tmp/keep/x（路径前缀是常见玩法）。
func matchCommandPrefix(cmd, prefix string) bool {
	cmd = normalizeCommand(cmd)
	prefix = normalizeCommand(prefix)
	if !strings.HasPrefix(cmd, prefix) {
		return false
	}
	if len(cmd) == len(prefix) {
		return true
	}
	return !isNameChar(cmd[len(prefix)])
}

// isNameChar 判断字符是否属于「命令名/标识符」的继续字符
func isNameChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_' || c == '-' || c == '.':
		return true
	}
	return false
}

// normalizeCommand 折叠空白并去首尾，用于命令比较
func normalizeCommand(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// commandCandidates 返回用于匹配的命令候选：完整命令 + 分解后的每一段。
// 逐段匹配是为了挡住 "a && rm -rf /" 这类串联绕过。
func commandCandidates(sub PermissionSubject, a CommandAnalysis) []string {
	out := []string{normalizeCommand(sub.Command)}
	for _, seg := range a.Segments {
		n := normalizeCommand(seg)
		if n != "" && n != out[0] {
			out = append(out, n)
		}
	}
	return out
}

// matchAny 在候选命令上依次套用规则，返回命中的第一条
func matchAny(rules []Rule, sub PermissionSubject, candidates []string) (Rule, bool) {
	for _, r := range rules {
		if r.Tool != sub.ToolName {
			continue
		}
		if r.Kind == SpecKindAny {
			return r, true
		}
		if r.Kind == SpecKindDomain {
			if matchDomain(r.Spec, sub.Domain) {
				return r, true
			}
			continue
		}
		// 命令类：逐候选匹配。非命令类退化到通用取值
		if sub.Command == "" {
			if matchGenericSpec(r, sub.SpecValue) {
				return r, true
			}
			continue
		}
		for _, cand := range candidates {
			if r.IsPrefix {
				if matchCommandPrefix(cand, r.Spec) {
					return r, true
				}
			} else if cand == normalizeCommand(r.Spec) {
				return r, true
			}
		}
	}
	return Rule{}, false
}

// ===== 命令分解与替换检测 =====

// CommandAnalysis 命令解析结果
type CommandAnalysis struct {
	Segments        []string // 分解后的子命令
	HasSubstitution bool     // 含 $(...) 或反引号
	HasRedirection  bool     // 含 > >> < <<
	HasPipe         bool     // 含管道或换行
	Uncertain       bool     // 引号未闭合等，解析不可信
	Reason          string   // Uncertain 的原因
}

// AnalyzeCommand 保守地分解 shell 命令。
//
// 刻意**不追求 shell 语法完备**：遇到引号未闭合等不确定情况就标记 Uncertain，
// 由调用方降级为询问。追求「解析正确」而猜错，比承认不知道更危险。
func AnalyzeCommand(cmd string) CommandAnalysis {
	var (
		a       CommandAnalysis
		seg     strings.Builder
		segs    []string
		inSgl   bool
		inDbl   bool
		escaped bool
	)

	flush := func() {
		s := strings.TrimSpace(seg.String())
		if s != "" {
			segs = append(segs, s)
		}
		seg.Reset()
	}

	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if escaped {
			seg.WriteRune(c)
			escaped = false
			continue
		}
		switch c {
		case '\\':
			seg.WriteRune(c)
			escaped = true
			continue
		case '\'':
			if !inDbl {
				inSgl = !inSgl
			}
			seg.WriteRune(c)
			continue
		case '"':
			if !inSgl {
				inDbl = !inDbl
			}
			seg.WriteRune(c)
			continue
		case '`':
			if !inSgl {
				a.HasSubstitution = true
			}
			seg.WriteRune(c)
			continue
		case '$':
			if !inSgl && i+1 < len(runes) && runes[i+1] == '(' {
				a.HasSubstitution = true
			}
			seg.WriteRune(c)
			continue
		}

		if inSgl || inDbl {
			seg.WriteRune(c)
			continue
		}

		switch c {
		case '\n':
			a.HasPipe = true // 换行同样是命令分隔
			flush()
			continue
		case ';':
			flush()
			continue
		case '|':
			if i+1 < len(runes) && runes[i+1] == '|' {
				flush()
				i++
				continue
			}
			a.HasPipe = true
			flush()
			continue
		case '&':
			if i+1 < len(runes) && runes[i+1] == '&' {
				flush()
				i++
				continue
			}
			flush()
			continue
		case '>', '<':
			a.HasRedirection = true
			seg.WriteRune(c)
			continue
		}
		seg.WriteRune(c)
	}
	flush()

	if inSgl || inDbl {
		a.Uncertain = true
		a.Reason = "引号未闭合"
	}
	a.Segments = segs
	return a
}

// ===== 内置危险名单 =====

type dangerousPattern struct {
	Pattern string // 字面量（不是通配），按 normalizeCommand 后的命令比较
	Reason  string
}

// builtinDenyExact 灾难性且不可逆的操作：精确匹配整条命令。
//
// 刻意用「精确匹配」而非前缀：若写成前缀 "rm -rf /"，那么正常的
// "rm -rf /tmp/build" 也会被拒绝 —— 而这份名单是**不可撤销**的，
// 过宽会直接阻断用户的正常工作。这里只拦「根目录 / 主目录本身」。
var builtinDenyExact = []dangerousPattern{
	{"rm -rf /", "递归删除根目录"},
	{"rm -rf /*", "递归删除根目录全部内容"},
	{"rm -fr /", "递归删除根目录"},
	{"rm -fr /*", "递归删除根目录全部内容"},
	{"rm -rf ~", "递归删除用户主目录"},
	{"rm -rf ~/*", "递归删除用户主目录全部内容"},
	{"rm -fr ~", "递归删除用户主目录"},
	{"rm -rf $HOME", "递归删除用户主目录"},
	{"rm -rf $HOME/*", "递归删除用户主目录全部内容"},
	{"rm -rf .", "递归删除当前目录"},
	{"rm -rf ..", "递归删除上级目录"},
	{"chmod -R 777 /", "递归放开根目录权限"},
	{":(){ :|:& };:", "fork 炸弹"},
	{":(){:|:&};:", "fork 炸弹"},
	{"sudo rm -rf /", "以特权递归删除根目录"},
}

// builtinDenyPrefix 只要以该命令开头就危险的操作
var builtinDenyPrefix = []string{
	"mkfs",     // 格式化文件系统
	"shutdown", // 关闭系统
	"reboot",   // 重启系统
	"halt",     // 停止系统
	"poweroff", // 关闭系统
	"init 0",   // 切换运行级
}

// builtinDenyContains 命令中只要出现该片段就危险（多为重定向到块设备）
var builtinDenyContains = []dangerousPattern{
	{"of=/dev/", "直接写入块设备"},
	{"> /dev/sd", "覆写块设备"},
	{"> /dev/nvme", "覆写块设备"},
	{">/dev/sd", "覆写块设备"},
	{">/dev/nvme", "覆写块设备"},
	{"mkfs.", "格式化文件系统"},
}

// builtinSensitivePaths 敏感路径：不硬拒（可能是正常排障），但强制人工确认。
// 这些是「防误读」而非「防破坏」，因此归入 ask 而非 deny —— 与 deny 不同，
// 询问是可以被用户当场批准的。
var builtinSensitivePaths = []dangerousPattern{
	{".env", "可能读取环境变量文件（含密钥）"},
	{".ssh/", "可能读取 SSH 私钥目录"},
	{"id_rsa", "可能读取 SSH 私钥"},
	{"id_ed25519", "可能读取 SSH 私钥"},
	{".aws/credentials", "可能读取 AWS 凭据"},
	{".kube/config", "可能读取 Kubernetes 凭据"},
	{".git/config", "可能读取 Git 配置"},
	{".npmrc", "可能读取 npm 凭据"},
	{".pypirc", "可能读取 PyPI 凭据"},
	{"credentials.json", "可能读取服务账号凭据"},
	{".netrc", "可能读取网络凭据"},
}

// matchDangerousDeny 检查内置硬拒绝名单，返回命中的原因
func matchDangerousDeny(candidates []string) (string, bool) {
	for _, cand := range candidates {
		norm := normalizeCommand(cand)
		if norm == "" {
			continue
		}
		for _, p := range builtinDenyExact {
			if norm == normalizeCommand(p.Pattern) {
				return "内置拒止名单：" + p.Reason + "（" + p.Pattern + "）", true
			}
		}
		for _, prefix := range builtinDenyPrefix {
			if strings.HasPrefix(norm, prefix) {
				// 前缀须落在名字边界，避免 "mkfsx"、"rebootfoo" 之类误判
				if len(norm) == len(prefix) || !isNameChar(norm[len(prefix)]) {
					return "内置拒止名单：危险系统命令 " + prefix, true
				}
			}
		}
		for _, p := range builtinDenyContains {
			if strings.Contains(norm, p.Pattern) {
				return "内置拒止名单：" + p.Reason + "（" + p.Pattern + "）", true
			}
		}
	}
	return "", false
}

// matchSensitivePath 检查是否触碰敏感路径，命中则强制询问
func matchSensitivePath(candidates []string) (string, bool) {
	for _, cand := range candidates {
		lower := strings.ToLower(cand)
		for _, p := range builtinSensitivePaths {
			if strings.Contains(lower, strings.ToLower(p.Pattern)) {
				return p.Pattern + "：" + p.Reason, true
			}
		}
	}
	return "", false
}

// ===== 授权（Grant）=====

// 授权范围
const (
	ScopeOnce    = "once"
	ScopeSession = "session"
	ScopeAlways  = "always"
)

// Grant 用户对某类操作的放行许可
type Grant struct {
	ToolName  string   `json:"toolName"`
	Spec      string   `json:"spec"`
	IsPrefix  bool     `json:"isPrefix"`
	Kind      SpecKind `json:"kind"`
	Scope     string   `json:"scope"`
	CreatedAt int64    `json:"createdAt"`
}

func (g Grant) Describe() string {
	if g.Kind == SpecKindAny || g.Spec == "" {
		return g.ToolName + "（工具级）"
	}
	return g.ToolName + "(" + g.Spec + ")"
}

// Rule 把授权转成等价规则（用于落盘到 settings.local.json）
func (g Grant) Rule() Rule {
	r := Rule{
		Tool:     g.ToolName,
		Kind:     g.Kind,
		Spec:     g.Spec,
		IsPrefix: g.IsPrefix,
		Source:   SourceLocal,
	}
	r.Raw = r.String()
	return r
}

func (r Rule) String() string {
	if r.Kind == SpecKindAny || r.Spec == "" {
		return r.Tool
	}
	if r.Kind == SpecKindDomain {
		return r.Tool + "(domain:" + r.Spec + ")"
	}
	if r.IsPrefix {
		return r.Tool + "(" + r.Spec + ":*)"
	}
	return r.Tool + "(" + r.Spec + ")"
}

// GrantStore 会话内的授权集合（内存，会话结束即失效）
type GrantStore struct {
	mu     sync.RWMutex
	grants []Grant
}

func NewGrantStore() *GrantStore { return &GrantStore{} }

// Add 记录一条授权（去重）
func (g *GrantStore) Add(gr Grant) {
	if gr.CreatedAt == 0 {
		gr.CreatedAt = time.Now().UnixMilli()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, existing := range g.grants {
		if existing.ToolName == gr.ToolName && existing.Spec == gr.Spec &&
			existing.IsPrefix == gr.IsPrefix && existing.Kind == gr.Kind {
			g.grants[i] = gr // 更新范围（session -> always）
			return
		}
	}
	g.grants = append(g.grants, gr)
}

// Match 查找能覆盖该操作的授权
func (g *GrantStore) Match(sub PermissionSubject, a CommandAnalysis) (Grant, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	candidates := commandCandidates(sub, a)
	for _, gr := range g.grants {
		if gr.ToolName != sub.ToolName {
			continue
		}
		if gr.Kind == SpecKindAny || gr.Spec == "" {
			return gr, true
		}
		if gr.Kind == SpecKindDomain {
			if matchDomain(gr.Spec, sub.Domain) {
				return gr, true
			}
			continue
		}
		if sub.Command == "" {
			if sub.SpecValue == gr.Spec {
				return gr, true
			}
			continue
		}
		for _, cand := range candidates {
			if gr.IsPrefix {
				if matchCommandPrefix(cand, gr.Spec) {
					return gr, true
				}
			} else if cand == normalizeCommand(gr.Spec) {
				return gr, true
			}
		}
	}
	return Grant{}, false
}

// List 返回全部授权（供界面展示）
func (g *GrantStore) List() []Grant {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Grant, len(g.grants))
	copy(out, g.grants)
	return out
}

// Clear 清空（会话结束时调用）
func (g *GrantStore) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.grants = nil
}

// ===== 询问接口 =====

// PermissionRequest 一次授权询问（作为 chat:event 的 permission_request 载荷）
type PermissionRequest struct {
	RequestID   string                 `json:"requestId"`
	SessionID   string                 `json:"sessionId,omitempty"`
	ToolCallID  string                 `json:"toolCallId,omitempty"`
	ToolName    string                 `json:"toolName"`
	ToolLabel   string                 `json:"toolLabel,omitempty"`
	ToolType    string                 `json:"toolType,omitempty"`
	Risk        string                 `json:"risk"`
	Command     string                 `json:"command,omitempty"`
	Argv        []string               `json:"argv,omitempty"`
	Summary     string                 `json:"summary"`
	Reason      string                 `json:"reason"`                // 为什么需要询问
	MatchedRule string                 `json:"matchedRule,omitempty"` // 触发的 ask 规则（避免被误认为 bug）
	RuleSource  string                 `json:"ruleSource,omitempty"`
	SuggestRule string                 `json:"suggestRule,omitempty"` // 建议写入的规则文本（前端可编辑）
	Raw         map[string]interface{} `json:"raw,omitempty"`
}

// PermissionDecision 用户对询问的答复
type PermissionDecision struct {
	Allow bool   `json:"allow"`
	Scope string `json:"scope"` // once | session | always
	// Rule 用户在弹窗里编辑后的规则文本（为空则用引擎建议值）
	Rule string `json:"rule,omitempty"`
}

// Asker 询问实现。
// 生产环境发事件给前端并阻塞等待；测试环境注入固定答案。
type Asker interface {
	Ask(ctx context.Context, req PermissionRequest) (PermissionDecision, error)
}

// ===== 审计 =====

// AuditEntry 一条决策记录
type AuditEntry struct {
	SessionID string `json:"sessionId"`
	ToolName  string `json:"toolName"`
	Command   string `json:"command,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	At        int64  `json:"at"`
}

const auditRingCap = 500

// AuditLog 决策审计（环形缓冲）。
// 刻意独立于 PermissionEngine：设置页改动规则后引擎会被重建，
// 而审计与会话授权都应当跨重建保留。
type AuditLog struct {
	mu   sync.Mutex
	ring []AuditEntry
}

func NewAuditLog() *AuditLog { return &AuditLog{} }

func (l *AuditLog) Append(e AuditEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring = append(l.ring, e)
	if len(l.ring) > auditRingCap {
		l.ring = l.ring[len(l.ring)-auditRingCap:]
	}
}

// List 返回审计记录（最近的在最后）
func (l *AuditLog) List() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEntry, len(l.ring))
	copy(out, l.ring)
	return out
}

// Clear 清空审计
func (l *AuditLog) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring = nil
}

// ===== 引擎 =====

// PermissionEngine 决策引擎。
type PermissionEngine struct {
	sessionID string
	mode      PermissionMode
	rules     RuleSet // 用户配置分层合并后的规则
	grants    *GrantStore
	asker     Asker
	audit     *AuditLog
}

// NewPermissionEngine 构造引擎。asker 为 nil 表示无人可问 —— 此时任何需要
// 询问的决策都会降级为拒绝（fail closed）。
func NewPermissionEngine(sessionID string, mode PermissionMode, rules RuleSet, grants *GrantStore, asker Asker, audit *AuditLog) *PermissionEngine {
	if !ValidPermissionMode(mode) {
		mode = ModeDefault
	}
	if grants == nil {
		grants = NewGrantStore()
	}
	if audit == nil {
		audit = NewAuditLog()
	}
	return &PermissionEngine{
		sessionID: sessionID,
		mode:      mode,
		rules:     rules,
		grants:    grants,
		asker:     asker,
		audit:     audit,
	}
}

// SetMode 切换权限模式
func (e *PermissionEngine) SetMode(m PermissionMode) {
	if !ValidPermissionMode(m) {
		return
	}
	e.mode = m
}

// Mode 返回当前模式
func (e *PermissionEngine) Mode() PermissionMode { return e.mode }

// SetRules 替换用户规则（设置页改动后刷新）
func (e *PermissionEngine) SetRules(rules RuleSet) {
	e.rules = rules
}

// Grants 暴露授权集合（供界面展示与落盘）
func (e *PermissionEngine) Grants() *GrantStore { return e.grants }

// ===== 决策管线 =====

// Authorize 唯一的决策入口。
//
// 顺序（与 docs/permission-management-plan.md 4.3 一致）：
//
//	1. deny 规则（内置硬名单 + 用户）—— 绝对优先，模式与授权均不可覆盖
//	2. plan 模式拒绝非只读
//	3. 命令解析不可信 / 含命令替换 —— 强制询问
//	4. ask 规则（含内置敏感路径）
//	5. 会话内 / 永久授权
//	6. bypassPermissions
//	7. acceptEdits（仅文件编辑类）
//	8. allow 规则
//	9. 只读工具
//	10. 兜底询问（fail closed）
func (e *PermissionEngine) Authorize(ctx context.Context, sub PermissionSubject) (Decision, string) {
	// 0. 命令分解（仅命令类需要）
	analysis := CommandAnalysis{}
	if sub.Command != "" {
		analysis = AnalyzeCommand(sub.Command)
	}
	candidates := commandCandidates(sub, analysis)

	// 1. deny —— 绝对优先
	if reason, ok := matchDangerousDeny(candidates); ok {
		return e.finish(DecisionDeny, reason, sub)
	}
	if r, ok := matchAny(e.rules.Deny, sub, candidates); ok {
		return e.finish(DecisionDeny, "deny 规则命中: "+r.Raw+"（来源 "+r.Source+"）", sub)
	}

	// 2. plan 模式：非只读一律拒绝
	if e.mode == ModePlan && sub.Risk != RiskRead {
		return e.finish(DecisionDeny, "plan 模式仅允许只读操作", sub)
	}

	// 3. 解析不可信 / 含命令替换 —— 不许任何规则静默放行
	if analysis.Uncertain {
		return e.askOrDeny(ctx, sub, candidates, "命令解析不可信（"+analysis.Reason+"），需人工确认", "", "")
	}
	if analysis.HasSubstitution {
		return e.askOrDeny(ctx, sub, candidates, "命令含 $() 或反引号替换，替换结果无法静态判定，需人工确认", "", "")
	}

	// 4. ask 规则（内置敏感路径 + 用户规则）
	if reason, ok := matchSensitivePath(candidates); ok {
		return e.askOrDeny(ctx, sub, candidates, "触及敏感路径（"+reason+"）", "", SourceBuiltin)
	}
	if r, ok := matchAny(e.rules.Ask, sub, candidates); ok {
		return e.askOrDeny(ctx, sub, candidates, "ask 规则命中: "+r.Raw, r.Raw, r.Source)
	}

	// 5. 会话内 / 永久授权
	if g, ok := e.grants.Match(sub, analysis); ok {
		return e.finish(DecisionAllow, "已授权（"+g.Scope+"）："+g.Describe(), sub)
	}

	// 6. bypassPermissions
	if e.mode == ModeBypass {
		return e.finish(DecisionAllow, "bypassPermissions 模式", sub)
	}

	// 7. acceptEdits
	if e.mode == ModeAcceptEdits && sub.Risk == RiskWrite && isFileEditCommand(analysis) {
		return e.finish(DecisionAllow, "acceptEdits 模式自动批准文件编辑", sub)
	}

	// 8. allow 规则
	if r, ok := matchAny(e.rules.Allow, sub, candidates); ok {
		return e.finish(DecisionAllow, "allow 规则命中: "+r.Raw, sub)
	}

	// 9. 只读工具默认放行
	if sub.Risk == RiskRead {
		return e.finish(DecisionAllow, "只读工具默认放行", sub)
	}

	// 10. 兜底：fail closed
	return e.askOrDeny(ctx, sub, candidates, "未匹配任何规则，默认询问", "", "")
}

// askOrDeny 发起询问；无人可问或用户拒绝则拒绝
func (e *PermissionEngine) askOrDeny(ctx context.Context, sub PermissionSubject, candidates []string, reason, matchedRule, ruleSource string) (Decision, string) {
	if e.asker == nil {
		return e.finish(DecisionDeny, "需要授权但无人可应答，已拒绝（"+reason+"）", sub)
	}
	req := e.buildRequest(ctx, sub, candidates, reason, matchedRule, ruleSource)
	dec, err := e.asker.Ask(ctx, req)
	if err != nil {
		return e.finish(DecisionDeny, "授权未获通过："+err.Error(), sub)
	}
	if !dec.Allow {
		return e.finish(DecisionDeny, "用户拒绝执行", sub)
	}
	// 记录授权范围
	scope := dec.Scope
	if scope != ScopeSession && scope != ScopeAlways {
		scope = ScopeOnce
	}
	if scope != ScopeOnce {
		e.grants.Add(e.grantFrom(sub, dec.Rule, scope))
	}
	return e.finish(DecisionAllow, "用户批准（"+scope+"）", sub)
}

// grantFrom 依据判定对象与用户可能编辑过的规则文本构造授权
func (e *PermissionEngine) grantFrom(sub PermissionSubject, editedRule, scope string) Grant {
	if strings.TrimSpace(editedRule) != "" {
		if r, err := ParseRule(editedRule, SourceLocal); err == nil && r.Tool == sub.ToolName {
			return Grant{
				ToolName:  r.Tool,
				Spec:      r.Spec,
				IsPrefix:  r.IsPrefix,
				Kind:      r.Kind,
				Scope:     scope,
				CreatedAt: time.Now().UnixMilli(),
			}
		}
	}
	// 默认：精确到本次操作
	if sub.Command != "" {
		return Grant{
			ToolName:  sub.ToolName,
			Spec:      normalizeCommand(sub.Command),
			IsPrefix:  false,
			Kind:      SpecKindCommand,
			Scope:     scope,
			CreatedAt: time.Now().UnixMilli(),
		}
	}
	if sub.Domain != "" {
		return Grant{
			ToolName:  sub.ToolName,
			Spec:      sub.Domain,
			Kind:      SpecKindDomain,
			Scope:     scope,
			CreatedAt: time.Now().UnixMilli(),
		}
	}
	return Grant{
		ToolName:  sub.ToolName,
		Spec:      sub.SpecValue,
		Kind:      SpecKindCommand,
		Scope:     scope,
		CreatedAt: time.Now().UnixMilli(),
	}
}

// buildRequest 构造发送给前端的询问
func (e *PermissionEngine) buildRequest(ctx context.Context, sub PermissionSubject, candidates []string, reason, matchedRule, ruleSource string) PermissionRequest {
	var argv []string
	if sub.Command != "" {
		argv = candidates
	}
	req := PermissionRequest{
		RequestID:   fmt.Sprintf("perm-%d-%s", time.Now().UnixNano(), sub.ToolName),
		SessionID:   e.sessionID,
		ToolCallID:  toolCallIDFrom(ctx),
		ToolName:    sub.ToolName,
		ToolLabel:   sub.ToolLabel,
		ToolType:    sub.ToolType,
		Risk:        string(sub.Risk),
		Command:     sub.Command,
		Argv:        argv,
		Summary:     summaryOf(sub),
		Reason:      reason,
		MatchedRule: matchedRule,
		RuleSource:  ruleSource,
		Raw:         sub.Raw,
	}
	// 建议写入的规则：命令类给精确命令，用户可自行放宽为前缀
	switch {
	case sub.Command != "":
		req.SuggestRule = Rule{Tool: sub.ToolName, Kind: SpecKindCommand, Spec: normalizeCommand(sub.Command)}.String()
	case sub.Domain != "":
		req.SuggestRule = Rule{Tool: sub.ToolName, Kind: SpecKindDomain, Spec: sub.Domain}.String()
	case sub.SpecValue != "":
		req.SuggestRule = Rule{Tool: sub.ToolName, Kind: SpecKindCommand, Spec: sub.SpecValue}.String()
	default:
		req.SuggestRule = Rule{Tool: sub.ToolName, Kind: SpecKindAny}.String()
	}
	return req
}

// summaryOf 生成给用户看的一句话描述
func summaryOf(sub PermissionSubject) string {
	switch sub.Risk {
	case RiskRead:
		return "读取操作"
	case RiskNetwork:
		if sub.Domain != "" {
			return "访问网络：" + sub.Domain
		}
		return "发起网络请求"
	case RiskProcess:
		return "启动本地进程"
	default:
		if sub.Command != "" {
			return "执行终端命令"
		}
		return "修改本机状态"
	}
}

// isFileEditCommand acceptEdits 模式下自动放行的文件系统类命令
func isFileEditCommand(a CommandAnalysis) bool {
	if len(a.Segments) == 0 {
		return false
	}
	editCmds := map[string]bool{
		"mkdir": true, "touch": true, "mv": true, "cp": true,
		"ln": true, "install": true, "truncate": true,
	}
	for _, seg := range a.Segments {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			return false
		}
		if !editCmds[fields[0]] {
			return false
		}
	}
	return true
}

// finish 记录审计并返回决策
func (e *PermissionEngine) finish(d Decision, reason string, sub PermissionSubject) (Decision, string) {
	e.audit.Append(AuditEntry{
		SessionID: e.sessionID,
		ToolName:  sub.ToolName,
		Command:   sub.Command,
		Domain:    sub.Domain,
		Decision:  d.String(),
		Reason:    reason,
		At:        time.Now().UnixMilli(),
	})
	return d, reason
}

// Audit 返回审计记录（最近的在最后）
func (e *PermissionEngine) Audit() []AuditEntry {
	return e.audit.List()
}

// ===== 风险推导与判定对象构造 =====

// riskOf 按工具类型推导风险级别。
// 用户可在 ToolConfig.Risk 显式覆盖（留空则由类型推导）。
func riskOf(cfg *ToolConfig) RiskClass {
	if cfg == nil {
		return RiskWrite
	}
	if cfg.Risk != "" {
		return RiskClass(cfg.Risk)
	}
	switch cfg.Type {
	case ToolTypeAPI:
		return RiskNetwork
	case ToolTypeMCP:
		// stdio 会拉起本地进程，比远端传输更重
		var mcpCfg MCPToolConfig
		if err := json.Unmarshal(cfg.Config, &mcpCfg); err == nil && mcpCfg.Transport == "stdio" {
			return RiskProcess
		}
		return RiskNetwork
	case ToolTypeBuiltin:
		if cfg.Name == "read_skill" {
			return RiskRead
		}
		return RiskWrite
	default:
		return RiskWrite
	}
}

// buildSubject 构造判定对象。
//
// 优先采用工具自述的操作描述（Specifiable）：模板类工具的命令行只有在工具内部
// 渲染后才能确定，由装饰器代劳会导致「匹配串 ≠ 执行串」。
// 未实现该接口的工具退化为「仅按工具名 + 类型判定」—— 此时所有带限定符的规则
// 都不会命中，于是必然落到兜底询问，仍是 fail closed 的安全方向。
func buildSubject(t ToolInterface, cfg *ToolConfig, toolType string, args map[string]interface{}) PermissionSubject {
	if sp, ok := t.(Specifiable); ok {
		sub := sp.DescribeOperation(args)
		if sub.ToolName == "" {
			sub.ToolName = cfg.Name
		}
		sub.ToolID = cfg.ID
		sub.ToolType = toolType
		sub.ToolLabel = cfg.Label
		if sub.Risk == "" {
			sub.Risk = riskOf(cfg)
		}
		if sub.Raw == nil {
			sub.Raw = args
		}
		return sub
	}
	return PermissionSubject{
		ToolID:    cfg.ID,
		ToolName:  cfg.Name,
		ToolType:  toolType,
		ToolLabel: cfg.Label,
		Risk:      riskOf(cfg),
		Raw:       args,
	}
}

// ===== 工具调用上下文 =====

type permissionCtxKey struct{}

// WithToolCallID 把当前工具调用 ID 放进 ctx，供权限事件与 UI 卡片关联
func WithToolCallID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, permissionCtxKey{}, id)
}

// toolCallIDFrom 取出工具调用 ID
func toolCallIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(permissionCtxKey{}).(string); ok {
		return v
	}
	return ""
}
