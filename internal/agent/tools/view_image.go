package tools

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/disintegration/imaging"
)

const (
	// maxModelImageEdge 下发图片的最长边。多数多模态模型会把超过该值的
	// 图片自行缩放，提前按此归一可省带宽，也避免各家实现差异。
	maxModelImageEdge = 1568
	// modelImageMinEdge 逐级缩边的下限，避免极端情况缩成缩略图。
	modelImageMinEdge = 320
	// modelImageQuality 重编码 JPEG 的起始质量，超出体积上限再逐档下调。
	modelImageQuality = 80
)

// normalizedImage 是准备下发给模型的图片：既描述尺寸/体积，也保留原始信息，
// 让工具结果能说明"这张图被缩过"。
type normalizedImage struct {
	Data             []byte
	MIMEType         string
	Width            int
	Height           int
	OriginalMIMEType string
	OriginalWidth    int
	OriginalHeight   int
	OriginalBytes    int
	Downscaled       bool
}

// describe 返回伴随图片的文本：模型据此知道图片是否被压缩过。
func (n normalizedImage) describe() string {
	if !n.Downscaled {
		return fmt.Sprintf("Loaded %s content (%d×%d, %s)", n.MIMEType, n.Width, n.Height, formatBytes(n.OriginalBytes))
	}
	// 只压体积但没改像素尺寸时别说成缩图，模型据此判断细节是否有损
	action := "re-encoded"
	if n.Width != n.OriginalWidth || n.Height != n.OriginalHeight {
		action = "downscaled"
	}
	return fmt.Sprintf(
		"Loaded %s content (%d×%d, %s), %s from %s %d×%d (%s)",
		n.MIMEType, n.Width, n.Height, formatBytes(len(n.Data)),
		action, n.OriginalMIMEType, n.OriginalWidth, n.OriginalHeight, formatBytes(n.OriginalBytes),
	)
}

// normalizeImageForModel 参数：raw 原始文件字节；mimeType 已按内容嗅探过的类型
// 核心流程：先只读图片头拿宽高，未超体积/尺寸上限时原样透传；
//
//	否则解码 → 等比缩到最长边 maxModelImageEdge → 铺白底重编码 JPEG，
//	逐档降质、必要时缩边，直到不超过 MaxViewSize
func normalizeImageForModel(raw []byte, mimeType string) (normalizedImage, error) {
	passThrough := normalizedImage{
		Data:             raw,
		MIMEType:         mimeType,
		OriginalMIMEType: mimeType,
		OriginalBytes:    len(raw),
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		// 解不出头部（例如未注册解码器的格式）：小图保持原有透传行为，
		// 大图无法压缩，只能明确拒绝
		if len(raw) <= MaxViewSize {
			return passThrough, nil
		}
		return normalizedImage{}, fmt.Errorf(
			"image file is too large (%d bytes, maximum %d) and its format could not be decoded to downscale it",
			len(raw), MaxViewSize,
		)
	}

	passThrough.Width = cfg.Width
	passThrough.Height = cfg.Height
	passThrough.OriginalWidth = cfg.Width
	passThrough.OriginalHeight = cfg.Height

	// GIF 可能是动图，重编码只剩首帧：不超限保持原样
	if format == "gif" {
		if len(raw) <= MaxViewSize {
			return passThrough, nil
		}
		return normalizedImage{}, fmt.Errorf(
			"animated image is too large (%d bytes, maximum %d)",
			len(raw), MaxViewSize,
		)
	}

	// 体积和尺寸都在范围内：不重编码，保留原始画质
	if len(raw) <= MaxViewSize && max(cfg.Width, cfg.Height) <= maxModelImageEdge {
		return passThrough, nil
	}

	img, err := imaging.Decode(bytes.NewReader(raw), imaging.AutoOrientation(true))
	if err != nil {
		if len(raw) <= MaxViewSize {
			return passThrough, nil
		}
		return normalizedImage{}, fmt.Errorf("image file is too large (%d bytes) and could not be decoded: %w", len(raw), err)
	}

	bounds := img.Bounds()
	if max(bounds.Dx(), bounds.Dy()) > maxModelImageEdge {
		img = imaging.Fit(img, maxModelImageEdge, maxModelImageEdge, imaging.Lanczos)
	}
	// JPEG 无 alpha 通道：先铺白底，避免透明区域编码成黑块
	cur := flattenOnWhite(img)

	for {
		for _, quality := range []int{modelImageQuality, 70, 60} {
			var buf bytes.Buffer
			if err := imaging.Encode(&buf, cur, imaging.JPEG, imaging.JPEGQuality(quality)); err != nil {
				return normalizedImage{}, fmt.Errorf("encode downscaled image: %w", err)
			}
			if buf.Len() <= MaxViewSize {
				resized := cur.Bounds()
				return normalizedImage{
					Data:             buf.Bytes(),
					MIMEType:         "image/jpeg",
					Width:            resized.Dx(),
					Height:           resized.Dy(),
					OriginalMIMEType: mimeType,
					OriginalWidth:    cfg.Width,
					OriginalHeight:   cfg.Height,
					OriginalBytes:    len(raw),
					Downscaled:       true,
				}, nil
			}
		}

		resized := cur.Bounds()
		if resized.Dx() <= modelImageMinEdge || resized.Dy() <= modelImageMinEdge {
			break
		}
		// 降质到头仍超限：等比缩边后重来一轮
		cur = imaging.Resize(cur, resized.Dx()*3/4, 0, imaging.Lanczos)
	}

	return normalizedImage{}, fmt.Errorf(
		"image could not be downscaled below %d bytes",
		MaxViewSize,
	)
}

// flattenOnWhite 参数：img 原始图像
// 核心流程：新建同尺寸 NRGBA 画布铺白，再把原图按 alpha 叠加，
//
//	让带透明通道的截图转 JPEG 后不变黑
func flattenOnWhite(img image.Image) *image.NRGBA {
	bounds := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, bounds.Min, draw.Over)
	return dst
}

// formatBytes 参数：n 字节数
// 核心流程：1024 进制换算到 KB/MB/GB，保留一位小数
func formatBytes(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := int64(n) / unit; m >= unit && exp < 2; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMG"[exp])
}
