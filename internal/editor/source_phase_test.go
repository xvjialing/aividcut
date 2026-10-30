package editor

import (
	"math/big"
	"reflect"
	"testing"
)

func phaseFixture(t *testing.T) (*Engine, Project) {
	t.Helper()
	p := NewProject("原处理相位生命周期")
	p.Settings.FPSNum, p.Settings.FPSDen = 30000, 1001
	count := int64(441000)
	asset := Asset{ID: "phase", Name: "原讲话", Kind: "audio", Path: "/fixture/phase.wav", DurationUs: 10_000_000, HasAudio: true, AudioSampleRate: 44100, AudioSampleCount: &count, Fingerprint: "sha256:phase"}
	clip := DefaultClip("source", "track-audio-1", "audio")
	clip.AssetID, clip.SourceInUs, clip.DurationFrames = asset.ID, 12345, 70
	p.Assets, p.Clips = []Asset{asset}, []Clip{clip}
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	return e, p
}

func phaseClip(p Project, id string) Clip {
	for _, c := range p.Clips {
		if c.ID == id {
			return c
		}
	}
	return Clip{}
}

func TestSourcePhaseRecursiveSplitMoveDuplicateUndoAndClone(t *testing.T) {
	e, before := phaseFixture(t)
	p := apply(t, e, Command{Type: "split_clip", ClipID: "source", AtFrame: 2})
	right := p.Clips[1]
	p = apply(t, e, Command{Type: "split_clip", ClipID: right.ID, AtFrame: 5})
	last := p.Clips[2]
	if last.SourcePhase.OffsetFrames != 5 || last.SourcePhase.Recipe.SourceInUs != 12345 || last.SourcePhase.Recipe.DurationFrames != 70 {
		t.Fatal("递归分割丢失完整原处理范围", last.SourcePhase)
	}
	expected := new(big.Rat).Add(big.NewRat(12345, 1), frameTimeRat(5, p.Settings))
	want, _ := floorRat(expected)
	if last.SourceInUs != want {
		t.Fatal("微秒入点沿递归分割累计了截断误差", last.SourceInUs, want)
	}
	last.StartFrame = 200
	p = apply(t, e, Command{Type: "update_clip", Clip: &last})
	copy := cloneClip(last)
	copy.ID, copy.StartFrame = "copy", 300
	p = apply(t, e, Command{Type: "duplicate_clip", ClipID: last.ID, Clip: &copy})
	if !sameSourcePhase(phaseClip(p, "copy").SourcePhase, last.SourcePhase) {
		t.Fatal("复制重新开始派生段的处理")
	}
	p.Clips[0].SourcePhase.Recipe.AudioRange.Start++
	if e.Snapshot().Clips[0].SourcePhase.Recipe.AudioRange.Start == p.Clips[0].SourcePhase.Recipe.AudioRange.Start {
		t.Fatal("快照共享可变配方指针")
	}
	if _, err := e.Undo(); err != nil {
		t.Fatal(err)
	}
	redone, err := e.Redo()
	if err != nil || phaseClip(redone, "copy").SourcePhase.OffsetFrames != 5 {
		t.Fatal("撤销重做未还原相位", err)
	}
	for e.CanUndo() {
		if _, err = e.Undo(); err != nil {
			t.Fatal(err)
		}
	}
	undone := e.Snapshot()
	undone.Revision = before.Revision
	if !reflect.DeepEqual(before, undone) {
		t.Fatal("撤销未恢复旧nil配方和旧音频选区")
	}
}

func TestSourcePhaseTrimEqualsSplitAndExplicitTimingResets(t *testing.T) {
	e, before := phaseFixture(t)
	trim := before.Clips[0]
	trim.StartFrame, trim.DurationFrames = 5, 65
	delta, _ := SourceDurationUs(5, before.Settings, 1, 1)
	trim.SourceInUs += delta
	a := apply(t, e, Command{Type: "update_clip", Clip: &trim})
	other, _ := NewEngine(before)
	b := apply(t, other, Command{Type: "split_clip", ClipID: "source", AtFrame: 5})
	if !sameSourcePhase(a.Clips[0].SourcePhase, b.Clips[1].SourcePhase) || !sameAudioRange(a.Clips[0].AudioRange, b.Clips[1].AudioRange) {
		t.Fatal("直接左trim与split输出窗不一致")
	}
	current := a.Clips[0]
	current.Volume = .2
	current.Name = "人工命名"
	a = apply(t, e, Command{Type: "update_clip", Clip: &current})
	if !sameSourcePhase(a.Clips[0].SourcePhase, b.Clips[1].SourcePhase) {
		t.Fatal("外观编辑重置了输入")
	}
	current = a.Clips[0]
	current.SourceInUs += 10000
	a = apply(t, e, Command{Type: "update_clip", Clip: &current})
	if a.Clips[0].SourcePhase.OffsetFrames != 0 || a.Clips[0].SourcePhase.Recipe.SourceInUs != current.SourceInUs {
		t.Fatal("显式slip复用了旧配方")
	}
	current = a.Clips[0]
	current.SpeedNum = 2
	a = apply(t, e, Command{Type: "update_clip", Clip: &current})
	if a.Clips[0].SourcePhase.Recipe.SpeedNum != 2 || a.Clips[0].SourcePhase.OffsetFrames != 0 {
		t.Fatal("变速未重建真实输入")
	}
}

func TestSourcePhaseRejectsTamperingAndHonorsLockAtomically(t *testing.T) {
	e, _ := phaseFixture(t)
	p := apply(t, e, Command{Type: "split_clip", ClipID: "source", AtFrame: 5})
	bad := cloneClip(p.Clips[1])
	bad.SourcePhase.Recipe.SourceInUs++
	_, err := e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "bad-phase", Commands: []Command{{Type: "update_clip", Clip: &bad}}})
	assertCode(t, err, "source_phase")
	track := p.Tracks[trackIndex(&p, "track-audio-1")]
	track.Locked = true
	p = apply(t, e, Command{Type: "update_track", Track: &track})
	_, err = e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "locked-phase", Commands: []Command{{Type: "split_clip", ClipID: p.Clips[1].ID, AtFrame: 10}}})
	assertCode(t, err, "track_locked")
	if !reflect.DeepEqual(p, e.Snapshot()) {
		t.Fatal("失败的相位操作修改工程")
	}
}
