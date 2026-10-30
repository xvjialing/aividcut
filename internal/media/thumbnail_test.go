package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func TestThumbnailWriterLimitsStream(t *testing.T) {
	var output thumbnailBytes
	_, err := io.Copy(&output, io.LimitReader(strings.NewReader(strings.Repeat("x", MaxThumbnailBytes+1)), MaxThumbnailBytes+1))
	if err == nil || output.Len() > MaxThumbnailBytes {
		t.Fatal("流读取绕过了缩略图字节上界", output.Len(), err)
	}
}

func thumbnailPNG(t *testing.T) editor.Asset {
	t.Helper()
	path := filepath.Join(t.TempDir(), "左右源图.png")
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 320 {
				c = color.RGBA{0, 0, 255, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return editor.Asset{ID: "image", Path: path, Kind: "image", Fingerprint: "fixture", Width: 640, Height: 360}
}
func thumbnailPixel(t *testing.T, value Thumbnail, x, y int) (int, int, int) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(value.JPEG))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(x, y).RGBA()
	return int(r >> 8), int(g >> 8), int(b >> 8)
}
func TestThumbnailStaticFallbackPixelsBoundsAndCancellation(t *testing.T) {
	a := thumbnailPNG(t)
	value, err := (Toolchain{}).ExtractThumbnail(context.Background(), a, 0)
	if err != nil || value.Width != 320 || value.Height != 180 || len(value.JPEG) > MaxThumbnailBytes {
		t.Fatal(value.Width, value.Height, err)
	}
	r, _, b := thumbnailPixel(t, value, 30, 90)
	if r < 220 || b > 20 {
		t.Fatal("左侧源颜色错误", r, b)
	}
	r, _, b = thumbnailPixel(t, value, 290, 90)
	if b < 220 || r > 20 {
		t.Fatal("右侧源颜色错误", r, b)
	}
	for _, position := range []int64{-1, 1, 86400_000_000} {
		if _, err := (Toolchain{}).ExtractThumbnail(context.Background(), a, position); err == nil {
			t.Fatal("无效位置被接受", position)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Toolchain{}).ExtractThumbnail(ctx, a, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("取消未传播", err)
	}
	a.Kind = "video"
	a.DurationUs = 1e6
	if _, err := (Toolchain{}).ExtractThumbnail(context.Background(), a, 0); err == nil {
		t.Fatal("无工具返回伪视频缩略图")
	}
}
func TestThumbnailRealVideoPositionsRotationSARAndVFR(t *testing.T) {
	tools := speechIntegrationTools(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "四段颜色.mp4")
	command(t, tools, "-v", "error", "-f", "lavfi", "-i", "color=red:s=160x90:r=10:d=1", "-f", "lavfi", "-i", "color=green:s=160x90:r=10:d=1", "-f", "lavfi", "-i", "color=blue:s=160x90:r=10:d=1", "-f", "lavfi", "-i", "color=yellow:s=160x90:r=10:d=1", "-filter_complex", "[0:v][1:v][2:v][3:v]concat=n=4:v=1:a=0[v]", "-map", "[v]", "-c:v", "libx264", "-threads", "1", "-g", "30", path)
	a, err := tools.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	positions := []int64{0, 1_250_000, 2_250_000, 3_900_000}
	for i, position := range positions {
		value, err := tools.ExtractThumbnail(context.Background(), a, position)
		if err != nil {
			t.Fatal(position, err)
		}
		r, g, b := thumbnailPixel(t, value, value.Width/2, value.Height/2)
		good := []bool{r > 220 && g < 20 && b < 20, g > 100 && r < 20 && b < 20, b > 220 && r < 20 && g < 20, r > 220 && g > 220 && b < 20}[i]
		if !good || value.PositionUs != position || value.Width != 320 || value.Height != 180 {
			t.Fatal("取样与源颜色/时间不符", position, r, g, b, value.Width, value.Height)
		}
	}
	sar := filepath.Join(dir, "宽像素.mp4")
	command(t, tools, "-v", "error", "-i", path, "-vf", "setsar=2", "-c:v", "libx264", "-threads", "1", sar)
	rotated := filepath.Join(dir, "旋转.mp4")
	command(t, tools, "-v", "error", "-display_rotation", "90", "-i", path, "-c", "copy", rotated)
	vfr := filepath.Join(dir, "变帧率.mp4")
	command(t, tools, "-v", "error", "-i", path, "-vf", "select='not(mod(n,3))'", "-fps_mode", "vfr", "-c:v", "libx264", "-threads", "1", vfr)
	for _, item := range []struct {
		path string
		w, h int
		pos  int64
	}{{sar, 320, 90, 0}, {rotated, 100, 180, 0}, {vfr, 320, 180, 3_800_000}} {
		a, err := tools.Probe(context.Background(), item.path)
		if err != nil {
			t.Fatal(err)
		}
		value, err := tools.ExtractThumbnail(context.Background(), a, item.pos)
		if err != nil || value.Width != item.w || value.Height != item.h {
			t.Fatal(item, value.Width, value.Height, err)
		}
		r, g, b := thumbnailPixel(t, value, value.Width/2, value.Height/2)
		if item.pos > 0 {
			if r < 220 || g < 220 || b > 20 {
				t.Fatal("VFR近末尾取样不正确", r, g, b)
			}
		} else if r < 220 || g > 20 || b > 20 {
			t.Fatal("宽高比/旋转源颜色不正确", r, g, b)
		}
	}
}
func TestThumbnailCacheCopiesBytesAndInvalidatesSource(t *testing.T) {
	r := NewThumbnailReader(Toolchain{})
	defer r.Close()
	a := thumbnailPNG(t)
	first, err := r.Read(context.Background(), a, 0)
	if err != nil {
		t.Fatal(err)
	}
	first.JPEG[0] = 0
	second, err := r.Read(context.Background(), a, 0)
	if err != nil || second.JPEG[0] != 255 {
		t.Fatal("调用方污染缓存", err)
	}
	if err := os.WriteFile(a.Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(context.Background(), a, 0); err == nil {
		t.Fatal("源变化仍返回缓存")
	}
}
