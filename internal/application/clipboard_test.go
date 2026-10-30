package application

import (
	"aividcut/internal/editor"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func clipboardFixture(t *testing.T) (*Service, Bootstrap) {
	t.Helper()
	s := newMCPApplication(t)
	p := s.GetBootstrap().Project
	a := editor.DefaultClip("a", "track-text-1", "text")
	a.Text = "复制时的秘密文案"
	a.DurationFrames = 10
	b := editor.DefaultClip("b", "track-text-1", "text")
	b.Text = "第二片段"
	b.StartFrame = 30
	b.DurationFrames = 10
	state, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "clipboard-fixture", Commands: []editor.Command{{Type: "add_clip", Clip: &a}, {Type: "add_clip", Clip: &b}}})
	if err != nil {
		t.Fatal(err)
	}
	return s, state
}
func clipboardSelection(state Bootstrap, ids ...string) ClipboardSelectionInput {
	return ClipboardSelectionInput{ProjectID: state.Project.ID, SourceEpoch: state.SourceEpoch, BaseRevision: state.Project.Revision, ClipIDs: ids}
}
func TestClipboardServiceFrozenPasteRetryAndOpaqueBoundary(t *testing.T) {
	s, state := clipboardFixture(t)
	copy, err := s.CopyTimelineSelection(clipboardSelection(state, "a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if copy.Bootstrap.Project.Revision != state.Project.Revision || !copy.Clipboard.Available || copy.Clipboard.ClipCount != 2 {
		t.Fatal("复制修改工程或摘要错误")
	}
	clip := state.Project.Clips[0]
	clip.Text = "改过的值"
	state, err = s.ApplyEdit(editor.EditRequest{BaseRevision: state.Project.Revision, IdempotencyKey: "edit-delete-original", Commands: []editor.Command{{Type: "update_clip", Clip: &clip}, {Type: "delete_clips", ClipIDs: []string{"a", "b"}}}})
	if err != nil {
		t.Fatal(err)
	}
	input := ClipboardPasteInput{ProjectID: state.Project.ID, SourceEpoch: state.SourceEpoch, BaseRevision: state.Project.Revision, ClipboardID: copy.Clipboard.ID, IdempotencyKey: "paste-clipboard", AtFrame: 100}
	pasted, err := s.PasteTimelineSelection(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(pasted.SelectedClipIDs) != 2 || pasted.Bootstrap.Project.Clips[0].Text != "复制时的秘密文案" || pasted.Bootstrap.Project.Clips[1].StartFrame != 130 {
		t.Fatal("粘贴不是旧快照")
	}
	newer, err := s.CopyTimelineSelection(clipboardSelection(pasted.Bootstrap, pasted.SelectedClipIDs[0]))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.PasteTimelineSelection(input)
	if err != nil || len(retry.Bootstrap.Project.Clips) != 2 || !reflect.DeepEqual(retry.SelectedClipIDs, pasted.SelectedClipIDs) || retry.Clipboard.ID != newer.Clipboard.ID {
		t.Fatal("替换剪贴板后幂等重试错误", err)
	}
	_, err = s.ApplyEdit(editor.EditRequest{BaseRevision: retry.Bootstrap.Project.Revision, IdempotencyKey: "forged-paste", Commands: []editor.Command{{Type: "paste_clips", ClipboardID: newer.Clipboard.ID, AtFrame: 200}}})
	if err == nil {
		t.Fatal("普通编辑接受了快照注入")
	}
}
func TestClipboardCutCommitUndoRetryAndFailureKeepsBuffer(t *testing.T) {
	s, state := clipboardFixture(t)
	input := ClipboardCutInput{ClipboardSelectionInput: clipboardSelection(state, "a", "b"), IdempotencyKey: "cut-once"}
	cut, err := s.CutTimelineSelection(input)
	if err != nil || len(cut.Bootstrap.Project.Clips) != 0 {
		t.Fatal("剪切失败", err)
	}
	restored, err := s.Undo()
	if err != nil || len(restored.Project.Clips) != 2 {
		t.Fatal("剪切未一次撤销", err)
	}
	copy, err := s.CopyTimelineSelection(clipboardSelection(restored, "b"))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.CutTimelineSelection(input)
	if err != nil || len(retry.Bootstrap.Project.Clips) != 2 || retry.Clipboard.ID != copy.Clipboard.ID {
		t.Fatal("剪切重试再次删除或倒退剪贴板", err)
	}
	changed := input
	changed.ClipIDs = []string{"b"}
	if _, err = s.CutTimelineSelection(changed); err == nil {
		t.Fatal("同幂等标识接受不同剪切")
	}
	track := restored.Project.Tracks[1]
	track.Locked = true
	locked, err := s.ApplyEdit(editor.EditRequest{BaseRevision: restored.Project.Revision, IdempotencyKey: "lock-clipboard-source", Commands: []editor.Command{{Type: "update_track", Track: &track}}})
	if err != nil {
		t.Fatal(err)
	}
	read, err := s.CopyTimelineSelection(clipboardSelection(locked, "a"))
	if err != nil {
		t.Fatal("锁轨应允许只读复制", err)
	}
	before := s.GetBootstrap()
	_, err = s.CutTimelineSelection(ClipboardCutInput{ClipboardSelectionInput: clipboardSelection(before, "a"), IdempotencyKey: "cut-locked"})
	if err == nil || s.GetClipboardStatus().ID != read.Clipboard.ID || !reflect.DeepEqual(before.Project, s.GetBootstrap().Project) {
		t.Fatal("剪切失败覆盖剪贴板或工程", err)
	}
}
func TestClipboardProjectEpochIsolationAndSameProjectReopen(t *testing.T) {
	s, state := clipboardFixture(t)
	path := filepath.Join(t.TempDir(), "clipboard.aividcut")
	state, err := s.SaveProjectTo(path)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := s.CopyTimelineSelection(clipboardSelection(state, "a"))
	if err != nil {
		t.Fatal(err)
	}
	old := ClipboardPasteInput{ProjectID: state.Project.ID, SourceEpoch: state.SourceEpoch, BaseRevision: state.Project.Revision, ClipboardID: copy.Clipboard.ID, IdempotencyKey: "old-instance", AtFrame: 100}
	reopened, err := s.OpenProjectPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PasteTimelineSelection(old); err == nil {
		t.Fatal("旧工程实例请求没有被拒绝")
	}
	old.SourceEpoch = reopened.SourceEpoch
	old.BaseRevision = reopened.Project.Revision
	old.IdempotencyKey = "reopened-paste"
	if _, err = s.PasteTimelineSelection(old); err != nil {
		t.Fatal("同工程重开应能恢复资源一致的剪贴板", err)
	}
	if _, err = s.NewProject("另一工程"); err != nil {
		t.Fatal(err)
	}
	status := s.GetClipboardStatus()
	if status.Available || status.ID != "" || status.ClipCount != 0 {
		t.Fatal("摘要泄漏其它工程内容")
	}
}
func TestClipboardMCPPermissionsAndResultContainsNoContent(t *testing.T) {
	s, state := clipboardFixture(t)
	session := mcpApplicationClient(t, s, true)
	copied := callApplicationMCP(t, session, "aividcut_copy_clips", map[string]any{"baseRevision": state.Project.Revision, "clipIds": []string{"a", "b"}}, false)
	clipboard := copied["clipboard"].(map[string]any)
	id := clipboard["id"].(string)
	pasted := callApplicationMCP(t, session, "aividcut_paste_clips", map[string]any{"baseRevision": state.Project.Revision, "clipboardId": id, "idempotencyKey": "mcp-clipboard", "atFrame": 100}, false)
	encoded, _ := json.Marshal(pasted)
	if strings.Contains(string(encoded), "秘密文案") || strings.Contains(string(encoded), "/private/") || strings.Contains(string(encoded), "projectPath") {
		t.Fatal("MCP剪贴板返回内容或路径")
	}
	if len(s.GetBootstrap().Project.Clips) != 4 {
		t.Fatal("MCP没有应用真实粘贴")
	}
	readonly, roState := clipboardFixture(t)
	if _, err := readonly.CopyTimelineSelection(clipboardSelection(roState, "a")); err != nil {
		t.Fatal(err)
	}
	ro := mcpApplicationClient(t, readonly, false)
	tools, err := ro.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "aividcut_copy_clips" || tool.Name == "aividcut_cut_clips" || tool.Name == "aividcut_paste_clips" {
			t.Fatal("只读连接暴露剪贴板写入")
		}
	}
	summary := callApplicationMCP(t, ro, "aividcut_get_clipboard", map[string]any{}, false)
	encoded, _ = json.Marshal(summary)
	if strings.Contains(string(encoded), "秘密文案") {
		t.Fatal("摘要泄漏文本")
	}
}
