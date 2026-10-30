package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/workflows"
	"context"
	"testing"
)

func TestRipplePreviewApplyScopeAndMCP(t *testing.T) {
	s, word := transcriptCutApplication(t)
	defer s.Close()
	ctx := context.Background()
	p := s.GetBootstrap().Project
	input := RippleInput{RippleInput: workflows.RippleInput{ProjectID: p.ID, ProjectRevision: p.Revision, FromFrame: 30, ToFrame: 45}, SourceEpoch: word.SourceEpoch}
	view, err := s.PreviewRippleDelete(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if s.GetBootstrap().Project.Revision != p.Revision {
		t.Fatal("预览写入")
	}
	after, err := s.ApplyWorkflowPreview(ctx, view.ID)
	if err != nil || editor.DurationFrames(after.Project) != 75 {
		t.Fatal("未实际删除", err)
	}
	undone, err := s.Undo()
	if err != nil || editor.DurationFrames(undone.Project) != 90 {
		t.Fatal("没有一次恢复", err)
	}
	again, err := s.ApplyWorkflowPreview(ctx, view.ID)
	if err != nil || again.Project.Revision != undone.Project.Revision {
		t.Fatal("撤销后旧候选重放", err)
	}
	input.ProjectRevision = undone.Project.Revision
	read := mcpApplicationClient(t, s, false)
	args := map[string]any{"projectRevision": input.ProjectRevision, "fromFrame": 30, "toFrame": 45}
	candidate := callApplicationMCP(t, read, "aividcut_preview_ripple_delete", args, false)
	callApplicationMCP(t, read, "aividcut_apply_workflow", map[string]any{"id": candidate["id"]}, true)
	s.StopMCP()
	write := mcpApplicationClient(t, s, true)
	candidate = callApplicationMCP(t, write, "aividcut_preview_ripple_delete", args, false)
	callApplicationMCP(t, write, "aividcut_apply_workflow", map[string]any{"id": candidate["id"]}, false)
	s.resetThumbnails(false)
	if _, err = s.PreviewRippleDelete(ctx, input); err == nil {
		t.Fatal("旧实例预览")
	}
}
