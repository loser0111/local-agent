package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// ===== 从图片链接抓取（只由用户明确点按触发）=====
//
// 为什么需要它：有一类截图工具、以及不少内部控制台，复制图片时放进剪贴板的
// **不是图片字节，而是一条链接**。此时前端拿不到任何 File，"粘贴图片"这件事从
// 源头就不成立——浏览器默认粘贴只会把那串 URL 插进输入框，用户以为图发出去了，
// 而模型只看到链接（真机踩到过：模型回"我无法访问该 URL"）。
//
// **边界，每一条都是刻意的**：
//
//  1. 只有**用户点按**才能触发（前端用一个显式的「链接」入口，收集 URL 后调用本接口）。
//     它不是工具、不进工具注册表，模型没有任何办法触发它——因此不存在"提示词注入
//     骗本机去抓一个内网地址"的通路。
//  2. 只允许 http / https；重定向最多 3 跳且**逐跳重新校验**（否则第一跳合法、
//     第二跳换协议就绕过了）；整体超时；响应体边读边限长（不能先读全再判大小）。
//  3. 内容必须是能嗅探成受支持图片的字节——判断交给同一条入站规整管线
//     （attachments.Save → normalizeImage），这里不另写一套。
//  4. **不按 IP 段拦内网**：内部图床 / 日志平台几乎都在内网，拦掉内网会让这条链路在
//     真实工作流里直接不可用。等价的安全边界是"用户看着完整 URL 点确认"——
//     他本来也能在浏览器里打开它。要收紧时只改 assertFetchableURL 一处。
const (
	imageFetchTimeout      = 20 * time.Second
	imageFetchMaxRedirects = 3
	imageFetchUserAgent    = "local-agent (image fetch)"
)

// assertFetchableURL 校验一个待抓取的地址；返回规整后的 URL。
// 收紧策略（例如禁止私网地址）应当只改这里——它是唯一的入口与唯一的重定向校验点。
func assertFetchableURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("链接格式不正确: %v", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("只支持 http/https 链接（收到 %q）", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return nil, fmt.Errorf("链接缺少主机名")
	}
	return u, nil
}

// FetchImageURL 下载一个图片链接，按与"用户贴图"完全相同的方式存成会话附件。
//
// 返回值与 SaveAttachment 同形：拿到它就可以直接写进消息的 Attachments。
func (a *App) FetchImageURL(sessionID, rawURL string) (*Attachment, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("会话 ID 不能为空")
	}
	if a.attachments == nil {
		return nil, fmt.Errorf("附件存储未初始化")
	}
	u, err := assertFetchableURL(rawURL)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), imageFetchTimeout)
	defer cancel()

	client := &http.Client{
		Timeout: imageFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= imageFetchMaxRedirects {
				return fmt.Errorf("重定向次数过多（上限 %d）", imageFetchMaxRedirects)
			}
			// 逐跳校验：只在入口校验一次的话，第二跳就能把请求带到别的协议/主机上
			_, err := assertFetchableURL(req.URL.String())
			return err
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %v", err)
	}
	req.Header.Set("User-Agent", imageFetchUserAgent)
	req.Header.Set("Accept", "image/*,*/*;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 这条错误是给用户看的：内部日志平台的链接多半需要登录，或它根本不是一个图片直链
		return nil, fmt.Errorf("下载失败：服务器返回 %d（链接可能需要登录，或它不是图片直链）", resp.StatusCode)
	}
	// 边读边限长。先 ReadAll 再判大小等于让一个超大响应把内存吃掉。
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageSourceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}
	if len(data) > maxImageSourceBytes {
		return nil, fmt.Errorf("图片过大（超过 %d MB）", maxImageSourceBytes>>20)
	}

	// 复用同一条入站规整管线：嗅探类型 → 解码 → 缩放 → 重编码 → 内容寻址落盘。
	// 因此"抓来的图"与"贴进来的图"在存储、协议组装、token 估算上完全同路，
	// 不需要任何下游分支。
	return a.attachments.Save(sessionID, nameFromImageURL(u), data, attachmentSourceUser)
}

// nameFromImageURL 从链接里取一个像文件名的展示名；取不到就给个通用名。
// 名字只用于展示，落盘路径仍由内容哈希决定（见 AttachmentStore.Save）。
func nameFromImageURL(u *url.URL) string {
	base := path.Base(u.Path)
	if base == "" || base == "/" || base == "." || base == ".." || !strings.Contains(base, ".") {
		return "远程图片"
	}
	return base
}
