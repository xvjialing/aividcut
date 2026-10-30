package media

import (
	"aividcut/internal/editor"
	"math"
	"reflect"
	"testing"
)

func TestAudioFadeRealFFmpegSampleGainSplitAndRange(t *testing.T) {
	tools := speechIntegrationTools(t)
	samples := make([]int16, 96000)
	for i := range samples {
		samples[i] = 10000
	}
	p := exactAudioProject(t, exactWAV(t, "fade-constant", 48000, samples))
	p.Clips[0].StartFrame = 7
	before, _ := exactRenderPCM(t, tools, p, 0, 0)
	faded := editor.CloneProject(p)
	faded.Clips[0].AudioFade = editor.NewAudioFade(faded.Clips[0], 250000, 300000)
	got, _ := exactRenderPCM(t, tools, faded, 0, 0)
	if len(got) != len(before) {
		t.Fatal("淡化改变采样总长")
	}
	start := 7.0 * 48000 * 1001 / 30000
	end := float64(7+p.Clips[0].DurationFrames) * 48000 * 1001 / 30000
	for i, v := range got {
		gain := math.Max(0, math.Min(1, math.Min((float64(i)-start)/12000, (end-float64(i))/14400)))
		want := float64(before[i]) * gain
		if math.Abs(float64(v)-want) > 1e-7 {
			t.Fatalf("FFmpeg逐采样淡化有误 n%d got%g want%g", i, v, want)
		}
	}
	split := exactEdit(t, faded, editor.Command{Type: "split_clip", ClipID: p.Clips[0].ID, AtFrame: 15})
	parts, _ := exactRenderPCM(t, tools, split, 0, 0)
	if !reflect.DeepEqual(parts, got) {
		t.Fatal("分割重新淡化或改变源样本")
	}
	cropped, _ := exactRenderPCM(t, tools, split, 10, 17)
	a, _ := renderFrameSample(10, p.Settings)
	b, _ := renderFrameSample(17, p.Settings)
	if !reflect.DeepEqual(cropped, got[a:b]) {
		t.Fatal("非零导出窗淡化位置错误")
	}
	var right editor.Clip
	for _, c := range split.Clips {
		if c.ID != p.Clips[0].ID {
			right = c
		}
	}
	right.StartFrame = 0
	moved := exactEdit(t, split, editor.Command{Type: "delete_clip", ClipID: p.Clips[0].ID}, editor.Command{Type: "update_clip", Clip: &right})
	negative, _ := exactRenderPCM(t, tools, moved, 0, 0)
	offset, _ := renderFrameSample(15, p.Settings)
	if len(negative) != len(got)-int(offset) {
		t.Fatal("负虚拟起点改变样本数")
	}
	for i, v := range negative {
		if math.Abs(float64(v-got[int(offset)+i])) > 1e-7 {
			t.Fatal("负虚拟起点的淡化位置错误")
		}
	}
}
