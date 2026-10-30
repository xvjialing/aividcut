package playback

import (
	"context"
	"encoding/binary"
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

func nativeConfig(t *testing.T) Config {
	t.Helper()
	path := os.Getenv("AIVIDCUT_MLT_WORKER")
	if path == "" {
		t.Skip("未显式设置AIVIDCUT_MLT_WORKER；原生MLT验收跳过，不能算通过")
	}
	return Config{WorkerPath: path, MLTRepository: os.Getenv("AIVIDCUT_MLT_REPOSITORY"), MLTProfilesPath: os.Getenv("AIVIDCUT_MLT_PROFILES"), MLTDataPath: os.Getenv("AIVIDCUT_MLT_DATA"), MaxWidth: 160, MaxHeight: 90, Timeout: 10 * time.Second}
}
func picture(t *testing.T, name string, c color.RGBA) editor.Asset {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(f, img)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	return editor.Asset{ID: name, Name: name, Path: path, Kind: "image", Width: 32, Height: 32}
}
func tone(t *testing.T) editor.Asset {
	return toneFrequency(t, 440)
}
func toneFrequency(t *testing.T, frequency float64) editor.Asset {
	t.Helper()
	const samples = 96000
	data := make([]byte, 44+samples*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 48000)
	binary.LittleEndian.PutUint32(data[28:], 96000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], samples*2)
	for n := 0; n < samples; n++ {
		value := int16(12000 * math.Sin(2*math.Pi*frequency*float64(n)/48000))
		binary.LittleEndian.PutUint16(data[44+n*2:], uint16(value))
	}
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return editor.Asset{ID: "tone", Name: "tone.wav", Path: path, Kind: "audio", DurationUs: 2_000_000, HasAudio: true}
}

func TestNativeExactAudioTailIsAppliedBeforeMixingOtherTrack(t *testing.T) {
	session, err := Start(context.Background(), nativeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	project := nativeProject(t)
	count := int64(96000)
	project.Assets[2].AudioSampleRate, project.Assets[2].AudioSampleCount = 48000, &count
	project.Clips[2].AudioRange = &editor.AudioRange{Unit: "samples", Start: 0, End: 5777, SampleRate: 48000, OffsetDen: 1}
	if _, err := session.Load(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	if err := session.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	var samples []float64
	for n := 0; n < 30; n++ {
		frame, err := session.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(frame.PCM); i += 8 {
			samples = append(samples, float64(math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[i:]))))
		}
	}
	if len(samples) < 20000 {
		t.Fatal("未取得足够真实样本")
	}
	before := false
	for i, value := range samples {
		if i < 5777 && math.Abs(value) > .01 {
			before = true
		}
		if i >= 5777 && math.Abs(value) > 1e-7 {
			t.Fatalf("精确源样本末端之后仍有声音: index=%d value=%g", i, value)
		}
	}
	if !before {
		t.Fatal("音尾门控把整段音轨静音")
	}
	other := toneFrequency(t, 880)
	other.ID = "other-tone"
	project.Assets = append(project.Assets, other)
	project.Tracks = append(project.Tracks, editor.Track{ID: "other-audio", Kind: "audio", Name: "不应被截断"})
	clip := editor.DefaultClip("other-tone-clip", "other-audio", "audio")
	clip.AssetID, clip.DurationFrames = other.ID, 30
	project.Clips = append(project.Clips, clip)
	if _, err := session.Load(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	if err := session.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	samples = samples[:0]
	for n := 0; n < 30; n++ {
		frame, err := session.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(frame.PCM); i += 8 {
			samples = append(samples, float64(math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[i:]))))
		}
	}
	amplitude := func(frequency float64) float64 {
		var real, imag float64
		for i := 10000; i < 40000; i++ {
			phase := 2 * math.Pi * frequency * float64(i) / 48000
			real += samples[i] * math.Cos(phase)
			imag += samples[i] * math.Sin(phase)
		}
		return math.Hypot(real, imag) * 2 / 30000
	}
	if amplitude(880) < .1 || amplitude(440) > .02 {
		t.Fatal("音尾未在逐路混音前生效，影响了其它音轨", amplitude(880), amplitude(440))
	}
}

