package media

import (
	"aividcut/internal/managedprocess"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"aividcut/internal/editor"
)

const MaxThumbnailBytes = 256 << 10

type Thumbnail struct {
	JPEG          []byte
	Width, Height int
	PositionUs    int64
}
type ThumbnailReader = sourceReader[Thumbnail]

func NewThumbnailReader(tools Toolchain) *ThumbnailReader {
	return newSourceReader(tools.ExtractThumbnail, func(value Thumbnail) Thumbnail { value.JPEG = append([]byte{}, value.JPEG...); return value }, "缩略图")
}

// 不嵌入bytes.Buffer，避免io.Copy经其ReadFrom绕过Write字节上界。
type thumbnailBytes struct{ buffer bytes.Buffer }

func (b *thumbnailBytes) Len() int      { return b.buffer.Len() }
func (b *thumbnailBytes) Bytes() []byte { return b.buffer.Bytes() }

func (b *thumbnailBytes) Write(data []byte) (int, error) {
	if len(data) > MaxThumbnailBytes-b.Len() {
		return 0, errors.New("缩略图超过256KiB预算")
	}
	return b.buffer.Write(data)
}

// ExtractThumbnail仅读取单个受控源位置；输出为源画面，不能作为帧精确编辑或最终合成证据。
func (t Toolchain) ExtractThumbnail(ctx context.Context, asset editor.Asset, position int64) (Thumbnail, error) {
	return t.extractSourceFrame(ctx, asset, position, 320, 180)
}

// ExtractSourceFrame 保留更高分辨率供视觉核对，仍为近似源取样且不含时间线效果。
func (t Toolchain) ExtractSourceFrame(ctx context.Context, asset editor.Asset, position int64) (Thumbnail, error) {
	return t.extractSourceFrame(ctx, asset, position, 960, 540)
}
func NewSourceFrameReader(tools Toolchain) *ThumbnailReader {
	return newSourceReader(tools.ExtractSourceFrame, func(value Thumbnail) Thumbnail { value.JPEG = append([]byte{}, value.JPEG...); return value }, "源画面")
}
func (t Toolchain) extractSourceFrame(ctx context.Context, asset editor.Asset, position int64, maxWidth, maxHeight int) (Thumbnail, error) {
	var empty Thumbnail
	if !filepath.IsAbs(asset.Path) || asset.Missing || (asset.Kind != "image" && asset.Kind != "video") || position < 0 || position >= 86400_000_000 || asset.Kind == "image" && position != 0 || asset.Kind == "video" && (asset.DurationUs <= 0 || position >= asset.DurationUs) {
		return empty, errors.New("缩略图需要已登记图片/视频和有效源位置")
	}
	before, err := os.Stat(asset.Path)
	if err != nil || !before.Mode().IsRegular() {
		return empty, errors.New("缩略图源文件不可读取，请重新定位素材")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var output thumbnailBytes
	if t.FFmpegPath == "" {
		if asset.Kind != "image" {
			return empty, errors.New("视频缩略图需要配置FFmpeg")
		}
		if before.Size() > 32<<20 {
			return empty, errors.New("未配置FFmpeg时，静态缩略图源最多32MiB")
		}
		file, err := os.Open(asset.Path)
		if err != nil {
			return empty, err
		}
		data, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
		file.Close()
		if err != nil {
			return empty, err
		}
		if len(data) > 32<<20 {
			return empty, errors.New("静态缩略图源超过32MiB预算")
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 8<<20 {
			return empty, errors.New("静态缩略图回退支持8Mi像素内的PNG/JPEG，请配置FFmpeg处理其他素材")
		}
		// EXIF方向由媒体工具处理；不能将未旋转的JPEG伪装成正确源画面。
		if format == "jpeg" && bytes.Contains(data, []byte("Exif\x00\x00")) {
			return empty, errors.New("此JPEG含方向元数据，请配置FFmpeg生成缩略图")
		}
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		original, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return empty, errors.New("无法解码静态源图")
		}
		w, h := config.Width, config.Height
		if w > maxWidth {
			h = max(1, h*maxWidth/w)
			w = maxWidth
		}
		if h > maxHeight {
			w = max(1, w*maxHeight/h)
			h = maxHeight
		}
		thumb := image.NewRGBA(image.Rect(0, 0, w, h))
		bounds := original.Bounds()
		for y := 0; y < h; y++ {
			if err := ctx.Err(); err != nil {
				return empty, err
			}
			for x := 0; x < w; x++ {
				r, g, b, _ := original.At(bounds.Min.X+x*config.Width/w, bounds.Min.Y+y*config.Height/h).RGBA()
				thumb.SetRGBA(x, y, color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 255})
			}
		}
		if err = jpeg.Encode(&output, thumb, &jpeg.Options{Quality: 80}); err != nil {
			return empty, err
		}
	} else {
		args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-filter_threads", "1", "-protocol_whitelist", "file,pipe", "-format_whitelist", speechInputFormats + ",image2,png_pipe,jpeg_pipe,webp_pipe,bmp_pipe,gif"}
		if position > 0 {
			args = append(args, "-ss", strconv.FormatFloat(float64(position)/1e6, 'f', 6, 64))
		}
		args = append(args, "-i", asset.Path, "-map", "0:v:0", "-an", "-sn", "-dn", "-frames:v", "1", "-vf", fmt.Sprintf("scale=w='max(2,trunc(min(%d,%d*dar)/2)*2)':h='max(2,trunc(min(%d,%d/dar)/2)*2)',setsar=1", maxWidth, maxHeight, maxHeight, maxWidth), "-c:v", "mjpeg", "-threads", "1", "-q:v", "4", "-f", "image2pipe", "pipe:1")
		command := managedprocess.CommandContext(ctx, t.FFmpegPath, args...)
		command.Stdout = &output
		var stderr tailBuffer
		command.Stderr = &stderr
		command.WaitDelay = 2 * time.Second
		if err = command.Run(); err != nil {
			if ctx.Err() != nil {
				return empty, ctx.Err()
			}
			return empty, errors.New("无法读取该位置的源画面，请检查素材或换个位置")
		}
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(output.Bytes()))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxWidth || config.Height > maxHeight {
		return empty, errors.New("该位置没有可读取画面，请换个位置")
	}
	after, err := os.Stat(asset.Path)
	if err != nil || !sameWaveFile(before, after) {
		return empty, errors.New("读取缩略图期间源文件变化，请重新定位素材")
	}
	return Thumbnail{JPEG: append([]byte{}, output.Bytes()...), Width: config.Width, Height: config.Height, PositionUs: position}, nil
}

var _ io.Writer = (*thumbnailBytes)(nil)
