package playback

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
)

func nativeSourceSession(t *testing.T) *Session {
	t.Helper()
	s, err := Start(context.Background(), nativeConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func nativeSourceProject(t *testing.T, sourceIn int64, speedN, speedD int) editor.Project {
	t.Helper()
	p := editor.NewProject("源采样时钟实测")
	p.Settings.Width, p.Settings.Height, p.Settings.FPSNum, p.Settings.FPSDen = 32, 32, 30000, 1001
	asset := tone(t)
	count := int64(96000)
	asset.AudioSampleRate, asset.AudioSampleCount = 48000, &count
	p.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("source-audio", "track-audio-1", "audio")
	clip.AssetID = asset.ID
	clip.SourceInUs = sourceIn
	clip.SpeedNum, clip.SpeedDen = speedN, speedD
	var err error
	clip, err = editor.AudioClipToSourceEnd(clip, asset, p.Settings)
	if err != nil {
		t.Fatal(err)
	}
	p.Clips = []editor.Clip{clip}
	return p
}
func nativeSourcePCM(t *testing.T, s *Session, p editor.Project) []float64 {
	t.Helper()
	info, err := s.Load(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	var values []float64
	for i := int64(0); i < info.DurationFrames; i++ {
		frame, err := s.Next(context.Background())
		if err != nil {
			t.Fatalf("frame%d: %v", i, err)
		}
		if frame.AudioStartSample != int64(len(values)) {
			t.Fatalf("有理输出时钟不连续：frame%d start=%d want=%d", i, frame.AudioStartSample, len(values))
		}
		for at := 0; at < len(frame.PCM); at += 8 {
			values = append(values, float64(math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[at:]))))
		}
	}
	return values
}
func nativeSourceEdit(t *testing.T, p editor.Project, commands ...editor.Command) editor.Project {
	t.Helper()
	engine, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	p, err = engine.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: editor.NewID("native-source-test"), Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestNativeSourceFractionalSplitTrimAndNegativeOffsetKeepSamples(t *testing.T) {
	s := nativeSourceSession(t)
	p := nativeSourceProject(t, 0, 1, 1)
	whole := nativeSourcePCM(t, s, p)
	split := nativeSourceEdit(t, p, editor.Command{Type: "split_clip", ClipID: p.Clips[0].ID, AtFrame: 2})
	joined := nativeSourcePCM(t, s, split)
	if len(whole) != len(joined) {
		t.Fatal("切开后改变输出样本长度")
	}
	for i := range whole {
		if math.Abs(whole[i]-joined[i]) > 1e-7 {
			t.Fatalf("29.97共同sample切口丢失/重复或相位改变：sample%d %g != %g", i, whole[i], joined[i])
		}
	}
	trimmed := p.Clips[0]
	trimmed.StartFrame = 2
	trimmed.DurationFrames -= 2
	trimmed.SourceInUs, _ = editor.SourceDurationUs(2, p.Settings, 1, 1)
	trimProject := nativeSourceEdit(t, p, editor.Command{Type: "update_clip", Clip: &trimmed})
	right := nativeSourceEdit(t, split, editor.Command{Type: "delete_clip", ClipID: split.Clips[0].ID})
	a, b := nativeSourcePCM(t, s, trimProject), nativeSourcePCM(t, s, right)
	for i := range a {
		if math.Abs(a[i]-b[i]) > 1e-7 {
			t.Fatal("直接trim与split删左的实际PCM不一致", i)
		}
	}
	// 负分数偏移使第3203样本在画面StartFrame2的3204采样界之前发声。
	negative := p.Clips[0]
	negative.StartFrame, negative.DurationFrames, negative.SourceInUs = 2, 2, 66675
	negative.AudioRange = &editor.AudioRange{Unit: "samples", Start: 3200, End: 5000, SampleRate: 48000, OffsetNum: -25, OffsetDen: 3}
	p.Clips = []editor.Clip{negative}
	audio := nativeSourcePCM(t, s, p)
	for i, value := range audio {
		expected := 0.0
		if i >= 3203 && i < 5003 {
			raw := int16(12000 * math.Sin(2*math.Pi*440*float64(i-3203+3200)/48000))
			expected = float64(raw) / 32768 / math.Sqrt2
		}
		if math.Abs(value-expected) > 1e-6 {
			t.Fatalf("负offset没有覆盖视觉起点前的真实样本：sample%d=%g want=%g", i, value, expected)
		}
	}
}

func TestNativeSourceHugeOffsetDenominatorKeepsSingleActualSample(t *testing.T) {
	s := nativeSourceSession(t)
	for _, denominator := range []int64{10_000_000_000_000, 100_000_000_000_000} {
		p := nativeSourceProject(t, 0, 1, 1)
		clip := p.Clips[0]
		clip.SourceInUs, clip.DurationFrames = 20, 1
		clip.AudioRange = &editor.AudioRange{Unit: "samples", Start: 1, End: 2, SampleRate: 48000, OffsetNum: 1, OffsetDen: denominator}
		p.Clips = []editor.Clip{clip}
		audio := nativeSourcePCM(t, s, p)
		want := float64(int16(12000*math.Sin(2*math.Pi*440/48000))) / 32768 / math.Sqrt2
		for i, value := range audio {
			expected := 0.0
			if i == 1 {
				expected = want
			}
			if math.Abs(value-expected) > 1e-7 {
				log := s.command.Stderr.(*boundedStderr)
				log.mu.Lock()
				data := string(log.data)
				log.mu.Unlock()
				t.Fatalf("合法大分母被溢出/浮点ceil静音或错位：den=%d sample%d=%g want=%g\n%s", denominator, i, value, expected, data)
			}
		}
	}
}
func TestNativeSourceArbitraryMicrosecondInAndPitchPreservingSpeeds(t *testing.T) {
	s := nativeSourceSession(t)
	for _, speed := range [][2]int{{1, 8}, {1, 2}, {3, 2}, {2, 1}, {8, 1}} {
		t.Run(fmt.Sprintf("%d-%d", speed[0], speed[1]), func(t *testing.T) {
			p := nativeSourceProject(t, 12345, speed[0], speed[1])
			values := nativeSourcePCM(t, s, p)
			amplitude := func(frequency float64) float64 {
				var re, im float64
				for i := 4800; i < min(24000, len(values)); i++ {
					phase := 2 * math.Pi * frequency * float64(i) / 48000
					re += values[i] * math.Cos(phase)
					im += values[i] * math.Sin(phase)
				}
				return math.Hypot(re, im) * 2 / float64(min(24000, len(values))-4800)
			}
			if amplitude(440) < .12 || amplitude(440*float64(speed[0])/float64(speed[1])) > .04 {
				t.Fatalf("变速改变口播音高或没有真实声音：440=%g wrong=%g", amplitude(440), amplitude(440*float64(speed[0])/float64(speed[1])))
			}
			if _, err := s.Seek(context.Background(), min(int64(15), editor.DurationFrames(p)-1)); err != nil {
				t.Fatal(err)
			}
			if err := s.Play(context.Background()); err != nil {
				t.Fatal(err)
			}
			frame, err := s.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < frame.Samples; i++ {
				value := float64(math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[i*8:])))
				want := values[int(frame.AudioStartSample)+i]
				if math.Abs(value-want) > 1e-6 {
					t.Fatalf("seek重建atempo状态改变相位：sample%d got=%g want=%g", i, value, want)
				}
			}
		})
	}
}

