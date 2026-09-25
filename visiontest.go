package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"strings"
)

// ===== 模型看图能力自检（/vision）=====
//
// 为什么需要它：真机上出过一次极难查的故障——本应用把图片**正确地**放进了请求
// （「请求」快照里能看到那条带 `[图片] 图片已省略：…` 的消息），但模型说它
// "实际接收到的内容里，图片仍然只以 URL 链接的形式出现"，也就是**没有拿到像素**。
//
// 问题在于：从外面**看不出**一个端点是否把图像交给了模型。模型自己的说法也不可靠
// ——它完全可能是在顺着上下文里的链接编解释（这场故障里它就编过一次）。
// 所以这个命令把判断变成一次实测：
//
//	探针 1（对照，纯文本）：端点通不通？
//	探针 2（文本 + 图，与真实链路**完全同形**）：图像能不能被读出来？
//
// **对照探针是关键**：只有它通过、图像探针失败，结论才能干净地落在"端点不接受图像"上；
// 否则可能是鉴权/网络/模型名写错，那些用「测试连接」排除更合适。
//
// 设计成斜杠命令而不是设置页里的按钮：它走的正是聊天那条真实链路（同一个模型、
// 同一套协议适配、同一条消息组装路径），结论因此对"我贴图时能不能用"直接有效；
// 也不需要新增 Wails 绑定。
const visionTestCmd = "vision"

// visionProbeQuestion 要求按顺序只回答颜色，便于机械判定。
// 用"左中右三个竖条"而不是更复杂的图形：判定不需要字体渲染或空间推理。
const visionProbeQuestion = "这张图从左到右由三个纯色竖条组成。" +
	"请只按顺序回答这三个颜色的中文名称，用逗号分隔，不要任何解释。"

// visionTextProbeQuestion 对照组。要求一个固定答案，便于判定端点本身是否可用。
const visionTextProbeQuestion = "请只回答两个字：可以"

// visionProbeSystem 把角色压到最小，避免它去干别的
const visionProbeSystem = "你是一个自检助手。只按要求回答，不要解释，不要调用工具。"

// visionProbeColors 测试图从左到右的颜色，以及每个颜色可接受的回答片段。
// 判定用"包含"而不是精确匹配：不同模型会写"红色"或"红"。
var visionProbeColors = []struct {
	name string
	rgb  color.NRGBA
	keys []string
}{
	{"红", color.NRGBA{R: 220, G: 40, B: 40, A: 255}, []string{"红", "red"}},
	{"绿", color.NRGBA{R: 40, G: 190, B: 70, A: 255}, []string{"绿", "green"}},
	{"蓝", color.NRGBA{R: 40, G: 90, B: 220, A: 255}, []string{"蓝", "blue"}},
}

// makeVisionProbe 生成测试图并返回可发送的图片载荷。
//
// 用纯色块而不是文字/图形：判定只依赖颜色名，任何字体渲染、OCR、空间推理的差异
// 都不会干扰"能不能看到图"这个结论。JPEG 编码让体积稳定在几十 KB。
func makeVisionProbe() (LLMImage, error) {
	const (
		probeW = 480
		probeH = 180
	)
	img := image.NewNRGBA(image.Rect(0, 0, probeW, probeH))
	stripe := probeW / len(visionProbeColors)
	for i, c := range visionProbeColors {
		x0 := i * stripe
		x1 := x0 + stripe
		if i == len(visionProbeColors)-1 {
			x1 = probeW // 最后一格吃掉除不尽的余数
		}
		draw.Draw(img, image.Rect(x0, 0, x1, probeH),
			image.NewUniform(c.rgb), image.Point{}, draw.Src)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		return LLMImage{}, fmt.Errorf("生成测试图失败: %v", err)
	}
	return LLMImage{
		MediaType: "image/jpeg",
		Data:      buf.Bytes(),
		Bytes:     buf.Len(),
		Width:     probeW,
		Height:    probeH,
	}, nil
}

// judgeVisionAnswer 判定回答里是否认出了三种颜色。
// 返回 (认出的颜色名, 缺失的颜色名)。
func judgeVisionAnswer(answer string) (hit, missing []string) {
	low := strings.ToLower(answer)
	for _, c := range visionProbeColors {
		found := false
		for _, k := range c.keys {
			if strings.Contains(low, k) {
				found = true
				break
			}
		}
		if found {
			hit = append(hit, c.name)
		} else {
			missing = append(missing, c.name)
		}
	}
	return hit, missing
}

