package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/workflows"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func transcriptCutApplication(t *testing.T) (*Service, TranscriptCutInput) {
	t.Helper()
	s, asset, _ := sourceTranscriptFixture(t)
	if err := s.ConfigureWorkflows(filepath.Join(t.TempDir(), "workflows")); err != nil {
		t.Fatal(err)
	}
	p := s.GetBootstrap().Project
	tr := editor.Transcript{ID: "wordcut", AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, RawWords: []editor.SourceWord{{ID: "w1", Text: "保留前", StartUs: 0, EndUs: 700_000}, {ID: "w2", Text: "删除", StartUs: 1_000_000, EndUs: 1_500_000}, {ID: "w3", Text: "保留后", StartUs: 2_000_000, EndUs: 3_000_000}}}
	s.mu.Lock()
	_, err := s.engine.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "words", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &tr}, {Type: "generate_bound_captions", TranscriptID: tr.ID, TrackID: "track-caption-1"}}})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b := s.GetBootstrap()
	return s, TranscriptCutInput{TranscriptCutInput: workflows.TranscriptCutInput{ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision, TranscriptID: tr.ID, TranscriptRevision: b.Project.Transcripts[0].Revision, ClipID: "speech-source-clip", WordIDs: []string{"w2"}, CloseGap: true}, SourceEpoch: b.SourceEpoch}
}
func TestTranscriptCutApplicationSourceGuardAndNoReplay(t *testing.T) {
	s, input := transcriptCutApplication(t)
	defer s.Close()
	ctx := context.Background()
	before := s.GetBootstrap().Project
	located, err := s.LocateTranscriptWords(ctx, input)
	if err != nil || located.StartFrame != 30 || located.EndFrame != 45 {
		t.Fatal(located, err)
	}
	preview, err := s.PreviewTranscriptCut(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, s.GetBootstrap().Project) {
		t.Fatal("定位/预览改动工程")
	}
	after, err := s.ApplyWorkflowPreview(ctx, preview.ID)
	if err != nil || editor.DurationFrames(after.Project) != 75 {
		t.Fatal("媒体未删减", err)
	}
	repeat, err := s.ApplyWorkflowPreview(ctx, preview.ID)
	if err != nil || repeat.Project.Revision != after.Project.Revision {
		t.Fatal("重复应用", err)
	}
	undo, err := s.Undo()
	if err != nil || editor.DurationFrames(undo.Project) != 90 {
		t.Fatal("撤销失败", err)
	}
	repeat, err = s.ApplyWorkflowPreview(ctx, preview.ID)
	if err != nil || repeat.Project.Revision != undo.Project.Revision {
		t.Fatal("Undo后旧预览重放", err)
	}
	input.ProjectRevision = undo.Project.Revision
	preview, err = s.PreviewTranscriptCut(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(before.Assets[0].Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyWorkflowPreview(ctx, preview.ID); err == nil {
		t.Fatal("已变源应用旧证据")
	}
	if _, err = s.PreviewTranscriptCut(ctx, input); err == nil {
		t.Fatal("已变源仍预览")
	}
	s.resetThumbnails(false)
	if _, err = s.LocateTranscriptWords(ctx, input); err == nil {
		t.Fatal("旧实例仍定位")
	}
}
func TestMCPTranscriptCutReadonlyPreviewAndEditPermission(t *testing.T) {
	s, input := transcriptCutApplication(t)
	defer s.Close()
	client := mcpApplicationClient(t, s, false)
	args := map[string]any{"projectRevision": input.ProjectRevision, "transcriptId": input.TranscriptID, "transcriptRevision": input.TranscriptRevision, "clipId": input.ClipID, "wordIds": input.WordIDs, "closeGap": true}
	result := callApplicationMCP(t, client, "aividcut_preview_transcript_cut", args, false)
	callApplicationMCP(t, client, "aividcut_apply_workflow", map[string]any{"id": result["id"]}, true)
	if s.GetBootstrap().Project.Revision != input.ProjectRevision {
		t.Fatal("只读预览改变工程")
	}
	s.StopMCP()
	client = mcpApplicationClient(t, s, true)
	result = callApplicationMCP(t, client, "aividcut_preview_transcript_cut", args, false)
	callApplicationMCP(t, client, "aividcut_apply_workflow", map[string]any{"id": result["id"]}, false)
	if editor.DurationFrames(s.GetBootstrap().Project) != 75 {
		t.Fatal("MCP未复用同一编辑核心")
	}
}
