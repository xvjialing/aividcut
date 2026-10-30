package editor

import (
	"reflect"
	"testing"
)

func TestAddTrackBeforeSameKindKeepsExistingRelativeOrder(t *testing.T) {
	engine := fixture(t)
	before := engine.Snapshot()
	track := Track{ID: "broll-above", Name: "上层B-roll", Kind: "video"}
	after := apply(t, engine, Command{Type: "add_track", Track: &track, BeforeTrackID: "track-video-1"})
	if trackIndex(&after, track.ID) >= trackIndex(&after, "track-video-1") {
		t.Fatal("B-roll轨没有放在原视频上方")
	}
	kept := []Track{}
	for _, value := range after.Tracks {
		if value.ID != track.ID {
			kept = append(kept, value)
		}
	}
	if !reflect.DeepEqual(before.Tracks, kept) || !reflect.DeepEqual(before.Clips, after.Clips) {
		t.Fatal("插入轨改变原有相对顺序或原片段")
	}
	undone, err := engine.Undo()
	if err != nil || !reflect.DeepEqual(undone.Tracks, before.Tracks) {
		t.Fatal("插轨不能整组撤销", err)
	}
}

func TestBeforeTrackRejectsCrossKindMissingAndOtherCommandsAtomically(t *testing.T) {
	for _, target := range []string{"track-caption-1", "missing"} {
		engine := fixture(t)
		before := engine.Snapshot()
		track := Track{ID: "insert", Name: "不能插入", Kind: "video"}
		_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: "invalid-before", Commands: []Command{{Type: "add_track", Track: &track, BeforeTrackID: target}}})
		assertCode(t, err, "validation")
		if !reflect.DeepEqual(before, engine.Snapshot()) {
			t.Fatal("无效插入位置改变工程")
		}
	}
	engine := fixture(t)
	before := engine.Snapshot()
	_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: "invalid-command-before", Commands: []Command{{Type: "update_project", Name: "不应保存", BeforeTrackID: "track-video-1"}}})
	assertCode(t, err, "validation")
}

func TestIdentityReturnsScalarsWithoutSnapshotAllocations(t *testing.T) {
	engine := fixture(t)
	id, revision := engine.Identity()
	if id == "" || revision != 0 {
		t.Fatal("轻量身份字段错误")
	}
	if allocations := testing.AllocsPerRun(1000, func() { id, revision = engine.Identity() }); allocations != 0 {
		t.Fatalf("标量身份检查不应深拷贝工程：%f", allocations)
	}
	apply(t, engine, Command{Type: "update_project", Name: "修订后"})
	newID, newRevision := engine.Identity()
	if newID != id || newRevision != revision+1 {
		t.Fatal("轻量修订未反映统一编辑结果")
	}
}
