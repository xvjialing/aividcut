package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/media"
)

func captionLayoutFixture(t *testing.T) (*Service, CaptionLayoutInput, string) {
	t.Helper()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	path := filepath.Join(t.TempDir(), "own-caption-audio.wav")
	if err := writeApplicationWAV(path, 3000000); err != nil {
		t.Fatal(err)
	}
	p := s.GetBootstrap().Project
	asset := editor.Asset{ID: "caption-source", Name: "自建静音与合成源词", Kind: "audio", Path: path, DurationUs: 3000000, HasAudio: true, Fingerprint: fileDigest(t, path)}
	clip := editor.DefaultClip("caption-source-clip", "track-audio-1", "audio")
	clip.AssetID = asset.ID
	clip.DurationFrames = 90
	words := []editor.SourceWord{{ID: "w1", StartUs: 100000, EndUs: 300000, Text: "你好"}, {ID: "w2", StartUs: 350000, EndUs: 600000, Text: "世界。"}, {ID: "w3", StartUs: 1700000, EndUs: 2000000, Text: "测试"}, {ID: "w4", StartUs: 2100000, EndUs: 2500000, Text: "字幕。"}}
	transcript := editor.Transcript{ID: "caption-source-transcript", AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, SourceDurationUs: asset.DurationUs, RawWords: words, Origin: "asr"}
	s.mu.Lock()
	_, err = s.engine.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "caption-fixture", Commands: []editor.Command{{Type: "add_asset", Asset: &asset}, {Type: "add_clip", Clip: &clip}, {Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"}}})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	p = s.GetBootstrap().Project
	return s, CaptionLayoutInput{ProjectID: p.ID, ProjectRevision: p.Revision, TranscriptID: transcript.ID, TranscriptRevision: 1, Layout: editor.CaptionLayout{Mode: "readable"}}, path
}

func TestCaptionLayoutPreviewApplyUndoKeepsRealSourceWords(t *testing.T) {
	s, input, _ := captionLayoutFixture(t)
	before := s.GetBootstrap().Project
	preview, err := s.PreviewCaptionLayout(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.BeforeCount != 4 || preview.AfterCount != 2 || preview.Layout.MaxCharsPerLine != 18 || preview.ProtectedCount != 0 {
		t.Fatal("分句预览不符合真实词/停顿", preview)
	}
	if s.GetBootstrap().Project.Revision != before.Revision || projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("预览修改工程")
	}
	if len(preview.Captions) != 2 || len(preview.Captions[0].CaptionSource.WordIDs) != 2 || preview.Captions[0].Text != "你好世界。" {
		t.Fatal("多词绑定或中文分句不正确", preview.Captions)
	}
	changed, err := s.ApplyCaptionLayout(context.Background(), preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Project.Transcripts[0].RawWords[0].Text != "你好" || len(changed.Project.Transcripts[0].AcceptedWords) != 4 {
		t.Fatal("排版改写源词")
	}
	again, err := s.ApplyCaptionLayout(context.Background(), preview.ID)
	if err != nil || again.Project.Revision != changed.Project.Revision {
		t.Fatal("重放已应用预览", err)
	}
	undone, err := s.Undo()
	if err != nil || projectHash(undone.Project) != projectHash(before) {
		t.Fatal("分句未整组撤销", err)
	}
	if _, err := s.ApplyCaptionLayout(context.Background(), preview.ID); err != nil || projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("Undo后自动重放旧预览", err)
	}
}

func TestCaptionLayoutPreservesManualGroupsAndRejectsStaleOrLocked(t *testing.T) {
	s, input, _ := captionLayoutFixture(t)
	p := s.GetBootstrap().Project
	var caption editor.Clip
	for _, clip := range p.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.WordIDs[0] == "w1" {
			caption = clip
			break
		}
	}
	caption.Text = "人工文字不能丢"
	caption.TextStyle.Color = "#ffddaa"
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "manual-caption-protection", Commands: []editor.Command{{Type: "update_clip", Clip: &caption}}}); err != nil {
		t.Fatal(err)
	}
	input.ProjectRevision = s.GetBootstrap().Project.Revision
	preview, err := s.PreviewCaptionLayout(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ProtectedCount != 1 || len(preview.Warnings) == 0 {
		t.Fatal("未显示手改分句保护")
	}
	protected := false
	for _, clip := range preview.Captions {
		if clip.ID == caption.ID && clip.Text == caption.Text && clip.TextStyle.Color == caption.TextStyle.Color {
			protected = true
		}
	}
	if !protected {
		t.Fatal("阅读重排丢人工文字/样式")
	}
	p = s.GetBootstrap().Project
	track := p.Tracks[0]
	track.Locked = true
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "lock-layout-target", Commands: []editor.Command{{Type: "update_track", Track: &track}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyCaptionLayout(context.Background(), preview.ID); err == nil {
		t.Fatal("旧预览覆盖锁轨")
	}
	input.ProjectRevision = s.GetBootstrap().Project.Revision
	if _, err := s.PreviewCaptionLayout(context.Background(), input); err == nil {
		t.Fatal("锁轨仍改字幕组")
	}
}

func TestCaptionLayoutSourceTamperReopenAndPublicFieldsAreRejected(t *testing.T) {
	s, input, path := captionLayoutFixture(t)
	preview, err := s.PreviewCaptionLayout(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	before := s.GetBootstrap().Project
	if _, err := s.ApplyCaptionLayout(context.Background(), preview.ID); err == nil {
		t.Fatal("源变化还可排版")
	}
	if projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("失败部分更新工程")
	}
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: before.Revision, IdempotencyKey: "forge-caption-layout", Commands: []editor.Command{{Type: "update_project", Name: "不应生效", CaptionLayout: &editor.CaptionLayout{Mode: "readable"}}}}); err == nil {
		t.Fatal("普通编辑可夹带排版字段")
	}
	s2, input2, _ := captionLayoutFixture(t)
	preview2, err := s2.PreviewCaptionLayout(context.Background(), input2)
	if err != nil {
		t.Fatal(err)
	}
	savePath := filepath.Join(t.TempDir(), "same-id-caption-project.aividcut")
	if _, err := s2.SaveProjectTo(savePath); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.OpenProjectPath(savePath); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.ApplyCaptionLayout(context.Background(), preview2.ID); err == nil {
		t.Fatal("同ID重开工程还可用旧预览")
	}
}

