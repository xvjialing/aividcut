package exporter

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
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

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/playback"
)

func nativeTools(t *testing.T) (media.Toolchain, playback.Config) {
	t.Helper()
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" || os.Getenv("AIVIDCUT_FFMPEG") == "" || os.Getenv("AIVIDCUT_FFPROBE") == "" {
		t.Skip("原生导出实测需要显式worker/FFmpeg/ffprobe，不把跳过当作通过")
	}
	return media.Discover(), playback.Config{WorkerPath: os.Getenv("AIVIDCUT_MLT_WORKER"), MLTRepository: os.Getenv("AIVIDCUT_MLT_REPOSITORY"), MLTProfilesPath: os.Getenv("AIVIDCUT_MLT_PROFILES")}
}
func nativeFixture(t *testing.T) editor.Project {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "蓝底.png")
	img := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			img.SetRGBA(x, y, color.RGBA{10, 30, 100, 255})
		}
	}
	f, _ := os.Create(path)
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	const count = 96000
	wav := make([]byte, 44+count*2)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 48000)
	binary.LittleEndian.PutUint32(wav[28:], 96000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], count*2)
	for i := 0; i < count; i++ {
		binary.LittleEndian.PutUint16(wav[44+i*2:], uint16(int16(12000*math.Sin(2*math.Pi*440*float64(i)/48000))))
	}
	audioPath := filepath.Join(dir, "声源.wav")
	os.WriteFile(audioPath, wav, 0600)
	p := editor.NewProject("同源字幕导出")
	p.Settings = editor.Settings{Width: 320, Height: 180, FPSNum: 30000, FPSDen: 1001, SampleRate: 48000}
	p.Assets = []editor.Asset{{ID: "image", Kind: "image", Name: "蓝底", Path: path, Width: 320, Height: 180}, {ID: "voice", Kind: "audio", Name: "声源", Path: audioPath, DurationUs: 2e6, HasAudio: true}}
	bg := editor.DefaultClip("bg", "track-video-1", "image")
	bg.AssetID = "image"
	bg.DurationFrames = 30
	text := editor.DefaultClip("title", "track-caption-1", "caption")
	text.Text = "你好 AIVidCut"
	text.TextStyle.FontSize = 24
	text.DurationFrames = 30
	audio := editor.DefaultClip("audio", "track-audio-1", "audio")
	audio.AssetID = "voice"
	audio.DurationFrames = 30
	p.Clips = []editor.Clip{bg, text, audio}
	return p
}
func command(t *testing.T, tool string, args ...string) []byte {
	t.Helper()
	c := exec.Command(tool, args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	data, err := c.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	return data
}

func TestNativeExportChineseAndNonzeroRationalRange(t *testing.T) {
	tools, config := nativeTools(t)
	p := nativeFixture(t)
	before, _ := json.Marshal(p)
	output := filepath.Join(t.TempDir(), "含字幕.mp4")
	opts := media.ExportOptions{Path: output, FromFrame: 3, ToFrame: 17}
	config.OfflineRender = true
	session, err := playback.Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	reference, err := session.Seek(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	expectedImage, expectedAudio := bytes.Clone(reference.RGBA), bytes.Clone(reference.PCM)
	session.Close()
	var last media.Progress
	if err := Native(context.Background(), tools, config, p, opts, func(value media.Progress) { last = value }); err != nil {
		t.Fatal(err)
	}
	if last.Fraction != 1 || last.Frame != 14 {
		t.Fatal("未在提交后报告实际完成", last)
	}
	decoded := command(t, tools.FFmpegPath, "-v", "error", "-i", output, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
	if len(decoded) != len(expectedImage) {
		t.Fatal("导出画幅变化", len(decoded))
	}
	var difference float64
	white := 0
	for i := 0; i < len(decoded); i += 4 {
		for channel := 0; channel < 3; channel++ {
			difference += math.Abs(float64(decoded[i+channel]) - float64(expectedImage[i+channel]))
		}
		if decoded[i] > 200 && decoded[i+1] > 200 && decoded[i+2] > 200 {
			white++
		}
	}
	difference /= float64(320 * 180 * 3)
	if difference > 4 || white < 50 {
		t.Fatal("导出未保留原生文字/色彩", difference, white)
	}
	pcm := command(t, tools.FFmpegPath, "-v", "error", "-i", output, "-vn", "-f", "f32le", "-ac", "2", "pipe:1")
	if len(pcm) < len(expectedAudio) {
		t.Fatal("导出音频缺失")
	}
	var mse float64
	for i := 0; i < len(expectedAudio); i += 4 {
		a := math.Float32frombits(binary.LittleEndian.Uint32(expectedAudio[i:]))
		b := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:]))
		mse += float64(a-b) * float64(a-b)
	}
	rms := math.Sqrt(mse / float64(len(expectedAudio)/4))
	if rms > .02 {
		t.Fatal("非零区间声音与源时钟错位", rms)
	}
	var metadata struct {
		Streams []struct {
			Type          string `json:"codec_type"`
			Width, Height int
			Frames        string `json:"nb_frames"`
			DurationTS    int64  `json:"duration_ts"`
			TimeBase      string `json:"time_base"`
		}
	}
	json.Unmarshal(command(t, tools.FFprobePath, "-v", "error", "-show_streams", "-of", "json", output), &metadata)
	expectedSamples := sampleAt(17, p.Settings) - sampleAt(3, p.Settings)
	for _, stream := range metadata.Streams {
		if stream.Type == "video" && stream.Frames != "14" {
			t.Fatal("视频帧数错误", stream)
		}
		if stream.Type == "audio" && (stream.TimeBase != "1/48000" || stream.DurationTS != expectedSamples) {
			t.Fatal("音频区间被封装阶段改变", stream, expectedSamples)
		}
	}
	after, _ := json.Marshal(p)
	if !bytes.Equal(before, after) {
		t.Fatal("导出修改工程")
	}
	t.Logf("原生中文导出14帧/%d样本，像素平均差%.6f，白字像素%d，声音RMS差%.8f", expectedSamples, difference, white, rms)
}
func TestNativeExportCancellationKeepsOldOutputAndCleansStaging(t *testing.T) {
	tools, config := nativeTools(t)
	p := nativeFixture(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "原有.mp4")
	old := []byte("保留原来的完整文件")
	os.WriteFile(output, old, 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := Native(ctx, tools, config, p, media.ExportOptions{Path: output, Overwrite: true}, func(v media.Progress) {
		if v.Stage == "原生合成与编码" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("取消原因不正确", err)
	}
	data, _ := os.ReadFile(output)
	if !bytes.Equal(data, old) {
		t.Fatal("取消破坏现存文件")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("失败暂存未清理", entries)
	}
}
func TestNativeExportOutputAndOptionGuards(t *testing.T) {
	p := nativeFixture(t)
	tools := media.Toolchain{FFmpegPath: "configured"}
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.mp4")
	os.WriteFile(existing, []byte("keep"), 0600)
	for _, opts := range []media.ExportOptions{{Path: existing}, {Path: filepath.Join(dir, "bad.mov")}, {Path: filepath.Join(dir, "new.mp4"), FontPath: "/font.ttf"}, {Path: filepath.Join(dir, "new.mp4"), FromFrame: 30, ToFrame: 1}, {Path: filepath.Join(dir, "new.mp4"), Preset: "bad"}} {
		if err := Native(context.Background(), tools, playback.Config{}, p, opts, nil); err == nil {
			t.Fatal("非法参数被接受", opts)
		}
	}
	for preset, expected := range map[string][2]int{"project": {1080, 1920}, "720p": {720, 1280}, "1080p": {1080, 1920}, "4k": {2160, 3840}} {
		w, h := media.OutputSize(1080, 1920, preset)
		if w != expected[0] || h != expected[1] {
			t.Fatal("纵向预设尺寸错误", preset, w, h)
		}
	}
}

func TestNativeExportFrameSampleClockUsesSharedUpwardBoundary(t *testing.T) {
	s := editor.Settings{FPSNum: 30000, FPSDen: 1001, SampleRate: 48000}
	for _, v := range [][2]int64{{0, 0}, {3, 4805}, {17, 27228}, {30, 48048}} {
		if got := sampleAt(v[0], s); got != v[1] {
			t.Fatal(v, got)
		}
	}
}

func TestNativeExportOriginal4KPortraitAndProjectSampleRate(t *testing.T) {
	tools, config := nativeTools(t)
	config.RenderQuality = "quarter"
	for _, size := range [][3]int{{3840, 2160, 48000}, {1080, 1920, 44100}} {
		p := nativeFixture(t)
		p.Settings.Width, p.Settings.Height, p.Settings.SampleRate = size[0], size[1], size[2]
		for i := range p.Clips {
			p.Clips[i].DurationFrames = 1
		}
		output := filepath.Join(t.TempDir(), "原画幅.mp4")
		if err := Native(context.Background(), tools, config, p, media.ExportOptions{Path: output, Preset: "project"}, nil); err != nil {
			t.Fatal(size, err)
		}
		var metadata struct {
			Streams []struct {
				Type          string `json:"codec_type"`
				Width, Height int
				Rate          string `json:"sample_rate"`
				Frames        string `json:"nb_frames"`
				Duration      int64  `json:"duration_ts"`
			}
		}
		if err := json.Unmarshal(command(t, tools.FFprobePath, "-v", "error", "-show_streams", "-of", "json", output), &metadata); err != nil {
			t.Fatal(err)
		}
		for _, s := range metadata.Streams {
			if s.Type == "video" && (s.Width != size[0] || s.Height != size[1] || s.Frames != "1") {
				t.Fatal("原画幅被预览尺寸替代", size, s)
			}
			if s.Type == "audio" && (s.Rate != fmt.Sprint(size[2]) || s.Duration != sampleAt(1, p.Settings)) {
				t.Fatal("项目采样率未保留有效时长", size, s)
			}
		}
		t.Logf("实际原画幅%d×%d、%dHz、1帧/%d有效样本", size[0], size[1], size[2], sampleAt(1, p.Settings))
	}
}

func TestNativeExportWindowedGraphAndLastCaption(t *testing.T) {
	tools, config := nativeTools(t)
	p := nativeFixture(t)
	p.Settings.FPSNum, p.Settings.FPSDen = 30, 1
	base, title, audio := p.Clips[0], p.Clips[1], p.Clips[2]
	p.Clips = nil
	for i := int64(0); i < 150; i++ {
		c := base
		c.ID = fmt.Sprintf("cut-%d", i)
		c.StartFrame = i
		c.DurationFrames = 1
		p.Clips = append(p.Clips, c)
	}
	title.DurationFrames = 150
	audio.DurationFrames = 60
	p.Clips = append(p.Clips, title, audio)
	output := filepath.Join(t.TempDir(), "跨图窗.mp4")
	if err := Native(context.Background(), tools, config, p, media.ExportOptions{Path: output}, nil); err != nil {
		t.Fatal(err)
	}
	frames := strings.TrimSpace(string(command(t, tools.FFprobePath, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=nb_frames", "-of", "default=nw=1:nk=1", output)))
	if frames != "150" {
		t.Fatal("窗口导出丢帧", frames)
	}
	last := command(t, tools.FFmpegPath, "-v", "error", "-i", output, "-vf", "select=eq(n\\,149)", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
	white := 0
	for i := 0; i < len(last); i += 4 {
		if last[i] > 200 && last[i+1] > 200 && last[i+2] > 200 {
			white++
		}
	}
	if white < 50 {
		t.Fatal("末窗字幕消失", white)
	}
	t.Logf("152片段窗口导出150帧，末帧白字像素%d", white)
}

func TestNativeExportVideoLUTOverlapAndSpeedMatchesNativeFrames(t *testing.T) {
	tools, config := nativeTools(t)
	p := nativeFixture(t)
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "编号测试源.mp4")
	command(t, tools.FFmpegPath, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=3", "-c:v", "libx264", "-threads", "1", "-g", "50", "-bf", "2", videoPath)
	a, err := tools.Probe(context.Background(), videoPath)
	if err != nil {
		t.Fatal(err)
	}
	p.Assets = append(p.Assets, a)
	lutPath := filepath.Join(dir, "交换红蓝.cube")
	cube := "LUT_3D_SIZE 2\n"
	for b := 0; b < 2; b++ {
		for g := 0; g < 2; g++ {
			for r := 0; r < 2; r++ {
				cube += fmt.Sprintf("%d %d %d\n", b, g, r)
			}
		}
	}
	os.WriteFile(lutPath, []byte(cube), 0600)
	p.Assets = append(p.Assets, editor.Asset{ID: "look", Kind: "lut", Path: lutPath, Name: "交换红蓝"})
	p.Clips[0].Kind, p.Clips[0].AssetID = "video", a.ID
	p.Clips[0].SpeedNum, p.Clips[0].SpeedDen = 3, 2
	p.Clips[0].SourceInUs = 137000
	p.Clips[0].LUT = &editor.LUT{AssetID: "look", Intensity: .37}
	p.Clips[0].Transform.Scale = .85
	p.Clips[0].Transform.Rotation = 12
	p.Clips[0].Transform.X = 17
	tracks := append([]editor.Track{}, p.Tracks[:2]...)
	tracks = append(tracks, editor.Track{ID: "overlay", Name: "叠加", Kind: "video"})
	p.Tracks = append(tracks, p.Tracks[2:]...)
	overlay := editor.DefaultClip("overlay", "overlay", "image")
	overlay.AssetID = p.Assets[0].ID
	overlay.DurationFrames = 30
	overlay.Transform.Scale = .35
	overlay.Transform.X = -80
	overlay.Transform.Y = -25
	alpha := image.NewNRGBA(image.Rect(0, 0, 320, 180))
	for i := 0; i < len(alpha.Pix); i += 4 {
		alpha.Pix[i], alpha.Pix[i+1], alpha.Pix[i+2], alpha.Pix[i+3] = 20, 80, 220, 166
	}
	alphaFile, err := os.Create(p.Assets[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(alphaFile, alpha); err != nil {
		t.Fatal(err)
	}
	alphaFile.Close()
	p.Clips = append(p.Clips, overlay)
	config.OfflineRender = true
	native, err := playback.Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	if _, err = native.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "复合画面.mp4")
	if err = Native(context.Background(), tools, config, p, media.ExportOptions{Path: output, FromFrame: 3, ToFrame: 17}, nil); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{3, 16} {
		reference, err := native.Seek(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		decoded := command(t, tools.FFmpegPath, "-v", "error", "-i", output, "-vf", fmt.Sprintf("select=eq(n\\,%d)", at-3), "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
		if len(decoded) != len(reference.RGBA) {
			t.Fatal("复合画幅变化")
		}
		var difference float64
		for i := 0; i < len(decoded); i += 4 {
			for c := 0; c < 3; c++ {
				difference += math.Abs(float64(decoded[i+c]) - float64(reference.RGBA[i+c]))
			}
		}
		difference /= float64(320 * 180 * 3)
		if difference > 6 {
			t.Fatal("变速/源入点/叠加/LUT未保留", at, difference)
		}
		t.Logf("源137ms/1.5倍速/37%%LUT/叠加，第%d全局帧导出像素平均差%.6f", at, difference)
	}
}

func TestNativeExportKeepsMeasuredShortAudioTail(t *testing.T) {
	tools, config := nativeTools(t)
	p := nativeFixture(t)
	count := int64(5777)
	a := &p.Assets[1]
	data, err := os.ReadFile(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	data = data[:44+int(count)*2]
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	binary.LittleEndian.PutUint32(data[40:], uint32(count*2))
	os.WriteFile(a.Path, data, 0600)
	a.DurationUs = 120355
	a.AudioSampleRate = 48000
	a.AudioSampleCount = &count
	for i := range p.Clips {
		p.Clips[i].DurationFrames = 4
	}
	p.Clips[2].AudioRange = &editor.AudioRange{Unit: "samples", Start: 0, End: count, SampleRate: 48000, OffsetDen: 1}
	output := filepath.Join(t.TempDir(), "精确音尾.mp4")
	if err := Native(context.Background(), tools, config, p, media.ExportOptions{Path: output, FromFrame: 3, ToFrame: 4}, nil); err != nil {
		t.Fatal(err)
	}
	pcm := command(t, tools.FFmpegPath, "-v", "error", "-i", output, "-vn", "-f", "f32le", "-ac", "2", "pipe:1")
	if len(pcm) < 1602*8 {
		t.Fatal("短音尾输出缺失", len(pcm))
	}
	rms := func(from, to int) float64 {
		var sum float64
		for i := from; i < to; i++ {
			v := float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[i*8:])))
			sum += v * v
		}
		return math.Sqrt(sum / float64(to-from))
	}
	before, after := rms(0, 800), rms(1250, 1550)
	if before < .03 || after > .003 {
		t.Fatal("精确音尾被截断或延长", before, after)
	}
	t.Logf("5777样本源尾导出最后1帧，前段RMS%.6f，尾部静音RMS%.8f", before, after)
}

func TestNativeLowerMemoryCancellationPreservesOldOutput(t *testing.T) {
	tools, config := nativeTools(t)
	p := nativeFixture(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "existing.mp4")
	old := []byte("保留原文件")
	if err := os.WriteFile(output, old, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := Native(ctx, tools, config, p, media.ExportOptions{Path: output, Overwrite: true, ResourceMode: "lower_memory"}, func(v media.Progress) {
		if v.Stage == "原生合成与编码" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(output)
	if !bytes.Equal(data, old) {
		t.Fatal("低内存取消覆盖了原输出")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("取消未清理暂存")
	}
}
