package media

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===== 图片规整与附件存储 =====
//
// 从根包 imageinput_test.go 迁入：这两段只依赖本包的导出 API。
// 协议组装、read_image 工具、vision 结论等用例仍留在 main（它们要 main 的
// buildLLMMessages / imageLoaderFor / imageTokens 等）。

// makeTestPNG 生成一张可指定尺寸的 PNG。渐变内容避免被 PNG 压成极小体积，
// 也让"是否重编码"这类判断不至于因为内容退化而失真。
func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x % 251),
				G: uint8(y % 241),
				B: uint8((x + y) % 231),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("生成测试 PNG 失败: %v", err)
	}
	return buf.Bytes()
}

// ===== 1. 规整 =====

func TestNormalizeImageKeepsSmallImageUntouched(t *testing.T) {
	raw := makeTestPNG(t, 800, 600)
	got, err := NormalizeImage(raw)
	if err != nil {
		t.Fatalf("规整失败: %v", err)
	}
	if got.Changed {
		t.Error("尺寸与体积都在限内的图片不应被改动（截图里的文字一个像素都不该动）")
	}
	if !bytes.Equal(got.Data, raw) {
		t.Error("未改动的图片应原样返回同一份字节")
	}
	if got.Width != 800 || got.Height != 600 || got.MediaType != "image/png" {
		t.Errorf("元数据不对: %d×%d %s", got.Width, got.Height, got.MediaType)
	}
}

func TestNormalizeImageDownscalesToMaxEdge(t *testing.T) {
	raw := makeTestPNG(t, 3200, 2400)
	got, err := NormalizeImage(raw)
	if err != nil {
		t.Fatalf("规整失败: %v", err)
	}
	if !got.Changed {
		t.Fatal("超尺寸图片必须被标记为已改动")
	}
	if MaxEdgeOf(got.Width, got.Height) != ImageModelMaxEdge {
		t.Fatalf("长边应压到 %d，实际 %d×%d", ImageModelMaxEdge, got.Width, got.Height)
	}
	// 宽高比要保住（2400/3200 = 0.75）
	if got.Height*4 != got.Width*3 {
		t.Errorf("缩放后应保持 4:3，实际 %d×%d", got.Width, got.Height)
	}
	// 声明的尺寸必须与真正编码出来的维度一致，否则 token 估算会算错
	cfg, format, err := image.DecodeConfig(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatalf("产出的字节解不开: %v", err)
	}
	if cfg.Width != got.Width || cfg.Height != got.Height || format != "png" {
		t.Errorf("声明 %d×%d png，实际 %d×%d %s", got.Width, got.Height, cfg.Width, cfg.Height, format)
	}
}

func TestNormalizeImageRejections(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"空内容", nil, "为空"},
		{"非图片", []byte("这是一段文本，不是图片"), "不支持的图片格式"},
		{"WebP", append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 64)...), "不支持的图片格式"},
		{"超过单张上限", make([]byte, MaxImageSourceBytes+1), "过大"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NormalizeImage(c.data); err == nil {
				t.Fatal("应当报错")
			} else if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误信息应包含 %q，实际 %q", c.want, err.Error())
			}
		})
	}
}

// ===== 2. 附件存储 =====

func TestAttachmentStoreContentAddressedAndRoundTrip(t *testing.T) {
	st := NewAttachmentStore(t.TempDir())
	raw := makeTestPNG(t, 400, 300)

	a1, err := st.Save("s1", "截图.png", raw, AttachmentSourceUser)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	// 同一张图再存一次（换个文件名）：内容寻址 → 同一个 ID，磁盘上只有一份
	a2, err := st.Save("s1", "another.png", raw, AttachmentSourceTool)
	if err != nil {
		t.Fatalf("重复保存失败: %v", err)
	}
	if a1.ID != a2.ID {
		t.Fatalf("同一份内容应有相同 ID：%s vs %s", a1.ID, a2.ID)
	}
	if a1.Name != "截图.png" || a2.Name != "another.png" {
		t.Error("展示名应各自保留（它不参与落盘路径）")
	}

	entries, err := os.ReadDir(filepath.Join(st.Root(), "s1"))
	if err != nil {
		t.Fatalf("读附件目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("同一张图应只落一份文件，实际 %d 个", len(entries))
	}

	data, err := st.Load(*a1)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if !bytes.Equal(data, raw) {
		t.Error("读回的字节应与写入一致")
	}

	url, err := st.DataURL("s1", a1.ID)
	if err != nil {
		t.Fatalf("取 data URL 失败: %v", err)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("data URL 前缀不对: %.40s", url)
	}

	// 级联删除：附件不在会话列表里，会话删掉之后没有别的入口能发现它们
	if err := st.DeleteSession("s1"); err != nil {
		t.Fatalf("删除会话附件失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(st.Root(), "s1")); !os.IsNotExist(err) {
		t.Error("删除会话后附件目录应消失")
	}
	if err := st.DeleteSession("s1"); err != nil {
		t.Errorf("重复删除应幂等: %v", err)
	}
}

func TestAttachmentStoreRejectsPathTraversal(t *testing.T) {
	st := NewAttachmentStore(t.TempDir())
	if _, err := st.Save("../evil", "x.png", makeTestPNG(t, 8, 8), AttachmentSourceUser); err == nil {
		t.Fatal("非法会话 ID 应被拒绝")
	}
	if _, err := st.DataURL("s1", "../../etc/passwd"); err == nil {
		t.Fatal("非法附件 ID 应被拒绝")
	}
	// 会话文件是用户可手改的 JSON，Path 等同于外部输入，读取时必须校验
	tampered := Attachment{ID: "x", Name: "x.png", Path: "../../../etc/passwd"}
	if _, err := st.Load(tampered); err == nil || !strings.Contains(err.Error(), "越界") {
		t.Fatalf("越界的附件路径应被拒绝，实际: %v", err)
	}
	// 文件确实丢了要能区分出来（界面据此提示"图片已丢失"，而不是显示破图）
	missing := Attachment{ID: "y", Name: "gone.png", Path: "s1/gone.png"}
	if _, err := st.Load(missing); err == nil || !strings.Contains(err.Error(), "丢失") {
		t.Fatalf("丢失的附件应给出明确原因，实际: %v", err)
	}
}

func TestDecodeUploadAcceptsBothForms(t *testing.T) {
	raw := makeTestPNG(t, 16, 16)
	b64 := base64.StdEncoding.EncodeToString(raw)

	for _, in := range []string{b64, "data:image/png;base64," + b64} {
		got, err := DecodeUpload(in)
		if err != nil {
			t.Fatalf("解析 %q 失败: %v", in[:20], err)
		}
		if !bytes.Equal(got, raw) {
			t.Error("解码结果应与原字节一致")
		}
	}
	if _, err := DecodeUpload(""); err == nil {
		t.Error("空负载应报错")
	}
	if _, err := DecodeUpload("data:text/plain,hello"); err == nil {
		t.Error("非 base64 的 data URL 应报错")
	}
	if _, err := DecodeUpload("!!!not base64!!!"); err == nil {
		t.Error("非法 base64 应报错")
	}
}
