package editor

import (
	"math"
	"reflect"
	"testing"
)

func fadeFixture(t *testing.T) (*Engine, Clip) {
	t.Helper()
	e := selectionFixture(t)
	p := e.Snapshot()
	c := p.Clips[1]
	c.AudioFade = NewAudioFade(c, 200_000, 400_000)
	p = apply(t, e, Command{Type: "update_clip", Clip: &c, IncludeLinked: fadeBool(false)})
	for _, c := range p.Clips {
		if c.ID == "audio-clip" {
			return e, c
		}
	}
	t.Fatal("夹具音轨缺失")
	return nil, Clip{}
}
func TestAudioFadeMigrationValidationCloneAndUndo(t *testing.T) {
	e, c := fadeFixture(t)
	if !sameCaption(c, cloneClip(c)) {
		t.Fatal("值相同的淡化被指针地址误判为变化")
	}
	p := e.Snapshot()
	before := CloneProject(p)
	p.Clips[1].AudioFade.InUs = 1
	if e.Snapshot().Clips[1].AudioFade.InUs != 200_000 {
		t.Fatal("浅拷贝改变包络")
	}
	for _, version := range []int{1, 2, 3} {
		legacy := CloneProject(before)
		legacy.SchemaVersion = version
		if _, err := UpgradeProject(legacy); err == nil {
			t.Fatal("旧版本夹带新字段", version)
		}
		for i := range legacy.Clips {
			legacy.Clips[i].AudioFade = nil
		}
		got, err := UpgradeProject(legacy)
		if err != nil || got.SchemaVersion != SchemaVersion {
			t.Fatal("旧版本不能迁移", err)
		}
	}
	for _, f := range []*AudioFade{{InUs: -1, SpanFrames: 60}, {InUs: 1, SpanFrames: 1}, {InUs: 1, SpanFrames: 60, OffsetFrames: 1}, {InUs: math.MaxInt64, OutUs: 1, SpanFrames: 60}, {InUs: 3_000_000, SpanFrames: 60}} {
		bad := CloneProject(before)
		bad.Clips[1].AudioFade = f
		if ValidateProject(bad) == nil {
			t.Fatal("非法淡化被接纳", f)
		}
	}
	c.AudioFade = nil
	apply(t, e, Command{Type: "update_clip", Clip: &c, IncludeLinked: fadeBool(false)})
	restored, err := e.Undo()
	if err != nil || !reflect.DeepEqual(restored.Clips[1].AudioFade, before.Clips[1].AudioFade) {
		t.Fatal("撤销没恢复包络", err)
	}
}
func TestAudioFadeSplitTrimRippleAndSpeedEnvelope(t *testing.T) {
	e, original := fadeFixture(t)
	p := apply(t, e, Command{Type: "split_clip", ClipID: original.ID, AtFrame: 15, IncludeLinked: fadeBool(false)})
	var right Clip
	for _, c := range p.Clips {
		if c.Kind == "audio" && c.ID != original.ID {
			right = c
		}
	}
	if right.AudioFade == nil || right.AudioFade.OffsetFrames != 15 || right.AudioFade.SpanFrames != 60 {
		t.Fatal("分割重新淡入", right.AudioFade)
	}
	right.StartFrame = 100
	p = apply(t, e, Command{Type: "update_clip", Clip: &right, IncludeLinked: fadeBool(false)})
	for _, c := range p.Clips {
		if c.ID == right.ID {
			right = c
		}
	}
	if right.AudioFade.OffsetFrames != 15 {
		t.Fatal("移动重设包络")
	}
	right.SpeedNum = 2
	right.DurationFrames = 23
	p = apply(t, e, Command{Type: "update_clip", Clip: &right, IncludeLinked: fadeBool(false)})
	for _, c := range p.Clips {
		if c.ID == right.ID {
			right = c
		}
	}
	if right.AudioFade.OffsetFrames != 0 || right.AudioFade.SpanFrames != 23 {
		t.Fatal("变速没重设包络", right.AudioFade)
	}
	right.DurationFrames = 1
	p = apply(t, e, Command{Type: "update_clip", Clip: &right, IncludeLinked: fadeBool(false)})
	for _, c := range p.Clips {
		if c.ID == right.ID {
			right = c
		}
	}
	right.SpeedNum = 4
	p = apply(t, e, Command{Type: "update_clip", Clip: &right, IncludeLinked: fadeBool(false)})
	for _, c := range p.Clips {
		if c.ID == right.ID {
			right = c
		}
	}
	if right.AudioFade.InUs+right.AudioFade.OutUs > 33_334 {
		t.Fatal("短段没有有界缩短淡化")
	}

	e, original = fadeFixture(t)
	p = apply(t, e, Command{Type: "ripple_delete_range", FromFrame: 15, ToFrame: 25})
	count := 0
	for _, c := range p.Clips {
		if c.Kind == "audio" {
			count++
			if c.StartFrame == 15 && c.AudioFade.OffsetFrames != 25 {
				t.Fatal("波纹闭合重启淡化")
			}
		}
	}
	if count != 2 {
		t.Fatal("波纹没有保留两段")
	}
}

func fadeBool(v bool) *bool { return &v }
