package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aividcut/internal/generationjobs"
)

func TestGenerationSyncExplicitEmptyAndUnchangedReply(t *testing.T) {
	s := configuredGenerationService(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, &appProviderControl{})
	first, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{})
	if err != nil || !first.Changed || first.Jobs == nil || len(*first.Jobs) != 0 {
		t.Fatal("首次空列表未显式返回", first, err)
	}
	data, _ := json.Marshal(first)
	if !strings.Contains(string(data), `"jobs":[]`) {
		t.Fatal("空列表语义丢失")
	}
	next, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{Fingerprint: first.Fingerprint})
	if err != nil || next.Changed || next.Jobs != nil {
		t.Fatal("未变化仍传完整列表", next, err)
	}
	data, _ = json.Marshal(next)
	if strings.Contains(string(data), `"jobs"`) || len(data) > 128 {
		t.Fatal("未变化回复未保持轻量", len(data))
	}
	if _, err = s.SyncGenerationJobs(context.Background(), GenerationSyncInput{Fingerprint: "invalid"}); err == nil {
		t.Fatal("无效摘要未拒绝")
	}
}

func TestGenerationSyncTracksProjectFilesArchiveAndRestore(t *testing.T) {
	store, control := &appSecretStore{values: map[string][]byte{}}, &appProviderControl{}
	s := configuredGenerationService(t, t.TempDir(), store, control)
	connection := appSaveConnection(t, s, "", "fixture-sync-key")
	initial, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{})
	if err != nil {
		t.Fatal(err)
	}
	in := appSubmit("sync-original", connection.ID, 1)
	if _, err = s.SubmitGeneration(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	job := appAwaitJob(t, s, in.Request.ID, generationjobs.Succeeded)
	reads, posts := store.readCount(), control.posts.Load()
	before, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{Fingerprint: initial.Fingerprint})
	if err != nil || !before.Changed || before.Jobs == nil || len(*before.Jobs) != 1 {
		t.Fatal("新任务没有完整同步", err)
	}
	unchanged, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{Fingerprint: before.Fingerprint})
	if err != nil || unchanged.Changed {
		t.Fatal("稳定任务反复变化", err)
	}
	mediaPath := job.Assets[0].Path
	backup := filepath.Join(t.TempDir(), "keep.png")
	if err = os.Rename(mediaPath, backup); err != nil {
		t.Fatal(err)
	}
	missing, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{Fingerprint: before.Fingerprint})
	if err != nil || !missing.Changed || (*missing.Jobs)[0].Assets[0].URL != "" {
		t.Fatal("未变化摘要掩盖文件缺失", err)
	}
	if err = os.Rename(backup, mediaPath); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{Fingerprint: missing.Fingerprint})
	if err != nil || !recovered.Changed || (*recovered.Jobs)[0].Assets[0].URL == "" {
		t.Fatal("素材还原没有更新", err)
	}
	if _, err = s.ImportGenerationResult(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	imported, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{Fingerprint: recovered.Fingerprint})
	if err != nil || !imported.Changed || !(*imported.Jobs)[0].Imported {
		t.Fatal("工程导入未同步", err)
	}
	if _, err = s.Undo(); err != nil {
		t.Fatal(err)
	}
	undone, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{Fingerprint: imported.Fingerprint})
	if err != nil || !undone.Changed || (*undone.Jobs)[0].Imported {
		t.Fatal("工程撤销未更新视图", err)
	}
	appEventually(t, func() error { _, e := s.ArchiveGenerationJob(context.Background(), job.ID); return e })
	archived, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{Fingerprint: undone.Fingerprint})
	if err != nil || !archived.Changed || archived.Jobs == nil || len(*archived.Jobs) != 0 {
		t.Fatal("归档未明确清空", err)
	}
	if _, err = s.RestoreArchivedGenerationJob(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := s.SyncGenerationJobs(context.Background(), GenerationSyncInput{Fingerprint: archived.Fingerprint})
	if err != nil || !restored.Changed || len(*restored.Jobs) != 1 {
		t.Fatal("还原未同步", err)
	}
	if store.readCount() != reads || control.posts.Load() != posts || posts != 1 {
		t.Fatal("读取任务触发凭据或新生成")
	}
}
