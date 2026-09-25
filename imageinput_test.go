package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ===== 图片输入：从"字节进来"到"发给模型"的全链路测试 =====
//
// 这条链路的四段各自有独立的失败模式，所以分开测：
//
//	1. imageproc       规整（尺寸/格式/上限）——纯计算
//	2. AttachmentStore 落盘与读取（含路径穿越）
//	3. 协议组装        LLMMessage 序列化 / Anthropic 块转换
//	4. 工具与消息       read_image 产图 → 消息附件 → 请求图片
//
// 最值钱的两条是**形状断言**：无图时 LLMMessage 的 JSON 必须与加字段之前完全一致
// （快照/测试/网关都依赖它），以及 Anthropic 侧 tool_result 必须在同一个 user 回合里
// 且排在图片之前（顺序错了 API 直接拒）。

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
	got, err := normalizeImage(raw)
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
	got, err := normalizeImage(raw)
	if err != nil {
		t.Fatalf("规整失败: %v", err)
	}
	if !got.Changed {
		t.Fatal("超尺寸图片必须被标记为已改动")
	}
	if maxEdgeOf(got.Width, got.Height) != imageModelMaxEdge {
		t.Fatalf("长边应压到 %d，实际 %d×%d", imageModelMaxEdge, got.Width, got.Height)
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
		{"超过单张上限", make([]byte, maxImageSourceBytes+1), "过大"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := normalizeImage(c.data); err == nil {
				t.Fatal("应当报错")
			} else if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误信息应包含 %q，实际 %q", c.want, err.Error())
			}
		})
	}
}

func TestImageTokensFollowPixelCountNotBytes(t *testing.T) {
	// Anthropic 口径：(宽 × 高) / 750，向上取整
	if got := imageTokens(1568, 1568); got != 3278 {
		t.Errorf("1568×1568 应约 3278 token，实际 %d", got)
	}
	if got := imageTokens(0, 0); got != 0 {
		t.Errorf("缺尺寸时不应凭空计费，实际 %d", got)
	}
	// 关键性质：一张大尺寸的图必须比一张小尺寸的贵，与字节数无关
	big := storedMessageTokens(Message{
		Role:        RoleUser,
		Content:     "看图",
		Attachments: []Attachment{{Width: 4000, Height: 3000}},
	})
	small := storedMessageTokens(Message{
		Role:        RoleUser,
		Content:     "看图",
		Attachments: []Attachment{{Width: 200, Height: 200}},
	})
	if big <= small {
		t.Fatalf("大图应比小图贵：%d vs %d", big, small)
	}
}

// ===== 2. 附件存储 =====

