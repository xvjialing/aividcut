package application

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"testing"

	"aividcut/internal/generationjobs"
)

func TestGenerationHistoryArchiveLeavesProjectMediaAndCredentialsUntouched(t *testing.T) {
	store, control := &appSecretStore{values: map[string][]byte{}}, &appProviderControl{}
	s := configuredGenerationService(t, t.TempDir(), store, control)
	connection := appSaveConnection(t, s, "", "fixture-only-key")
	in := appSubmit("history-archive", connection.ID, 2)
	in.Estimate = &generationjobs.EstimateInput{Currency: "CNY", Unit: "image", UnitPrice: "0.30"}
	if _, err := s.SubmitGeneration(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	job := appAwaitJob(t, s, in.Request.ID, generationjobs.Succeeded)
	if _, err := s.ImportGenerationResult(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	before := s.GetBootstrap().Project
	reads, posts := store.readCount(), control.posts.Load()
	media, err := os.ReadFile(job.Assets[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	appEventually(t, func() error { _, err := s.ArchiveGenerationJob(context.Background(), job.ID); return err })
	jobs, err := s.ListGenerationJobs()
	if err != nil || len(jobs) != 0 {
		t.Fatal("归档仍在默认列表", err)
	}
	page, err := s.ListArchivedGenerationJobs(context.Background(), generationjobs.ArchivePageInput{})
	if err != nil || page.Total != 1 || page.Items[0].Job.Estimate.Amount != "0.60" {
		t.Fatal("归档费用/分页不符", err)
	}
	if !reflect.DeepEqual(s.GetBootstrap().Project, before) {
		t.Fatal("归档修改了当前工程")
	}
	got, err := s.RestoreArchivedGenerationJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Imported || got.Estimate.Amount != "0.60" {
		t.Fatal("还原没有按当前工程复核导入状态")
	}
	after, err := os.ReadFile(job.Assets[0].Path)
	if err != nil || !bytes.Equal(media, after) {
		t.Fatal("归档移动/改变媒体", err)
	}
	if store.readCount() != reads || control.posts.Load() != posts || posts != 1 {
		t.Fatal("归档管理读取凭据或重新生成")
	}
}
