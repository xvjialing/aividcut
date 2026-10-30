package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"hash/crc32"
	"reflect"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func TestPreviewQualityGeometryAndSnapshotIsolation(t *testing.T) {
	p := editor.NewProject("精度坐标")
	p.Settings.Width, p.Settings.Height = 640, 360
	c := editor.DefaultClip("caption", "track-caption-1", "caption")
	c.Text = "精度"
	c.DurationFrames = 30
	c.Transform.X, c.Transform.Y, c.Transform.Scale = 100, 20, 1.25
	c.TextStyle.FontSize = 32
	p.Clips = []editor.Clip{c}
	before := editor.CloneProject(p)
	derived, err := renderProject(p, .5)
	if err != nil {
		t.Fatal(err)
	}
	xml, _, err := compileScaledSelection(derived, derived.Clips, graphTestCaps(), 0, false, .5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml, prop("geometry", "66.000000/-14.000000:288x180:100")) || !strings.Contains(xml, prop("size", "20")) {
		t.Fatal("平移/边距/有效字体字号未一致缩放", xml)
	}
	if !reflect.DeepEqual(p, before) {
		t.Fatal("预览修改了原工程")
	}
	if derived.Settings.FPSNum != p.Settings.FPSNum || derived.Settings.FPSDen != p.Settings.FPSDen || derived.Clips[0].DurationFrames != c.DurationFrames {
		t.Fatal("精度改变时间线")
	}
	for _, q := range []string{"fast", "1/2", "NaN"} {
		if _, err := QualityScale(q); err == nil {
			t.Fatal("未知精度被接受", q)
		}
	}
	p.Settings.Width, p.Settings.Height = 32, 32
	if _, err := renderProject(p, .25); err == nil {
		t.Fatal("过小画幅被静默放大")
	}
	if _, err := Start(context.Background(), Config{OfflineRender: true, RenderQuality: "half"}); err == nil || !strings.Contains(err.Error(), "全精度") {
		t.Fatal("离线允许低精度", err)
	}
}

func TestNativePreviewQualityKeepsPCMAndOriginalValidation(t *testing.T) {
	p := nativeProject(t)
	caption := editor.DefaultClip("caption", "track-caption-1", "caption")
	caption.Text = "精度"
	caption.TextStyle.FontSize = 24
	caption.DurationFrames = 30
	p.Clips = append(p.Clips, caption)
	before, _ := json.Marshal(p)
	var reference uint32
	for _, value := range []struct {
		quality string
		w, h    int
	}{{"full", 320, 180}, {"half", 160, 90}, {"quarter", 80, 45}} {
		config := nativeConfig(t)
		config.MaxWidth, config.MaxHeight = 1920, 1080
		config.RenderQuality = value.quality
		session, err := Start(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		info, err := session.Load(context.Background(), p)
		if err != nil {
			session.Close()
			t.Fatal(value.quality, err)
		}
		if info.Width != value.w || info.Height != value.h {
			t.Fatal("只缩小了输出而未得到目标画幅", value, info)
		}
		if err = session.Play(context.Background()); err != nil {
			t.Fatal(err)
		}
		hash := crc32.NewIEEE()
		var samples int
		white := 0
		var firstImage []byte
		for at := int64(0); at < 30; at++ {
			frame, err := session.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if frame.Position != at || frame.AudioStartSample != int64(samples) {
				t.Fatal("低精度改变声音锚点或帧序", value, at, frame.AudioStartSample, samples)
			}
			if at == 0 {
				firstImage = bytes.Clone(frame.RGBA)
			}
			hash.Write(frame.PCM)
			samples += frame.Samples
			for i := 0; i < len(frame.RGBA); i += 4 {
				if frame.RGBA[i] > 220 && frame.RGBA[i+1] > 220 && frame.RGBA[i+2] > 220 {
					white++
				}
			}
		}
		session.Close()
		without := editor.CloneProject(p)
		without.Clips = without.Clips[:len(without.Clips)-1]
		plain, err := Start(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = plain.Load(context.Background(), without); err != nil {
			t.Fatal(err)
		}
		baseline, err := plain.Preview(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}
		changed := 0
		for i := 0; i < len(firstImage); i += 4 {
			if !bytes.Equal(firstImage[i:i+3], baseline.RGBA[i:i+3]) {
				changed++
			}
		}
		plain.Close()
		if samples != 48048 || changed < 2 {
			t.Fatal("精度丢失真实声音或字幕", value, samples, white, changed)
		}
		if value.quality == "full" {
			reference = hash.Sum32()
		} else if reference != hash.Sum32() {
			t.Fatal("低精度改变PCM", value, reference, hash.Sum32())
		}
		t.Logf("%s合成%d×%d，30帧/%d样本，PCM CRC=%d，白字像素总数%d，字幕对照差异像素%d", value.quality, value.w, value.h, samples, hash.Sum32(), white, changed)
	}
	after, _ := json.Marshal(p)
	if !bytes.Equal(before, after) {
		t.Fatal("真实加载改写原工程")
	}
	bad := editor.CloneProject(p)
	bad.Clips[len(bad.Clips)-1].Transform.Rotation = 7
	config := nativeConfig(t)
	config.RenderQuality = "quarter"
	s, err := Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Load(context.Background(), bad); err == nil {
		t.Fatal("低精度掩盖原始不支持语义")
	}
}

func TestNativePreviewQualityWindowReplacementKeepsScale(t *testing.T) {
	p := nativeProject(t)
	base := p.Clips[0]
	p.Clips = p.Clips[1:]
	for i := int64(0); i < 150; i++ {
		c := base
		c.ID = editor.NewID("cut")
		c.StartFrame = i
		c.DurationFrames = 1
		p.Clips = append(p.Clips, c)
	}
	caption := editor.DefaultClip("text", "track-caption-1", "caption")
	caption.Text = "精度"
	caption.TextStyle.FontSize = 24
	caption.DurationFrames = 150
	p.Clips = append(p.Clips, caption)
	config := nativeConfig(t)
	config.RenderQuality = "half"
	config.MaxWidth, config.MaxHeight = 1920, 1080
	s, err := Start(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err = s.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	var samples int64
	for at := int64(0); at < 150; at++ {
		frame, err := s.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if frame.Position != at || frame.Width != 160 || frame.Height != 90 || frame.AudioStartSample != samples {
			t.Fatal("图窗口没有保持精度或全局时钟", at, frame)
		}
		samples += int64(frame.Samples)
	}
	t.Logf("半精度153片段跨窗150帧/%d样本，持续160×90", samples)
}
