package tool

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestWebSearchLive 真实联网验证：只有显式设置 WEBSEARCH_LIVE=1 时才跑。
//
// 为什么默认跳过：它依赖公网与 DuckDuckGo 的可用性，放进常规测试会变成
// 偶发失败（CI 里尤其明显）。但**必须保留在仓库里**——单测用的是固定 HTML 夹具，
// 只能证明"给定这样的 HTML 能解析对"，证明不了"DDG 现在真的返回这样的 HTML"。
// 页面的类名一旦改版，夹具仍会全绿而线上功能已经坏了。
//
// 用法：WEBSEARCH_LIVE=1 go test ./internal/tool/ -run TestWebSearchLive -v
func TestWebSearchLive(t *testing.T) {
	if os.Getenv("WEBSEARCH_LIVE") == "" {
		t.Skip("需要联网：设置 WEBSEARCH_LIVE=1 才运行")
	}

	out, err := NewWebSearchTool().Execute(context.Background(), map[string]interface{}{
		"query":       "golang concurrency patterns",
		"max_results": float64(3),
	})
	if err != nil {
		t.Fatalf("真实搜索失败：%v", err)
	}
	t.Logf("搜索结果：\n%s", out)

	if !strings.Contains(out, "http") {
		t.Error("结果里应至少含一个 URL")
	}
	if n := strings.Count(out, "\n   http"); n == 0 {
		t.Error("没有任何结果条目——可能是页面结构改版导致解析器失效")
	}
	if n := strings.Count(out, "\n   http"); n > 3 {
		t.Errorf("max_results=3 但返回了 %d 条", n)
	}
	// 广告链必须被剔掉：出现 duckduckgo.com/y.js 说明过滤失效。
	if strings.Contains(out, "y.js") {
		t.Error("结果里混入了广告跳转链")
	}
}
