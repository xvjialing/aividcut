package playback

import (
	"aividcut/internal/editor"
	"context"
	"encoding/binary"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestCompileAudioFadeRejectsOldWorker(t *testing.T) {
	p := nativeSourceProject(t, 0, 1, 1)
	p.Clips[0].AudioFade = editor.NewAudioFade(p.Clips[0], 100000, 100000)
	caps := graphTestCaps()
	if _, _, err := compileProject(p, caps); err == nil || !strings.Contains(err.Error(), "不支持音频淡入淡出") {
		t.Fatal("旧worker静默忽略淡化")
	}
}

func TestNativeAudioFadeSampleGainSplitSeekWindowAndSharedPCM(t *testing.T) {
	a, b := windowSessions(t, 4, 7)
	if !a.Capabilities().AudioFades.Supported {
		t.Fatal("新worker没有声明淡化")
	}
	p := nativeSourceProject(t, 12345, 3, 2)
	p.Clips[0].StartFrame = 7
	before := nativeStereoPCM(t, a, p)
	faded := editor.CloneProject(p)
	faded.Clips[0].AudioFade = editor.NewAudioFade(faded.Clips[0], 250000, 300000)
	got := nativeStereoPCM(t, a, faded)
	start := 7.0 * 48000 * 1001 / 30000
	end := float64(7+p.Clips[0].DurationFrames) * 48000 * 1001 / 30000
	if len(got) != len(before) {
		t.Fatal("淡化改变样本数")
	}
	for i, v := range got {
		n := float64(i / 2)
		gain := math.Max(0, math.Min(1, math.Min((n-start)/12000, (end-n)/14400)))
		if math.Abs(float64(v)-float64(before[i])*gain) > 1e-7 {
			t.Fatalf("原生包络 n%d got%g", i/2, v)
		}
	}
	split := faded
	for _, at := range []int64{8, 10, 15, 20, 25} {
		for _, c := range split.Clips {
			if c.StartFrame < at && at < c.StartFrame+c.DurationFrames {
				split = nativeSourceEdit(t, split, editor.Command{Type: "split_clip", ClipID: c.ID, AtFrame: at})
				break
			}
		}
	}
	sliced := nativeStereoPCM(t, b, split)
	if !reflect.DeepEqual(got, sliced) {
		t.Fatal("分割/换窗改变原淡化")
	}
	for _, at := range []int64{8, 12, 20, 7} {
		f, e := b.Seek(context.Background(), at)
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < len(f.PCM); i += 4 {
			v := math.Float32frombits(binary.LittleEndian.Uint32(f.PCM[i:]))
			if v != got[int(f.AudioStartSample)*2+i/4] {
				t.Fatal("seek重启淡化")
			}
		}
	}
	mixed := editor.CloneProject(faded)
	mixed.Tracks = append(mixed.Tracks, editor.Track{ID: "unfaded", Name: "同源未淡化", Kind: "audio"})
	c := p.Clips[0]
	c.ID, c.TrackID = "copy", "unfaded"
	mixed.Clips = append(mixed.Clips, c)
	sum := nativeStereoPCM(t, a, mixed)
	for i, v := range sum {
		if math.Abs(float64(v)-float64(got[i])-float64(before[i])) > 2e-7 {
			t.Fatal("淡化污染共享源PCM")
		}
	}
}
