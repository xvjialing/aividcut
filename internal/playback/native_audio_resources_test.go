package playback

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"reflect"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/media"
)

func nativeStereoPCM(t *testing.T, s *Session, p editor.Project) []float32 {
	t.Helper()
	info, err := s.Load(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	var output []float32
	for at := int64(0); at < info.DurationFrames; at++ {
		frame, err := s.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if frame.AudioStartSample != int64(len(output)/2) {
			t.Fatal("实际样本锚不连续")
		}
		for i := 0; i < len(frame.PCM); i += 4 {
			output = append(output, math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[i:])))
		}
	}
	return output
}

func audioDiagnostics(t *testing.T, s *Session) Diagnostics {
	t.Helper()
	d, err := s.Diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.SourceAudio == nil || d.AudioMix == nil {
		t.Fatal("当前worker缺少真实音频资源诊断")
	}
	return d
}

func TestNativeAudioDerivedSegmentsShareBoundedProcessorAndSeek(t *testing.T) {
	s := nativeSourceSession(t)
	p := nativeSourceProject(t, 12345, 1, 1)
	whole := nativeStereoPCM(t, s, p)
	before := audioDiagnostics(t, s)
	split := p
	for at := int64(1); at <= 40; at++ {
		for _, clip := range split.Clips {
			if clip.StartFrame < at && at < clip.StartFrame+clip.DurationFrames {
				split = nativeSourceEdit(t, split, editor.Command{Type: "split_clip", ClipID: clip.ID, AtFrame: at})
				break
			}
		}
	}
	joined := nativeStereoPCM(t, s, split)
	if !reflect.DeepEqual(whole, joined) {
		t.Fatal("共享处理后改变了真实立体声样本")
	}
	after := audioDiagnostics(t, s)
	if after.SourceAudio.Opens-before.SourceAudio.Opens != 1 || after.SourceAudio.GraphBuilds-before.SourceAudio.GraphBuilds != 1 || after.SourceAudio.ResidentStates != 1 || after.SourceAudio.ActiveDecoders != 1 {
		t.Fatalf("派生段未共享原完整处理器：before=%+v after=%+v", before.SourceAudio, after.SourceAudio)
	}
	if after.AudioMix.PeakFrameBytes > 24000*8 || after.SourceAudio.PeakQueueSampleFrames > 192000 {
		t.Fatal("音频工作面超出有界范围")
	}
	for _, at := range []int64{50, 0, 45, 2, 35} {
		frame, err := s.Seek(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(frame.PCM); i += 4 {
			value := math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[i:]))
			want := whole[int(frame.AudioStartSample)*2+i/4]
			if value != want {
				t.Fatalf("随机回跳重建改变样本：frame%d index%d %g != %g", at, i/4, value, want)
			}
		}
	}
	if _, err := s.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	cleared := audioDiagnostics(t, s)
	if cleared.SourceAudio.ResidentStates != 0 || cleared.SourceAudio.ActiveDecoders != 0 {
		t.Fatal("重新Load复用了上个工程的处理状态")
	}
}

func TestNativeAudioSharedIdentityKeepsPlacementVolumeAndMuteIndependent(t *testing.T) {
	s := nativeSourceSession(t)
	p := nativeSourceProject(t, 0, 1, 1)
	base := nativeStereoPCM(t, s, p)
	copy := p.Clips[0]
	copy.ID, copy.TrackID, copy.StartFrame, copy.Volume = "shifted", "shifted-track", 5, .35
	p.Tracks = append(p.Tracks, editor.Track{ID: copy.TrackID, Name: "错位声音", Kind: "audio"})
	p.Clips = append(p.Clips, copy)
	mixed := nativeStereoPCM(t, s, p)
	const delay = 8008 // 5帧29.97fps在48k全局网格恰为8008样本。
	for i, value := range mixed {
		want := float32(0)
		if i < len(base) {
			want += base[i]
		}
		if i >= delay*2 && i-delay*2 < len(base) {
			want += base[i-delay*2] * .35
		}
		if math.Abs(float64(value-want)) > 2e-6 {
			t.Fatalf("放置/音量错误：sample%d %g != %g", i/2, value, want)
		}
	}
	d := audioDiagnostics(t, s)
	if d.SourceAudio.ResidentStates > 2 {
		t.Fatal("相同放置相位之外创建了额外解码器")
	}
	p.Clips[1].Muted = true
	muted := nativeStereoPCM(t, s, p)
	for i, value := range muted {
		want := float32(0)
		if i < len(base) {
			want = base[i]
		}
		if value != want {
			t.Fatal("静音片段污染了共享状态", i)
		}
	}
}

