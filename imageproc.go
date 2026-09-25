package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"

	// 注册解码器：image.Decode / image.DecodeConfig 按注册表分派，
	// 不 import 对应包就会出现"格式未注册"这种看不出原因的失败。
	_ "image/gif"
)

// ===== 图片规整管线（纯计算：嗅探 → 解码 → 缩放 → 重编码，无 IO）=====
//
// 为什么在**入站**就规整，而不是发请求时再转：
//
//  1. 视觉编码器对超过 ~1568px 的图片会在内部降采样，多出来的像素一分钱不值、
//     却按整图计费。提前压到上限等于白省一大笔 token。
//  2. 尺寸一确定，上下文用量估算就能在**发送之前**算准（见 contextmgmt.go 的
//     imageTokens）——运行期转换的话，估算与真实请求之间会差出一整张图的量。
//  3. 请求路径零转换：每次组装请求都只做 base64，不做任何解码/缩放，
//     循环里那段热路径因此没有失败面。
//
// 只依赖标准库（image / image/png / image/jpeg / image/gif）：本项目在离线沙箱里
// 构建，任何新模块依赖都可能拉不下来，而缩放本身只要几十行面积平均。

const (
	// imageModelMaxEdge 送入模型的图片最长边上限。
	// 1568 是主流视觉编码器（Claude / GPT-4o 系）的输入上限，超过只会被内部降采样。
	imageModelMaxEdge = 1568
	// maxImageSourceBytes 单张原图的**原始**字节上限。超过直接拒绝而不是默默压缩：
	// 十几 MB 的图多半不是"要看的截图"，而是误拖进来的原始素材。
	maxImageSourceBytes = 10 << 20
	// imageModelMaxBytes 单张规整后送给模型的字节上限。它是硬上限，超了就再转一次 JPEG。
	imageModelMaxBytes = 4 << 20
	// imagePNGKeepMaxBytes PNG 优先保留的上限。截图类图片转 JPEG 会让文字糊掉，
	// 所以原格式是 PNG 时优先继续用 PNG；但 PNG 压不动，超这个值就只好转 JPEG。
	imagePNGKeepMaxBytes = 1536 << 10
	// imageModelJPEGQuality 照片类图片的 JPEG 质量。85 是肉眼几乎无损、体积又明显下降的位置。
	imageModelJPEGQuality = 85
)

// imageMediaTypes 支持的媒体类型白名单。
//
// 刻意**不含 WebP**：标准库没有解码器，无法缩放也就无法保证尺寸上限；
// 而"原样透传"会让一张 8MB 的 WebP 直接按原尺寸计费，与上面三条理由都相悖。
// 宁可给一句明确的错误（请转成 PNG/JPEG），也不要静默发一张超限的图。
var imageMediaTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
}

// imageExtFor 媒体类型 → 扩展名（附件落盘用）
func imageExtFor(mediaType string) string {
	switch mediaType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	default:
		return ".bin"
	}
}

// sniffImageMediaType 从**内容**判定媒体类型（不看文件名与前端传的 MIME）。
//
// 必须按内容判：前端传的 type 是浏览器猜的，可以随便改；扩展名更是纯装饰。
// 这里既决定"能不能处理"，也决定落盘扩展名，信任错了就是拿一个 .png 去装非图片内容。
func sniffImageMediaType(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	// DetectContentType 只看前 512 字节，对图片足够（各格式的魔数都在开头）
	detected := http.DetectContentType(data)
	if i := strings.IndexByte(detected, ';'); i >= 0 {
		detected = detected[:i] // 去掉 "; charset=..." 之类参数
	}
	if imageMediaTypes[detected] {
		return detected
	}
	return ""
}

// normalizedImage 一次规整的结果
type normalizedImage struct {
	Data      []byte // 规整后的字节（要送给模型的那一份）
	MediaType string
	Width     int
	Height    int
	// Changed 是否发生过缩放或重编码。界面据此提示"已压缩"，
	// 排障时也靠它区分"模型看到的是原图"还是"我们动过"。
	Changed bool
}

