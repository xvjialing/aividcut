package application

import (
	"testing"

	"aividcut/internal/editor"
)

func TestMCPSelectionUsesAtomicCoreAndSingleUndo(t *testing.T) {
	s := newMCPApplication(t)
	p := s.GetBootstrap().Project
	var trackID string
	for _, track := range p.Tracks {
		if track.Kind == "text" {
			trackID = track.ID
		}
	}
	commands := []editor.Command{}
	for i, id := range []string{"first", "second", "blocker"} {
		c := editor.DefaultClip(id, trackID, "text")
		c.StartFrame, c.Text, c.DurationFrames = int64(10+i*30), id, 10
		commands = append(commands, editor.Command{Type: "add_clip", Clip: &c})
	}
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "selection-fixture", Commands: commands}); err != nil {
		t.Fatal(err)
	}
	session := mcpApplicationClient(t, s, true)
	base := s.GetBootstrap().Project
	request := editor.EditRequest{BaseRevision: base.Revision, IdempotencyKey: "mcp-selection", Commands: []editor.Command{{Type: "move_clips", ClipIDs: []string{"first", "second"}, FrameDelta: 20}}}
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": request}, false)
	moved := s.GetBootstrap().Project
	if moved.Clips[0].StartFrame != 30 || moved.Clips[1].StartFrame != 60 || moved.Revision != base.Revision+1 {
		t.Fatal("MCP未将选择一次性应用到同一核心")
	}
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": request}, false)
	if s.GetBootstrap().Project.Revision != moved.Revision {
		t.Fatal("MCP重试重复移动")
	}
	callApplicationMCP(t, session, "aividcut_undo", map[string]any{"baseRevision": moved.Revision}, false)
	restored := s.GetBootstrap().Project
	if restored.Clips[0].StartFrame != 10 || restored.Clips[1].StartFrame != 40 {
		t.Fatal("一次撤销未恢复整组")
	}
	request.BaseRevision, request.IdempotencyKey = restored.Revision, "mcp-selection-collision"
	request.Commands[0].FrameDelta = 30
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": request}, true)
	after := s.GetBootstrap().Project
	if after.Revision != restored.Revision || after.Clips[0].StartFrame != 10 || after.Clips[1].StartFrame != 40 {
		t.Fatal("MCP失败留下部分移动")
	}
	request = editor.EditRequest{BaseRevision: after.Revision, IdempotencyKey: "mcp-split-selection", Commands: []editor.Command{{Type: "split_clips", ClipIDs: []string{"first", "second"}, AtFrame: 15}}}
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": request}, false)
	split := s.GetBootstrap().Project
	if len(split.Clips) != 4 || split.Clips[0].DurationFrames != 5 || split.Clips[1].DurationFrames != 10 {
		t.Fatal("MCP未只分割跨越播放头的选择")
	}
	request = editor.EditRequest{BaseRevision: split.Revision, IdempotencyKey: "mcp-copy-selection", Commands: []editor.Command{{Type: "duplicate_clips", ClipIDs: []string{"first", "second"}, AtFrame: 100}}}
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": request}, false)
	copy := s.GetBootstrap().Project
	if len(copy.Clips) != 6 || copy.Clips[4].StartFrame != 100 || copy.Clips[5].StartFrame != 130 {
		t.Fatal("MCP副本丢失相对时间")
	}
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": request}, false)
	if s.GetBootstrap().Project.Revision != copy.Revision {
		t.Fatal("MCP幂等重复构造副本")
	}
	callApplicationMCP(t, session, "aividcut_undo", map[string]any{"baseRevision": copy.Revision}, false)
	if len(s.GetBootstrap().Project.Clips) != 4 {
		t.Fatal("MCP副本没有整组撤销")
	}
}