// probeOnce 发一次探针请求，返回模型回答。
func probeOnce(ctx context.Context, model *Model, modelID, question string, img *LLMImage) (string, error) {
	msg := LLMMessage{Role: RoleUser, Content: question}
	if img != nil {
		msg.Images = []LLMImage{*img}
	}
	req := &LLMReq{
		Model:       modelID,
		Temperature: 0,
		Messages: []LLMMessage{
			{Role: RoleSystem, Content: visionProbeSystem},
			msg,
		},
	}
	resp, err := callLLMForModel(ctx, model, req)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("返回了空响应")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

// handleVisionTestCommand 处理 /vision：用当前会话的模型实测"能不能看图"。
func (a *App) handleVisionTestCommand(sessionID string) (*ChatResult, error) {
	session, err := a.sessionStore.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("加载会话失败: %v", err)
	}
	// 必须 GetModelForCall：这里要真发请求，脱敏 Key 会被网关拒
	model, err := a.modelStore.GetModelForCall(session.Model)
	if err != nil {
		return nil, fmt.Errorf("获取模型配置失败: %v", err)
	}
	// 用与工具循环同一口径解析模型 ID（见 modelCallID 的说明：口径不一致会让结论永远匹配不上）
	modelID := modelCallID(&model)
	probe, err := makeVisionProbe()
	if err != nil {
		return nil, err
	}

	// 与普通对话一样登记为可取消的运行：自检也要能被「停止」断掉
	run := a.runs.begin(sessionID, "")
	defer a.runs.end(run)
	ctx := run.Ctx()

	var b strings.Builder
	// 这一行是给模型看的：它会把自检结果留在历史里，而历史里的测试图结论
	// 曾被它误当成"用户那张截图的内容"。把作用域写死，避免再次串味。
	b.WriteString("看图能力自检（用的是**自动生成的合成测试图**，与用户发过的任何图片无关）\n\n")
	fmt.Fprintf(&b, "模型：%s\n\n", modelID)

	// ---- 探针 1：纯文本对照 ----
	textOK := false
	textAnswer, textErr := probeOnce(ctx, &model, modelID, visionTextProbeQuestion, nil)
	switch {
	case textErr != nil:
		fmt.Fprintf(&b, "1）纯文本对照：**失败**（请求没能完成）\n   错误：%v\n", textErr)
	case strings.Contains(textAnswer, "可以"):
		textOK = true
		b.WriteString("1）纯文本对照：通过\n")
	default:
		fmt.Fprintf(&b, "1）纯文本对照：**异常**（回答里没有「可以」）\n   它的回答：%s\n", textAnswer)
	}

	// ---- 探针 2：文本 + 图（与真实链路同形）----
	imgAnswer, imgErr := probeOnce(ctx, &model, modelID, visionProbeQuestion, &probe)
	hit, missing := judgeVisionAnswer(imgAnswer)
	imgOK := imgErr == nil && len(missing) == 0 && len(hit) > 0

	switch {
	case imgErr != nil:
		fmt.Fprintf(&b, "2）文本 + 图：**失败**（请求没能完成）\n   错误：%v\n", imgErr)
		b.WriteString("   若错误里提到 content / image / 格式不合法，说明这个端点不接受图像输入。\n")
	case imgOK:
		fmt.Fprintf(&b, "2）文本 + 图：**通过** ✅\n   它正确读出了测试图里的三种颜色（%s）。\n",
			strings.Join(hit, "、"))
	default:
		b.WriteString("2）文本 + 图：**未通过** ❌\n")
		b.WriteString("   测试图从左到右是【红、绿、蓝】三个纯色竖条；它的回答是：\n")
		if strings.TrimSpace(imgAnswer) == "" {
			b.WriteString("   > （空）\n")
		} else {
			fmt.Fprintf(&b, "   > %s\n", imgAnswer)
		}
		fmt.Fprintf(&b, "   没能认出：%s\n", strings.Join(missing, "、"))
	}

	// ---- 结论 ----
	b.WriteString("\n**结论**：")
	switch {
	case imgOK:
		b.WriteString("这个端点能接受图像输入，本应用的图片链路（含协议适配）是通的。\n" +
			"如果你仍看到模型说「看不到图 / 只看到链接」，请把那次会话的「请求」快照发出来" +
			"——那说明问题在别处，而不在端点能力上。")
	case imgErr != nil && textErr != nil:
		b.WriteString("两次请求都没完成，先用设置里的「测试连接」把鉴权/网络/模型名问题排除掉再重测。")
	case !textOK:
		b.WriteString("连纯文本对照都没通过，先排除端点本身的问题（见上），再看图像探针。")
	default:
		b.WriteString("**端点能通文本、但读不出图像内容。** 本应用已经把图放进了请求" +
			"（「请求」里能看到 `[图片] 图片已省略：…` 那条），是这个模型/网关没有把图像交给模型。\n" +
			"要能看图，需要换一个支持视觉的端点；若必须走这个网关，图片输入当前无法工作。")
	}

	// 把结论落盘：它有两个用处——下次跑 /vision 前先看历史结论；以及**在用户贴图时
	// 提前告知"这个模型看不到图"**（见 visionUnsupportedNote），免得模型去 curl、编造内容。
	// 键用会话里存的模型名（会话引用的是它），值里另记实际请求用的模型 ID。
	//
	// 只在**能下结论**时记录：
	//   - 图像探针拿到了回答 → 有判定，记；
	//   - 图像探针报错、但纯文本对照通过 → 说明端点拒的是图像（不是鉴权/网络），也记"未通过"；
	//   - 两个都失败 → 端点是坏的，此时关于"看不看图"什么也推不出来，不记。
	if imgErr == nil || textOK {
		a.visionVerdicts.Set(session.Model, modelID, model.URL, imgOK, imgAnswer)
	}

	reply := b.String()
	result := &ChatResult{Reply: reply}
	// 与 /compact、/context-stat 一致：落一条 assistant 消息，保持 user/assistant 成对，
	// 也让这次自检留在会话记录里（它是一份结论，日后能查）。
	if saved, serr := a.sessionStore.AppendMessage(sessionID, Message{Role: RoleAssistant, Content: reply}); serr == nil {
		result.Messages = []Message{*saved}
	}
	a.emitChatEvent(ChatEvent{Type: "done", Reply: reply})
	result.Context = a.contextStatForSession(sessionID)
	return result, nil
}