func TestCaptionLayoutMCPReadOnlyPreviewAndScopedApply(t *testing.T) {
	s, input, _ := captionLayoutFixture(t)
	readOnly := mcpApplicationClient(t, s, false)
	preview := callApplicationMCP(t, readOnly, "aividcut_preview_caption_layout", input, false)
	if preview["afterCount"] != float64(2) {
		t.Fatal("MCP没有真实阅读分句预览")
	}
	callApplicationMCP(t, readOnly, "aividcut_apply_caption_layout", map[string]any{"id": preview["id"]}, true)
	if _, err := s.StopMCP(); err != nil {
		t.Fatal(err)
	}
	writable := mcpApplicationClient(t, s, true)
	callApplicationMCP(t, writable, "aividcut_apply_caption_layout", map[string]any{"id": preview["id"]}, false)
	if s.GetBootstrap().Project.Transcripts[0].CaptionLayout == nil {
		t.Fatal("MCP只是假应用")
	}
}

func TestCaptionLayoutExplicitNewTrackDoesNotSilentlyReuseOldTarget(t *testing.T) {
	s, input, _ := captionLayoutFixture(t)
	before := s.GetBootstrap().Project
	oldTrack := before.Transcripts[0].CaptionTrackID
	input.CreateNewTrack = true
	preview, err := s.PreviewCaptionLayout(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if preview.AfterCount != 2 {
		t.Fatal("新建目标轨不应把未手改字幕误判为人工组，须执行明确的阅读分句", preview.AfterCount)
	}
	if len(s.GetBootstrap().Project.Tracks) != len(before.Tracks) {
		t.Fatal("新轨预览先行修改工程")
	}
	changed, err := s.ApplyCaptionLayout(context.Background(), preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	newTrack := changed.Project.Transcripts[0].CaptionTrackID
	if newTrack == oldTrack || len(changed.Project.Tracks) != len(before.Tracks)+1 {
		t.Fatal("新建选项静默沿用原轨")
	}
	for _, clip := range changed.Project.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.TranscriptID == input.TranscriptID && clip.TrackID != newTrack {
			t.Fatal("字幕未进入已明确新建的目标轨")
		}
	}
	if _, err := s.Undo(); err != nil || projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("新轨和分句没有整组撤销", err)
	}
	input.TrackID = oldTrack
	if _, err := s.PreviewCaptionLayout(context.Background(), input); err == nil {
		t.Fatal("新建和已有目标同时指定未拒绝")
	}
}

func TestCaptionLayoutPreviewReportsProtectedLegacyManualPlacement(t *testing.T) {
	s, input, _ := captionLayoutFixture(t)
	p := s.GetBootstrap().Project
	track := editor.Track{ID: "legacy-manual-caption-track", Name: "旧版手工布局", Kind: "caption"}
	updated, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "legacy-caption-track", Commands: []editor.Command{{Type: "add_track", Track: &track}}})
	if err != nil {
		t.Fatal(err)
	}
	p = updated.Project
	var manualID string
	for i := range p.Clips {
		if p.Clips[i].CaptionSource != nil && p.Clips[i].CaptionSource.WordIDs[0] == "w1" {
			p.Clips[i].TrackID = track.ID
			p.Clips[i].Transform.Y = 12
			p.Clips[i].CaptionSource.StyleOverride = false
			manualID = p.Clips[i].ID
			break
		}
	}
	legacy, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal("旧版没有style标志的布局应可读", err)
	}
	s.mu.Lock()
	s.engine = legacy
	s.mu.Unlock()
	input.ProjectRevision = p.Revision
	preview, err := s.PreviewCaptionLayout(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	preserved := false
	for _, clip := range preview.Captions {
		if clip.ID == manualID && clip.TrackID == track.ID && clip.Transform.Y == 12 && clip.CaptionSource.StyleOverride {
			preserved = true
		}
	}
	if !preserved || preview.ProtectedCount != 1 || len(preview.Warnings) == 0 {
		t.Fatal("核心实际保护的旧布局未在影响中说明", preview.ProtectedCount, preview.Warnings)
	}
}
