package application

import (
	"aividcut/internal/editor"
	"testing"
)

func TestAudioFadeMCPUsesSameEditAndUndo(t *testing.T) {
	s, _, _ := sourceTranscriptFixture(t)
	defer s.Close()
	read := mcpApplicationClient(t, s, false)
	p := s.GetBootstrap().Project
	c := p.Clips[0]
	c.AudioFade = editor.NewAudioFade(c, 100000, 200000)
	req := editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "mcp-audio-fade", Commands: []editor.Command{{Type: "update_clip", Clip: &c}}}
	callApplicationMCP(t, read, "aividcut_apply_edit", map[string]any{"request": req}, true)
	s.StopMCP()
	write := mcpApplicationClient(t, s, true)
	callApplicationMCP(t, write, "aividcut_apply_edit", map[string]any{"request": req}, false)
	if s.GetBootstrap().Project.Clips[0].AudioFade == nil {
		t.Fatal("授权编辑没有实际淡化")
	}
	if _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if s.GetBootstrap().Project.Clips[0].AudioFade != nil {
		t.Fatal("撤销未恢复")
	}
}
