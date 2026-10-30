package application

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/workflows"
)

func workflowApplication(t *testing.T) *Service {
	t.Helper()
	s := newMCPApplication(t)
	if err := s.ConfigureWorkflows(filepath.Join(t.TempDir(), "workflows")); err != nil {
		t.Fatal(err)
	}
	clip := editor.DefaultClip("workflow-caption", "track-caption-1", "caption")
	clip.Text = "已有字幕"
	p := s.GetBootstrap().Project
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "caption-fixture", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWorkflowApplicationPreviewApplyUndoAndPresetPersistence(t *testing.T) {
	s := workflowApplication(t)
	before := s.GetBootstrap().Project
	input := workflows.Input{WorkflowID: "captions", Selection: []string{"workflow-caption"}, Parameters: map[string]any{"fontSize": 64}}
	preview, err := s.PreviewWorkflow(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ID == "" || len(preview.Effects) != 1 || preview.ProjectRevision != before.Revision {
		t.Fatal("工作流没有真实影响预览")
	}
	if s.GetBootstrap().Project.Clips[0].TextStyle.FontSize != 48 {
		t.Fatal("预览改了真实工程")
	}
	after, err := s.ApplyWorkflowPreview(context.Background(), preview.ID)
	if err != nil || after.Project.Clips[0].TextStyle.FontSize != 64 {
		t.Fatal("不能应用真实核心命令", err)
	}
	again, err := s.ApplyWorkflowPreview(context.Background(), preview.ID)
	if err != nil || again.Project.Revision != after.Project.Revision {
		t.Fatal("同预览被重复应用", err)
	}
	undone, err := s.Undo()
	if err != nil || undone.Project.Clips[0].TextStyle.FontSize != 48 {
		t.Fatal("工作流不能统一撤销", err)
	}
	again, err = s.ApplyWorkflowPreview(context.Background(), preview.ID)
	if err != nil || again.Project.Clips[0].TextStyle.FontSize != 48 {
		t.Fatal("撤销后旧预览自动重放", err)
	}
	preset, err := s.SaveWorkflowPreset(context.Background(), workflows.PresetInput{Name: "字幕字号", WorkflowID: "captions", Parameters: input.Parameters})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.GetWorkflowPresets()
	if err != nil || len(rows) != 1 || rows[0].ID != preset.ID {
		t.Fatal("共享预设服务不一致", err)
	}
	if err := s.DeleteWorkflowPreset(context.Background(), preset.ID, preset.Version); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowApplicationStaleAndForeignPreviewCannotApply(t *testing.T) {
	s := workflowApplication(t)
	input := workflows.Input{WorkflowID: "captions", Selection: []string{"workflow-caption"}, Parameters: map[string]any{"fontSize": 56}}
	preview, err := s.PreviewWorkflow(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	p := s.GetBootstrap().Project
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "human-name", Commands: []editor.Command{{Type: "update_project", Name: "人工修改"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyWorkflowPreview(context.Background(), preview.ID); err == nil {
		t.Fatal("旧版本预览覆盖人工修改")
	}
	if _, err := s.NewProject("另一个工程"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyWorkflowPreview(context.Background(), preview.ID); err == nil {
		t.Fatal("旧预览用于新工程")
	}
}

func TestWorkflowMCPUsesSamePreviewAndRequiresEditScope(t *testing.T) {
	s := workflowApplication(t)
	session := mcpApplicationClient(t, s, false)
	result := callApplicationMCP(t, session, "aividcut_preview_workflow", map[string]any{"input": workflows.Input{WorkflowID: "captions", Selection: []string{"workflow-caption"}, Parameters: map[string]any{"fontSize": 60}}}, false)
	id, ok := result["id"].(string)
	if !ok || id == "" {
		t.Fatal("MCP没有后端预览标识")
	}
	callApplicationMCP(t, session, "aividcut_apply_workflow", map[string]any{"id": id}, true)
	if s.GetBootstrap().Project.Clips[0].TextStyle.FontSize != 48 {
		t.Fatal("只读MCP应用了工作流")
	}
	if _, err := s.StopMCP(); err != nil {
		t.Fatal(err)
	}
	editorSession := mcpApplicationClient(t, s, true)
	callApplicationMCP(t, editorSession, "aividcut_apply_workflow", map[string]any{"id": id}, false)
	if s.GetBootstrap().Project.Clips[0].TextStyle.FontSize != 60 {
		t.Fatal("授权MCP没用同一预览/编辑核心")
	}
}

func TestBrollPreviewChecksActualSourceAndOverlayTrack(t *testing.T) {
	s := workflowApplication(t)
	path := filepath.Join(t.TempDir(), "own-image.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	bootstrap, err := s.ImportPaths(context.Background(), []string{path})
	if err != nil {
		t.Fatal(err)
	}
	asset := bootstrap.Project.Assets[0]
	preview, err := s.PreviewWorkflow(context.Background(), workflows.Input{WorkflowID: "broll", Parameters: map[string]any{"assetId": asset.ID, "startFrame": 30, "durationFrames": 60}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.ApplyWorkflowPreview(context.Background(), preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Project.Assets) != 1 || len(after.Project.Clips) != 2 {
		t.Fatal("B-roll误删原字幕或伪造新媒体")
	}
	var videos []editor.Track
	for _, track := range after.Project.Tracks {
		if track.Kind == "video" {
			videos = append(videos, track)
		}
	}
	if len(videos) != 2 {
		t.Fatal("没有独立叠加轨")
	}
	var added editor.Clip
	for _, clip := range after.Project.Clips {
		if clip.AssetID == asset.ID {
			added = clip
		}
	}
	if added.TrackID != videos[0].ID || !added.Muted {
		t.Fatal("补充镜头不可见或未经选择叠加声音")
	}
	if _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	second, err := s.PreviewWorkflow(context.Background(), workflows.Input{WorkflowID: "broll", Parameters: map[string]any{"assetId": asset.ID}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyWorkflowPreview(context.Background(), second.ID); err == nil {
		t.Fatal("源图已变化仍应用旧镜头证据")
	}
}

func TestTalkingHeadWorkflowActualGapEvidenceAndCaptionOverrides(t *testing.T) {
	s, asset, _ := sourceTranscriptFixture(t)
	if err := s.ConfigureWorkflows(filepath.Join(t.TempDir(), "workflows")); err != nil {
		t.Fatal(err)
	}
	project := s.GetBootstrap().Project
	transcript := editor.Transcript{ID: "gap-transcript", AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, RawWords: []editor.SourceWord{{ID: "w1", StartUs: 100_000, EndUs: 400_000, Text: "第一句"}, {ID: "w2", StartUs: 1_700_000, EndUs: 2_000_000, Text: "第二句"}, {ID: "w3", StartUs: 2_600_000, EndUs: 2_900_000, Text: "第三句"}}}
	s.mu.Lock()
	_, err := s.engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "workflow-source-evidence", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"}}})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	project = s.GetBootstrap().Project
	var human editor.Clip
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil && clip.Text == "第二句" {
			human = clip
		}
	}
	human.Text = "人工保留文字"
	human.TextStyle.FontSize = 72
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "human-caption-override", Commands: []editor.Command{{Type: "update_clip", Clip: &human}}}); err != nil {
		t.Fatal(err)
	}
	before := s.GetBootstrap().Project
	preview, err := s.PreviewWorkflow(context.Background(), workflows.Input{WorkflowID: "talking-head", Selection: []string{"speech-source-clip"}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.SavedFrames <= 0 || len(preview.Evidence) == 0 || len(preview.Request.Commands) == 0 {
		t.Fatal("没有基于实际接受词产生删减证据")
	}
	after, err := s.ApplyWorkflowPreview(context.Background(), preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	if editor.DurationFrames(after.Project) >= editor.DurationFrames(before) {
		t.Fatal("粗剪没有缩短实际工程")
	}
	found := false
	for _, clip := range after.Project.Clips {
		if clip.CaptionSource != nil && clip.Text == "人工保留文字" && clip.TextStyle.FontSize == 72 {
			found = true
		}
	}
	if !found {
		t.Fatal("词间删减后丢失正确源实例的人工字幕覆盖")
	}
	undone, err := s.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if editor.DurationFrames(undone.Project) != editor.DurationFrames(before) {
		t.Fatal("口播粗剪不能整组撤销")
	}
}
