package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aividcut/internal/editor"
)

func identityCube(invert bool) string {
	var data strings.Builder
	data.WriteString("TITLE \"测试 LUT\"\nLUT_3D_SIZE 2\nDOMAIN_MIN 0 0 0\nDOMAIN_MAX 1 1 1\n")
	for b := 0; b <= 1; b++ {
		for g := 0; g <= 1; g++ {
			for r := 0; r <= 1; r++ {
				if invert {
					fmt.Fprintf(&data, "%d %d %d\n", 1-r, 1-g, 1-b)
				} else {
					fmt.Fprintf(&data, "%d %d %d\n", r, g, b)
				}
			}
		}
	}
	return data.String()
}

func TestCubeValidation(t *testing.T) {
	valid := identityCube(false)
	info, err := parseCube([]byte(valid))
	if err != nil || info.Size != 2 || info.Entries != 8 || info.Title != "测试 LUT" {
		t.Fatalf("有效 LUT 未正确解析：%+v %v", info, err)
	}
	for name, data := range map[string]string{
		"缺少数据": strings.TrimSuffix(valid, "1 1 1\n"),
		"非有限值": strings.Replace(valid, "0 0 0\n", "NaN 0 0\n", 1),
		"一维":   "LUT_1D_SIZE 2\n0 0 0\n1 1 1\n",
		"域倒置":  strings.Replace(valid, "DOMAIN_MIN 0 0 0", "DOMAIN_MIN 1 0 0", 1),
		"超过网格": valid + "0 0 0\n",
		"重复声明": strings.Replace(valid, "LUT_3D_SIZE 2", "LUT_3D_SIZE 2\nLUT_3D_SIZE 2", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseCube([]byte(data)); err == nil {
				t.Fatal("错误 LUT 被接受")
			}
		})
	}
}

func writeImage(t *testing.T, path string, fill color.NRGBA) {
	writeSizedImage(t, path, 160, 90, fill)
}

func writeSizedImage(t *testing.T, path string, width, height int, fill color.NRGBA) {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			im.SetNRGBA(x, y, fill)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, im); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestImageProbeWithoutExternalTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "测试图片.png")
	writeImage(t, path, color.NRGBA{R: 255, A: 255})
	asset, err := (Toolchain{}).Probe(context.Background(), path)
	if err != nil || asset.Kind != "image" || asset.Width != 160 || asset.Height != 90 || !strings.HasPrefix(asset.Fingerprint, "sha256:") {
		t.Fatalf("图片导入失败：%+v %v", asset, err)
	}
	if _, err := (Toolchain{}).Probe(context.Background(), filepath.Dir(path)); err == nil {
		t.Fatal("目录不应被导入为媒体")
	}
}

// 可选地使用本机已安装应用的工具做开发验证；产品 Discover 不做这一回退。
func integrationTools(t *testing.T) Toolchain {
	t.Helper()
	tools := Discover()
	if tools.FFmpegPath == "" || tools.FFprobePath == "" {
		base := "/Applications/ChatCut.app/Contents/Resources/app.asar.unpacked/node_modules/ffmpeg-ffprobe-static"
		if _, err := os.Stat(filepath.Join(base, "ffmpeg")); err == nil {
			tools = Toolchain{FFmpegPath: filepath.Join(base, "ffmpeg"), FFprobePath: filepath.Join(base, "ffprobe")}
		}
	}
	if tools.FFmpegPath == "" || tools.FFprobePath == "" {
		t.Skip("未配置 ffmpeg/ffprobe，真实媒体验证未执行")
	}
	t.Logf("开发验证工具（不随产品分发）：%s", tools.FFmpegPath)
	return tools
}

func command(t *testing.T, tools Toolchain, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tools.FFmpegPath, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("创建或检查样本失败：%v\n%s", err, out)
	}
	return out
}

