package tool

import "testing"

func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "src/main.go", false}, // 单星不跨段
		{"**/*.go", "main.go", true},   // ** 可匹配零个目录
		{"**/*.go", "a/b/main.go", true},
		{"src/*.go", "src/a.go", true},
		{"src/*.go", "src/a/b.go", false},
		{"src/**/*.ts", "src/a/b/c.ts", true},
		{"**/*_test.go", "x/y_test.go", true},
		{"a?.txt", "ab.txt", true},
		{"a?.txt", "abc.txt", false},
		{"docs/**", "docs/a/b.md", true},
	}
	for _, c := range cases {
		re, err := globToRegexp(c.pattern)
		if err != nil {
			t.Fatalf("globToRegexp(%q) 报错: %v", c.pattern, err)
		}
		if got := re.MatchString(c.path); got != c.want {
			t.Errorf("glob %q 匹配 %q = %v，期望 %v（regex=%s）", c.pattern, c.path, got, c.want, re.String())
		}
	}
	if _, err := globToRegexp(""); err == nil {
		t.Error("空模式应报错")
	}
}
