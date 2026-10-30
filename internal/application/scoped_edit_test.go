package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/projectstore"
	"path/filepath"
	"reflect"
	"testing"
)

func scopedTrackInput(b Bootstrap) ScopedEditInput {
	track := b.Project.Tracks[0]
	track.Name = "字幕主轨"
	return ScopedEditInput{Scope: ProjectScope{b.Project.ID, b.Project.Revision, b.SourceEpoch}, Request: editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: editor.NewID("scoped"), Commands: []editor.Command{{Type: "update_track", Track: &track}}}}
}
func TestScopedEditUsesCoreAndRejectsOldRevisionAndInstance(t *testing.T) {
	s := setupService(t)
	before := s.GetBootstrap()
	input := scopedTrackInput(before)
	after, err := s.ApplyScopedEdit(input)
	if err != nil || after.Project.Tracks[0].Name != "字幕主轨" || !after.CanUndo {
		t.Fatalf("提交失败：%v", err)
	}
	if _, err = s.ApplyScopedEdit(input); err == nil {
		t.Fatal("旧修订通过")
	}
	path := filepath.Join(t.TempDir(), "same.aividcut")
	if err = projectstore.Save(path, before.Project); err != nil {
		t.Fatal(err)
	}
	if _, err = s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	current := s.GetBootstrap()
	if current.Project.ID != before.Project.ID || current.Project.Revision != before.Project.Revision || current.SourceEpoch == before.SourceEpoch {
		t.Fatal("未构成同ID同修订新实例")
	}
	if _, err = s.ApplyScopedEdit(input); err == nil {
		t.Fatal("旧实例通过")
	}
	if !reflect.DeepEqual(current, s.GetBootstrap()) {
		t.Fatal("拒绝后状态变化")
	}
}
func TestScopedEditDoesNotBypassPublicRestrictions(t *testing.T) {
	s := setupService(t)
	b := s.GetBootstrap()
	v := scopedTrackInput(b)
	v.Request.Commands = []editor.Command{{Type: "add_asset", Asset: &editor.Asset{ID: "x", Kind: "video", Path: "/unverified"}}}
	if _, err := s.ApplyScopedEdit(v); err == nil {
		t.Fatal("绕过素材校验")
	}
	v = scopedTrackInput(b)
	v.Request.BaseRevision++
	if _, err := s.ApplyScopedEdit(v); err == nil {
		t.Fatal("范围修订不一致")
	}
	if !reflect.DeepEqual(b, s.GetBootstrap()) {
		t.Fatal("非法请求改变工程")
	}
}

func TestMCPTrackMoveUsesRealTransportAndUndo(t *testing.T) {
	s := setupService(t)
	b := s.GetBootstrap()
	track := editor.Track{ID: "v2", Name: "补充镜头", Kind: "video"}
	b, err := s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "add", Commands: []editor.Command{{Type: "add_track", Track: &track}}})
	if err != nil {
		t.Fatal(err)
	}
	readOnly := mcpApplicationClient(t, s, false)
	request := editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "move", Commands: []editor.Command{{Type: "move_track", TrackID: track.ID, TrackDelta: -1}}}
	callApplicationMCP(t, readOnly, "aividcut_apply_edit", map[string]any{"request": request}, true)
	if _, err = s.StopMCP(); err != nil {
		t.Fatal(err)
	}
	client := mcpApplicationClient(t, s, true)
	track.Name = "主要镜头"
	request.Commands = append([]editor.Command{{Type: "update_track", Track: &track}}, request.Commands...)
	callApplicationMCP(t, client, "aividcut_apply_edit", map[string]any{"request": request}, false)
	after := s.GetBootstrap()
	if after.Project.Tracks[2].ID != track.ID || after.Project.Tracks[2].Name != track.Name || !reflect.DeepEqual(b.Project.Clips, after.Project.Clips) {
		t.Fatal("真实MCP排序错误")
	}
	callApplicationMCP(t, client, "aividcut_undo", map[string]any{"baseRevision": after.Project.Revision}, false)
	if !reflect.DeepEqual(b.Project.Tracks, s.GetBootstrap().Project.Tracks) {
		t.Fatal("真实MCP一次撤销未还原")
	}
}