func TestNativeSource44100ResampleSplitTrimAndSpeedSeek(t *testing.T) {
	s := nativeSourceSession(t)
	wav := tone(t)
	path := filepath.Join(t.TempDir(), "source-44100.wav")
	nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-i", wav.Path, "-ar", "44100", "-c:a", "pcm_s16le", path)
	tools := media.Discover()
	asset, err := tools.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	measured, err := tools.MeasureAudioSamples(context.Background(), asset)
	if err != nil {
		t.Fatal(err)
	}
	count := measured.SampleCount
	asset.AudioSampleRate, asset.AudioSampleCount, asset.AudioStartUs = measured.SampleRate, &count, measured.SourceStartUs
	for _, speed := range [][2]int{{1, 1}, {1, 2}, {3, 2}} {
		p := editor.NewProject("44.1到48k源相位")
		p.Settings.Width, p.Settings.Height, p.Settings.FPSNum, p.Settings.FPSDen = 32, 32, 30000, 1001
		p.Assets = []editor.Asset{asset}
		clip := editor.DefaultClip("source-44100", "track-audio-1", "audio")
		clip.AssetID, clip.SourceInUs, clip.SpeedNum, clip.SpeedDen = asset.ID, 12345, speed[0], speed[1]
		clip, err = editor.AudioClipToSourceEnd(clip, asset, p.Settings)
		if err != nil {
			t.Fatal(err)
		}
		p.Clips = []editor.Clip{clip}
		whole := nativeSourcePCM(t, s, p)
		{
			split := p
			for _, at := range []int64{1, 2, 5, 10, 20, 30, 40, 50} {
				for _, selected := range split.Clips {
					if selected.StartFrame < at && at < selected.StartFrame+selected.DurationFrames {
						split = nativeSourceEdit(t, split, editor.Command{Type: "split_clip", ClipID: selected.ID, AtFrame: at})
						break
					}
				}
			}
			joined := nativeSourcePCM(t, s, split)
			for i := range whole {
				if math.Abs(whole[i]-joined[i]) > 1e-7 {
					t.Fatalf("44.1→48k切开后源相位/端点改变：sample%d whole=%g split=%g", i, whole[i], joined[i])
				}
			}
			trim := clip
			trim.StartFrame, trim.DurationFrames = 30, trim.DurationFrames-30
			delta, _ := editor.SourceDurationUs(30, p.Settings, speed[0], speed[1])
			trim.SourceInUs += delta
			a := nativeSourcePCM(t, s, nativeSourceEdit(t, p, editor.Command{Type: "update_clip", Clip: &trim}))
			right := nativeSourceEdit(t, p, editor.Command{Type: "split_clip", ClipID: clip.ID, AtFrame: 30})
			b := nativeSourcePCM(t, s, nativeSourceEdit(t, right, editor.Command{Type: "delete_clip", ClipID: right.Clips[0].ID}))
			for i := range a {
				if math.Abs(a[i]-b[i]) > 1e-7 {
					t.Fatal("44.1→48k trim和split删左不一致", i)
				}
			}
		}
		if _, err := s.Load(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Seek(context.Background(), 20); err != nil {
			t.Fatal(err)
		}
		if err := s.Play(context.Background()); err != nil {
			t.Fatal(err)
		}
		frame, err := s.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < frame.Samples; i++ {
			value := float64(math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[i*8:])))
			if math.Abs(value-whole[int(frame.AudioStartSample)+i]) > 1e-7 {
				t.Fatalf("44.1→48k speed=%v Seek源相位改变：sample%d", speed, i)
			}
		}
	}
}

