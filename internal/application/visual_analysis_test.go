package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/localagent"
	"aividcut/internal/media"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func visualCandidateFixture(t *testing.T) (*Service, editor.Asset, MCPAction) {
	t.Helper()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	asset := sourceFrameFixture(t, s)
	_, err = s.StartMCP(MCPStartInput{ProjectID: s.GetBootstrap().Project.ID, AllowSourceFrames: true})
	if err != nil {
		t.Fatal(err)
	}
	config, err := s.GetMCPConnection()
	if err != nil {
		t.Fatal(err)
	}
	input := localagent.ExecutionInput{MCP: localagent.ExecutionMCP{URL: config.URL, Token: config.HTTPHeaders["Authorization"][7:]}}
	session, err := fixtureExecutionClient(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	frames := callApplicationMCP(t, session, "aividcut_read_source_frames", map[string]any{"assetId": asset.ID, "positionsUs": []int{0}}, false)
	frameID := frames["frames"].([]any)[0].(map[string]any)["id"].(string)
	args := map[string]any{"id": "visual-candidate", "assetId": asset.ID, "notes": []any{map[string]any{"kind": "observation", "text": "本地夹具观察文字，需人工核对", "tags": []string{"相机"}, "evidenceIds": []string{frameID}}}}
	callApplicationMCP(t, session, "aividcut_prepare_visual_analysis", map[string]any{"id": "forged", "assetId": asset.ID, "notes": []any{map[string]any{"kind": "observation", "text": "错误证据", "evidenceIds": []string{"forged"}}}}, true)
	callApplicationMCP(t, session, "aividcut_prepare_visual_analysis", args, false)
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": editor.EditRequest{BaseRevision: 0, IdempotencyKey: "bypass", Commands: []editor.Command{{Type: "accept_visual_analysis"}}}}, true)
	actions := s.ListMCPRequests()
	if len(actions) != 1 || actions[0].Analysis == nil || !actions[0].CanApprove || s.GetBootstrap().Dirty {
		t.Fatal("准备分析已改工程或缺少候选")
	}
	return s, asset, actions[0]
}
func TestVisualAnalysisHumanAcceptancePersistenceSearchUndoAndNoReplay(t *testing.T) {
	s, _, action := visualCandidateFixture(t)
	ctx := context.Background()
	before := s.GetBootstrap()
	input := AcceptVisualAnalysisInput{ID: action.ID, SourceEpoch: before.SourceEpoch, ProjectID: before.Project.ID, ProjectRevision: before.Project.Revision}
	if _, err := s.ApproveMCPRequest(ctx, action.ID, MCPActionConfirmation{}); err == nil {
		t.Fatal("分析混走收费确认入口")
	}
	accepted, err := s.AcceptMCPVisualAnalysis(ctx, input)
	if err != nil || len(accepted.Project.VisualAnalyses) != 1 || len(accepted.Project.Clips) != len(before.Project.Clips) {
		t.Fatalf("未接纳: %v", err)
	}
	repeated, err := s.AcceptMCPVisualAnalysis(ctx, input)
	if err != nil || repeated.Project.Revision != accepted.Project.Revision {
		t.Fatal("重复确认产生重复写入")
	}
	search, err := s.SearchSourceContent(ctx, SourceContentQuery{SourceEpoch: accepted.SourceEpoch, ProjectID: accepted.Project.ID, ProjectRevision: accepted.Project.Revision, Text: "相机", Origin: "visual"})
	if err != nil || search.Total != 1 || search.Hits[0].Kind != "visual" || search.Hits[0].StartUs != 0 || search.Hits[0].EndUs != 0 {
		t.Fatalf("视觉标签检索失去离散证据: %+v %v", search, err)
	}
	undone, err := s.Undo()
	if err != nil || len(undone.Project.VisualAnalyses) != 0 {
		t.Fatal("Undo未移除记录")
	}
	again, err := s.AcceptMCPVisualAnalysis(ctx, input)
	if err != nil || len(again.Project.VisualAnalyses) != 0 {
		t.Fatal("旧确认在Undo后重放")
	}
	if _, err = s.Redo(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "accepted.aividcut")
	if _, err = s.SaveProjectTo(file); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.OpenProjectPath(file)
	if err != nil || loaded.Project.SchemaVersion != editor.SchemaVersion || len(loaded.Project.VisualAnalyses) != 1 {
		t.Fatal("分析未保存重开")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var stored editor.Project
	if json.Unmarshal(data, &stored) != nil || stored.VisualAnalyses[0].Coverage != "sampled_frames" {
		t.Fatal("保存结构不正确")
	}
}
func TestVisualAcceptanceFileFailureCanBeExplicitlyRetried(t *testing.T) {
	s, asset, action := visualCandidateFixture(t)
	ctx := context.Background()
	b := s.GetBootstrap()
	input := AcceptVisualAnalysisInput{ID: action.ID, SourceEpoch: b.SourceEpoch, ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision}
	original, err := os.ReadFile(asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(asset.Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptMCPVisualAnalysis(ctx, input); err == nil || s.GetBootstrap().Project.Revision != b.Project.Revision {
		t.Fatal("变更源被接纳")
	}
	if err = os.WriteFile(asset.Path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptMCPVisualAnalysis(ctx, input); err != nil {
		t.Fatal("修复后显式重试不可用", err)
	}
}
