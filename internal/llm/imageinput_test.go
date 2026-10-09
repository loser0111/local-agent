package llm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"wails-tmp/internal/store"
)

// 本文件从根包 imageinput_test.go 拆出：只覆盖 llm 协议组装一节的纯用例。
// read_image 工具装配、buildLLMMessages 等要根包能力的用例仍留在根包。

// ===== 3. 协议组装 =====

// 无图时 LLMMessage 的 JSON 必须与加 Images 字段之前**逐字节一致**：
// 请求快照、既有测试、各家网关都依赖这个形状。
func TestLLMMessageMarshalWithoutImagesKeepsLegacyShape(t *testing.T) {
	msg := LLMMessage{
		Role:    store.RoleAssistant,
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
		Role:    store.RoleUser,
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
	if got.Role != store.RoleUser || len(got.Content) != 2 {
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
			Role:    store.RoleUser,
			Content: "看这张图",
			Images:  []LLMImage{{MediaType: "image/jpeg", Data: []byte("JPEGDATA")}},
		}},
	}
	out := ToAnthropicRequest(req)
	if len(out.Messages) != 1 {
		t.Fatalf("应有 1 条消息，实际 %d", len(out.Messages))
	}
	blocks, ok := out.Messages[0].Content.([]AnthropicContentBlock)
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

// 纯图片消息（正文为空）序列化后**必须同时带一个文本块**。
//
// 真机故障回归：内容数组里只有 image 块时模型读不到图，而同一次对话里
// read_image 工具结果的图（带一句说明文本）能读到——差别正是有没有文本块。
// 这条一旦被改回去，就又会变成"用户贴的图看不见、但工具读的图看得见"这种极难查的现象。
func TestImageOnlyMessageCarriesTextPart(t *testing.T) {
	msg := LLMMessage{Role: store.RoleUser, Images: []LLMImage{{MediaType: "image/jpeg", Data: []byte("JPEG")}}}

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

	// Anthropic 侧同一条规则（两条协议共用 TextPartFor）
	out := ToAnthropicRequest(&LLMReq{Model: "m", Messages: []LLMMessage{msg}})
	blocks, ok := out.Messages[0].Content.([]AnthropicContentBlock)
	if !ok {
		t.Fatalf("content 应为块数组，实际 %T", out.Messages[0].Content)
	}
	if len(blocks) != 2 || blocks[0].Type != "text" || blocks[1].Type != "image" {
		t.Fatalf("Anthropic 侧应为 [text, image]，实际 %+v", blocks)
	}

	// 有正文时不得被占位顶掉
	withText := LLMMessage{Role: store.RoleUser, Content: "这个报错怎么回事", Images: msg.Images}
	if TextPartFor(withText.Content, withText.Images) != "这个报错怎么回事" {
		t.Error("有正文时不该塞占位文本")
	}
}
