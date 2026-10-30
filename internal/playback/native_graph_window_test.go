package playback

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"aividcut/internal/editor"
)

func windowSessions(t *testing.T, limit int, frames int64) (*Session, *Session) {
	t.Helper()
	config := nativeConfig(t)
	a, err := Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	config.WindowClipLimit, config.WindowFrames = limit, frames
	b, err := Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	if !b.Capabilities().WindowedGraph.Supported {
		t.Fatal("当前worker未提供图窗口替换")
	}
	return a, b
}
func sameNativeFrame(t *testing.T, a, b Frame) {
	t.Helper()
	if a.Position != b.Position || a.AudioStartSample != b.AudioStartSample || a.Samples != b.Samples || a.End != b.End || a.PictureOnly != b.PictureOnly || !bytes.Equal(a.RGBA, b.RGBA) || !bytes.Equal(a.PCM, b.PCM) {
		t.Fatalf("窗口改变真实画面/声音或全局时钟：%d/%d sample=%d/%d", a.Position, b.Position, a.AudioStartSample, b.AudioStartSample)
	}
}

func TestNativeGraphWindowContinuousAudioAndSeekKeepsRecipe(t *testing.T) {
	a, b := windowSessions(t, 4, 7)
	p := nativeSourceProject(t, 12345, 3, 2)
	for at := int64(1); at < 36; at++ {
		for _, clip := range p.Clips {
			if clip.StartFrame < at && at < clip.StartFrame+clip.DurationFrames {
				p = nativeSourceEdit(t, p, editor.Command{Type: "split_clip", ClipID: clip.ID, AtFrame: at})
				break
			}
		}
	}
	if _, err := a.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	for at := int64(0); at < b.Info().DurationFrames; at++ {
		x, err := a.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		y, err := b.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		sameNativeFrame(t, x, y)
	}
	d, err := b.Diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.Graph == nil || d.Graph.Replacements < 5 || d.SourceAudio == nil || d.SourceAudio.Opens != 1 || d.SourceAudio.GraphBuilds != 1 {
		t.Fatal("跨窗重新启动原音轨处理", d)
	}
	for _, at := range []int64{20, 0, 35, 4, 4, 1} {
		x, err := a.Preview(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		y, err := b.Preview(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		sameNativeFrame(t, x, y)
		if err := a.Play(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := b.Play(context.Background()); err != nil {
			t.Fatal(err)
		}
		x, err = a.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		y, err = b.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		sameNativeFrame(t, x, y)
		_ = a.Pause(context.Background())
		_ = b.Pause(context.Background())
	}
	if _, err := b.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	d, err = b.Diagnostics(context.Background())
	if err != nil || d.SourceAudio.ResidentStates != 0 {
		t.Fatal("新Load未清空源缓存", d, err)
	}
}

func TestNativeGraphWindowPreservesVisualLUTRotationTextAndMix(t *testing.T) {
	a, b := windowSessions(t, 6, 7)
	p := visualProject(t)
	p.Settings.FPSNum, p.Settings.FPSDen = 30000, 1001
	for i := range p.Clips {
		p.Clips[i].DurationFrames = 48
	}
	p.Clips[1].Transform.Rotation = 17
	p.Clips[1].Transform.X = 5
	p.Clips[1].LUT = &editor.LUT{AssetID: "cube", Intensity: .45}
	for at := int64(4); at < 48; at += 4 {
		for _, clip := range p.Clips {
			if clip.TrackID == "visual-top" && clip.StartFrame < at && at < clip.StartFrame+clip.DurationFrames {
				p = nativeSourceEdit(t, p, editor.Command{Type: "split_clip", ClipID: clip.ID, AtFrame: at})
				break
			}
		}
	}
	voice := tone(t)
	p.Assets = append(p.Assets, voice)
	sound := editor.DefaultClip("voice", "track-audio-1", "audio")
	sound.AssetID = voice.ID
	sound.StartFrame, sound.DurationFrames, sound.SourceInUs = 3, 36, 12345
	p.Clips = append(p.Clips, sound)
	caption := editor.DefaultClip("label", "track-caption-1", "caption")
	caption.StartFrame, caption.DurationFrames, caption.Text = 6, 24, "窗口交接"
	caption.TextStyle.FontSize = 16
	p.Clips = append(p.Clips, caption)
	if _, err := a.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{0, 3, 6, 7, 8, 14, 29, 30, 39, 47, 1, 14} {
		x, err := a.Seek(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		y, err := b.Seek(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		sameNativeFrame(t, x, y)
	}
}

func TestNativeGraphWindowLargeProjectAndBlankGap(t *testing.T) {
	config := nativeConfig(t)
	s, err := Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := imageCuts(t, 1000)
	if _, err := s.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := s.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	for at := int64(0); at < 1000; at++ {
		frame, err := s.Next(context.Background())
		if err != nil || frame.Position != at || frame.AudioStartSample != at*1600 || frame.End != (at == 999) {
			t.Fatalf("大工程帧%d：%+v %v", at, frame, err)
		}
	}
	d, err := s.Diagnostics(context.Background())
	if err != nil || d.Graph == nil || d.Graph.Replacements < 7 || d.Graph.PeakXMLBytes > 3<<20 || d.Graph.LiveGraphs != 1 || d.Graph.LiveProducers > 1024 || d.Graph.LiveServices > 2048 {
		t.Fatal("没有实际替换有界图", d, err)
	}
	for _, at := range []int64{999, 128, 127, 998, 0} {
		frame, err := s.Seek(context.Background(), at)
		if err != nil || frame.Position != at {
			t.Fatal("反向/重复定位失败", at, err)
		}
	}
	gap := imageCuts(t, 129)
	for i := range gap.Clips {
		gap.Clips[i].StartFrame += 600
	}
	if _, err := s.Load(context.Background(), gap); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{0, 599, 600, 728} {
		frame, err := s.Seek(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		nonzero := false
		for i := 0; i < len(frame.RGBA); i += 4 {
			if frame.RGBA[i] > 0 {
				nonzero = true
				break
			}
		}
		if nonzero != (at >= 600) {
			t.Fatal(fmt.Sprintf("空隙被压缩或补造帧：%d", at))
		}
	}
}

func TestNativeGraphWindowVideoKeepsDecoderAndSamplingPhase(t *testing.T) {
	a, b := windowSessions(t, 8, 17)
	asset := numberedSource(t, true)
	p := numberedProject(asset, 3, 2)
	p.Clips[0].DurationFrames = 180
	for at := int64(3); at < 180; at += 3 {
		for _, clip := range p.Clips {
			if clip.StartFrame < at && at < clip.StartFrame+clip.DurationFrames {
				p = nativeSourceEdit(t, p, editor.Command{Type: "split_clip", ClipID: clip.ID, AtFrame: at})
				break
			}
		}
	}
	if _, err := a.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	_ = a.Play(context.Background())
	_ = b.Play(context.Background())
	for at := int64(0); at < 180; at++ {
		x, err := a.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		y, err := b.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		sameNativeFrame(t, x, y)
	}
	d, err := b.Diagnostics(context.Background())
	if err != nil || d.Graph == nil || d.Graph.Replacements < 5 || d.SourceVideo == nil || d.SourceVideo.Opens != 1 || d.SourceVideo.Anchors != 1 {
		t.Fatal("换图重启视频处理相位或解码器", d, err)
	}
	for _, at := range []int64{179, 17, 16, 0, 170, 17} {
		x, err := a.Preview(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		y, err := b.Preview(context.Background(), at)
		if err != nil {
			t.Fatal(err)
		}
		sameNativeFrame(t, x, y)
	}
}