func TestNativeSourceVideoSplitPreservesOriginalSamplingPhase(t *testing.T) {
	s := nativeSourceSession(t)
	dir := t.TempDir()
	for n := 0; n < 18; n++ {
		file, err := os.Create(filepath.Join(dir, fmt.Sprintf("f-%02d.png", n)))
		if err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for at := 0; at < len(img.Pix); at += 4 {
			copy(img.Pix[at:], []byte{byte(n * 13), 80, byte(230 - n*9), 255})
		}
		if err := png.Encode(file, img); err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
	path := filepath.Join(dir, "sixty-fps.mkv")
	nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-framerate", "60", "-i", filepath.Join(dir, "f-%02d.png"), "-c:v", "ffv1", "-pix_fmt", "bgr0", path)
	asset, err := media.Discover().Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	p := editor.NewProject("60fps源的共享画面采样相位")
	p.Settings.Width, p.Settings.Height, p.Settings.FPSNum, p.Settings.FPSDen = 32, 32, 30000, 1001
	p.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("video-phase", "track-video-1", "video")
	clip.AssetID, clip.DurationFrames = asset.ID, 8
	p.Clips = []editor.Clip{clip}
	if _, err := s.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	var whole [][4]byte
	for i := int64(0); i < 8; i++ {
		frame, err := s.Seek(context.Background(), i)
		if err != nil {
			t.Fatal(err)
		}
		at := (16*32 + 16) * 4
		whole = append(whole, [4]byte{frame.RGBA[at], frame.RGBA[at+1], frame.RGBA[at+2], frame.RGBA[at+3]})
	}
	for _, cut := range []int64{1, 2} {
		split := nativeSourceEdit(t, p, editor.Command{Type: "split_clip", ClipID: clip.ID, AtFrame: cut})
		if _, err := s.Load(context.Background(), split); err != nil {
			t.Fatal(err)
		}
		for i := int64(0); i < 8; i++ {
			frame, err := s.Seek(context.Background(), i)
			if err != nil {
				t.Fatal(err)
			}
			at := (16*32 + 16) * 4
			actual := [4]byte{frame.RGBA[at], frame.RGBA[at+1], frame.RGBA[at+2], frame.RGBA[at+3]}
			if actual != whole[i] {
				t.Fatalf("仅split改变了未删除画面：cut%d frame%d actual=%v whole=%v", cut, i, actual, whole[i])
			}
		}
	}
}
func nativeSourceCommand(t *testing.T, args ...string) []byte {
	t.Helper()
	ffmpeg := os.Getenv("AIVIDCUT_FFMPEG")
	if ffmpeg == "" {
		t.Skip("需显式FFmpeg生成真实源PTS/参考帧")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("自造源/参考失败：%v %s", err, data)
	}
	return data
}
func TestNativeSourceCFRAndVFRActualPTSMatchFFmpegSampling(t *testing.T) {
	s := nativeSourceSession(t)
	for _, variable := range []bool{false, true} {
		t.Run(fmt.Sprint(variable), func(t *testing.T) {
			dir := t.TempDir()
			for n := 0; n < 18; n++ {
				path := filepath.Join(dir, fmt.Sprintf("frame-%02d.png", n))
				file, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				img := image.NewRGBA(image.Rect(0, 0, 32, 32))
				for at := 0; at < len(img.Pix); at += 4 {
					copy(img.Pix[at:], []byte{byte(n * 13), 80, byte(230 - n*9), 255})
				}
				if err := png.Encode(file, img); err != nil {
					t.Fatal(err)
				}
				file.Close()
			}
			path := filepath.Join(dir, "source.mkv")
			args := []string{"-hide_banner", "-loglevel", "error", "-framerate", "25", "-i", filepath.Join(dir, "frame-%02d.png")}
			if variable {
				args = append(args, "-vf", "setpts=(N+floor(N/3))*0.04/TB", "-fps_mode", "vfr")
			}
			args = append(args, "-c:v", "ffv1", "-pix_fmt", "bgr0", path)
			nativeSourceCommand(t, args...)
			tools := media.Discover()
			asset, err := tools.Probe(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			for _, speed := range [][2]int{{1, 2}, {3, 2}, {2, 1}} {
				p := editor.NewProject("实际PTS视频")
				p.Settings.Width, p.Settings.Height, p.Settings.FPSNum, p.Settings.FPSDen = 32, 32, 30000, 1001
				p.Assets = []editor.Asset{asset}
				clip := editor.DefaultClip("source-picture", "track-video-1", "video")
				clip.AssetID, clip.SourceInUs, clip.DurationFrames = asset.ID, 17000, 8
				clip.SpeedNum, clip.SpeedDen = speed[0], speed[1]
				p.Clips = []editor.Clip{clip}
				if _, err := s.Load(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				reference := nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-ss", "0.017", "-i", path, "-vf", fmt.Sprintf("setpts=(PTS-STARTPTS)*%d/%d,fps=30000/1001", speed[1], speed[0]), "-frames:v", "8", "-pix_fmt", "rgba", "-f", "rawvideo", "pipe:1")
				if len(reference) < 8*32*32*4 {
					t.Fatal("参考帧不足")
				}
				for _, position := range []int64{0, 1, 2, 5, 3, 7} {
					frame, err := s.Seek(context.Background(), position)
					if err != nil {
						log := s.command.Stderr.(*boundedStderr)
						log.mu.Lock()
						data := string(log.data)
						log.mu.Unlock()
						t.Fatalf("原生源读取失败：%v\n%s", err, data)
					}
					at := (16*32 + 16) * 4
					want := reference[int(position)*32*32*4+at:]
					for c := 0; c < 3; c++ {
						if math.Abs(float64(frame.RGBA[at+c])-float64(want[c])) > 2 {
							t.Fatalf("VFR=%t speed=%v frame%d pixel=%v want=%v", variable, speed, position, frame.RGBA[at:at+4], want[:4])
						}
					}
				}
				// 离开两次pull后旧decoder会释放，再回跳必须恢复相同首有效PTS。
				copy := clip
				copy.ID, copy.StartFrame = "later-picture", 10
				p.Clips = append(p.Clips, copy)
				if _, err := s.Load(context.Background(), p); err != nil {
					t.Fatal(err)
				}
				for _, position := range []int64{0, 10, 11, 0} {
					frame, err := s.Seek(context.Background(), position)
					if err != nil {
						t.Fatal("驱逐后按实际PTS重开失败", err)
					}
					if position == 0 {
						at := (16*32 + 16) * 4
						for c := 0; c < 3; c++ {
							if math.Abs(float64(frame.RGBA[at+c])-float64(reference[at+c])) > 2 {
								t.Fatal("驱逐再开后源相位改变")
							}
						}
					}
				}
			}
		})
	}
}

func TestNativeSourceDisplayMatrixIsAppliedBeforeFit(t *testing.T) {
	s := nativeSourceSession(t)
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "quadrants.png")
	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	colors := [4][4]byte{{220, 20, 30, 255}, {10, 210, 40, 255}, {30, 40, 220, 255}, {210, 180, 20, 255}}
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			copy(img.Pix[y*img.Stride+x*4:], colors[(y/16)*2+x/32][:])
		}
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	file.Close()
	base, rotated := filepath.Join(dir, "base.mp4"), filepath.Join(dir, "rotated.mov")
	nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-loop", "1", "-framerate", "25", "-i", imagePath, "-t", "0.5", "-c:v", "libx264", "-pix_fmt", "yuv420p", base)
	nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-display_rotation", "90", "-i", base, "-c", "copy", rotated)
	asset, err := media.Discover().Probe(context.Background(), rotated)
	if err != nil {
		t.Fatal(err)
	}
	p := editor.NewProject("手机源方向")
	p.Settings.Width, p.Settings.Height = 32, 32
	p.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("oriented-source", "track-video-1", "video")
	clip.AssetID, clip.DurationFrames = asset.ID, 10
	p.Clips = []editor.Clip{clip}
	if _, err := s.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	frame, err := s.Seek(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	ref := nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-i", rotated, "-vf", "scale=32:32:force_original_aspect_ratio=decrease,pad=32:32:(ow-iw)/2:(oh-ih)/2:black,format=rgba", "-frames:v", "1", "-f", "rawvideo", "pipe:1")
	if len(ref) != 32*32*4 {
		t.Fatal("方向参考帧尺寸错误")
	}
	for _, point := range [][2]int{{2, 16}, {30, 16}, {12, 8}, {20, 8}, {12, 24}, {20, 24}} {
		at := (point[1]*32 + point[0]) * 4
		for c := 0; c < 3; c++ {
			if math.Abs(float64(frame.RGBA[at+c])-float64(ref[at+c])) > 4 {
				log := s.command.Stderr.(*boundedStderr)
				log.mu.Lock()
				data := string(log.data)
				log.mu.Unlock()
				t.Fatalf("源display matrix未在fit前处理：xy=%v got=%v want=%v\n%s", point, frame.RGBA[at:at+4], ref[at:at+4], data)
			}
		}
	}
}

func TestNativeSourceFailedOpenCannotReuseHalfInitializedState(t *testing.T) {
	s := nativeSourceSession(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "mutable-source.mp4")
	nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=red:s=32x32:r=30:d=1", "-c:v", "mpeg4", path)
	asset, err := media.Discover().Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	p := editor.NewProject("源在加载后损坏")
	p.Settings.Width, p.Settings.Height = 32, 32
	p.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("changed-source", "track-video-1", "video")
	clip.AssetID, clip.DurationFrames = asset.ID, 10
	p.Clips = []editor.Clip{clip}
	if _, err := s.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	// 仍是合法容器、但已无video：open已有format，codec尚未初始化时就会失败。
	wav := tone(t)
	bytes, err := os.ReadFile(wav.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := s.Seek(context.Background(), 0); err == nil {
			t.Fatal("损坏源返回测试卡并冒充真实图像")
		}
		if _, err := s.Diagnostics(context.Background()); err != nil {
			t.Fatal("报错后的worker不能响应诊断，不能把进程崩溃算作成功拒绝素材", err)
		}
		select {
		case <-s.Done():
			t.Fatal("重复定位崩溃或解引用半初始化codec")
		default:
		}
	}
}
