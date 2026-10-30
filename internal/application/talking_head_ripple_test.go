package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/workflows"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTalkingHeadAllTracksMCPAndUnselectedSourceProof(t *testing.T) {
	s, _ := transcriptCutApplication(t)
	defer s.Close()
	ctx := context.Background()
	p := s.GetBootstrap().Project
	path := filepath.Join(t.TempDir(), "unselected.bin")
	if err := os.WriteFile(path, []byte("registered unselected media"), 0600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := media.LocalFileFingerprint(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	a := editor.Asset{ID: "unselected-source", Name: "未选视频", Kind: "video", Path: path, DurationUs: 4_000_000, Fingerprint: fingerprint}
	p.Assets = append(p.Assets, a)
	p.Tracks = append(p.Tracks, editor.Track{ID: "unselected-track", Name: "未选画面", Kind: "video"})
	c := editor.DefaultClip("unselected-clip", "unselected-track", "video")
	c.AssetID, c.DurationFrames = a.ID, 120
	p.Clips = append(p.Clips, c)
	s.engine, err = editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	input := workflows.Input{WorkflowID: "talking-head", Selection: []string{"speech-source-clip"}, Parameters: map[string]any{"gapThresholdMs": 100, "keepGapMs": 100, "edgeProtectionMs": 0, "sentencePauseMs": 0, "rippleScope": "all_tracks"}}
	before := s.GetBootstrap().Project
	read := mcpApplicationClient(t, s, false)
	args := map[string]any{"input": input}
	preview := callApplicationMCP(t, read, "aividcut_preview_workflow", args, false)
	if preview["speechRippleSummary"] == nil || !reflect.DeepEqual(before, s.GetBootstrap().Project) {
		t.Fatal("只读MCP没有全轨统计或修改工程")
	}
	callApplicationMCP(t, read, "aividcut_apply_workflow", map[string]any{"id": preview["id"]}, true)
	s.StopMCP()
	write := mcpApplicationClient(t, s, true)
	callApplicationMCP(t, write, "aividcut_apply_workflow", map[string]any{"id": preview["id"]}, false)
	if editor.DurationFrames(s.GetBootstrap().Project) >= editor.DurationFrames(p) {
		t.Fatal("授权MCP没有闭合未选画面")
	}
	if _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	undone := s.GetBootstrap()
	callApplicationMCP(t, write, "aividcut_apply_workflow", map[string]any{"id": preview["id"]}, false)
	if s.GetBootstrap().Project.Revision != undone.Project.Revision {
		t.Fatal("撤销后旧计划再次删除")
	}
	next, err := s.PreviewWorkflow(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed unselected media"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyWorkflowPreview(ctx, next.ID); err == nil {
		t.Fatal("全轨裁切未核对未选源的变化")
	}
	if !reflect.DeepEqual(undone.Project, s.GetBootstrap().Project) {
		t.Fatal("来源拒绝后部分应用")
	}
}
