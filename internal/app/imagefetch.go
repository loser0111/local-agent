package app

import (
	"fmt"
	"strings"

	"wails-tmp/internal/media"
)

// ===== 从图片链接抓取（Wails 绑定入口）=====
//
// 只有用户在前端点按「链接」入口才会走到这里——它不是工具、不进工具注册表，
// 模型无法触发它。抓取的边界（协议白名单、重定向逐跳校验、整体超时、边读边限长）
// 以及"为什么不按 IP 段拦内网"的理由，见 internal/media/imagefetch.go 的文件头注释。
//
// 引擎已下沉到 internal/media：本文件只剩 Wails 绑定所需的 App 方法。

// FetchImageURL 下载一个图片链接，按与"用户贴图"完全相同的方式存成会话附件。
//
// 返回值与 SaveAttachment 同形：拿到它就可以直接写进消息的 Attachments。
func (a *App) FetchImageURL(sessionID, rawURL string) (*media.Attachment, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("会话 ID 不能为空")
	}
	if a.attachments == nil {
		return nil, fmt.Errorf("附件存储未初始化")
	}
	data, name, err := media.FetchImage(rawURL)
	if err != nil {
		return nil, err
	}
	return a.attachments.Save(sessionID, name, data, media.AttachmentSourceUser)
}
