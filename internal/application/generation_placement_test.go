package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/generationjobs"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func placementFixture(t *testing.T) (*Service, *appSecretStore, *appProviderControl, GenerationJob) {
	t.Helper()
	store := &appSecretStore{values: map[string][]byte{}}
	control := &appProviderControl{}
	s := configuredGenerationService(t, t.TempDir(), store, control)
	profile := appSaveConnection(t, s, "", "fixture-only")
	input := appSubmit("placement-image", profile.ID, 1)
	if _, err := s.SubmitGeneration(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	return s, store, control, appAwaitJob(t, s, input.Request.ID, generationjobs.Succeeded)
}
func placementInput(s *Service, job GenerationJob) GenerationPlacementInput {
	b := s.GetBootstrap()
	return GenerationPlacementInput{JobID: job.ID, CandidateID: job.Assets[0].ID, ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision, SourceEpoch: b.SourceEpoch, StartFrame: 7, DurationFrames: 30, AudioMode: "muted"}
}

func TestGenerationPlacementAtomicUndoNoReplayAndReopen(t *testing.T) {
	s, store, control, job := placementFixture(t)
	before := s.GetBootstrap()
	reads, posts := store.readCount(), control.posts.Load()
	input := placementInput(s, job)
	view, err := s.PreviewGenerationPlacement(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !view.ImportRequired || !view.NewTrack || view.Clip.StartFrame != 7 || view.Clip.DurationFrames != 30 || view.Asset.ID != job.Assets[0].ID {
		t.Fatal("预览后果错误")
	}
	if strings.Contains(strings.Join(view.Warnings, "\n"), "移除0帧") || !strings.Contains(strings.Join(view.Warnings, "\n"), "工程总长增加37帧") {
		t.Fatal("新增总长提示误称为删除")
	}
	if s.GetBootstrap().Project.Revision != before.Project.Revision || len(s.GetBootstrap().Project.Assets) != len(before.Project.Assets) || store.readCount() != reads || control.posts.Load() != posts {
		t.Fatal("预览导入/编辑工程或调用凭据/模型")
	}
	clipID := view.Clip.ID
	view.Clip.ID = "caller-modified"
	after, err := s.ApplyGenerationPlacement(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Project.Assets) != len(before.Project.Assets)+1 || len(after.Project.Clips) != len(before.Project.Clips)+1 || len(after.Project.Tracks) != len(before.Project.Tracks)+1 || after.Project.Clips[0].ID != clipID {
		t.Fatal("素材/轨道/片段未整组加入或返回值可改写计划")
	}
	again, err := s.ApplyGenerationPlacement(context.Background(), view.ID)
	if err != nil || again.Project.Revision != after.Project.Revision {
		t.Fatal("重复应用产生新编辑", err)
	}
	undone, err := s.Undo()
	if err != nil || len(undone.Project.Assets) != len(before.Project.Assets) || !reflect.DeepEqual(undone.Project.Clips, before.Project.Clips) || !reflect.DeepEqual(undone.Project.Tracks, before.Project.Tracks) {
		t.Fatal("一次撤销不完整", err)
	}
	again, err = s.ApplyGenerationPlacement(context.Background(), view.ID)
	if err != nil || again.Project.Revision != undone.Project.Revision || len(again.Project.Clips) != len(before.Project.Clips) {
		t.Fatal("撤销后旧预览重放", err)
	}
	redone, err := s.Redo()
	if err != nil || redone.Project.Clips[0].ID != clipID {
		t.Fatal("重做未恢复片段", err)
	}
	path := filepath.Join(t.TempDir(), "placed.aividcut")
	if _, err = s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "http://127.0.0.1") || strings.Contains(string(data), "placement-candidate-") {
		t.Fatal("持久工程保存了预览能力")
	}
	if _, err = s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyGenerationPlacement(context.Background(), view.ID); err == nil {
		t.Fatal("同ID重开复用了旧应用")
	}
	b := s.GetBootstrap()
	if len(b.Project.Clips) != 1 || b.Project.Clips[0].ID != clipID {
		t.Fatal("保存重开丢片段")
	}
}

func TestGenerationPlacementExplicitTracksAndSourceFailures(t *testing.T) {
	s, _, _, job := placementFixture(t)
	input := placementInput(s, job)
	input.TrackID = "track-video-1"
	view, err := s.PreviewGenerationPlacement(context.Background(), input)
	if err != nil || view.NewTrack {
		t.Fatal("未使用显式视频轨", err)
	}
	after, err := s.ApplyGenerationPlacement(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	next := placementInput(s, job)
	next.TrackID = input.TrackID
	if _, err = s.PreviewGenerationPlacement(context.Background(), next); err == nil {
		t.Fatal("同轨重叠没有拒绝")
	}
	next.StartFrame = 50
	second, err := s.PreviewGenerationPlacement(context.Background(), next)
	if err != nil || second.ImportRequired {
		t.Fatal("已导入素材未复用", err)
	}
	if len(s.GetBootstrap().Project.Clips) != len(after.Project.Clips) {
		t.Fatal("第二预览修改工程")
	}
	for _, flags := range []struct{ locked, hidden bool }{{true, false}, {false, true}} {
		b := s.GetBootstrap()
		target := b.Project.Tracks[2]
		for _, track := range b.Project.Tracks {
			if track.ID == input.TrackID {
				target = track
			}
		}
		target.Locked, target.Hidden = flags.locked, flags.hidden
		if _, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: editor.NewID("target-flags"), Commands: []editor.Command{{Type: "update_track", Track: &target}}}); err != nil {
			t.Fatal(err)
		}
		next = placementInput(s, job)
		next.TrackID = input.TrackID
		next.StartFrame = 50
		if _, err = s.PreviewGenerationPlacement(context.Background(), next); err == nil {
			t.Fatal("锁定/隐藏目标轨没有拒绝")
		}
	}
	raw, err := os.ReadFile(job.Assets[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(job.Assets[0].Path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	initial := s.GetBootstrap().Project
	if _, err = s.ApplyGenerationPlacement(context.Background(), second.ID); err == nil {
		t.Fatal("应用时未重新核对候选")
	}
	if !reflect.DeepEqual(s.GetBootstrap().Project, initial) {
		t.Fatal("失败写入部分结果")
	}
	if err = os.WriteFile(job.Assets[0].Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationPlacementInputAndPreviewScope(t *testing.T) {
	s, _, _, job := placementFixture(t)
	input := placementInput(s, job)
	cases := []GenerationPlacementInput{input, input, input, input, input, input}
	cases[0].DurationFrames = 0
	cases[1].StartFrame = -1
	cases[2].AutoDuration = true
	cases[2].DurationFrames = 0
	cases[3].SourceInUs = 1
	cases[4].AudioMode = "embedded"
	cases[5].CandidateID = "not-owned"
	for _, bad := range cases {
		if _, err := s.PreviewGenerationPlacement(context.Background(), bad); err == nil {
			encoded, _ := json.Marshal(bad)
			t.Fatalf("非法参数通过：%s", encoded)
		}
	}
	view, err := s.PreviewGenerationPlacement(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	b := s.GetBootstrap()
	name := "修订变化"
	if _, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "change-project", Commands: []editor.Command{{Type: "update_project", Name: name}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyGenerationPlacement(context.Background(), view.ID); err == nil {
		t.Fatal("旧修订应用通过")
	}
	input = placementInput(s, job)
	input.SourceEpoch++
	if _, err = s.PreviewGenerationPlacement(context.Background(), input); err == nil {
		t.Fatal("旧实例预览通过")
	}
}
