package diff

import (
	"os"
	"testing"
)

// TestMain 掐掉 git 的 trace2 上报，让本包的 git 测试不被机器上的 git-ai 干扰。
//
// 背景：开发机上装了 git-ai，它在**全局 git 配置**里挂了
// trace2.eventtarget=af_unix:...（与 PATH 无关），于是每条 git 命令都被上报，
// git-ai 顺手在每个仓库里造出 .git/ai/ 目录。本包用 t.TempDir() 建临时仓库，
// 测试结束清理时那个目录还在被写，于是偶发
// "TempDir RemoveAll cleanup: ... .git: directory not empty" —— 表现为 flaky。
//
// GIT_TRACE2_EVENT=/dev/null 覆盖 eventtarget 的目标，git-ai 收不到事件就不再动手；
// 全局的 user.name/email 等配置保持原样。干净环境（CI）没有这个配置，这里等于空操作。
func TestMain(m *testing.M) {
	os.Setenv("GIT_TRACE2_EVENT", "/dev/null")
	os.Exit(m.Run())
}
