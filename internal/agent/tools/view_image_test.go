package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// noisyPNG 生成指定尺寸、内容接近随机的 PNG，用于构造远超体积上限的真实图片。
func noisyPNG(t *testing.T, width, height int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// 固定随机源：同一用例每次生成的字节数稳定
	_, err := rand.New(rand.NewSource(1)).Read(img.Pix)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestNormalizeImageForModelPassesThroughSmallImage(t *testing.T) {
	t.Parallel()

	raw := noisyPNG(t, 200, 120)
	require.Less(t, len(raw), MaxViewSize)

	got, err := normalizeImageForModel(raw, "image/png")
	require.NoError(t, err)
	require.False(t, got.Downscaled)
	require.Equal(t, "image/png", got.MIMEType)
	require.Equal(t, raw, got.Data)
	require.Equal(t, 200, got.Width)
	require.Equal(t, 120, got.Height)
	require.Equal(t, len(raw), got.OriginalBytes)
}

func TestNormalizeImageForModelDownscalesOversizedImage(t *testing.T) {
	t.Parallel()

	raw := noisyPNG(t, 2400, 1600)
	require.Greater(t, len(raw), MaxViewSize)

	got, err := normalizeImageForModel(raw, "image/png")
	require.NoError(t, err)
	require.True(t, got.Downscaled)
	require.Equal(t, "image/jpeg", got.MIMEType)
	require.LessOrEqual(t, len(got.Data), MaxViewSize)
	require.LessOrEqual(t, max(got.Width, got.Height), maxModelImageEdge)
	require.Equal(t, 2400, got.OriginalWidth)
	require.Equal(t, 1600, got.OriginalHeight)
	require.Equal(t, len(raw), got.OriginalBytes)

	// 下发数据必须是可解码的 JPEG，且尺寸与自报一致
	decoded, err := jpeg.Decode(bytes.NewReader(got.Data))
	require.NoError(t, err)
	require.Equal(t, got.Width, decoded.Bounds().Dx())
	require.Equal(t, got.Height, decoded.Bounds().Dy())
	require.Contains(t, got.describe(), "downscaled from image/png 2400×1600")
}

func TestNormalizeImageForModelDownscalesOversizedDimensions(t *testing.T) {
	t.Parallel()

	// 体积很小但边长超限：同样要缩到 maxModelImageEdge 以内
	img := image.NewNRGBA(image.Rect(0, 0, 3000, 40))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	got, err := normalizeImageForModel(buf.Bytes(), "image/png")
	require.NoError(t, err)
	require.True(t, got.Downscaled)
	require.LessOrEqual(t, max(got.Width, got.Height), maxModelImageEdge)
	require.Equal(t, 3000, got.OriginalWidth)
}

func TestFlattenOnWhiteKeepsTransparentPixelsWhite(t *testing.T) {
	t.Parallel()

	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	flat := flattenOnWhite(src)

	require.Equal(t, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, flat.NRGBAAt(0, 0))
}

func TestNormalizeImageForModelRejectsUndecodableOversizedImage(t *testing.T) {
	t.Parallel()

	raw := bytes.Repeat([]byte("a"), MaxViewSize+1)
	_, err := normalizeImageForModel(raw, "image/png")
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not be decoded")
}

func TestNormalizeImageForModelRejectsOversizedGIF(t *testing.T) {
	t.Parallel()

	// 动图重编码只剩首帧，超限时保持拒绝而不是悄悄丢帧
	img := image.NewRGBA(image.Rect(0, 0, 900, 900))
	_, err := rand.New(rand.NewSource(2)).Read(img.Pix)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, gif.Encode(&buf, img, nil))
	require.Greater(t, buf.Len(), MaxViewSize, "random noise GIF should exceed the size limit")

	_, err = normalizeImageForModel(buf.Bytes(), "image/gif")
	require.Error(t, err)
	require.Contains(t, err.Error(), "animated image is too large")
}

func TestViewToolDownscalesOversizedImages(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "large.png")
	require.NoError(t, os.WriteFile(filePath, noisyPNG(t, 2400, 1600), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	ctx = context.WithValue(ctx, SupportsImagesContextKey, true)
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filePath,
	})

	require.False(t, resp.IsError)
	require.Equal(t, "image", resp.Type)
	require.Equal(t, "image/jpeg", resp.MediaType)
	require.LessOrEqual(t, len(resp.Data), MaxViewSize)
	require.Contains(t, resp.Content, "downscaled from image/png 2400×1600")

	var meta ViewResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.LessOrEqual(t, max(meta.ImageWidth, meta.ImageHeight), maxModelImageEdge)
	require.Equal(t, len(resp.Data), meta.ImageBytes)
	require.Greater(t, meta.OriginalBytes, meta.ImageBytes)
}

func TestViewToolRejectsOversizedUndecodableImages(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "broken.png")
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Repeat("a", MaxViewSize+1)), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	ctx = context.WithValue(ctx, SupportsImagesContextKey, true)
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filePath,
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "could not be decoded")
}