// normalizeImage 把任意受支持的图片规整成"适合送进视觉模型"的一份字节。
//
// 三条分支：
//   - 尺寸与体积都在限内 → **原样返回**。这一步很关键：绝大多数截图本来就小于 1568px，
//     走这条分支既不解码也不重编码，PNG 截图里的文字一个像素都不动。
//   - 超出尺寸 → 面积平均缩放到最长边 1568。
//   - 只有体积超限 → 只重编码，不改尺寸。
//
// 出错一律返回可读的中文原因：这条链路的调用方是"用户贴了一张图"，不是程序内部调用。
func normalizeImage(data []byte) (*normalizedImage, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("图片内容为空")
	}
	if len(data) > maxImageSourceBytes {
		return nil, fmt.Errorf("图片过大（%.1f MB，上限 %d MB），请先压缩或裁剪后再发送",
			float64(len(data))/(1<<20), maxImageSourceBytes>>20)
	}
	mediaType := sniffImageMediaType(data)
	if mediaType == "" {
		return nil, fmt.Errorf("不支持的图片格式（仅支持 PNG / JPEG / GIF；WebP 请先转成 PNG）")
	}

	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("图片解析失败（文件可能已损坏）: %v", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("图片尺寸异常（%d×%d）", cfg.Width, cfg.Height)
	}

	needResize := maxEdgeOf(cfg.Width, cfg.Height) > imageModelMaxEdge
	needReencode := len(data) > imageModelMaxBytes
	if !needResize && !needReencode {
		return &normalizedImage{
			Data:      data,
			MediaType: mediaType,
			Width:     cfg.Width,
			Height:    cfg.Height,
			Changed:   false,
		}, nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("图片解码失败: %v", err)
	}
	if needResize {
		img = downscaleToMaxEdge(img, imageModelMaxEdge)
	}

	// 格式跟随原格式：截图（PNG/GIF）继续用 PNG，保住文字边缘；
	// 照片（JPEG）继续用 JPEG，体积更友好。
	out, outType, err := encodeImage(img, mediaType != "image/jpeg")
	if err != nil {
		return nil, fmt.Errorf("图片重新编码失败: %v", err)
	}
	// PNG 压不动：超大时退回 JPEG。这一步是硬上限的保障，不是为了好看。
	// 退之前先压到白底上——JPEG 存不了 alpha，透明区域直接编码会变成黑块。
	if len(out) > imagePNGKeepMaxBytes && outType == "image/png" {
		if alt, _, aerr := encodeImage(flattenOnWhite(img), false); aerr == nil && len(alt) < len(out) {
			out, outType = alt, "image/jpeg"
		}
	}
	if len(out) > imageModelMaxBytes {
		return nil, fmt.Errorf("图片压缩后仍超过 %d MB，请先裁剪到更小尺寸", imageModelMaxBytes>>20)
	}
	// 尺寸取自**被编码的那张图**，不是编码后的字节（[]byte 没有 Bounds）
	b := img.Bounds()
	return &normalizedImage{
		Data:      out,
		MediaType: outType,
		Width:     b.Dx(),
		Height:    b.Dy(),
		Changed:   true,
	}, nil
}

// maxEdgeOf 取长边
func maxEdgeOf(w, h int) int {
	if w > h {
		return w
	}
	return h
}

// encodeImage 编码图片：preferPNG 为 true 时用 PNG，否则用 JPEG。
// 返回实际采用的媒体类型——返回值可能与入参不一致（PNG 编码失败会回退 JPEG）。
func encodeImage(img image.Image, preferPNG bool) ([]byte, string, error) {
	var buf bytes.Buffer
	if preferPNG {
		if err := png.Encode(&buf, img); err == nil {
			return buf.Bytes(), "image/png", nil
		}
		buf.Reset()
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: imageModelJPEGQuality}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/jpeg", nil
}

// downscaleToMaxEdge 把图片等比缩放到长边不超过 maxEdge。已在限内则原样返回。
func downscaleToMaxEdge(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	if maxEdgeOf(w, h) <= maxEdge {
		return src
	}
	scale := float64(maxEdge) / float64(maxEdgeOf(w, h))
	dstW := int(float64(w)*scale + 0.5)
	dstH := int(float64(h)*scale + 0.5)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}
	return downscale(src, dstW, dstH)
}

// downscale 面积平均（box filter）缩放。
//
// 为什么不用最近邻：截图里的文字缩一半后会碎成锯齿，模型对细字的识别率明显下降。
// 面积平均把每个目标像素覆盖的源像素全部纳入，缩小的图不会丢细节。
//
// 累加在**非预乘**颜色上、按 alpha 加权，最后再还原——直接对预乘值取平均会让
// 半透明边缘发黑（alpha 混进去之后没法还原）。
func downscale(src image.Image, dstW, dstH int) *image.NRGBA {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))

	xRatio := float64(srcW) / float64(dstW)
	yRatio := float64(srcH) / float64(dstH)

	for dy := 0; dy < dstH; dy++ {
		y0 := b.Min.Y + int(float64(dy)*yRatio)
		y1 := b.Min.Y + int(float64(dy+1)*yRatio)
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > b.Max.Y {
			y1 = b.Max.Y
		}
		for dx := 0; dx < dstW; dx++ {
			x0 := b.Min.X + int(float64(dx)*xRatio)
			x1 := b.Min.X + int(float64(dx+1)*xRatio)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > b.Max.X {
				x1 = b.Max.X
			}

			var sumA, sumRA, sumGA, sumBA, n uint64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, bl, a := src.At(x, y).RGBA() // 16 位预乘
					if a == 0 {
						n++
						continue
					}
					// 还原成非预乘的 8 位分量：RGBA() 已预乘，需除以 alpha
					ra := uint64(r) * 0xffff / uint64(a)
					ga := uint64(g) * 0xffff / uint64(a)
					ba := uint64(bl) * 0xffff / uint64(a)
					aa := uint64(a)
					sumRA += ra * aa
					sumGA += ga * aa
					sumBA += ba * aa
					sumA += aa
					n++
				}
			}
			if n == 0 {
				continue
			}
			if sumA == 0 {
				dst.SetNRGBA(dx, dy, color.NRGBA{})
				continue
			}
			dst.SetNRGBA(dx, dy, color.NRGBA{
				R: clamp8(sumRA / sumA),
				G: clamp8(sumGA / sumA),
				B: clamp8(sumBA / sumA),
				A: clamp8(sumA / n),
			})
		}
	}
	return dst
}

// flattenOnWhite 把图片合成到不透明白底上（JPEG 编码前的必要一步）。
// 用白色而不是黑色：截图/UI 稿的透明区域在视觉上几乎都是"白底"，
// 转成黑底会让整张图看起来像坏了。
func flattenOnWhite(src image.Image) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	return dst
}

// clamp8 把 0..0xffff 的累加值折算回 8 位
func clamp8(v uint64) uint8 {
	v = v >> 8
	if v > 0xff {
		return 0xff
	}
	return uint8(v)
}