func TestNativeLUTAndTextAlphaAreRealPixels(t *testing.T) {
	session, err := Start(context.Background(), nativeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	project := nativeProject(t)
	if !session.Capabilities().LUT.Supported {
		t.Skip("实际MLT没有avfilter.lut3d，LUT仍为明确门槛")
	}
	path := filepath.Join(t.TempDir(), "swap.cube")
	cube := "LUT_3D_SIZE 2\n"
	for b := 0; b < 2; b++ {
		for g := 0; g < 2; g++ {
			for r := 0; r < 2; r++ {
				cube += fmt.Sprintf("%d %d %d\n", g, r, b)
			}
		}
	}
	if err := os.WriteFile(path, []byte(cube), 0600); err != nil {
		t.Fatal(err)
	}
	project.Assets = append(project.Assets, editor.Asset{ID: "cube", Name: "swap.cube", Kind: "lut", Path: path})
	project.Clips[0].LUT = &editor.LUT{AssetID: "cube", Intensity: 1}
	if _, err := session.Load(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	frame, err := session.Seek(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	at := (45*frame.Width + 50) * 4
	if frame.RGBA[at+1] < 180 || frame.RGBA[at] > 50 {
		t.Fatal("3D LUT没有真正改变红色像素", frame.RGBA[at:at+4])
	}
	if !session.Capabilities().Text.Supported {
		t.Skip("实际MLT没有qtext，文字仍为明确门槛")
	}
	project.Clips[0].LUT = nil
	project.Tracks = append([]editor.Track{{ID: "native-text", Kind: "text", Name: "文字顶层"}}, project.Tracks...)
	text := editor.DefaultClip("native-text-clip", "native-text", "text")
	text.Text, text.DurationFrames, text.TextStyle.FontSize, text.TextStyle.Color = "MLT", 30, 48, "#ffffff80"
	project.Clips = append(project.Clips, text)
	if _, err := session.Load(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	frame, err = session.Seek(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for i := 0; i < len(frame.RGBA); i += 4 {
		if frame.RGBA[i+1] > 70 && frame.RGBA[i+1] < 190 {
			changed++
		}
	}
	if changed < 10 {
		t.Fatal("qtext没有生成实际半透明字体像素", changed)
	}
	if frame.RGBA[1] > 20 {
		t.Fatal("透明文字把背景整帧填满")
	}
}

func TestNativeQTextTreatsMarkupAsLiteralAndPreservesNewline(t *testing.T) {
	session, err := Start(context.Background(), nativeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !session.Capabilities().Text.Supported {
		t.Skip("缺少qtext，plain文字原生验收未通过")
	}
	project := editor.NewProject("纯文字夹具")
	project.Settings.Width, project.Settings.Height = 320, 180
	clip := editor.DefaultClip("literal", "track-text-1", "text")
	clip.DurationFrames, clip.TextStyle.FontSize, clip.TextStyle.Color = 30, 18, "#ffffff"
	project.Clips = []editor.Clip{clip}
	bounds := func(text string) (int, int) {
		project.Clips[0].Text = text
		if _, err := session.Load(context.Background(), project); err != nil {
			t.Fatal(err)
		}
		frame, err := session.Seek(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}
		left, top, right, bottom := frame.Width, frame.Height, -1, -1
		for y := 0; y < frame.Height; y++ {
			for x := 0; x < frame.Width; x++ {
				at := (y*frame.Width + x) * 4
				if frame.RGBA[at] > 80 && frame.RGBA[at+1] > 80 && frame.RGBA[at+2] > 80 {
					left = min(left, x)
					top = min(top, y)
					right = max(right, x)
					bottom = max(bottom, y)
				}
			}
		}
		if right < left {
			t.Fatal("没有文字像素")
		}
		return right - left + 1, bottom - top + 1
	}
	plainWidth, plainHeight := bounds("字")
	literalWidth, _ := bounds("<b>字</b>")
	_, multilineHeight := bounds("<b>字</b>\n换行")
	if literalWidth <= plainWidth*2 || multilineHeight <= plainHeight*3/2 {
		t.Fatal("qtext把字面标签当HTML或丢掉换行", plainWidth, literalWidth, plainHeight, multilineHeight)
	}
	// 同字符数但实际字形宽度不同，不能用rune数估算是否会触发qtext缩字。
	project.Clips[0].Text = strings.Repeat("i", 30)
	if err := CheckProject(context.Background(), nativeConfig(t), project); err != nil {
		t.Fatal("实际窄字形应容纳在框内", err)
	}
	project.Clips[0].Text = strings.Repeat("W", 30)
	if err := CheckProject(context.Background(), nativeConfig(t), project); err == nil || !strings.Contains(err.Error(), "实际字形") {
		t.Fatal("实际宽字形会触发自动缩字却未拒绝", err)
	}
}

func TestNativeRealVideoAndLateAudioKeepSourceTimeOrigin(t *testing.T) {
	config := nativeConfig(t)
	ffmpeg := os.Getenv("AIVIDCUT_FFMPEG")
	if ffmpeg == "" {
		t.Skip("未显式配置开发FFmpeg，真实AV晚起音轨验收跳过")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "late.mov")
	command := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "color=red:s=64x64:r=25:d=2", "-itsoffset", "0.5", "-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000:duration=0.75", "-c:v", "mpeg4", "-c:a", "pcm_s16le", path)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("自建AV夹具失败: %v %s", err, data)
	}
	session, err := Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	assertNativeLateAVSourceClock(t, session, path)
	shifted := filepath.Join(t.TempDir(), "late-format-start3.mov")
	command = exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "lavfi", "-i", "color=red:s=64x64:r=25:d=2", "-itsoffset", "0.5", "-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000:duration=0.75", "-c:v", "mpeg4", "-c:a", "pcm_s16le", "-output_ts_offset", "3", shifted)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("带format起点AV夹具失败: %v %s", err, data)
	}
	assertNativeLateAVSourceClock(t, session, shifted)
}

func assertNativeLateAVSourceClock(t *testing.T, session *Session, path string) {
	t.Helper()
	for _, sourceIn := range []int64{0, 400000, 800000} {
		project := editor.NewProject("晚起音轨")
		project.Settings.Width, project.Settings.Height, project.Settings.FPSNum, project.Settings.FPSDen = 320, 180, 25, 1
		project.Assets = []editor.Asset{{ID: "late", Kind: "video", Name: "late.mov", Path: path, Width: 64, Height: 64, DurationUs: 2_000_000, HasAudio: true}}
		clip := editor.DefaultClip("late-clip", "track-video-1", "video")
		clip.AssetID, clip.SourceInUs, clip.DurationFrames = "late", sourceIn, (2_000_000-sourceIn)*25/1_000_000
		project.Clips = []editor.Clip{clip}
		if _, err := session.Load(context.Background(), project); err != nil {
			t.Fatal(err)
		}
		if _, err := session.Seek(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
		if err := session.Play(context.Background()); err != nil {
			t.Fatal(err)
		}
		var audio []float64
		for n := int64(0); n < clip.DurationFrames; n++ {
			frame, err := session.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				at := (frame.Height/2*frame.Width + frame.Width/2) * 4
				if frame.RGBA[at] < 180 || frame.RGBA[at+1] > 50 {
					t.Fatal("真实视频未解码红色画面")
				}
			}
			for i := 0; i < len(frame.PCM); i += 8 {
				audio = append(audio, float64(math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[i:]))))
			}
		}
		start := int(max(int64(0), 500000-sourceIn) * 48000 / 1_000_000)
		for i := 0; i < start; i++ {
			if math.Abs(audio[i]) > 1e-6 {
				t.Fatalf("源入点%d：晚起音轨被压到零或提前发声，sample=%d", sourceIn, i)
			}
		}
		nonzero := false
		for i := start + 100; i < min(start+10000, len(audio)); i++ {
			if math.Abs(audio[i]) > .01 {
				nonzero = true
				break
			}
		}
		if !nonzero {
			t.Fatal("晚起音轨没有真实声音")
		}
		phaseOffset := int(max(int64(0), sourceIn-500000) * 48000 / 1_000_000)
		for sample := 100; sample < 1000; sample++ {
			expected := .125 / math.Sqrt2 * math.Sin(2*math.Pi*660*float64(phaseOffset+sample)/48000)
			if math.Abs(audio[start+sample]-expected) > .01 {
				t.Fatalf("源入点%d：实际音频源区间错位，sample=%d got=%g want=%g", sourceIn, sample, audio[start+sample], expected)
			}
		}
		end := int((1250000 - sourceIn) * 48000 / 1_000_000)
		for i := max(0, end); i < len(audio); i++ {
			if math.Abs(audio[i]) > 1e-6 {
				t.Fatalf("源入点%d：真实音轨尾后仍有声音 sample=%d value=%g", sourceIn, i, audio[i])
			}
		}
		target := (800000 - sourceIn) * 25 / 1_000_000
		if _, err := session.Seek(context.Background(), target); err != nil {
			t.Fatal(err)
		}
		if err := session.Play(context.Background()); err != nil {
			t.Fatal(err)
		}
		frame, err := session.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for sample := 100; sample < 500; sample++ {
			actual := float64(math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[sample*8:])))
			expected := .125 / math.Sqrt2 * math.Sin(2*math.Pi*660*float64(14400+sample)/48000)
			if math.Abs(actual-expected) > .01 {
				t.Fatalf("seek/preview后播放源相位错位: sourceIn=%d sample=%d got=%g want=%g", sourceIn, sample, actual, expected)
			}
		}
	}
}
func nativeProject(t *testing.T) editor.Project {
	p := editor.NewProject("MLT自建夹具")
	p.Settings.Width, p.Settings.Height, p.Settings.FPSNum, p.Settings.FPSDen = 320, 180, 30000, 1001
	p.Assets = []editor.Asset{picture(t, "bottom.png", color.RGBA{R: 255, A: 255}), picture(t, "top.png", color.RGBA{B: 255, A: 255}), tone(t)}
	bottom := editor.DefaultClip("bottom", "track-video-1", "image")
	bottom.AssetID, bottom.DurationFrames = p.Assets[0].ID, 30
	top := editor.DefaultClip("top", "track-image-top", "image")
	top.AssetID, top.DurationFrames, top.Transform.Scale, top.Transform.X = p.Assets[1].ID, 30, .5, 40
	p.Tracks = append([]editor.Track{{ID: "track-image-top", Kind: "video", Name: "顶层"}}, p.Tracks...)
	audio := editor.DefaultClip("audio", "track-audio-1", "audio")
	audio.AssetID, audio.DurationFrames = p.Assets[2].ID, 30
	p.Clips = []editor.Clip{bottom, top, audio}
	return p
}