func fixtureVideo(t *testing.T, tools Toolchain, path string) {
	t.Helper()
	command(t, tools, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=red:s=160x90:r=30:d=1",
		"-f", "lavfi", "-i", "color=c=blue:s=160x90:r=30:d=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1:sample_rate=48000",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=1:sample_rate=48000",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v];[2:a][3:a]concat=n=2:v=0:a=1[a]",
		"-map", "[v]", "-map", "[a]", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-t", "2", path)
}

func testProject() editor.Project {
	return editor.Project{ID: "test", SchemaVersion: editor.SchemaVersion,
		Settings: editor.Settings{Width: 160, Height: 90, FPSNum: 30, FPSDen: 1, SampleRate: 48000},
		Tracks:   []editor.Track{{ID: "title", Kind: "text"}, {ID: "video", Kind: "video"}, {ID: "sound", Kind: "audio"}},
	}
}

func mediaClip(id, assetID string, duration int64) editor.Clip {
	return editor.Clip{ID: id, TrackID: "video", AssetID: assetID, Kind: "video", DurationFrames: duration, SpeedNum: 1, SpeedDen: 1, Volume: 1, Transform: editor.Transform{Scale: 1}}
}

func previewPixel(t *testing.T, tools Toolchain, project editor.Project, frame int64, x, y int) color.NRGBA {
	t.Helper()
	path := filepath.Join(t.TempDir(), "preview.png")
	if err := tools.PreviewFrame(context.Background(), project, frame, path); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	im, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return color.NRGBAModel.Convert(im.At(x, y)).(color.NRGBA)
}

func TestRealVideoRangeAudioAndCancellation(t *testing.T) {
	tools := integrationTools(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "源 ' 视频,样本.mp4")
	fixtureVideo(t, tools, source)
	asset, err := tools.Probe(context.Background(), source)
	if err != nil || asset.Kind != "video" || !asset.HasAudio || asset.DurationUs < 1_900_000 {
		t.Fatalf("探测失败：%+v %v", asset, err)
	}
	project := testProject()
	project.Assets = []editor.Asset{asset}
	clip := mediaClip("source", asset.ID, 30)
	clip.StartFrame, clip.SourceInUs = 15, 1_000_000
	project.Clips = []editor.Clip{clip}
	if p := previewPixel(t, tools, project, 0, 80, 45); p.R > 15 || p.G > 15 || p.B > 15 {
		t.Fatalf("视频开始前应为黑色空档，实际 %+v", p)
	}
	if p := previewPixel(t, tools, project, 20, 80, 45); p.B < 180 || p.R > 60 || p.G > 60 {
		t.Fatalf("源入点应映射到蓝色后半段，实际 %+v", p)
	}
	output := filepath.Join(dir, "输出 ' 文件,名称.mp4")
	var progress []Progress
	if err := tools.Export(context.Background(), project, ExportOptions{Path: output, Preset: "project"}, func(p Progress) { progress = append(progress, p) }); err != nil {
		t.Fatal(err)
	}
	exported, err := tools.Probe(context.Background(), output)
	if err != nil || math.Abs(float64(exported.DurationUs)-1_500_000) > 50_000 || exported.Width != 160 || exported.Height != 90 || !exported.HasAudio {
		t.Fatalf("导出元数据不符合时间线：%+v %v", exported, err)
	}
	if len(progress) < 2 || progress[len(progress)-1].Fraction != 1 || progress[len(progress)-1].Stage != "completed" {
		t.Fatalf("缺少真实完成状态：%+v", progress)
	}
	pcm := command(t, tools, "-v", "error", "-i", output, "-map", "0:a", "-ar", "48000", "-ac", "1", "-f", "f32le", "pipe:1")
	var samples []float64
	for i := 0; i+4 <= len(pcm); i += 4 {
		samples = append(samples, float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:]))))
	}
	if len(samples) < 70_000 {
		t.Fatalf("音轨被提前截断，得到 %d 个样本，期望约 72000", len(samples))
	}
	if rms(samples, 0.05, 0.3) > 0.001 || rms(samples, 0.7, 1) < 0.02 {
		t.Fatal("音频空档或音量与时间线不符")
	}
	if hz := frequency(samples, 0.75, 0.95); math.Abs(hz-880) > 30 {
		t.Fatalf("音频未采用源后半段 880Hz，实际 %.1fHz", hz)
	}
	before, _ := os.ReadFile(output)
	if err := tools.Export(context.Background(), project, ExportOptions{Path: output}, nil); err == nil {
		t.Fatal("未获授权的现有输出覆盖应被拒绝")
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(before, after) {
		t.Fatal("输出拒绝后原文件发生变化")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tools.Export(ctx, project, ExportOptions{Path: filepath.Join(dir, "cancelled.mp4")}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未保留原因：%v", err)
	}

	// 2 倍速和导出范围同时改变源映射，所选后半段应对应蓝色/880Hz。
	project.Clips[0].StartFrame, project.Clips[0].SourceInUs = 0, 0
	project.Clips[0].SpeedNum = 2
	if p := previewPixel(t, tools, project, 5, 80, 45); p.R < 180 || p.B > 60 {
		t.Fatalf("2倍速前半段应为源红色，实际 %+v", p)
	}
	if p := previewPixel(t, tools, project, 20, 80, 45); p.B < 180 || p.R > 60 {
		t.Fatalf("2倍速后半段应为源蓝色，实际 %+v", p)
	}
	rangePath := filepath.Join(dir, "range.mp4")
	if err := tools.Export(context.Background(), project, ExportOptions{Path: rangePath, Preset: "project", FromFrame: 15, ToFrame: 30}, nil); err != nil {
		t.Fatal(err)
	}
	rangeAsset, err := tools.Probe(context.Background(), rangePath)
	if err != nil || math.Abs(float64(rangeAsset.DurationUs)-500_000) > 40_000 {
		t.Fatalf("选区导出时长错误：%+v %v", rangeAsset, err)
	}
	project.Assets = []editor.Asset{rangeAsset}
	project.Clips = []editor.Clip{mediaClip("range", rangeAsset.ID, 15)}
	if p := previewPixel(t, tools, project, 0, 80, 45); p.B < 180 || p.R > 60 {
		t.Fatalf("选区导出未采用正确源区间，实际 %+v", p)
	}
}

func rms(samples []float64, start, end float64) float64 {
	a, b := int(start*48000), min(len(samples), int(end*48000))
	var power float64
	for _, sample := range samples[a:b] {
		power += sample * sample
	}
	return math.Sqrt(power / float64(b-a))
}

func frequency(samples []float64, start, end float64) float64 {
	a, b := int(start*48000), min(len(samples), int(end*48000))
	crossings := 0
	for i := a + 1; i < b; i++ {
		if samples[i-1] <= 0 && samples[i] > 0 {
			crossings++
		}
	}
	return float64(crossings) / (end - start)
}

func TestRealLayersAlphaScaleAndLUT(t *testing.T) {
	tools := integrationTools(t)
	dir := t.TempDir()
	redPath, bluePath := filepath.Join(dir, "red.png"), filepath.Join(dir, "blue.png")
	writeImage(t, redPath, color.NRGBA{R: 255, A: 255})
	writeSizedImage(t, bluePath, 100, 100, color.NRGBA{B: 255, A: 128})
	red, _ := tools.Probe(context.Background(), redPath)
	blue, _ := tools.Probe(context.Background(), bluePath)
	project := testProject()
	project.Tracks = []editor.Track{{ID: "top", Kind: "video"}, {ID: "video", Kind: "video"}}
	project.Assets = []editor.Asset{red, blue}
	bottom := mediaClip("red", red.ID, 30)
	bottom.Kind = "image"
	top := mediaClip("blue", blue.ID, 30)
	top.Kind, top.TrackID, top.Transform.Scale = "image", "top", 0.5
	project.Clips = []editor.Clip{top, bottom}
	if p := previewPixel(t, tools, project, 0, 10, 10); p.R < 200 || p.B > 40 {
		t.Fatalf("缩放后边缘应保留底层红色，实际 %+v", p)
	}
	if p := previewPixel(t, tools, project, 0, 80, 45); p.R < 80 || p.R > 175 || p.B < 80 || p.B > 175 {
		t.Fatalf("上层半透明蓝色应混合为紫色，实际 %+v", p)
	}
	if p := previewPixel(t, tools, project, 0, 45, 45); p.R < 200 || p.B > 40 {
		t.Fatalf("正方形素材应保持比例，不能拉伸填满半幅，实际 %+v", p)
	}
	writeImage(t, bluePath, color.NRGBA{B: 255, A: 255})
	blue, _ = tools.Probe(context.Background(), bluePath)
	lutPath := filepath.Join(dir, "含有 ' 特殊,字符.cube")
	if err := os.WriteFile(lutPath, []byte(identityCube(true)), 0600); err != nil {
		t.Fatal(err)
	}
	project.Assets = []editor.Asset{blue, {ID: "invert", Kind: "lut", Path: lutPath}}
	clip := mediaClip("lut", blue.ID, 30)
	clip.Kind = "image"
	clip.LUT = &editor.LUT{AssetID: "invert", Intensity: 1}
	project.Clips = []editor.Clip{clip}
	if p := previewPixel(t, tools, project, 0, 80, 45); p.R < 180 || p.G < 180 || p.B > 60 {
		t.Fatalf("蓝色经反相 LUT 应变为黄色，实际 %+v", p)
	}
	project.Clips[0].LUT.Intensity = 0
	if p := previewPixel(t, tools, project, 0, 80, 45); p.B < 180 || p.R > 60 {
		t.Fatalf("0 强度应保持源蓝色，实际 %+v", p)
	}
	project.Clips[0].LUT.Intensity = 0.5
	if p := previewPixel(t, tools, project, 0, 80, 45); p.R < 85 || p.R > 175 || p.G < 85 || p.G > 175 || p.B < 85 || p.B > 175 {
		t.Fatalf("50%% LUT 应混为中性灰，实际 %+v", p)
	}
}

func TestCancellationDuringRealEncoding(t *testing.T) {
	tools := integrationTools(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "source.png")
	writeImage(t, path, color.NRGBA{G: 255, A: 255})
	asset, err := tools.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	project := testProject()
	project.Assets = []editor.Asset{asset}
	clip := mediaClip("long", asset.ID, 3000)
	clip.Kind = "image"
	project.Clips = []editor.Clip{clip}
	output := filepath.Join(dir, "cancelled.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observedFrame := false
	err = tools.Export(ctx, project, ExportOptions{Path: output, Preset: "project"}, func(progress Progress) {
		if progress.Stage == "encoding" && progress.Frame > 0 {
			observedFrame = true
			cancel()
		}
	})
	if !observedFrame || !errors.Is(err, context.Canceled) {
		t.Fatalf("实际编码后的取消失败：frame=%v err=%v", observedFrame, err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("取消任务不应提交半成品")
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".aividcut-output-*"))
	if len(leftovers) > 0 {
		t.Fatalf("取消后遗留临时输出：%v", leftovers)
	}
}

func TestRealTextAndRationalRate(t *testing.T) {
	tools := integrationTools(t)
	if report := tools.CheckCapabilities(context.Background()); !report.DrawText.Available {
		t.Skip("真实工具未编译文字专用滤镜，本项字形渲染未执行：" + report.DrawText.Reason)
	}
	if DefaultFont() == "" {
		t.Skip("本机无文字验证字体，文字测试未执行")
	}
	project := testProject()
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	project.Clips = []editor.Clip{{ID: "title", Kind: "text", TrackID: "title", DurationFrames: 30, SpeedNum: 1, SpeedDen: 1,
		Transform: editor.Transform{Scale: 1}, Text: "AIVidCut 测试\n%{x}: ' [1]", TextStyle: editor.TextStyle{FontSize: 16, Color: "#ffffff80", Align: "center"}}}
	dir := t.TempDir()
	preview := filepath.Join(dir, "text.png")
	if err := tools.PreviewFrame(context.Background(), project, 0, preview); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(preview)
	im, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	var maxLuminance uint32
	for y := 0; y < 90; y++ {
		for x := 0; x < 160; x++ {
			r, g, b, _ := im.At(x, y).RGBA()
			if value := (r + g + b) / 3; value > maxLuminance {
				maxLuminance = value
			}
		}
	}
	if maxLuminance < 20*257 || maxLuminance > 180*257 {
		t.Fatalf("文字不存在或透明度未应用：%d", maxLuminance/257)
	}
	output := filepath.Join(dir, "rational.mp4")
	if err := tools.Export(context.Background(), project, ExportOptions{Path: output, Preset: "project"}, nil); err != nil {
		t.Fatal(err)
	}
	asset, err := tools.Probe(context.Background(), output)
	if err != nil || asset.FPSNum != 30000 || asset.FPSDen != 1001 || math.Abs(float64(asset.DurationUs)-1_001_000) > 40_000 {
		t.Fatalf("29.97fps 工程导出不符：%+v %v", asset, err)
	}
}
