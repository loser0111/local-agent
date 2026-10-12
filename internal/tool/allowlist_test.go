package tool

import (
	"reflect"
	"sort"
	"testing"
)

// ===== allowed-tools 的匹配语义（#4）=====
//
// 这一层只测归一化与匹配规则，不涉及装配；装配侧的收窄效果见 internal/app/allowtools_test.go。

func TestAllowListEmptyMeansNoRestriction(t *testing.T) {
	for _, in := range [][]string{nil, {}, {"", "   ", "\t"}} {
		al := NewAllowList(in)
		if !al.Empty() {
			t.Errorf("输入 %#v 应视为未声明（不限制），实际 Empty()=false", in)
		}
		// 未声明时任何名字都放行——这正是"默认策略=不收窄"
		if !al.Match("exec_shell") || !al.Match("whatever") {
			t.Errorf("输入 %#v 未声明时不应限制任何工具", in)
		}
		if got := al.FilterNames([]string{"a", "b"}); got != nil {
			t.Errorf("未声明时 FilterNames 应返回 nil（表示不额外收窄），实际 %v", got)
		}
	}
}

func TestAllowListExactMatch(t *testing.T) {
	al := NewAllowList([]string{"read_file", "exec_shell"})
	if !al.Match("read_file") || !al.Match("exec_shell") {
		t.Fatal("声明的工具应被放行")
	}
	if al.Match("write_file") {
		t.Fatal("未声明的工具不应被放行")
	}
}

// 大小写不敏感：技能里常按展示名写 Read / Bash，而工具名是小写下划线
func TestAllowListCaseInsensitive(t *testing.T) {
	al := NewAllowList([]string{"READ_FILE"})
	if !al.Match("read_file") {
		t.Fatal("大小写不应影响匹配")
	}
	if !NewAllowList([]string{"read_file"}).Match("READ_FILE") {
		t.Fatal("反向大小写同样应匹配")
	}
}

// 分隔符归一：`-` / `.` / 空格 与 `_` 等价
func TestAllowListSeparatorNormalized(t *testing.T) {
	for _, pat := range []string{"read-file", "read.file", "read file", "  read_file  "} {
		if !NewAllowList([]string{pat}).Match("read_file") {
			t.Errorf("模式 %q 应命中 read_file", pat)
		}
	}
	// 反向：声明的名字是下划线写法，实际工具名带连字符（MCP 工具名常见）也要能对上
	if !NewAllowList([]string{"my_tool"}).Match("my-tool") {
		t.Error("声明与工具名的分隔符差异应被归一")
	}
}

func TestAllowListWildcard(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*", "read_file", true},
		{"*", "anything_at_all", true},
		{"read_*", "read_file", true},
		{"read_*", "read_image", true},
		{"read_*", "write_file", false},
		{"mcp__fs__*", "mcp__fs__read", true},
		{"mcp__fs__*", "mcp__git__read", false},
		{"*_file", "read_file", true},
		{"*_file", "read_image", false},
		{"re*d_*e", "read_file", true},
		{"a*b*c", "abc", true}, // `*` 匹配空串
		{"a*b*c", "axxbyyc", true},
		{"a*b*c", "abx", false}, // 尾部不匹配
		{"read_file", "read_file", true},
		{"read_file", "read_file_extra", false},
	}
	for _, c := range cases {
		if got := NewAllowList([]string{c.pattern}).Match(c.name); got != c.want {
			t.Errorf("模式 %q 匹配 %q: 期望 %v，实际 %v", c.pattern, c.name, c.want, got)
		}
	}
}

// FilterNames：命中则返回保留集，全不命中则返回 nil（调用方据此放弃收窄）
func TestAllowListFilterNames(t *testing.T) {
	all := []string{"read_file", "write_file", "exec_shell"}

	got := NewAllowList([]string{"read_file"}).FilterNames(all)
	if !reflect.DeepEqual(got, []string{"read_file"}) {
		t.Fatalf("期望只留 read_file，实际 %v", got)
	}

	got = NewAllowList([]string{"read_*", "exec_*"}).FilterNames(all)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"exec_shell", "read_file"}) {
		t.Fatalf("通配符过滤结果不符，实际 %v", got)
	}

	// 兜底：声明全是本仓没有的名字（照搬外部生态的 Read/Bash/Write 是常见情形）
	if got := NewAllowList([]string{"Read", "Bash", "Write"}).FilterNames(all); got != nil {
		t.Fatalf("全不命中时应返回 nil 以放弃收窄，实际 %v", got)
	}

	// 未声明时同样返回 nil
	if got := NewAllowList(nil).FilterNames(all); got != nil {
		t.Fatalf("未声明时应返回 nil，实际 %v", got)
	}
}

// Raw 返回副本：调用方改它不应影响集合本身
func TestAllowListRawIsCopy(t *testing.T) {
	al := NewAllowList([]string{"read_file"})
	raw := al.Raw()
	raw[0] = "exec_shell"
	if !al.Match("read_file") || al.Match("exec_shell") {
		t.Fatal("Raw 应返回副本，改动它不应影响匹配结果")
	}
}