func TestNativePersistentMLTPullCompositionAudioClockPauseSeekClose(t *testing.T) {
	ctx := context.Background()
	session, err := Start(ctx, nativeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !session.Capabilities().Available {
		t.Fatal("实际MLT缺少基础插件", session.Capabilities().Reason)
	}
	project := nativeProject(t)
	info, err := session.Load(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 160 || info.Height != 90 || info.SampleRate != 48000 {
		t.Fatal("代理输出或真实时钟不正确", info)
	}
	frame, err := session.Seek(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	pixel := func(x, y int) [4]byte {
		at := (y*frame.Width + x) * 4
		return [4]byte{frame.RGBA[at], frame.RGBA[at+1], frame.RGBA[at+2], frame.RGBA[at+3]}
	}
	if c := pixel(50, 45); c[0] < 200 || c[2] > 40 || c[3] < 250 {
		t.Fatal("真实底层画面不为红色", c)
	}
	if c := pixel(110, 45); c[2] < 200 || c[0] > 40 || c[3] < 250 {
		t.Fatal("顶层缩放/位移没有使用原画幅坐标", c)
	}
	if c := pixel(2, 2); c[0] > 20 || c[1] > 20 || c[2] > 20 {
		t.Fatal("fit未保留背景黑边", c)
	}
	if err := session.Play(ctx); err != nil {
		t.Fatal(err)
	}
	var nextSample int64
	nonzero := false
	sampleSizes := map[int]bool{}
	for n := 0; n < 30; n++ {
		frame, err = session.Next(ctx)
		if err != nil {
			t.Fatalf("第%d帧: %v", n, err)
		}
		if frame.Position != int64(n) || frame.AudioStartSample != nextSample || frame.SampleRate != 48000 || frame.Channels != 2 || len(frame.PCM) != frame.Samples*8 {
			t.Fatal("29.97输出采样不连续", n, frame.Position, frame.AudioStartSample, nextSample, frame.Samples)
		}
		nextSample += int64(frame.Samples)
		sampleSizes[frame.Samples] = true
		for i := 0; i < len(frame.PCM); i += 8 {
			if math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[i:])))) > .01 {
				nonzero = true
				break
			}
		}
		if frame.End != (n == 29) {
			t.Fatal("尾帧停止语义不符")
		}
	}
	if len(sampleSizes) < 2 || !nonzero {
		t.Fatal("未取得真实交替样本数或音频", sampleSizes, nonzero)
	}
	if _, err := session.Next(ctx); err == nil {
		t.Fatal("End后仍假装继续播放")
	}
	if _, err := session.Seek(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if err := session.Play(ctx); err != nil {
		t.Fatal(err)
	}
	frame, err = session.Next(ctx)
	if err != nil || frame.Position != 7 {
		t.Fatal("seek消耗或错位播放游标", err)
	}
	if err := session.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Next(ctx); err == nil {
		t.Fatal("暂停仍在拉播放帧")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("持久进程未退出")
	}
}

func TestNativeProjectRejectsUnverifiedFeaturesInsteadOfIgnoring(t *testing.T) {
	caps := Capabilities{Available: true, Filters: []string{"volume"}}
	for _, name := range []string{"rotation", "speed", "lut", "text"} {
		t.Run(name, func(t *testing.T) {
			p := nativeProject(t)
			switch name {
			case "rotation":
				p.Clips[0].Transform.Rotation = 90
			case "speed":
				p.Clips[0].SpeedNum = 2
			case "lut":
				p.Clips[0].LUT = &editor.LUT{AssetID: "missing-lut", Intensity: 1}
			case "text":
				p.Clips[0].Kind = "text"
				p.Clips[0].Text = "字"
				p.Clips[0].AssetID = ""
			}
			if _, _, err := compileProject(p, caps); err == nil {
				t.Fatal("未验证特性被静默忽略")
			}
		})
	}
}
