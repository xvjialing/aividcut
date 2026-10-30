package application

import (
	"aividcut/internal/editor"
	"testing"
)

func TestMCPSelectionTrimUsesSameAtomicCore(t *testing.T) {
	s, state := clipboardFixture(t)
	session := mcpApplicationClient(t, s, true)
	request := editor.EditRequest{BaseRevision: state.Project.Revision, IdempotencyKey: "mcp-trim", Commands: []editor.Command{{Type: "trim_clips", ClipIDs: []string{"a", "b"}, Edge: "start", FrameDelta: 2}}}
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": request}, false)
	p := s.GetBootstrap().Project
	if p.Clips[0].StartFrame != 2 || p.Clips[1].StartFrame != 32 || p.Clips[0].DurationFrames != 8 || p.Clips[1].DurationFrames != 8 {
		t.Fatal("MCP不是同量修剪")
	}
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": request}, false)
	if s.GetBootstrap().Project.Revision != p.Revision {
		t.Fatal("修剪重试重复执行")
	}
	callApplicationMCP(t, session, "aividcut_undo", map[string]any{"baseRevision": p.Revision}, false)
	if s.GetBootstrap().Project.Clips[1].StartFrame != 30 {
		t.Fatal("MCP修剪未一次撤销")
	}
}