func TestNativeAudioDecoderCacheEvictionKeepsSimultaneousMix(t *testing.T) {
	s := nativeSourceSession(t)
	source := tone(t)
	p := editor.NewProject("有界音轨缓存驱逐")
	p.Settings.Width, p.Settings.Height, p.Settings.FPSNum, p.Settings.FPSDen = 32, 32, 30000, 1001
	var expected []float32
	for n := 0; n < 12; n++ {
		asset := source
		asset.ID = fmt.Sprintf("identity-%d", n)
		track := editor.Track{ID: fmt.Sprintf("track-%d", n), Name: "独立源身份", Kind: "audio"}
		clip := editor.DefaultClip(fmt.Sprintf("clip-%d", n), track.ID, "audio")
		clip.AssetID, clip.SourceInUs, clip.DurationFrames = asset.ID, int64(n*10000), 8
		solo := p
		solo.Assets = []editor.Asset{asset}
		solo.Tracks = []editor.Track{track}
		solo.Clips = []editor.Clip{clip}
		pcm := nativeStereoPCM(t, s, solo)
		if expected == nil {
			expected = make([]float32, len(pcm))
		}
		for i, value := range pcm {
			expected[i] += value
		}
		p.Assets = append(p.Assets, asset)
		p.Tracks = append(p.Tracks, track)
		p.Clips = append(p.Clips, clip)
	}
	actual := nativeStereoPCM(t, s, p)
	for i, value := range actual {
		if math.Abs(float64(value-expected[i])) > 4e-6 {
			t.Fatalf("缓存驱逐改变并行声音：sample%d %g != %g", i/2, value, expected[i])
		}
	}
	d := audioDiagnostics(t, s)
	if d.SourceAudio.ResidentStates > 8 || d.SourceAudio.ActiveDecoders > 8 || d.SourceAudio.QueuedSampleFrames > 8*192000 {
		t.Fatalf("活跃状态未受限：%+v", d.SourceAudio)
	}
}

func TestNativeAudioSamePhaseCopiesKeepVolumeOutOfSharedPCM(t *testing.T) {
	s := nativeSourceSession(t)
	p := nativeSourceProject(t, 12345, 1, 1)
	base := nativeStereoPCM(t, s, p)
	before := audioDiagnostics(t, s)
	copy := p.Clips[0]
	copy.ID, copy.TrackID, copy.Volume = "volume-copy", "volume-track", .5
	p.Tracks = append(p.Tracks, editor.Track{ID: copy.TrackID, Name: "相同相位不同音量", Kind: "audio"})
	p.Clips = append(p.Clips, copy)
	mixed := nativeStereoPCM(t, s, p)
	for i, value := range mixed {
		if math.Abs(float64(value-(base[i]+base[i]*.5))) > 2e-6 {
			t.Fatalf("片段音量改写了共享原PCM：sample%d %g != %g", i/2, value, base[i]*1.5)
		}
	}
	after := audioDiagnostics(t, s)
	if after.SourceAudio.Opens-before.SourceAudio.Opens != 1 {
		t.Fatal("相同处理和放置相位的副本没有共享解码器")
	}
}

func TestNativePicturePreviewDoesNotReplayAudioOrChangeLaterPCM(t *testing.T) {
	s := nativeSourceSession(t)
	p := nativeSourceProject(t, 12345, 1, 2)
	whole := nativeStereoPCM(t, s, p)
	if _, err := s.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	before := audioDiagnostics(t, s)
	for _, at := range []int64{80, 2, 80, 35} {
		frame, err := s.Preview(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		if !frame.PictureOnly || frame.Position != at || frame.Samples != 0 || len(frame.PCM) != 0 || len(frame.RGBA) != frame.Width*frame.Height*4 {
			t.Fatal("纯画面定位契约不符")
		}
	}
	after := audioDiagnostics(t, s)
	if after.SourceAudio.Opens != before.SourceAudio.Opens || after.SourceAudio.DecodedFrames != before.SourceAudio.DecodedFrames || after.AudioMix.Calls != before.AudioMix.Calls {
		t.Fatal("取画面时仍重放了未消费的声音")
	}
	if err := s.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	frame, err := s.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if frame.PictureOnly || frame.Samples < 1 || frame.Position != 35 {
		t.Fatal("画面定位后Next没有从目标帧取得真实声音")
	}
	for i := 0; i < len(frame.PCM); i += 4 {
		value := math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[i:]))
		if value != whole[int(frame.AudioStartSample)*2+i/4] {
			t.Fatal("纯画面定位改变了后续声音相位", i)
		}
	}
	full, err := s.Seek(context.Background(), 0)
	if err != nil || full.PictureOnly || full.Samples < 1 {
		t.Fatal("完整音频Seek接口被缩减", err)
	}
}