func TestAttachmentStoreContentAddressedAndRoundTrip(t *testing.T) {
	store := NewAttachmentStore(t.TempDir())
	raw := makeTestPNG(t, 400, 300)

	a1, err := store.Save("s1", "截图.png", raw, attachmentSourceUser)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	// 同一张图再存一次（换个文件名）：内容寻址 → 同一个 ID，磁盘上只有一份
	a2, err := store.Save("s1", "another.png", raw, attachmentSourceTool)
	if err != nil {
		t.Fatalf("重复保存失败: %v", err)
	}
	if a1.ID != a2.ID {
		t.Fatalf("同一份内容应有相同 ID：%s vs %s", a1.ID, a2.ID)
	}
	if a1.Name != "截图.png" || a2.Name != "another.png" {
		t.Error("展示名应各自保留（它不参与落盘路径）")
	}

	entries, err := os.ReadDir(filepath.Join(store.Root(), "s1"))
	if err != nil {
		t.Fatalf("读附件目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("同一张图应只落一份文件，实际 %d 个", len(entries))
	}

	data, err := store.Load(*a1)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	if !bytes.Equal(data, raw) {
		t.Error("读回的字节应与写入一致")
	}

	url, err := store.DataURL("s1", a1.ID)
	if err != nil {
		t.Fatalf("取 data URL 失败: %v", err)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("data URL 前缀不对: %.40s", url)
	}

	// 级联删除：附件不在会话列表里，会话删掉之后没有别的入口能发现它们
	if err := store.DeleteSession("s1"); err != nil {
		t.Fatalf("删除会话附件失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "s1")); !os.IsNotExist(err) {
		t.Error("删除会话后附件目录应消失")
	}
	if err := store.DeleteSession("s1"); err != nil {
		t.Errorf("重复删除应幂等: %v", err)
	}
}

func TestAttachmentStoreRejectsPathTraversal(t *testing.T) {
	store := NewAttachmentStore(t.TempDir())
	if _, err := store.Save("../evil", "x.png", makeTestPNG(t, 8, 8), attachmentSourceUser); err == nil {
		t.Fatal("非法会话 ID 应被拒绝")
	}
	if _, err := store.DataURL("s1", "../../etc/passwd"); err == nil {
		t.Fatal("非法附件 ID 应被拒绝")
	}
	// 会话文件是用户可手改的 JSON，Path 等同于外部输入，读取时必须校验
	tampered := Attachment{ID: "x", Name: "x.png", Path: "../../../etc/passwd"}
	if _, err := store.Load(tampered); err == nil || !strings.Contains(err.Error(), "越界") {
		t.Fatalf("越界的附件路径应被拒绝，实际: %v", err)
	}
	// 文件确实丢了要能区分出来（界面据此提示"图片已丢失"，而不是显示破图）
	missing := Attachment{ID: "y", Name: "gone.png", Path: "s1/gone.png"}
	if _, err := store.Load(missing); err == nil || !strings.Contains(err.Error(), "丢失") {
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

// ===== 3. 协议组装 =====

// 无图时 LLMMessage 的 JSON 必须与加 Images 字段之前**逐字节一致**：
// 请求快照、既有测试、各家网关都依赖这个形状。
func TestLLMMessageMarshalWithoutImagesKeepsLegacyShape(t *testing.T) {
	msg := LLMMessage{
		Role:    RoleAssistant,
		Content: "hi",
		ToolCalls: []LLMToolCall{{
			ID: "c1", Type: ToolTypeFunction,
			Function: LLMToolFunction{Name: "grep", Arguments: `{"pattern":"x"}`},
		}},
	}
	got, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	type legacy struct {
		Role      string        `json:"role"`
		Content   string        `json:"content"`
		ToolCalls []LLMToolCall `json:"tool_calls,omitempty"`
	}
	want, _ := json.Marshal(legacy{Role: msg.Role, Content: msg.Content, ToolCalls: msg.ToolCalls})
	if string(got) != string(want) {
		t.Fatalf("无图消息的 JSON 形状变了：\n got=%s\nwant=%s", got, want)
	}
	if strings.Contains(string(got), "image") {
		t.Error("无图消息不应出现任何图片字段")
	}
}

func TestLLMMessageMarshalWithImagesUsesBlockArray(t *testing.T) {
	msg := LLMMessage{
		Role:    RoleUser,
		Content: "这张报错图怎么回事",
		Images:  []LLMImage{{MediaType: "image/png", Data: []byte("PNGBYTES"), Bytes: 8}},
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var got struct {
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("有图消息的 content 应是块数组，实际: %s（%v）", raw, err)
	}
	if got.Role != RoleUser || len(got.Content) != 2 {
		t.Fatalf("期望 1 条文本 + 1 张图，实际 %s", raw)
	}
	if got.Content[0].Type != "text" || got.Content[0].Text != "这张报错图怎么回事" {
		t.Errorf("文本块不对: %+v", got.Content[0])
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("PNGBYTES"))
	if got.Content[1].Type != "image_url" || got.Content[1].ImageURL.URL != want {
		t.Errorf("图片块应为 data URL，实际 %+v", got.Content[1])
	}
}

func TestAnthropicUserMessageCarriesImageBlock(t *testing.T) {
	req := &LLMReq{
		Model: "m",
		Messages: []LLMMessage{{
			Role:    RoleUser,
			Content: "看这张图",
			Images:  []LLMImage{{MediaType: "image/jpeg", Data: []byte("JPEGDATA")}},
		}},
	}
	out := toAnthropicRequest(req)
	if len(out.Messages) != 1 {
		t.Fatalf("应有 1 条消息，实际 %d", len(out.Messages))
	}
	blocks, ok := out.Messages[0].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("content 应为块数组，实际 %T", out.Messages[0].Content)
	}
	if len(blocks) != 2 || blocks[0].Type != "text" || blocks[1].Type != "image" {
		t.Fatalf("应为 [text, image]，实际 %+v", blocks)
	}
	src := blocks[1].Source
	if src == nil || src.Type != "base64" || src.MediaType != "image/jpeg" {
		t.Fatalf("image 块应带 base64 source，实际 %+v", src)
	}
	// Anthropic 要**裸 base64**，带上 "data:...;base64," 前缀会被网关拒
	if strings.HasPrefix(src.Data, "data:") {
		t.Error("Anthropic 的 source.data 不能带 data URL 前缀")
	}
	if src.Data != base64.StdEncoding.EncodeToString([]byte("JPEGDATA")) {
		t.Errorf("base64 内容不对: %s", src.Data)
	}
}

// 工具产出的图在 Anthropic 侧必须落在**同一个 user 回合**里，且 tool_result 排在图片之前。
// 这条一旦破了，表现是 API 直接 400（tool_result 必须在内容块最前），而不是悄悄少一张图。
func TestAnthropicMergesToolResultAndToolImageIntoOneTurn(t *testing.T) {
	store := NewAttachmentStore(t.TempDir())
	att, err := store.Save("s1", "shot.png", makeTestPNG(t, 64, 64), attachmentSourceTool)
	if err != nil {
		t.Fatalf("存图失败: %v", err)
	}
	history := []Message{
		{ID: "m1", Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: toolReadImage, Status: "success"}}},
		{ID: "m2", Role: RoleTool, Content: "已读取图片", ToolCallID: "c1", Attachments: []Attachment{*att}},
		{ID: "m3", Role: RoleUser, Content: "接下来怎么办"},
	}
	neutral := buildLLMMessages(history, "SYS", store.LoadImages)
	// 中立形状：tool 文本 + 一条承载图片的 user 消息（OpenAI 的 tool 消息装不下图片）
	if len(neutral) != 5 {
		t.Fatalf("中立序列应有 system + assistant + tool + 图片 user + 用户提问，实际 %d 条", len(neutral))
	}
	if neutral[3].Role != RoleUser || len(neutral[3].Images) != 1 {
		t.Fatalf("第 4 条应是承载图片的 user 消息，实际 %+v", neutral[3])
	}
	if !strings.Contains(neutral[3].Content, toolReadImage) {
		t.Errorf("图片前的说明应点名是哪个工具给的图，实际 %q", neutral[3].Content)
	}

	out := toAnthropicRequest(&LLMReq{Model: "m", Messages: neutral})
	if len(out.Messages) != 2 {
		t.Fatalf("Anthropic 侧应合并成 assistant + user 两条，实际 %d 条", len(out.Messages))
	}
	user := out.Messages[1]
	if user.Role != RoleUser {
		t.Fatalf("第 2 条应是 user，实际 %q", user.Role)
	}
	blocks, ok := user.Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("content 应为块数组，实际 %T", user.Content)
	}
	if blocks[0].Type != "tool_result" {
		t.Fatalf("tool_result 必须排在内容块最前，实际第 0 块是 %q", blocks[0].Type)
	}
	hasImage := false
	for _, b := range blocks {
		if b.Type == "image" {
			hasImage = true
		}
	}
	if !hasImage {
		t.Fatalf("同一个 user 回合里应带上工具产出的图片，实际 %+v", blocks)
	}
}

// 图读不出来时必须留下痕迹：静默跳过会让模型对着一张不存在的图瞎答。
func TestBuildLLMMessagesDegradesWhenImageMissing(t *testing.T) {
	store := NewAttachmentStore(t.TempDir())
	history := []Message{{
		ID: "m1", Role: RoleUser, Content: "看这张图",
		Attachments: []Attachment{{ID: "dead", Kind: attachmentKindImage, Name: "gone.png", Path: "s1/gone.png"}},
	}}

	msgs := buildLLMMessages(history, "SYS", store.LoadImages)
	if len(msgs[1].Images) != 0 {
		t.Fatal("读不到的图不该被当成图片送出去")
	}
	if !strings.Contains(msgs[1].Content, "未能载入") {
		t.Fatalf("应把失败写进消息文本，实际 %q", msgs[1].Content)
	}
	if !strings.Contains(msgs[1].Content, "看这张图") {
		t.Error("原始正文不能被附注覆盖掉")
	}

	// 装配缺失（loader 为 nil）同样要显式说明，而不是静默无声
	nilLoader := buildLLMMessages(history, "SYS", nil)
	if !strings.Contains(nilLoader[1].Content, "未装配附件存储") {
		t.Fatalf("没有附件存储时应明确说明，实际 %q", nilLoader[1].Content)
	}
}

// 纯图片消息（正文为空）序列化后**必须同时带一个文本块**。
//
// 真机故障回归：内容数组里只有 image 块时模型读不到图，而同一次对话里
// read_image 工具结果的图（带一句说明文本）能读到——差别正是有没有文本块。
// 这条一旦被改回去，就又会变成"用户贴的图看不见、但工具读的图看得见"这种极难查的现象。
func TestImageOnlyMessageCarriesTextPart(t *testing.T) {
	msg := LLMMessage{Role: RoleUser, Images: []LLMImage{{MediaType: "image/jpeg", Data: []byte("JPEG")}}}

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var got struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("content 应为块数组: %v（%s）", err, raw)
	}
	if len(got.Content) != 2 || got.Content[0].Type != "text" {
		t.Fatalf("纯图片消息也应补一个 text 块且排在最前，实际: %s", raw)
	}
	if strings.TrimSpace(got.Content[0].Text) == "" {
		t.Fatal("补出来的文本块不能是空串——空串等于没补")
	}

	// Anthropic 侧同一条规则（两条协议共用 textPartFor）
	out := toAnthropicRequest(&LLMReq{Model: "m", Messages: []LLMMessage{msg}})
	blocks, ok := out.Messages[0].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("content 应为块数组，实际 %T", out.Messages[0].Content)
	}
	if len(blocks) != 2 || blocks[0].Type != "text" || blocks[1].Type != "image" {
		t.Fatalf("Anthropic 侧应为 [text, image]，实际 %+v", blocks)
	}

	// 有正文时不得被占位顶掉
	withText := LLMMessage{Role: RoleUser, Content: "这个报错怎么回事", Images: msg.Images}
	if textPartFor(withText.Content, withText.Images) != "这个报错怎么回事" {
		t.Error("有正文时不该塞占位文本")
	}
}

