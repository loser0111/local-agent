package tool

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

// ===== web_search：联网搜索（内置工具）=====
//
// 为什么做成内置工具，而不是 cli / http 来源：
//   - cli 来源的命令模板是「字符串替换 + bash -c」（见 toolruntime.go:RenderTemplate / Execute），
//     模型给的 query 里的 shell 元字符会真的被执行；http 来源的模板替换同样不转义
//     （拼 JSON 时 query 含引号就产出非法 body），且响应不裁剪（toolruntime.go 读满 1MB 原样
//     塞进上下文）。两者都得靠外部脚本兜底。
//   - 走内置路径可以在 Go 里直接做 URL 编码、结果裁剪、去广告，把上面两个坑从根上消掉，
//     代价只是本文件 + 五处接线（names.go / toolstore.go / tools.go / chat.go 提示词 / 一处测试）。
//
// 为什么只做一个工具、不让模型选引擎：模型看得到的只有工具名与 description，它无从知道
// 背后是哪个引擎、哪个能连通。多引擎的选择是「运行时 fallback」，属于实现细节，不该外泄给模型。

// ddgSearchURL DuckDuckGo 的免 JS HTML 端点。
//
// 选它的原因：无需 API key、无需注册，实测本机可直连（api.search.brave.com 超时、
// www.bing.com 返回 152 字节的拦截页）。lite.duckduckgo.com/lite/ 结构更简单，
// 但 html 端点的 result__a / result__snippet 类名稳定、有摘要，故取此。
//
// ⚠️ 该端点有反爬：**必须带浏览器 UA 与 Referer**，否则返回 anomaly-modal 人机校验页
// （表现为「0 条结果」，很容易被误判成「没搜到」——见 Execute 里的显式识别）。
const ddgSearchURL = "https://html.duckduckgo.com/html/"

const (
	// 默认返回条数。5 条 × (标题+URL+摘要) 大致 1KB 量级，对上下文很友好。
	webSearchDefaultMax = 5
	// 上限：再多也没有边际收益，只会挤占上下文。
	webSearchMaxCap = 10
	// 单次请求超时。搜索接口通常在 1s 内返回，20s 是留足余量而非期望值。
	webSearchTimeout = 20 * time.Second
	// 响应体读取上限。搜索结果页约 30-40KB，2MB 足够且能挡住异常响应把内存吃满。
	webSearchMaxBody = 2 << 20
	// 单条摘要截断长度。
	webSearchSnippetMax = 300
	// 浏览器 UA：不带它 DDG 会直接返回人机校验页。
	webSearchUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// webSearchResult 一条搜索结果（只保留对模型有用的三个字段）
type webSearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// NewWebSearchTool 构造联网搜索工具。
func NewWebSearchTool() ToolInterface {
	params, required := FileToolParams([]FileArg{
		{Name: "query", Type: "string", Description: "搜索关键词（自然语言或关键词组合都行）", Required: true},
		{Name: "max_results", Type: "number", Description: fmt.Sprintf("返回条数，默认 %d，最多 %d", webSearchDefaultMax, webSearchMaxCap)},
		{Name: "site", Type: "string", Description: "可选：限定域名，如 docs.python.org（会拼成 site: 限定）"},
	})
	return &webSearchTool{
		BaseTool: &BaseTool{
			Name: ToolWebSearch,
			Description: "联网搜索（互联网，DuckDuckGo 引擎）：用关键词检索公开网页，返回标题、URL 与摘要。" +
				"当问题超出你的训练数据、或需要最新/实时信息时用它（新闻、版本号、依赖现状、报错信息、价格、人物近况等）；" +
				"也可以先用它拿到 URL，再用 exec_shell 的 curl 取全文细读。" +
				"不适合：查本地代码或文件（那些用 grep / glob / read_file，不要联网）。",
			Parameters: params,
		},
		required: required,
	}
}

type webSearchTool struct {
	*BaseTool
	required []string
}

func (t *webSearchTool) RequiredParams() []string { return t.required }

func (t *webSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query 参数是必需的")
	}
	// site 限定：拼成 DuckDuckGo 支持的 site: 语法。这里只是拼查询词，
	// 真正出网的是下方固定端点，所以不构成"模型可指定任意 URL"的风险。
	if site, _ := args["site"].(string); strings.TrimSpace(site) != "" {
		query = query + " site:" + strings.TrimSpace(site)
	}
	maxN := argInt(args["max_results"], webSearchDefaultMax)
	if maxN <= 0 {
		maxN = webSearchDefaultMax
	}
	if maxN > webSearchMaxCap {
		maxN = webSearchMaxCap
	}

	results, blocked, err := ddgSearch(ctx, query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		if blocked {
			// 单独把"被反爬挡下"与"真的没结果"分开报：两者的下一步动作完全不同，
			// 混成一句"没搜到"会让模型直接放弃，而实际上换个时间/关键词就能拿到。
			return "", fmt.Errorf("搜索被 DuckDuckGo 人机校验拦截（未返回结果页）。稍后重试，或换一个更具体的关键词")
		}
		return "", fmt.Errorf("未找到与 %q 相关的结果，试试更换关键词", query)
	}

	total := len(results)
	if total > maxN {
		results = results[:maxN]
	}
	return formatWebSearchResults(query, results, total), nil
}