func TestNativeCompressedNarrationSplitsRetainActualDecodedSamples(t *testing.T) {
	s := nativeSourceSession(t)
	wav := tone(t)
	for _, format := range []struct{ name, codec string }{{"voice.mp3", "libmp3lame"}, {"voice.m4a", "aac"}, {"voice.flac", "flac"}} {
		t.Run(format.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), format.name)
			nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-i", wav.Path, "-ar", "44100", "-c:a", format.codec, path)
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
			asset.AudioSampleCount, asset.AudioSampleRate, asset.AudioStartUs = &count, measured.SampleRate, measured.SourceStartUs
			p := editor.NewProject("压缩口播真实音尾")
			p.Settings.Width, p.Settings.Height, p.Settings.FPSNum, p.Settings.FPSDen = 32, 32, 30000, 1001
			p.Assets = []editor.Asset{asset}
			clip := editor.DefaultClip("voice", "track-audio-1", "audio")
			clip.AssetID, clip.SourceInUs = asset.ID, 12345
			clip, err = editor.AudioClipToSourceEnd(clip, asset, p.Settings)
			if err != nil {
				t.Fatal(err)
			}
			p.Clips = []editor.Clip{clip}
			whole := nativeStereoPCM(t, s, p)
			phase, err := editor.ResolveSourceAudioPhase(clip, asset, p.Settings)
			if err != nil {
				t.Fatal(err)
			}
			quantize := func(value editor.AudioTime) int64 {
				r, err := value.Rat()
				if err != nil {
					t.Fatal(err)
				}
				r.Mul(r, big.NewRat(48000, 1_000_000))
				q, rem := new(big.Int), new(big.Int)
				q.QuoRem(r.Num(), r.Denom(), rem)
				if rem.Sign() > 0 {
					q.Add(q, big.NewInt(1))
				}
				if !q.IsInt64() {
					t.Fatal("参考端点溢出")
				}
				return q.Int64()
			}
			// 与工程源采样契约一致：首个有效解码样本为零，先归一计数，避免MP3容器时间基干预重采样相位。
			graph := fmt.Sprintf("asetpts=N/SR/TB,aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,atrim=start_sample=%d:end_sample=%d,asetpts=N/SR/TB,aformat=sample_fmts=flt:sample_rates=48000:channel_layouts=stereo", quantize(phase.Root.SourceStart), quantize(phase.Root.SourceEnd))
			reference := nativeSourceCommand(t, "-hide_banner", "-loglevel", "error", "-threads", "1", "-i", path, "-af", graph, "-f", "f32le", "pipe:1")
			lead, end := quantize(phase.ProcessingTimelineStart), quantize(phase.Current.TimelineEnd)
			for i, value := range whole {
				index := int64(i/2) - lead
				want := float32(0)
				if index >= 0 && int64(i/2) < end && (index*2+int64(i%2))*4+4 <= int64(len(reference)) {
					want = math.Float32frombits(binary.LittleEndian.Uint32(reference[(index*2+int64(i%2))*4:]))
				}
				if math.Abs(float64(value-want)) > 1e-6 {
					t.Fatalf("实际压缩解码与独立FFmpeg不符：sample%d %g != %g", i/2, value, want)
				}
			}
			for _, at := range []int64{1, 2, 5, 10, 20, 30, 40, 50} {
				for _, part := range p.Clips {
					if part.StartFrame < at && at < part.StartFrame+part.DurationFrames {
						p = nativeSourceEdit(t, p, editor.Command{Type: "split_clip", ClipID: part.ID, AtFrame: at})
						break
					}
				}
			}
			joined := nativeStereoPCM(t, s, p)
			if !reflect.DeepEqual(whole, joined) {
				t.Fatal("压缩口播剪切改变了真实解码/重采样/末端样本")
			}
		})
	}
}
