package diff

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTestFile 写一个测试文件，自动建父目录。
//
// 这是 internal/diff 自带的副本：拆包前它定义在根包的 filetools_test.go 里，
// 但它只是"把字符串写到文件"这种与业务无关的动作，没有测试语义；而测试助手
// 无法从别的包引用——所以随测试一起复制一份，而不是让这个包反过来依赖根包
// （根包 import 本包，依赖方向必须是单向的）。
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