// ddgSearch 执行一次搜索。blocked 为 true 表示"拿到的不是结果页而是人机校验页"。
func ddgSearch(ctx context.Context, query string) (results []webSearchResult, blocked bool, err error) {
	form := url.Values{}
	form.Set("q", query)
	// 用 POST 表单：GET 的 query string 要自己转义，交给 url.Values 更稳妥。
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ddgSearchURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, false, fmt.Errorf("构造搜索请求失败：%w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", webSearchUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://html.duckduckgo.com/")

	client := &http.Client{Timeout: webSearchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("搜索请求失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("搜索请求返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webSearchMaxBody))
	if err != nil {
		return nil, false, fmt.Errorf("读取搜索响应失败：%w", err)
	}

	// 反爬识别要**在解析之前**做：校验页里没有 result 块，解析结果同样为空，
	// 事后无法区分，只能靠页面特征判断。
	page := string(body)
	if strings.Contains(page, "anomaly-modal") {
		return nil, true, nil
	}

	doc, err := xhtml.Parse(strings.NewReader(page))
	if err != nil {
		return nil, false, fmt.Errorf("解析搜索结果失败：%w", err)
	}
	return extractDDGResults(doc), false, nil
}

// extractDDGResults 从结果页 DOM 里抽取条目。
//
// 依赖的结构（实测 html.duckduckgo.com 的形状，2026-02 核对）：
//
//	<div class="result results_links ... [result--ad]">
//	  <h2 class="result__title"><a rel="nofollow" class="result__a" href="真实URL">标题</a></h2>
//	  ...<a class="result__snippet" href="...">摘要</a>
//	</div>
//
// 广告块带 result--ad 类，其 href 指向 duckduckgo.com/y.js 跳转链，两者都要剔掉，
// 否则模型会把广告当成搜索结果。
func extractDDGResults(root *xhtml.Node) []webSearchResult {
	var out []webSearchResult
	seen := map[string]bool{}

	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && n.Data == "div" &&
			hasClass(n, "result") && !hasClass(n, "result--ad") {
			if link := findFirstByClass(n, "result__a"); link != nil {
				href := strings.TrimSpace(attr(link, "href"))
				title := cleanHTMLText(nodeText(link))
				if title != "" && isExternalResultURL(href) && !seen[href] {
					seen[href] = true
					snippet := ""
					if s := findFirstByClass(n, "result__snippet"); s != nil {
						snippet = cleanHTMLText(nodeText(s))
					}
					out = append(out, webSearchResult{
						Title:   title,
						URL:     href,
						Snippet: truncateRunes(snippet, webSearchSnippetMax),
					})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// isExternalResultURL 过滤掉站内跳转与广告链（它们不指向真实内容页）。
func isExternalResultURL(raw string) bool {
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	// 广告与"站内跳转"都在 duckduckgo.com 域下（y.js?ad_domain=... 这类）。
	return !strings.Contains(strings.ToLower(u.Host), "duckduckgo.com")
}

// formatWebSearchResults 渲染成给模型看的纯文本。
//
// 输出刻意保持紧凑（标题 + URL + 摘要），既省上下文，也把"来源"留在结果里，
// 便于模型在回答里给出引用链接。
func formatWebSearchResults(query string, results []webSearchResult, total int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "联网搜索结果（DuckDuckGo，关键词：%s）：\n", query)
	for i, r := range results {
		fmt.Fprintf(&sb, "\n%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&sb, "   %s\n", r.Snippet)
		}
	}
	if total > len(results) {
		fmt.Fprintf(&sb, "\n（共 %d 条，已显示前 %d 条；需要更多可调大 max_results）", total, len(results))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ===== 小工具 =====

// argInt 把模型给的数值参数转成 int。模型有时把数字写成字符串（"5"），两种都认。
func argInt(v interface{}, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		var i int
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &i); err == nil {
			return i
		}
	}
	return def
}

// hasClass 判断元素的 class 属性里是否含某个类名（class 是空格分隔的列表）。
func hasClass(n *xhtml.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key != "class" {
			continue
		}
		for _, f := range strings.Fields(a.Val) {
			if f == class {
				return true
			}
		}
	}
	return false
}

// attr 取属性值（x/net/html 已把属性名规范成小写）。
func attr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// findFirstByClass 深度优先找第一个带指定 class 的元素。
func findFirstByClass(root *xhtml.Node, class string) *xhtml.Node {
	var found *xhtml.Node
	var walk func(*xhtml.Node) bool
	walk = func(n *xhtml.Node) bool {
		if n.Type == xhtml.ElementNode && hasClass(n, class) {
			found = n
			return true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(root)
	return found
}

// nodeText 收集子树里的全部文本（不含标签）。
func nodeText(n *xhtml.Node) string {
	var sb strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// cleanHTMLText 反转义 HTML 实体并把连续空白压成单个空格。
// 摘要里常见 &#x27; 这类实体，以及换行/多空格造成的排版噪声。
func cleanHTMLText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

// truncateRunes 按字符（而非字节）截断，避免把多字节字符切坏。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
