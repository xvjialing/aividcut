package localagent

import (
	"aividcut/internal/editor"
	"testing"
)

func TestTrackMovePlanUsesProjectScopeAndHonorsLocks(t *testing.T) {
	p := editor.NewProject("排序")
	p.Tracks = append(p.Tracks, editor.Track{ID: "v2", Name: "视频二", Kind: "video"})
	c := editor.DefaultClip("title", p.Tracks[1].ID, "text")
	p.Clips = append(p.Clips, c)
	req := editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "move", Commands: []editor.Command{{Type: "move_track", TrackID: "v2", TrackDelta: -1}}}
	if err := ValidatePlan(p, req); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlanForInput(PlanInput{Project: p, Selection: []string{c.ID}}, req); err == nil {
		t.Fatal("选区计划越权排序整轨")
	}
	p.Tracks[2].Locked = true
	if err := ValidatePlan(p, req); err == nil {
		t.Fatal("越过锁轨")
	}
}
