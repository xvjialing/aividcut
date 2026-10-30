package playback

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
)

func visualAsset(t *testing.T, name string, width, height int, pixel func(int, int) color.NRGBA) editor.Asset {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value.SetNRGBA(x, y, pixel(x, y))
		}
	}
	path := filepath.Join(t.TempDir(), name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, value); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return editor.Asset{ID: name, Name: name, Kind: "image", Path: path, Width: width, Height: height}
}

func visualProject(t *testing.T) editor.Project {
	t.Helper()
	project := editor.NewProject("真实视觉对照")
	project.Settings.Width, project.Settings.Height, project.Settings.FPSNum, project.Settings.FPSDen = 320, 240, 30, 1
	background := visualAsset(t, "background.png", 64, 48, func(int, int) color.NRGBA { return color.NRGBA{R: 40, G: 60, B: 90, A: 255} })
	foreground := visualAsset(t, "asymmetric.png", 160, 100, func(x, y int) color.NRGBA {
		if x >= 8 && x < 65 && y >= 8 && y < 38 {
			return color.NRGBA{R: 220, G: 30, B: 50, A: 255}
		}
		if x >= 80 && x < 145 && y >= 15 && y < 52 {
			return color.NRGBA{R: 40, G: 210, B: 70, A: 180}
		}
		if x >= 104 && x < 148 && y >= 62 && y < 90 {
			return color.NRGBA{R: 30, G: 70, B: 220, A: 96}
		}
		if x >= 30 && x < 75 && y >= 45 && y < 67 {
			return color.NRGBA{R: 95, G: 140, B: 180, A: 255}
		}
		if x >= 16 && x < 94 && y >= 75 && y < 92 {
			return color.NRGBA{R: uint8(50 + x), G: uint8(200 - x), B: 150, A: uint8((x - 16) * 255 / 78)}
		}
		return color.NRGBA{R: 235, G: 125, B: 80, A: 0}
	})
	path := filepath.Join(t.TempDir(), "domain nonlinear ' cube.cube")
	text := "LUT_3D_SIZE 2\nDOMAIN_MIN 0.1 0.05 0.2\nDOMAIN_MAX 0.9 0.95 0.8\n"
	for b := 0; b < 2; b++ {
		for g := 0; g < 2; g++ {
			for r := 0; r < 2; r++ {
				rf, gf, bf := float64(r), float64(g), float64(b)
				// 强交叉项使tetrahedral与trilinear在内部颜色上明显不同。
				text += fmt.Sprintf("%.9f %.9f %.9f\n", .05+.9*gf*bf, .05+.9*rf*bf, .05+.9*rf*gf)
			}
		}
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	project.Assets = []editor.Asset{background, foreground, {ID: "cube", Name: "domain cube", Kind: "lut", Path: path}}
	project.Tracks = append([]editor.Track{{ID: "visual-top", Name: "顶层", Kind: "video"}}, project.Tracks...)
	base := editor.DefaultClip("base", "track-video-1", "image")
	base.AssetID, base.DurationFrames = background.ID, 3
	top := editor.DefaultClip("top", "visual-top", "image")
	top.AssetID, top.DurationFrames = foreground.ID, 3
	project.Clips = []editor.Clip{base, top}
	return project
}

func visualTools(t *testing.T) media.Toolchain {
	t.Helper()
	ffmpeg, ffprobe := os.Getenv("AIVIDCUT_FFMPEG"), os.Getenv("AIVIDCUT_FFPROBE")
	if ffmpeg == "" {
		t.Skip("未显式配置开发FFmpeg，原生视觉/导出对照未执行")
	}
	if ffprobe == "" {
		ffprobe = filepath.Join(filepath.Dir(ffmpeg), "ffprobe")
	}
	if _, err := os.Stat(ffprobe); err != nil {
		t.Skip("未配置可用ffprobe，真实导出对照未执行")
	}
	return media.Toolchain{FFmpegPath: ffmpeg, FFprobePath: ffprobe}
}

func visualReference(t *testing.T, tools media.Toolchain, project editor.Project) image.Image {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reference.png")
	if err := tools.PreviewFrame(context.Background(), project, 0, path); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	value, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func compareVisual(t *testing.T, frame Frame, reference image.Image) (float64, int) {
	t.Helper()
	if reference.Bounds().Dx() != frame.Width || reference.Bounds().Dy() != frame.Height {
		t.Fatal("预览/导出画幅不同")
	}
	differences := make([]int, 0, frame.Width*frame.Height*3)
	var sum int64
	for y := 0; y < frame.Height; y++ {
		for x := 0; x < frame.Width; x++ {
			expected := color.NRGBAModel.Convert(reference.At(x, y)).(color.NRGBA)
			at := (y*frame.Width + x) * 4
			for c, v := range []uint8{expected.R, expected.G, expected.B} {
				difference := abs(int(frame.RGBA[at+c]) - int(v))
				differences = append(differences, difference)
				sum += int64(difference)
			}
			if frame.RGBA[at+3] != 255 {
				t.Fatal("有黑底的最终画面alpha应为不透明")
			}
		}
	}
	sort.Ints(differences)
	return float64(sum) / float64(len(differences)), differences[len(differences)*95/100]
}

func saveVisualEvidence(t *testing.T, name string, frame Frame, reference image.Image) {
	t.Helper()
	directory := os.Getenv("AIVIDCUT_VISUAL_EVIDENCE")
	if directory == "" {
		return
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for suffix, value := range map[string]image.Image{"native": &image.NRGBA{Pix: frame.RGBA, Stride: frame.Width * 4, Rect: image.Rect(0, 0, frame.Width, frame.Height)}, "ffmpeg": reference} {
		file, err := os.Create(filepath.Join(directory, name+"-"+suffix+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, value); err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
}

func TestNativeVisualRotationAndLUTAgainstFFmpeg(t *testing.T) {
	tools := visualTools(t)
	config := nativeConfig(t)
	config.MaxWidth, config.MaxHeight = 640, 480
	session, err := Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, test := range []struct {
		name                             string
		rotation, intensity, scale, x, y float64
	}{
		{"lut0", 0, 0, .7, 22, -12}, {"lut25", 0, .25, .7, 22, -12}, {"lut50", 0, .5, .7, 22, -12}, {"lut100", 0, 1, .7, 22, -12},
		{"rotate90", 90, 1, 1.35, 31, -17}, {"rotate30", 30, .5, 1.25, -23, 19}, {"rotateMinus45", -45, .25, .9, 17.5, -25.25},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := visualProject(t)
			clip := &project.Clips[1]
			clip.Transform = editor.Transform{Scale: test.scale, Rotation: test.rotation, X: test.x, Y: test.y}
			clip.LUT = &editor.LUT{AssetID: "cube", Intensity: test.intensity}
			if _, err := session.Load(context.Background(), project); err != nil {
				t.Fatal(err)
			}
			frame, err := session.Seek(context.Background(), 0)
			if err != nil {
				t.Fatal(err)
			}
			reference := visualReference(t, tools, project)
			saveVisualEvidence(t, test.name, frame, reference)
			mae, p95 := compareVisual(t, frame, reference)
			t.Logf("真实RGBA对照：MAE=%.3f p95=%d", mae, p95)
			if mae > 6 || p95 > 18 {
				t.Fatalf("旋转/强度/alpha与实际FFmpeg偏差过大: MAE=%.3f p95=%d", mae, p95)
			}
			if test.rotation == 0 {
				at := (115*frame.Width + 137) * 4
				expected := color.NRGBAModel.Convert(reference.At(137, 115)).(color.NRGBA)
				for channel, value := range []byte{expected.R, expected.G, expected.B} {
					if abs(int(frame.RGBA[at+channel])-int(value)) > 3 {
						t.Fatalf("DOMAIN/tetrahedral/强度关键色块不一致: native=%v reference=%v", frame.RGBA[at:at+4], expected)
					}
				}
			}
		})
	}
}

func TestNativeVisualLargeROIAndResourceRejection(t *testing.T) {
	config := nativeConfig(t)
	config.MaxWidth, config.MaxHeight = 1920, 1080
	session, err := Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	project := visualProject(t)
	project.Settings.Width, project.Settings.Height = 3840, 2160
	project.Clips[1].Transform = editor.Transform{Scale: 2, Rotation: 30, X: 31, Y: -17}
	project.Clips[1].LUT = &editor.LUT{AssetID: "cube", Intensity: .5}
	if _, err := session.Load(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	frame, err := session.Seek(context.Background(), 0)
	if err != nil {
		t.Fatal("普通4K 2x可见ROI不应先生成整幅旋转大图", err)
	}
	if frame.Width != 1920 || frame.Height != 1080 {
		t.Fatal("代理输出画幅变化")
	}
	t.Logf("4K/2x/30度真实帧：%s", time.Since(started))
	project.Clips[1].Transform.Scale = 16
	if _, err := session.Load(context.Background(), project); err == nil {
		t.Fatal("极端中间缩放没有在分配前拒绝")
	}
}

func TestNativeVisualVideoSourceScaleBeforeRGBAAgainstFFmpeg(t *testing.T) {
	tools := visualTools(t)
	config := nativeConfig(t)
	config.MaxWidth, config.MaxHeight = 640, 480
	session, err := Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	project := visualProject(t)
	path := filepath.Join(t.TempDir(), "own-yuv420.mp4")
	command := exec.CommandContext(context.Background(), tools.FFmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-loop", "1", "-framerate", "30", "-i", project.Assets[1].Path, "-vf", "scale=640:400", "-t", "0.2", "-c:v", "libx264", "-pix_fmt", "yuv420p", path)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("自建YUV视频失败: %v %s", err, data)
	}
	metadataPath := filepath.Join(t.TempDir(), "own-yuv-display90.mov")
	command = exec.CommandContext(context.Background(), tools.FFmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-display_rotation", "90", "-i", path, "-c", "copy", metadataPath)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("自建带方向元数据视频失败: %v %s", err, data)
	}
	project.Assets[1].Kind, project.Assets[1].Path, project.Assets[1].Width, project.Assets[1].Height, project.Assets[1].DurationUs = "video", path, 640, 400, 200000
	project.Clips[1].Kind = "video"
	for _, test := range []struct {
		name                string
		rotation, intensity float64
		metadata            bool
	}{{"video0", 0, 0, false}, {"video25", 30, .25, false}, {"video50", -45, .5, false}, {"video100", 90, 1, false}, {"videoMetadataThenUserRotation", 30, .5, true}} {
		t.Run(test.name, func(t *testing.T) {
			project.Assets[1].Path = path
			if test.metadata {
				project.Assets[1].Path = metadataPath
			}
			project.Clips[1].Transform = editor.Transform{Scale: .65, Rotation: test.rotation, X: 23, Y: -13}
			project.Clips[1].LUT = &editor.LUT{AssetID: "cube", Intensity: test.intensity}
			if _, err := session.Load(context.Background(), project); err != nil {
				t.Fatal(err)
			}
			frame, err := session.Seek(context.Background(), 0)
			if err != nil {
				t.Fatal(err)
			}
			reference := visualReference(t, tools, project)
			saveVisualEvidence(t, test.name, frame, reference)
			mae, p95 := compareVisual(t, frame, reference)
			t.Logf("真实YUV视频→scale→RGBA对照：MAE=%.3f p95=%d", mae, p95)
			if mae > 6 || p95 > 18 {
				t.Fatalf("视频源算子顺序/像素不一致: MAE=%.3f p95=%d", mae, p95)
			}
		})
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
