package application

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/inspectordrafts"
)

func draftField(key, raw string, base any) inspectordrafts.Field {
	data, _ := json.Marshal(base)
	return inspectordrafts.Field{Key: key, Raw: raw, Base: data}
}
func draftService(t *testing.T, dir string) *Service {
	s := recoveryService(t, filepath.Join(dir, "recovery"))
	if err := s.ConfigureInspectorDrafts(filepath.Join(dir, "drafts")); err != nil {
		t.Fatal(err)
	}
	return s
}
func mustDraft(t *testing.T, s *Service, b Bootstrap, target string, fields ...inspectordrafts.Field) inspectordrafts.Record {
	t.Helper()
	v, err := s.SaveInspectorDraft(InspectorDraftInput{ProjectScope: versionScope(b), Target: target, Fields: fields})
	if err != nil || v == nil {
		t.Fatal(v, err)
	}
	return *v
}
func draftRef(b Bootstrap, v inspectordrafts.Record) InspectorDraftRef {
	return InspectorDraftRef{ProjectScope: versionScope(b), ID: v.ID, SHA256: v.SHA256}
}
func TestDraftDoesNotEditAndNormalCommitCleansOnlyOwned(t *testing.T) {
	dir := t.TempDir()
	s := draftService(t, dir)
	b := s.GetBootstrap()
	v := mustDraft(t, s, b, "", draftField("projectName", "未提交", b.Project.Name))
	if status := s.GetRecoveryStatus(); status.InputDrafts != 1 {
		t.Fatal("未提示属性草稿", status)
	}
	if after := s.GetBootstrap(); !reflect.DeepEqual(after, b) {
		t.Fatal("保护草稿改了工程")
	}
	s.Close()
	s2 := draftService(t, dir)
	b2 := s2.GetBootstrap()
	mustDraft(t, s2, b2, "", draftField("projectName", "本会话", b2.Project.Name))
	renameRecoveryProject(t, s2, "本会话")
	page, err := s2.ListInspectorDrafts()
	if err != nil || len(page.Records) != 1 || page.Records[0].ID != v.ID {
		t.Fatal("正常提交清理了旧会话遗留草稿", page, err)
	}
}
func TestDraftConflictRecheckUndoReplayAndScope(t *testing.T) {
	s := draftService(t, t.TempDir())
	b := s.GetBootstrap()
	v := mustDraft(t, s, b, "", draftField("projectName", "找回文字", b.Project.Name))
	changed := renameRecoveryProject(t, s, "外部新值")
	review, err := s.PreviewInspectorDraft(InspectorDraftReview{InspectorDraftRef: draftRef(changed, v)})
	if err != nil || review.CanApply || !review.Rows[0].Conflict {
		t.Fatal(review, err)
	}
	review, err = s.PreviewInspectorDraft(InspectorDraftReview{InspectorDraftRef: draftRef(changed, v), RecheckCurrent: true})
	if err != nil || !review.CanApply {
		t.Fatal(review, err)
	}
	after, err := s.ApplyInspectorDraft(review.ID)
	if err != nil || after.Project.Name != "找回文字" || !after.CanUndo {
		t.Fatal(after, err)
	}
	undone, err := s.Undo()
	if err != nil || undone.Project.Name != "外部新值" {
		t.Fatal(undone, err)
	}
	if _, err = s.ApplyInspectorDraft(review.ID); err == nil {
		t.Fatal("撤销后的旧请求被重放")
	}
	old := mustDraft(t, s, undone, "", draftField("projectName", "旧实例输入", undone.Project.Name))
	path := filepath.Join(t.TempDir(), "same.aividcut")
	s.SaveProjectTo(path)
	fresh, err := s.OpenProjectPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveInspectorDraft(InspectorDraftInput{ProjectScope: versionScope(undone), Fields: old.Fields}); err == nil {
		t.Fatal("同ID旧实例仍能保存")
	}
	if _, err = s.PreviewInspectorDraft(InspectorDraftReview{InspectorDraftRef: draftRef(undone, old)}); err == nil {
		t.Fatal("旧实例仍能准备")
	}
	if fresh.Project.ID != undone.Project.ID {
		t.Fatal("夹具工程ID改变")
	}
}
func TestDraftInvalidNumericDeletedAndLockedTargets(t *testing.T) {
	s := draftService(t, t.TempDir())
	b := s.GetBootstrap()
	c := editor.DefaultClip("text-draft", "track-text-1", "text")
	c.Text = "原字幕"
	b, err := s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "add-draft-target", Commands: []editor.Command{{Type: "add_clip", Clip: &c}}})
	if err != nil {
		t.Fatal(err)
	}
	v := mustDraft(t, s, b, c.ID, draftField("fontSize", "", 48), draftField("text", "未提交字幕", c.Text))
	review, err := s.PreviewInspectorDraft(InspectorDraftReview{InspectorDraftRef: draftRef(b, v)})
	if err != nil || review.CanApply || review.Rows[0].Error == "" {
		t.Fatal(review, err)
	}
	review, err = s.PreviewInspectorDraft(InspectorDraftReview{InspectorDraftRef: draftRef(b, v), Values: map[string]string{"fontSize": "56"}})
	if err != nil || !review.CanApply {
		t.Fatal(review, err)
	}
	track := b.Project.Tracks[1]
	track.Locked = true
	locked, err := s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "lock", Commands: []editor.Command{{Type: "update_track", Track: &track}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyInspectorDraft(review.ID); err == nil {
		t.Fatal("旧修订预览仍应用")
	}
	review, err = s.PreviewInspectorDraft(InspectorDraftReview{InspectorDraftRef: draftRef(locked, v), Values: map[string]string{"fontSize": "56"}})
	if err != nil || review.CanApply || review.Error == "" {
		t.Fatal(review, err)
	}
	b, err = s.Undo()
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "delete", Commands: []editor.Command{{Type: "delete_clip", ClipID: c.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	v = mustDraft(t, s, deleted, c.ID, draftField("text", "目标删除后迟到的输入", c.Text))
	review, err = s.PreviewInspectorDraft(InspectorDraftReview{InspectorDraftRef: draftRef(deleted, v)})
	if err != nil || review.CanApply || review.Error == "" {
		t.Fatal("删除目标未保留/拒绝", review, err)
	}
}
func TestDraftRestoreBaseProtectsCurrentAndOriginal(t *testing.T) {
	dir := t.TempDir()
	s := draftService(t, dir)
	b := renameRecoveryProject(t, s, "基础")
	path := filepath.Join(t.TempDir(), "original.aividcut")
	b, err := s.SaveProjectTo(path)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	v := mustDraft(t, s, b, "", draftField("projectName", "找回输入", b.Project.Name))
	s.Close()
	s2 := draftService(t, dir)
	current := renameRecoveryProject(t, s2, "别的未保存工程")
	restored, err := s2.RestoreInspectorDraftBase(draftRef(current, v))
	if err != nil || restored.Project.Name != "基础" || restored.ProjectPath != "" || !restored.Dirty || restored.CanUndo {
		t.Fatal(restored, err)
	}
	page, err := s2.ListProjectRecoveries(RecoveryListInput{Limit: 20})
	if err != nil || page.Total != 1 || page.Records[0].Name != "别的未保存工程" {
		t.Fatal("当前状态未先保护", page, err)
	}
	after, _ := os.ReadFile(path)
	if !reflect.DeepEqual(original, after) {
		t.Fatal("原文件被覆盖")
	}
	preview, err := s2.PreviewInspectorDraft(InspectorDraftReview{InspectorDraftRef: draftRef(restored, v)})
	if err != nil || !preview.CanApply {
		t.Fatal(preview, err)
	}
	if _, err = s2.ApplyInspectorDraft(preview.ID); err != nil {
		t.Fatal(err)
	}
}