// ===== 3.5 请求快照脱敏 =====

func TestRedactMessagesDropsImageBytes(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("SUPER-SECRET-IMAGE-BYTES"))
	msgs := []LLMMessage{{
		Role: RoleUser, Content: "看图",
		Images: []LLMImage{{MediaType: "image/png", Data: []byte("SUPER-SECRET-IMAGE-BYTES"), Bytes: 24, Width: 10, Height: 10}},
	}}
	out := redactMessages(msgs)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("快照里不应出现图片的 base64 内容")
	}
	if !strings.Contains(string(raw), "图片已省略") || !strings.Contains(string(raw), "10×10") {
		t.Fatalf("应保留「这里有一张多大的图」这一信息，实际 %s", raw)
	}
	// 脱敏是拷贝，不能改到调用方手上的那份（它还要真的发出去）
	if len(msgs[0].Images[0].Data) == 0 || msgs[0].Images[0].Redacted {
		t.Fatal("redactMessages 不该改动原消息")
	}
}

// ===== 5. read_image 工具 =====

func TestReadImageToolProducesAttachment(t *testing.T) {
	dir := t.TempDir()
	raw := makeTestPNG(t, 3200, 2400) // 故意超尺寸，验证走的是规整后的那份
	imgPath := filepath.Join(dir, "screenshot.png")
	if err := os.WriteFile(imgPath, raw, 0o644); err != nil {
		t.Fatalf("写测试图片失败: %v", err)
	}

	store := NewAttachmentStore(t.TempDir())
	tm, _ := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir:  dir,
		SessionID:   "s1",
		Enforcer:    AllowAllEnforcer{},
		Attachments: store,
	})

	out, err := view.ExecuteTool(toolReadImage, map[string]interface{}{"path": "screenshot.png"})
	if err != nil {
		t.Fatalf("read_image 失败: %v", err)
	}
	if !strings.Contains(out, "screenshot.png") || !strings.Contains(out, "已等比缩小") {
		t.Fatalf("结果文本应说明读了哪张图以及做过缩放，实际: %s", out)
	}

	produced := view.TakeImages()
	if len(produced) != 1 {
		t.Fatalf("应产出 1 张图，实际 %d", len(produced))
	}
	att := produced[0]
	if att.Source != attachmentSourceTool {
		t.Errorf("来源应记为 tool，实际 %q", att.Source)
	}
	if maxEdgeOf(att.Width, att.Height) != imageModelMaxEdge {
		t.Errorf("存下来的应是规整后的尺寸，实际 %d×%d", att.Width, att.Height)
	}
	// 取走之后不能再取出第二次（"执行一次取一次"是归因准确的前提）
	if again := view.TakeImages(); len(again) != 0 {
		t.Errorf("take 之后应为空，实际 %d", len(again))
	}
	// 落盘的那份能读回来（发请求时走的就是 store.Load）
	if _, err := store.Load(att); err != nil {
		t.Fatalf("产出的附件应可读回: %v", err)
	}
}

