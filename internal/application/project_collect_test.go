package application

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/jobs"
	"aividcut/internal/media"
	"aividcut/internal/projectstore"
)

func awaitCollection(t *testing.T, s *Service, id string) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, job := range s.ListJobs() {
			if job.ID == id && job.Status != "queued" && job.Status != "running" {
				return job
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("收集任务没有终态")
	return jobs.Job{}
}

func TestCollectProjectSnapshotScopeAndOriginalState(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.ImportPaths(context.Background(), []string{fixtureImage(t)})
	if err != nil {
		t.Fatal(err)
	}
	before := editor.CloneProject(b.Project)
	inspection, err := s.InspectProjectCollection(context.Background(), versionScope(b))
	if err != nil || inspection.AssetCount != 1 || inspection.SourceBytes < 1 {
		t.Fatal("素材核对结果不实", inspection, err)
	}
	release := make(chan struct{})
	entered := make(chan struct{})
	_, err = s.jobs.Start("block", "固定排队时序", func(ctx context.Context, _ func(float64, string)) (string, error) {
		close(entered)
		select {
		case <-release:
			return "", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	input := CollectProjectInput{ProjectScope: versionScope(b), ParentDirectory: t.TempDir(), FolderName: "副本"}
	job, err := s.CollectProject(input)
	if err != nil {
		t.Fatal(err)
	}
	if after := s.GetBootstrap(); !reflect.DeepEqual(after.Project, b.Project) || after.Dirty != b.Dirty || after.CanUndo != b.CanUndo {
		t.Fatal("启动收集改变当前工程")
	}
	edited, err := s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "during-collect", Commands: []editor.Command{{Type: "update_project", Name: "后续编辑"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectProject(input); err == nil {
		t.Fatal("旧修订仍能启动任务")
	}
	close(release)
	result := awaitCollection(t, s, job.ID)
	if result.Status != "succeeded" {
		t.Fatal(result)
	}
	portable, err := projectstore.Load(result.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if portable.Revision != before.Revision || portable.Name != before.Name || s.GetBootstrap().Project.Name != edited.Project.Name {
		t.Fatal("排队快照被后续编辑替换")
	}
	if _, err := os.Stat(before.Assets[0].Path); err != nil {
		t.Fatal("原媒体被修改")
	}
	path := filepath.Join(t.TempDir(), "same.aividcut")
	if err := projectstore.Save(path, edited.Project); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	input.ProjectScope = versionScope(edited)
	input.FolderName = "旧实例"
	if _, err := s.CollectProject(input); err == nil {
		t.Fatal("同ID旧实例请求未拒绝")
	}
}
