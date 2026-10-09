package store

import (
	"path/filepath"
	"testing"

	"wails-tmp/internal/media"
)

// 本文件从根包 imageinput_test.go 拆出：看图结论的落盘/失效与"纯图片消息拿附件名当标题"。

// ===== 6.5 看图能力结论（/vision 的落盘与失效）=====

// 结论是**关于端点**的，不是关于配置名的：换了 ModelID 或 URL 就必须失效，
// 否则用户换好端点之后，系统提示词还会继续告诉模型"你看不到图"——
// 在用户刚修好的时候说反话，比不说更糟。
func TestVisionVerdictBindsToEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vision-check.json")
	vs := NewVisionVerdictStore(path)
	vs.Set("m1", "glm-5.2-discount", "https://gw/a", false, "无法回答")

	if !vs.Unsupported("m1", "glm-5.2-discount", "https://gw/a") {
		t.Fatal("同一端点应判为未通过")
	}
	if vs.Unsupported("m1", "glm-5.2-discount", "https://gw/b") {
		t.Fatal("URL 变了，旧结论必须失效")
	}
	if vs.Unsupported("m1", "glm-4v", "https://gw/a") {
		t.Fatal("ModelID 变了，旧结论必须失效")
	}
	// "没测过"与"测过且未通过"必须区分：前者不该影响任何行为
	if _, ok := vs.Get("m2", "x", "y"); ok {
		t.Fatal("没测过的模型应当返回「无结论」")
	}
	if vs.Unsupported("m2", "x", "y") {
		t.Fatal("没测过不该被当成看不到图")
	}

	// 结论要落盘：重开应用后仍应生效（否则用户每次都得重测）
	reloaded := NewVisionVerdictStore(path)
	if !reloaded.Unsupported("m1", "glm-5.2-discount", "https://gw/a") {
		t.Fatal("结论应落盘并在重启后仍生效")
	}

	// 通过时不产生警告
	vs.Set("m3", "glm-4v", "https://gw/a", true, "红,绿,蓝")
	if vs.Unsupported("m3", "glm-4v", "https://gw/a") {
		t.Fatal("自检通过的模型不该被标记为看不到图")
	}
}

// modelCallID 的回落口径必须只有一处：写入（/vision）与读取（工具循环）各写一份的话，
// 只要一边漏了"为空则用 Name"，键就对不上，表现为"测过了但警告永远不出现"。
func TestModelCallIDFallback(t *testing.T) {
	if got := ModelCallID(&Model{Name: "n", ModelID: "id"}); got != "id" {
		t.Errorf("配了 ModelID 时应用它，实际 %q", got)
	}
	if got := ModelCallID(&Model{Name: "n"}); got != "n" {
		t.Errorf("没配 ModelID 时应回落到配置名，实际 %q", got)
	}
	if got := ModelCallID(nil); got != "" {
		t.Errorf("nil 应返回空串，实际 %q", got)
	}
}

// ===== 7. 消息落库：纯图片消息的标题 =====

func TestImageOnlyMessageStillGetsATitle(t *testing.T) {
	st := NewSessionStore(t.TempDir())
	sess, err := st.CreateSession(SessionConfig{Project: t.TempDir()})
	if err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	if _, err := st.AppendMessage(sess.ID, Message{
		Role:        RoleUser,
		Content:     "",
		Attachments: []media.Attachment{{ID: "a1", Kind: media.AttachmentKindImage, Name: "报错截图.png"}},
	}); err != nil {
		t.Fatalf("追加消息失败: %v", err)
	}
	got, err := st.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("读会话失败: %v", err)
	}
	if got.Title != "报错截图.png" {
		t.Fatalf("纯图片消息应拿附件名当标题，实际 %q（空标题会让会话列表出现一行空白）", got.Title)
	}
}