func TestReadImageToolNotRegisteredWithoutStore(t *testing.T) {
	dir := t.TempDir()
	tm, _ := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir: dir,
		SessionID:  "s1",
		Enforcer:   AllowAllEnforcer{},
		// 刻意不给 Attachments：没有地方存图，注册出来只会每次调用都失败
	})
	for _, d := range view.GetToolsForLLM() {
		if d.Function.Name == toolReadImage {
			t.Fatal("没有附件存储时 read_image 不应注册")
		}
	}
	if _, err := view.ExecuteTool(toolReadImage, map[string]interface{}{"path": "x.png"}); err == nil {
		t.Fatal("未注册的工具调用应报「未找到工具」")
	}
}

func TestReadImageToolRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("这不是图片"), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	store := NewAttachmentStore(t.TempDir())
	tm, _ := newTestToolManager(t)
	view := tm.BuildView(context.Background(), BuildOptions{
		ProjectDir:  dir,
		SessionID:   "s1",
		Enforcer:    AllowAllEnforcer{},
		Attachments: store,
	})
	if _, err := view.ExecuteTool(toolReadImage, map[string]interface{}{"path": "notes.txt"}); err == nil {
		t.Fatal("非图片文件应被拒绝（并提示改用 read_file）")
	}
	if len(view.TakeImages()) != 0 {
		t.Error("失败的调用不应产出图片")
	}
}

