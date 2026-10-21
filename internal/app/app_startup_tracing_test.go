package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestStartupWithTracingStates：直接跑 App.Startup()，验证**真实启动路径**在
// 三种 tracing.json 状态下都不会失败。这是回答「不配 langfuse 启动有没有问题」
// 的唯一可信方式——前面那些测的是 observability 包，这里测的是 app.go 里
// 真正被 main.go 调用的那段初始化代码。
//
// 用 t.Setenv("HOME") 隔离，避免污染开发机上的 ~/.local-agent。
func TestStartupWithTracingStates(t *testing.T) {
	cases := []struct {
		name    string
		content string // "" = 不写文件
	}{
		{"完全没配（默认用户）", ""},
		{"文件损坏", "{{{ 坏掉的 json"},
		{"显式关闭", `{"enabled":false}`},
		{"开启但缺密钥", `{"enabled":true,"endpoint":"http://127.0.0.1:1/v1/traces"}`},
		{"开启且合法", `{"enabled":true,"endpoint":"http://127.0.0.1:1/v1/traces","publicKey":"pk-lf-a","secretKey":"sk-lf-b"}`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home) // Windows 侧同理

			dir := filepath.Join(home, ".local-agent")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if c.content != "" {
				if err := os.WriteFile(filepath.Join(dir, "tracing.json"), []byte(c.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			app := NewApp()
			// 不该 panic、不该 fatal。Wails 的 ctx 在这里只用于事件发射，
			// 传 background 即可（Startup 内部不会主动 Emit）。
			app.Startup(context.Background())
			// 达到这里就说明启动路径没有被打断；再确认一下核心存储都建好了。
			if app.sessionStore == nil || app.modelStore == nil {
				t.Error("启动后核心存储未初始化")
			}
		})
	}
}
