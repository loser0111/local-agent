package app

import (
	"strings"
	"testing"
)

// ===== 外部链接打开：scheme 白名单 =====
//
// 聊天内容来自模型输出与用户输入，属于**不可信内容**。若不校验 scheme，
// `file:///etc/passwd`、`javascript:`、`x-app://` 这类都会被原样交给操作系统处理
// ——那等于把"点一下就在本机触发动作"的能力交出去。
//
// 这里守的正是这条边界：只有 http/https 放行，其余一律拒绝。
// 测试环境没有界面（HasUI() 为 false），所以放行分支不会真的去调 runtime，
// 断言落在"是否报错"上——这恰好把校验逻辑与副作用隔离开。
func TestOpenExternalURLSchemeWhitelist(t *testing.T) {
	app := &App{} // 无 ctx / 无 emit → HasUI() 为 false，不会触碰 Wails runtime

	allowed := []string{
		"https://example.com",
		"http://localhost:8080/path?q=1#frag",
		"HTTPS://Example.COM", // scheme 大小写不敏感
	}
	for _, u := range allowed {
		if err := app.OpenExternalURL(u); err != nil {
			t.Errorf("http/https 应放行，%q 却报错: %v", u, err)
		}
	}

	blocked := []struct {
		url    string
		reason string
	}{
		{"file:///etc/passwd", "本地文件必须拒绝"},
		{"javascript:alert(1)", "脚本伪协议必须拒绝"},
		{"data:text/html,<script>x</script>", "data URL 必须拒绝"},
		{"x-app://open", "自定义 scheme 必须拒绝（白名单而非黑名单）"},
		{"mailto:a@b.com", "未列入白名单的一律拒绝"},
		{"", "空链接"},
		{"   ", "空白链接"},
		{"://nohost", "无 scheme"},
		{"http://", "缺主机名"},
	}
	for _, c := range blocked {
		if err := app.OpenExternalURL(c.url); err == nil {
			t.Errorf("%s（%q）应被拒绝，却放行了", c.reason, c.url)
		}
	}
}

// 拒绝时的错误信息要能看出"是什么被拒了"——排障时不能只看到一句"无效链接"。
func TestOpenExternalURLErrorIsReadable(t *testing.T) {
	app := &App{}
	err := app.OpenExternalURL("file:///etc/passwd")
	if err == nil {
		t.Fatal("file:// 应被拒绝")
	}
	if !strings.Contains(err.Error(), "http") {
		t.Errorf("错误信息应说明只允许 http/https，实际: %v", err)
	}
}