// ===== 6.5 看图能力结论（/vision 的落盘与失效）=====

// 结论是**关于端点**的，不是关于配置名的：换了 ModelID 或 URL 就必须失效，
// 否则用户换好端点之后，系统提示词还会继续告诉模型"你看不到图"——
// 在用户刚修好的时候说反话，比不说更糟。
func TestVisionVerdictBindsToEndpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vision-check.json")
	store := NewVisionVerdictStore(path)
	store.Set("m1", "glm-5.2-discount", "https://gw/a", false, "无法回答")

	if !store.Unsupported("m1", "glm-5.2-discount", "https://gw/a") {
		t.Fatal("同一端点应判为未通过")
	}
	if store.Unsupported("m1", "glm-5.2-discount", "https://gw/b") {
		t.Fatal("URL 变了，旧结论必须失效")
	}
	if store.Unsupported("m1", "glm-4v", "https://gw/a") {
		t.Fatal("ModelID 变了，旧结论必须失效")
	}
	// "没测过"与"测过且未通过"必须区分：前者不该影响任何行为
	if _, ok := store.Get("m2", "x", "y"); ok {
		t.Fatal("没测过的模型应当返回「无结论」")
	}
	if store.Unsupported("m2", "x", "y") {
		t.Fatal("没测过不该被当成看不到图")
	}

	// 结论要落盘：重开应用后仍应生效（否则用户每次都得重测）
	reloaded := NewVisionVerdictStore(path)
	if !reloaded.Unsupported("m1", "glm-5.2-discount", "https://gw/a") {
		t.Fatal("结论应落盘并在重启后仍生效")
	}

	// 通过时不产生警告
	store.Set("m3", "glm-4v", "https://gw/a", true, "红,绿,蓝")
	if store.Unsupported("m3", "glm-4v", "https://gw/a") {
		t.Fatal("自检通过的模型不该被标记为看不到图")
	}
}

// modelCallID 的回落口径必须只有一处：写入（/vision）与读取（工具循环）各写一份的话，
// 只要一边漏了"为空则用 Name"，键就对不上，表现为"测过了但警告永远不出现"。
func TestModelCallIDFallback(t *testing.T) {
	if got := modelCallID(&Model{Name: "n", ModelID: "id"}); got != "id" {
		t.Errorf("配了 ModelID 时应用它，实际 %q", got)
	}
	if got := modelCallID(&Model{Name: "n"}); got != "n" {
		t.Errorf("没配 ModelID 时应回落到配置名，实际 %q", got)
	}
	if got := modelCallID(nil); got != "" {
		t.Errorf("nil 应返回空串，实际 %q", got)
	}
}

// ===== 7. 消息落库：纯图片消息的标题 =====

func TestImageOnlyMessageStillGetsATitle(t *testing.T) {
	store := NewSessionStore(t.TempDir())
	sess, err := store.CreateSession(SessionConfig{Project: t.TempDir()})
	if err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	if _, err := store.AppendMessage(sess.ID, Message{
		Role:        RoleUser,
		Content:     "",
		Attachments: []Attachment{{ID: "a1", Kind: attachmentKindImage, Name: "报错截图.png"}},
	}); err != nil {
		t.Fatalf("追加消息失败: %v", err)
	}
	got, err := store.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("读会话失败: %v", err)
	}
	if got.Title != "报错截图.png" {
		t.Fatalf("纯图片消息应拿附件名当标题，实际 %q（空标题会让会话列表出现一行空白）", got.Title)
	}
}
