package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/workflows"
	"context"
	"path/filepath"
	"testing"
)

func TestAudioFadeWorkflowRealMCPPermissionScopeAndStalePlan(t *testing.T) {
	s, _, _ := sourceTranscriptFixture(t)
	defer s.Close()
	if err := s.ConfigureWorkflows(filepath.Join(t.TempDir(), "workflows")); err != nil {
		t.Fatal(err)
	}
	p := s.GetBootstrap().Project
	in := workflows.Input{WorkflowID: "audio-fades", Selection: []string{p.Clips[0].ID}, Parameters: map[string]any{"targetKinds": "audio_and_video"}}
	read := mcpApplicationClient(t, s, false)
	callApplicationMCP(t, read, "aividcut_preview_workflow", map[string]any{"input": in}, true)
	in.Scope = "selection"
	preview := callApplicationMCP(t, read, "aividcut_preview_workflow", map[string]any{"input": in}, false)
	if preview["audioFadeSummary"] == nil || s.GetBootstrap().Project.Revision != p.Revision {
		t.Fatal("只读预览缺少统计或改工程")
	}
	callApplicationMCP(t, read, "aividcut_apply_workflow", map[string]any{"id": preview["id"]}, true)
	s.StopMCP()
	write := mcpApplicationClient(t, s, true)
	callApplicationMCP(t, write, "aividcut_apply_workflow", map[string]any{"id": preview["id"]}, false)
	if s.GetBootstrap().Project.Clips[0].AudioFade == nil {
		t.Fatal("授权连接未应用同一淡化核心")
	}
	undo, err := s.Undo()
	if err != nil {
		t.Fatal(err)
	}
	callApplicationMCP(t, write, "aividcut_apply_workflow", map[string]any{"id": preview["id"]}, false)
	if s.GetBootstrap().Project.Revision != undo.Project.Revision {
		t.Fatal("撤销后旧回执重放")
	}
	v, err := s.PreviewWorkflow(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: undo.Project.Revision, IdempotencyKey: "batch-fade-stale", Commands: []editor.Command{{Type: "update_project", Name: "新修订"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyWorkflowPreview(context.Background(), v.ID); err == nil {
		t.Fatal("旧版本计划应用到新工程状态")
	}
}
