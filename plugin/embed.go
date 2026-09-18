package plugin

import (
	_ "embed"
	"fmt"
)

// manifestJSON 是内嵌的插件清单（描述文件）。
//
// 内嵌而非运行时读取：清单随二进制走，磁盘上的同名文件不参与加载，
// 避免「清单可被替换」带来的信任问题。
//
//go:embed manifest.json
var manifestJSON []byte

// manifestFileName 仅用于日志/错误提示。
const manifestFileName = "plugin/manifest.json"

// manifestSource 返回清单来源说明，便于排障时确认插件确实被加载。
func manifestSource() string { return manifestFileName }

// embedCheck 是编译期兜底：清单为空说明 go:embed 失效。
func embedCheck() error {
	if len(manifestJSON) == 0 {
		return fmt.Errorf("%s 内嵌内容为空", manifestFileName)
	}
	return nil
}
