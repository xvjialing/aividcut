package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/generationjobs"
)

func replacementFixture(t *testing.T) (*Service, GenerationJob, editor.Clip) {
	t.Helper()
	s := configuredGenerationService(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, &appProviderControl{})
	connection := appSaveConnection(t, s, "", "fixture-only-key")
	originalPath := filepath.Join(t.TempDir(), "original.png")
	if err := os.WriteFile(originalPath, appImageOutput().Data, 0600); err != nil {
		t.Fatal(err)
	}
	imported, err := s.ImportPaths(context.Background(), []string{originalPath})
	if err != nil {
		t.Fatal(err)
	}
	clip := editor.DefaultClip("original-shot", "track-video-1", "image")
	clip.AssetID = imported.Project.Assets[0].ID
	clip.StartFrame, clip.DurationFrames = 30, 120
	clip.Transform = editor.Transform{X: 12, Y: 20, Scale: 1.2, Rotation: 3}
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: imported.Project.Revision, IdempotencyKey: "fixture-shot", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}}}); err != nil {
		t.Fatal(err)
	}
	input := appSubmit("replacement-generation", connection.ID, 1)
	if _, err := s.SubmitGeneration(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := appAwaitJob(t, s, input.Request.ID, generationjobs.Succeeded)
	return s, job, clip
}
func replacementInput(s *Service, job GenerationJob, clip editor.Clip) GenerationReplacementInput {
	p := s.GetBootstrap().Project
	return GenerationReplacementInput{JobID: job.ID, CandidateID: job.Assets[0].ID, ClipID: clip.ID, ProjectID: p.ID, ProjectRevision: p.Revision, AudioMode: "keep_original", LUTMode: "keep", SpeedMode: "preserve"}
}

func TestGenerationReplacementPreviewAtomicApplyUndoRedoAndNoReplay(t *testing.T) {
	s, job, clip := replacementFixture(t)
	before := s.GetBootstrap().Project
	input := replacementInput(s, job, clip)
	preview, err := s.PreviewGenerationReplacement(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if projectHash(s.GetBootstrap().Project) != projectHash(before) || s.GetBootstrap().Project.Revision != before.Revision {
		t.Fatal("替换预览修改工程或先行导入")
	}
	if preview.ReplacementClip.ID != clip.ID || preview.ReplacementClip.AssetID != job.Assets[0].ID || preview.ReplacementClip.StartFrame != clip.StartFrame || preview.ReplacementClip.DurationFrames != clip.DurationFrames || preview.ReplacementClip.Transform != clip.Transform {
		t.Fatal("替换未保持镜头时序/ID/构图")
	}
	if preview.CandidateAsset.Width != 32 || preview.CandidateAsset.Height != 18 || preview.CandidateAsset.URL == "" || preview.OriginalAsset.URL == "" {
		t.Fatal("预览不含真实候选参数与受控对照图")
	}
	data, _ := json.Marshal(preview)
	var public map[string]any
	_ = json.Unmarshal(data, &public)
	if _, found := public["request"]; found {
		t.Fatal("前端可以篡改后端替换命令")
	}
	changed, err := s.ApplyGenerationReplacement(context.Background(), preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Project.Assets) != len(before.Assets)+1 || changed.Project.Clips[0].AssetID != job.Assets[0].ID || changed.Project.Revision != before.Revision+1 {
		t.Fatal("候选导入与镜头未一次原子应用")
	}
	if again, err := s.ApplyGenerationReplacement(context.Background(), preview.ID); err != nil || again.Project.Revision != changed.Project.Revision {
		t.Fatal("重复应用重放替换", err)
	}
	undone, err := s.Undo()
	if err != nil || projectHash(undone.Project) != projectHash(before) {
		t.Fatal("候选导入和替换未整组Undo", err)
	}
	if _, err := s.ApplyGenerationReplacement(context.Background(), preview.ID); err != nil || projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("Undo后旧preview重放", err)
	}
	redone, err := s.Redo()
	if err != nil || redone.Project.Clips[0].AssetID != job.Assets[0].ID {
		t.Fatal("替换Redo失败", err)
	}
	view := appAwaitJob(t, s, job.ID, generationjobs.Succeeded)
	if !view.Imported {
		t.Fatal("替换成功候选未标为实际导入")
	}
}

func TestGenerationReplacementRejectsStaleRevisionLockedTrackAndWrongCandidate(t *testing.T) {
	s, job, clip := replacementFixture(t)
	input := replacementInput(s, job, clip)
	bad := input
	bad.CandidateID = "other-job-candidate"
	if _, err := s.PreviewGenerationReplacement(context.Background(), bad); err == nil {
		t.Fatal("接受其他任务候选")
	}
	preview, err := s.PreviewGenerationReplacement(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	p := s.GetBootstrap().Project
	var track editor.Track
	for _, item := range p.Tracks {
		if item.ID == clip.TrackID {
			track = item
			break
		}
	}
	track.Locked = true
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "lock-before-replace", Commands: []editor.Command{{Type: "update_track", Track: &track}}}); err != nil {
		t.Fatal(err)
	}
	locked := s.GetBootstrap().Project
	if _, err := s.ApplyGenerationReplacement(context.Background(), preview.ID); err == nil {
		t.Fatal("旧预览覆盖锁轨或新版本")
	}
	if _, err := s.PreviewGenerationReplacement(context.Background(), input); err == nil {
		t.Fatal("旧版本还可创建新预览")
	}
	input.ProjectRevision = locked.Revision
	if _, err := s.PreviewGenerationReplacement(context.Background(), input); err == nil {
		t.Fatal("锁轨还可替换镜头")
	}
	if projectHash(s.GetBootstrap().Project) != projectHash(locked) {
		t.Fatal("失败的替换部分导入/修改工程")
	}
}

func TestGenerationReplacementRejectsMutatedCandidateAndSameIDReopenedProject(t *testing.T) {
	s, job, clip := replacementFixture(t)
	preview, err := s.PreviewGenerationReplacement(context.Background(), replacementInput(s, job, clip))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "before-replace.aividcut")
	if _, err := s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyGenerationReplacement(context.Background(), preview.ID); err == nil {
		t.Fatal("重开同ID工程仍接受旧预览")
	}
	preview, err = s.PreviewGenerationReplacement(context.Background(), replacementInput(s, job, clip))
	if err != nil {
		t.Fatal(err)
	}
	before := s.GetBootstrap().Project
	if err := os.WriteFile(job.Assets[0].Path, []byte("changed generated image"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyGenerationReplacement(context.Background(), preview.ID); err == nil {
		t.Fatal("候选文件被替换后仍提交")
	}
	if projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("文件校验失败修改工程")
	}
}

func TestGenerationReplacementCandidateSoundAndPublicCommandAreGuarded(t *testing.T) {
	s, job, clip := replacementFixture(t)
	input := replacementInput(s, job, clip)
	input.AudioMode = "use_candidate"
	before := s.GetBootstrap().Project
	if _, err := s.PreviewGenerationReplacement(context.Background(), input); err == nil {
		t.Fatal("无声图片被当成可用候选声音")
	}
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: before.Revision, IdempotencyKey: "forge-visual-replacement", Commands: []editor.Command{{Type: "replace_visual", Clip: &clip}}}); err == nil {
		t.Fatal("普通接口直接调用未验证替换")
	}
	if projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("不合法声音策略修改工程")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.PreviewGenerationReplacement(ctx, replacementInput(s, job, clip)); err == nil {
		t.Fatal("已取消请求仍创建预览")
	}
}
