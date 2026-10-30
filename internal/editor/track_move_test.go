package editor

import (
	"reflect"
	"strings"
	"testing"
)

func TestTrackMovePreservesClipsAndOtherKindsWithUndoRedo(t *testing.T) {
	e := fixture(t)
	track := Track{ID: "video2", Name: "覆盖镜头", Kind: "video"}
	before := apply(t, e, Command{Type: "add_track", Track: &track})
	after := apply(t, e, Command{Type: "move_track", TrackID: track.ID, TrackDelta: -1})
	if !reflect.DeepEqual(before.Clips, after.Clips) || !reflect.DeepEqual(before.Assets, after.Assets) {
		t.Fatal("排序改写片段或素材")
	}
	for i, v := range before.Tracks {
		if v.Kind != "video" && after.Tracks[i] != v {
			t.Fatal("改变其它类型轨位置")
		}
	}
	if trackIndex(&after, track.ID) >= trackIndex(&after, "track-video-1") {
		t.Fatal("未上移")
	}
	u, err := e.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(u.Tracks, before.Tracks) {
		t.Fatal("撤销未恢复顺序")
	}
	r, err := e.Redo()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Tracks, after.Tracks) {
		t.Fatal("重做未恢复顺序")
	}
}

func TestTrackMoveRejectsLocksBoundariesAndRollsBackRename(t *testing.T) {
	for _, mode := range []string{"target-lock", "source-lock", "boundary", "missing", "delta", "clip-fields"} {
		t.Run(mode, func(t *testing.T) {
			e := fixture(t)
			tr := Track{ID: "video2", Name: "二轨", Kind: "video"}
			apply(t, e, Command{Type: "add_track", Track: &tr})
			if mode == "source-lock" {
				tr.Locked = true
				apply(t, e, Command{Type: "update_track", Track: &tr})
			}
			if mode == "target-lock" {
				v := e.Snapshot().Tracks[2]
				v.Locked = true
				apply(t, e, Command{Type: "update_track", Track: &v})
			}
			before := e.Snapshot()
			renamed := tr
			renamed.Name = "不能部分保存"
			cmd := Command{Type: "move_track", TrackID: tr.ID, TrackDelta: -1}
			switch mode {
			case "boundary":
				cmd.TrackDelta = 1
			case "missing":
				cmd.TrackID = "missing"
			case "delta":
				cmd.TrackDelta = 2
			case "clip-fields":
				cmd.ClipIDs = []string{"video-clip"}
			}
			_, err := e.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: NewID("invalid"), Commands: []Command{{Type: "update_track", Track: &renamed}, cmd}})
			if err == nil || !reflect.DeepEqual(before, e.Snapshot()) {
				t.Fatalf("无效排序应整体拒绝：%v", err)
			}
		})
	}
}

func TestTrackNameValidationOnlyOnNewOrChangedNames(t *testing.T) {
	e := fixture(t)
	for _, name := range []string{" ", "控制\x7f", strings.Repeat("字", 121)} {
		tr := e.Snapshot().Tracks[0]
		tr.Name = name
		_, err := e.Apply(EditRequest{BaseRevision: e.Snapshot().Revision, IdempotencyKey: NewID("name"), Commands: []Command{{Type: "update_track", Track: &tr}}})
		if err == nil {
			t.Fatal("无效名称通过")
		}
	}
	p := e.Snapshot()
	p.Tracks[0].Name = strings.Repeat("旧", 121)
	legacy, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	tr := p.Tracks[0]
	tr.Hidden = true
	apply(t, legacy, Command{Type: "update_track", Track: &tr})
}
