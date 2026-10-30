package editor

import (
	"reflect"
	"testing"
)

func TestExplicitBoundCaptionRetargetPreservesIdentityOverridesAndHistory(t *testing.T) {
	engine := transcriptFixture(t)
	project := engine.Snapshot()
	manual := boundByWord(t, project, "speech-clip", "hello")
	manual.Text, manual.TextStyle.FontSize, manual.Transform.Y = "人工字幕", 72, -100
	target := Track{ID: "retarget-caption", Name: "目标字幕", Kind: "caption"}
	before := apply(t, engine, Command{Type: "update_clip", Clip: &manual}, Command{Type: "add_track", Track: &target})
	moved := apply(t, engine, Command{Type: "generate_bound_captions", TranscriptID: "transcript", TrackID: target.ID})
	if boundCount(moved) != boundCount(before) || moved.Transcripts[0].CaptionTrackID != target.ID {
		t.Fatal("重定向改变词条数量或没有改变派生目标")
	}
	for _, old := range before.Clips {
		if old.CaptionSource == nil {
			continue
		}
		index := clipIndex(&moved, old.ID)
		if index < 0 || moved.Clips[index].TrackID != target.ID || moved.Clips[index].Text != old.Text || moved.Clips[index].TextStyle != old.TextStyle || moved.Clips[index].CaptionSource.SourceClipID != old.CaptionSource.SourceClipID {
			t.Fatal("既有字幕未移至新目标或身份/覆盖丢失")
		}
	}
	got := boundByWord(t, moved, "speech-clip", "hello")
	if got.Transform != manual.Transform || !got.CaptionSource.TextOverride || !got.CaptionSource.StyleOverride {
		t.Fatal("重定向丢失人工变换或覆盖标记")
	}
	undone, err := engine.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if undone.Transcripts[0].CaptionTrackID != "track-caption-1" || boundByWord(t, undone, "speech-clip", "hello").TrackID != "track-caption-1" {
		t.Fatal("字幕重定向未整组撤销")
	}
	redone, err := engine.Redo()
	if err != nil || boundByWord(t, redone, "speech-clip", "hello").ID != manual.ID || boundByWord(t, redone, "speech-clip", "hello").TrackID != target.ID {
		t.Fatal("字幕重定向重做丢失稳定身份", err)
	}
}

func TestExplicitCaptionRetargetChecksOldAndNewLocksAtomically(t *testing.T) {
	for _, locked := range []string{"track-caption-1", "new-caption-target"} {
		t.Run(locked, func(t *testing.T) {
			engine := transcriptFixture(t)
			target := Track{ID: "new-caption-target", Name: "新字幕轨", Kind: "caption"}
			project := apply(t, engine, Command{Type: "add_track", Track: &target})
			track := project.Tracks[trackIndex(&project, locked)]
			track.Locked = true
			before := apply(t, engine, Command{Type: "update_track", Track: &track})
			_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: "locked-retarget", Commands: []Command{{Type: "update_project", Name: "不得部分保存"}, {Type: "generate_bound_captions", TranscriptID: "transcript", TrackID: target.ID}}})
			assertCode(t, err, "track_locked")
			if !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("旧或新字幕轨锁定时仍部分改了派生规则/字幕")
			}
		})
	}
}

func TestCaptionRetargetKeepsLaneConflictPolicyAndUnchangedBaseManualPlacement(t *testing.T) {
	engine := transcriptFixture(t)
	project := engine.Snapshot()
	manual := boundByWord(t, project, "speech-clip", "hello")
	target := Track{ID: "target-caption", Name: "目标字幕", Kind: "caption"}
	project = apply(t, engine, Command{Type: "add_track", Track: &target})
	manual.TrackID = target.ID
	project = apply(t, engine, Command{Type: "update_clip", Clip: &manual})
	project = apply(t, engine, Command{Type: "generate_bound_captions", TranscriptID: "transcript", TrackID: "track-caption-1"})
	if boundByWord(t, project, "speech-clip", "hello").TrackID != target.ID {
		t.Fatal("没有改变base的重生成不应重置手动展示轨")
	}
	static := DefaultClip("static-target-caption", target.ID, "caption")
	static.Text, static.StartFrame, static.DurationFrames = "已有目标字幕", 30, 3
	project = apply(t, engine, Command{Type: "add_clip", Clip: &static})
	beforeCount := boundCount(project)
	project = apply(t, engine, Command{Type: "generate_bound_captions", TranscriptID: "transcript", TrackID: target.ID})
	if boundCount(project) != beforeCount || boundByWord(t, project, "speech-clip", "hello").TrackID != target.ID {
		t.Fatal("换轨没有向新base放置可容纳词条")
	}
	if boundByWord(t, project, "speech-clip", "cut").TrackID == target.ID {
		t.Fatal("目标冲突没有沿用分轨，覆盖了原静态字幕")
	}
	if err := ValidateProject(project); err != nil {
		t.Fatal("换轨的lane分配破坏同轨不重叠约束", err)
	}
}
