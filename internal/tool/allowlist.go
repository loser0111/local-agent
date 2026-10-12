package tool

import "strings"

// ===== 工具名允许集合：技能 allowed-tools 的落点 =====
//
// 这是 `allowed-tools` 从「声明」变成「约束」的执行侧实现：装配期在会话白名单
// 之上再套一层过滤，于是技能/角色用不到的工具**根本不会出现在模型眼前**，
// 也经不过 tool_router 被发现（两处用的是同一个 registry，见 BuildView）。

// AllowList 一组工具名模式，用来把工具视图收窄到声明范围内。
//
// 匹配规则三条，都为兼容真实世界的写法而设：
//   - **通配符** `*` 匹配任意字符（含空串）：`mcp__filesystem__*` 覆盖该服务器全部
//     子工具；单独一项 `*` 等价于"全部放行"（即不额外收窄）。
//   - **大小写不敏感**：技能里常按展示名写 `Read` / `Bash`，而工具名是小写下划线。
//   - **分隔符归一**：比较前把 `-`、`.`、空格折叠成 `_`，于是 `read-file`、
//     `read file` 与 `read_file` 等价。
//
// 一个刻意的边界：外部生态（Claude Code 等）的技能常声明 `Read` / `Bash` / `Write`，
// 这些名字在本项目里**没有同名工具**。若原样收窄，技能会被自己的声明锁死到零工具——
// 那不是"更安全"，而是功能直接不可用。因此收窄时留了一道兜底，见 FilterNames。
type AllowList struct {
	raw []string
	// patterns 归一化后的模式（可能含 `*`）
	patterns []string
}

// NewAllowList 归一化一组工具名模式；空入参（或全是空白项）得到"不限制"的集合。
func NewAllowList(names []string) AllowList {
	al := AllowList{raw: append([]string(nil), names...)}
	for _, n := range names {
		if n = normalizeToolPattern(n); n != "" {
			al.patterns = append(al.patterns, n)
		}
	}
	return al
}

// Empty 是否未声明（或声明里全是空白项），即"不额外收窄"。
func (a AllowList) Empty() bool { return len(a.patterns) == 0 }

// Match 工具名是否被允许。集合为空时一律放行。
func (a AllowList) Match(toolName string) bool {
	if a.Empty() {
		return true
	}
	name := normalizeToolPattern(toolName)
	for _, p := range a.patterns {
		if wildcardMatch(p, name) {
			return true
		}
	}
	return false
}

// Raw 原始声明（诊断与回显用，不改动调用方切片）
func (a AllowList) Raw() []string { return append([]string(nil), a.raw...) }

// FilterNames 在候选名上应用允许集合，返回被放行的名字。
//
// **兜底语义（重要）**：一条都没命中时返回 nil，调用方应据此**放弃收窄**。
// 触发场景是"声明里的名字在本项目里没有对应工具"（照搬外部生态的 Read/Bash/Write
// 是最常见的例子）。此时照收窄会把技能锁死到零工具，而退回"不额外收窄"恰好等于
// 改动前的行为——声明写错名字不该让技能变成不可用，这是比 fail-closed 更合理的一侧。
// 调用方拿到 nil 时应打印告警，让写错的名字可见而不是静默失效。
func (a AllowList) FilterNames(names []string) []string {
	if a.Empty() {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if a.Match(n) {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeToolPattern 归一化工具名模式：去首尾空白、转小写、分隔符折叠成 `_`。
func normalizeToolPattern(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '-', '.', ' ', '\t':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// wildcardMatch 通配符匹配：`*` 匹配任意字符（含空串），其余逐字符相等。
//
// 手写而不用 filepath.Match：后者把 `*` 限定为"不跨路径分隔符"，而工具名里的分隔符
// 是 `_`、还会出现 `mcp__server__tool` 这种双下划线，用 filepath.Match 反而要额外
// 转义、语义也更绕。这里用双指针回溯实现，无正则编译开销、也无指数回溯风险。
func wildcardMatch(pattern, name string) bool {
	p, n := 0, 0
	star, mark := -1, 0
	for n < len(name) {
		if p < len(pattern) && pattern[p] == name[n] {
			p++
			n++
			continue
		}
		if p < len(pattern) && pattern[p] == '*' {
			star, mark = p, n
			p++
			continue
		}
		if star >= 0 {
			// 回溯：让上一个 `*` 多吃一个字符
			mark++
			p, n = star+1, mark
			continue
		}
		return false
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
