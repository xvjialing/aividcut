package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/projectcollect"
	"aividcut/internal/projectstore"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func sequenceInput(b Bootstrap, action, id, name string) SequenceChangeInput {
	return SequenceChangeInput{ProjectScope: versionScope(b), Action: action, SequenceID: id, Name: name, IdempotencyKey: editor.NewID("sequence-test")}
}

func TestMultiSequenceVersionRecoveryAndCollectionKeepHiddenTimeline(t *testing.T) {
	s := recoveryService(t, t.TempDir())
	if err := s.ConfigureProjectVersions(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	b := s.GetBootstrap()
	c := editor.DefaultClip("local-id", "track-text-1", "text")
	c.Text = "原稿"
	var err error
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "source", Commands: []editor.Command{{Type: "add_clip", Clip: &c}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.ChangeSequence(sequenceInput(b, "duplicate", editor.MainSequenceID, "副本"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "two.aividcut")
	b, err = s.SaveProjectTo(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.CreateProjectVersion(CreateProjectVersionInput{ProjectVersionScope: versionScope(b), Name: "全部序列"})
	if err != nil || v.Sequences != 2 {
		t.Fatal(v, err)
	}
	b, err = s.ChangeSequence(sequenceInput(b, "switch", editor.MainSequenceID, ""))
	if err != nil || b.Dirty {
		t.Fatal(err)
	}
	status, err := s.CheckpointRecovery()
	if err != nil || status.ProtectedRevision != 0 {
		t.Fatal("单纯导航不应产生恢复点", status, err)
	}
	c = b.Project.Clips[0]
	c.Text = "更改原稿"
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "dirty", Commands: []editor.Command{{Type: "update_clip", Clip: &c}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckpointRecovery(); err != nil {
		t.Fatal(err)
	}
	page, err := s.recovery.store.List("", 0, 20)
	if err != nil || len(page.Records) < 1 {
		t.Fatal(page, err)
	}
	record := page.Records[0]
	full, _, _, err := s.recovery.store.Load(record.ID, record.SHA256)
	if err != nil || len(full.OtherSequences) != 1 || record.Sequences != 2 {
		t.Fatal("自动恢复丢了非当前序列", err)
	}
	restored, err := s.RestoreProjectVersion(RestoreProjectVersionInput{ProjectVersionScope: versionScope(b), ID: v.ID, SHA256: v.SHA256})
	if err != nil || len(restored.Bootstrap.Sequences) != 2 {
		t.Fatal(restored, err)
	}
	full = s.engine.Snapshot()
	collected, err := projectcollect.Collect(context.Background(), full, t.TempDir(), "collected", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := projectstore.Load(collected)
	if err != nil || len(p.OtherSequences) != 1 || p.OtherSequences[0].Clips[0].Text != "原稿" {
		t.Fatal("收集没有保留全部序列", err)
	}
}
func TestSequenceNavigationDirtyUndoProjectionAndSavedFullProject(t *testing.T) {
	s := newRelinkService(t)
	b := s.GetBootstrap()
	c := editor.DefaultClip("same-id", "track-text-1", "text")
	c.Text = "原序列"
	var err error
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "text", Commands: []editor.Command{{Type: "add_clip", Clip: &c}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.ChangeSequence(sequenceInput(b, "duplicate", editor.MainSequenceID, "副本"))
	if err != nil {
		t.Fatal(err)
	}
	second := b.Project.SequenceID
	if !b.Project.ActiveSequenceProjection || len(b.Project.OtherSequences) != 0 || len(b.Sequences) != 2 {
		t.Fatal("Bootstrap应只发当前详细数据和摘要")
	}
	if _, err := projectstore.Encode(b.Project); err == nil {
		t.Fatal("投影不能冒充完整工程保存")
	}
	path := filepath.Join(t.TempDir(), "sequences.aividcut")
	b, err = s.SaveProjectTo(path)
	if err != nil {
		t.Fatal(err)
	}
	savedEpoch := b.SourceEpoch
	b, err = s.ChangeSequence(sequenceInput(b, "switch", editor.MainSequenceID, ""))
	if err != nil || b.Dirty || b.SourceEpoch == savedEpoch {
		t.Fatal("导航应不dirty且退休旧上下文", err)
	}
	b, err = s.ChangeSequence(sequenceInput(b, "switch", second, ""))
	if err != nil || b.Dirty {
		t.Fatal(err)
	}
	c = b.Project.Clips[0]
	c.Text = "副本修改"
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "copy-text", Commands: []editor.Command{{Type: "update_clip", Clip: &c}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.ChangeSequence(sequenceInput(b, "switch", editor.MainSequenceID, ""))
	if err != nil || !b.Dirty || b.Project.Clips[0].Text != "原序列" {
		t.Fatal("其它序列内容/dirty不正确", err)
	}
	b, err = s.Undo()
	if err != nil || b.Project.SequenceID != second || b.Dirty || b.Project.Clips[0].Text != "原序列" {
		t.Fatal("跨序列Undo错误", err)
	}
	if _, err = s.Redo(); err != nil {
		t.Fatal(err)
	}
	b, err = s.SaveProject()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	full, _, err := projectstore.DecodeWithWarnings(data, path)
	if err != nil || len(full.OtherSequences) != 1 {
		t.Fatal("保存丢了非当前序列", err)
	}
	b, err = s.OpenProjectPath(path)
	if err != nil || len(b.Sequences) != 2 || b.Dirty {
		t.Fatal("重开没有全部序列", err)
	}
}
func TestSequenceScopeClipboardAndMCPActivationNeverRevive(t *testing.T) {
	s := newRelinkService(t)
	b := s.GetBootstrap()
	c := editor.DefaultClip("same-id", "track-text-1", "text")
	c.Text = "序列A"
	b, _ = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "text", Commands: []editor.Command{{Type: "add_clip", Clip: &c}}})
	if _, err := s.CopyTimelineSelection(ClipboardSelectionInput{ProjectID: b.Project.ID, SourceEpoch: b.SourceEpoch, BaseRevision: b.Project.Revision, ClipIDs: []string{c.ID}}); err != nil {
		t.Fatal(err)
	}
	oldScope := versionScope(b)
	scope := &mcpProjectScope{engine: s.engine, sequenceGeneration: s.sequenceGeneration}
	scope.active.Store(true)
	scope.requestsActive.Store(true)
	b, err := s.ChangeSequence(sequenceInput(b, "duplicate", editor.MainSequenceID, "序列B"))
	if err != nil {
		t.Fatal(err)
	}
	if s.GetClipboardStatus().Available {
		t.Fatal("其它序列不应显示可粘贴")
	}
	s.mu.Lock()
	err = scope.checkLocked(s)
	s.mu.Unlock()
	if err == nil {
		t.Fatal("旧MCP不得进入新序列")
	}
	if _, err = s.ChangeSequence(SequenceChangeInput{ProjectScope: oldScope, Action: "switch", SequenceID: editor.MainSequenceID}); err == nil {
		t.Fatal("旧窗口应拒绝")
	}
	b, err = s.ChangeSequence(sequenceInput(b, "switch", editor.MainSequenceID, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !s.GetClipboardStatus().Available {
		t.Fatal("回来源序列应可继续使用原剪贴板")
	}
	s.mu.Lock()
	err = scope.checkLocked(s)
	s.mu.Unlock()
	if err == nil {
		t.Fatal("回原序列也不能复活旧工具连接")
	}
	if _, err := s.InspectBatchRelink(context.Background(), oldScope); err == nil {
		t.Fatal("原Scope应永久失效")
	}
}
