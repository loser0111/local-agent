package tool

import (
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

// ddgFixture 一段贴合 html.duckduckgo.com 真实形状的片段：
// 第 1 条是广告（result--ad + y.js 跳转链），后两条是正常结果。
// 解析器必须剔掉广告，否则模型会把广告当搜索结果。
const ddgFixture = `
<html><body>
<div class="results">
  <div class="result results_links result--ad">
    <h2 class="result__title">
      <a rel="nofollow" class="result__a" href="https://duckduckgo.com/y.js?ad_domain=udemy.com&amp;u3=xxx">Buy Courses Now</a>
    </h2>
    <a class="result__snippet" href="https://duckduckgo.com/y.js">Shop thousands of courses.</a>
  </div>
  <div class="result results_links results_links_deep">
    <h2 class="result__title">
      <a rel="nofollow" class="result__a" href="https://go.dev/blog/pipelines">Go Concurrency Patterns: Pipelines</a>
    </h2>
    <div class="result__extras"><span class="result__icon"><a href="https://go.dev/blog/pipelines">x</a></span></div>
    <a class="result__snippet" href="https://go.dev/blog/pipelines">Go&#x27;s concurrency primitives make it
      easy to construct streaming data pipelines.</a>
  </div>
  <div class="result results_links results_links_deep">
    <h2 class="result__title">
      <a rel="nofollow" class="result__a" href="https://pkg.go.dev/sync">sync package - Go</a>
    </h2>
    <a class="result__snippet" href="https://pkg.go.dev/sync">Package sync provides basic synchronization primitives.</a>
  </div>
</div>
</body></html>`

func parseFixture(t *testing.T, s string) *xhtml.Node {
	t.Helper()
	doc, err := xhtml.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("解析夹具失败: %v", err)
	}
	return doc
}

func TestExtractDDGResultsSkipsAdsAndKeepsOrder(t *testing.T) {
	got := extractDDGResults(parseFixture(t, ddgFixture))
	if len(got) != 2 {
		t.Fatalf("应抽出 2 条（广告被剔除），实际 %d 条：%+v", len(got), got)
	}
	if got[0].URL != "https://go.dev/blog/pipelines" {
		t.Errorf("首条应是 go.dev，实际 %q", got[0].URL)
	}
	if got[0].Title != "Go Concurrency Patterns: Pipelines" {
		t.Errorf("标题解析错误：%q", got[0].Title)
	}
	// 摘要要反转义（&#x27; → '）并把换行/多空格压成单空格。
	if !strings.Contains(got[0].Snippet, "Go's concurrency primitives") {
		t.Errorf("摘要未正确反转义/压空白：%q", got[0].Snippet)
	}
	if strings.Contains(got[0].Snippet, "\n") {
		t.Errorf("摘要不应含换行：%q", got[0].Snippet)
	}
	if got[1].URL != "https://pkg.go.dev/sync" {
		t.Errorf("第二条应是 pkg.go.dev，实际 %q", got[1].URL)
	}
}

func TestExtractDDGResultsDedupesSameURL(t *testing.T) {
	fixture := `<div class="result"><h2 class="result__title"><a class="result__a" href="https://a.com/x">A</a></h2></div>
<div class="result"><h2 class="result__title"><a class="result__a" href="https://a.com/x">A again</a></h2></div>`
	got := extractDDGResults(parseFixture(t, fixture))
	if len(got) != 1 {
		t.Fatalf("同一 URL 应去重成 1 条，实际 %d 条", len(got))
	}
}

func TestIsExternalResultURL(t *testing.T) {
	cases := map[string]bool{
		"https://go.dev/blog":                     true,
		"http://example.com/a":                    true,
		"https://duckduckgo.com/y.js?ad_domain=x": false,
		"https://html.duckduckgo.com/html/?q=x":   false,
		"javascript:void(0)":                      false,
		"/relative/path":                          false,
		"":                                        false,
	}
	for raw, want := range cases {
		if got := isExternalResultURL(raw); got != want {
			t.Errorf("isExternalResultURL(%q) = %v，期望 %v", raw, got, want)
		}
	}
}

func TestFormatWebSearchResultsMentionsOverflow(t *testing.T) {
	out := formatWebSearchResults("golang", []webSearchResult{
		{Title: "A", URL: "https://a.com", Snippet: "sa"},
	}, 7)
	if !strings.Contains(out, "关键词：golang") {
		t.Errorf("应带上关键词：%s", out)
	}
	if !strings.Contains(out, "https://a.com") {
		t.Errorf("应带上 URL：%s", out)
	}
	// 共 7 条只显示 1 条时，要提示还有更多，否则模型以为只有一条。
	if !strings.Contains(out, "共 7 条") {
		t.Errorf("应提示总数：%s", out)
	}
}

func TestArgIntAcceptsStringAndFloat(t *testing.T) {
	if got := argInt(float64(3), 5); got != 3 {
		t.Errorf("float64 应转成 3，实际 %d", got)
	}
	if got := argInt("7", 5); got != 7 {
		t.Errorf("字符串 \"7\" 应转成 7，实际 %d", got)
	}
	if got := argInt(nil, 5); got != 5 {
		t.Errorf("缺失时应回退默认值 5，实际 %d", got)
	}
	if got := argInt("abc", 5); got != 5 {
		t.Errorf("无法解析时应回退默认值 5，实际 %d", got)
	}
}

func TestTruncateRunesDoesNotSplitMultibyte(t *testing.T) {
	s := "中文测试"
	got := truncateRunes(s, 3)
	if !strings.HasPrefix(got, "中文测") {
		t.Errorf("截断结果错误：%q", got)
	}
	if truncateRunes(s, 10) != s {
		t.Errorf("未超长时应原样返回")
	}
}

func TestWebSearchToolSchema(t *testing.T) {
	tool := NewWebSearchTool()
	if tool.GetName() != ToolWebSearch {
		t.Fatalf("工具名应为 %s，实际 %s", ToolWebSearch, tool.GetName())
	}
	req := tool.(interface{ RequiredParams() []string }).RequiredParams()
	if len(req) != 1 || req[0] != "query" {
		t.Fatalf("只有 query 必填，实际 %v", req)
	}
	if _, ok := tool.GetParameters()["query"]; !ok {
		t.Fatal("参数表里应有 query")
	}
}

func TestWebSearchRejectsEmptyQuery(t *testing.T) {
	_, err := NewWebSearchTool().Execute(nil, map[string]interface{}{"query": "   "})
	if err == nil {
		t.Fatal("空 query 应报错，且不应发起网络请求")
	}
}
