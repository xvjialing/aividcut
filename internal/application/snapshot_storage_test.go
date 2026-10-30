package application

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/projectrecovery"
	"fmt"
)

func storageList(t *testing.T, s *Service, kind, view string) SnapshotStorageInput {
	t.Helper()
	return SnapshotStorageInput{ProjectScope: versionScope(s.GetBootstrap()), Kind: kind, View: view, Limit: 50}
}

func TestSnapshotVersionTrashScopeAndPermanentConfirmation(t *testing.T) {
	s := versionService(t, t.TempDir())
	defer s.Close()
	before := s.GetBootstrap()
	v, err := s.CreateProjectVersion(CreateProjectVersionInput{ProjectVersionScope: versionScope(before), Name: "可还原的版本"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSnapshotStorage(storageList(t, s, "version", "active"))
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	input := SnapshotStorageChange{ProjectScope: versionScope(before), Token: page.Items[0].Token, Action: "trash"}
	if _, err := s.ChangeSnapshotStorage(input); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ChangeSnapshotStorage(input); err != nil || !r.AlreadyApplied {
		t.Fatal("重复操作不应重放", r, err)
	}
	trash, err := s.ListSnapshotStorage(storageList(t, s, "version", "trash"))
	if err != nil || trash.Total != 1 || trash.Items[0].ID != v.ID {
		t.Fatal(trash, err)
	}
	if after := s.GetBootstrap(); !reflect.DeepEqual(after.Project, before.Project) || after.Dirty != before.Dirty || after.CanUndo != before.CanUndo {
		t.Fatal("资料操作修改了活动工程")
	}
	input.Token = trash.Items[0].Token
	input.Action = "purge"
	if _, err := s.ChangeSnapshotStorage(input); err == nil {
		t.Fatal("未确认永久删除仍执行")
	}
	input.Action = "restore"
	if _, err := s.ChangeSnapshotStorage(input); err != nil {
		t.Fatal(err)
	}
	versions, err := s.ListProjectVersions(ListProjectVersionsInput{ProjectVersionScope: versionScope(before), Limit: 20})
	if err != nil || versions.Total != 1 {
		t.Fatal("未回到原列表", versions, err)
	}
	page, err = s.ListSnapshotStorage(storageList(t, s, "version", "active"))
	if err != nil {
		t.Fatal(err)
	}
	input.Token = page.Items[0].Token
	input.Action = "trash"
	if _, err := s.ChangeSnapshotStorage(input); err != nil {
		t.Fatal(err)
	}
	trash, err = s.ListSnapshotStorage(storageList(t, s, "version", "trash"))
	if err != nil {
		t.Fatal(err)
	}
	input.Token = trash.Items[0].Token
	input.Action = "purge"
	input.ConfirmPermanent = true
	if _, err := s.ChangeSnapshotStorage(input); err != nil {
		t.Fatal(err)
	}
	trash, err = s.ListSnapshotStorage(storageList(t, s, "version", "trash"))
	if err != nil || trash.Total != 0 {
		t.Fatal(trash, err)
	}
}

func TestRecoveryTrashRestoresDamagedRecordsAndProtectsActivePoint(t *testing.T) {
	dir := t.TempDir()
	first := recoveryService(t, dir)
	renameRecoveryProject(t, first, "待整理工作")
	if _, err := first.CheckpointRecovery(); err != nil {
		t.Fatal(err)
	}
	first.Close()
	s := recoveryService(t, dir)
	before := s.GetBootstrap()
	page, err := s.ListSnapshotStorage(storageList(t, s, "recovery", "active"))
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	input := SnapshotStorageChange{ProjectScope: versionScope(before), Token: page.Items[0].Token, Action: "trash"}
	if _, err := s.ChangeSnapshotStorage(input); err != nil {
		t.Fatal(err)
	}
	if s.GetRecoveryStatus().Pending != 0 {
		t.Fatal("回收资料仍触发恢复提示")
	}
	trash, err := s.ListSnapshotStorage(storageList(t, s, "recovery", "trash"))
	if err != nil || trash.Total != 1 {
		t.Fatal(trash, err)
	}
	input.Token = trash.Items[0].Token
	input.Action = "restore"
	if _, err := s.ChangeSnapshotStorage(input); err != nil {
		t.Fatal(err)
	}
	if s.GetRecoveryStatus().Pending != 1 {
		t.Fatal("还原没有回到恢复中心")
	}
	current := renameRecoveryProject(t, s, "当前自动保护")
	if _, err := s.CheckpointRecovery(); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListSnapshotStorage(storageList(t, s, "recovery", "active"))
	if err != nil {
		t.Fatal(err)
	}
	locked := false
	for _, item := range page.Items {
		if item.Locked {
			locked = true
			if _, err := s.ChangeSnapshotStorage(SnapshotStorageChange{ProjectScope: versionScope(current), Token: item.Token, Action: "trash"}); err == nil {
				t.Fatal("当前保护点可被清理")
			}
		}
	}
	if !locked {
		t.Fatal("没有标记当前保护点")
	}
	// 损坏内容没有被假装为可恢复工程，但仍能进入可还原回收站。
	id := editor.NewID("recovery")
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListSnapshotStorage(storageList(t, s, "recovery", "active"))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == id {
			if item.Readable {
				t.Fatal("损坏内容被标为可读")
			}
			if _, err := s.ChangeSnapshotStorage(SnapshotStorageChange{ProjectScope: versionScope(current), Token: item.Token, Action: "trash"}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSnapshotStorageRejectsOldScopeAndProtectsOpenedVersionFile(t *testing.T) {
	dir := t.TempDir()
	s := versionService(t, dir)
	defer s.Close()
	b := s.GetBootstrap()
	v, err := s.CreateProjectVersion(CreateProjectVersionInput{ProjectVersionScope: versionScope(b), Name: "版本"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSnapshotStorage(storageList(t, s, "version", "active"))
	if err != nil {
		t.Fatal(err)
	}
	old := SnapshotStorageChange{ProjectScope: versionScope(b), Token: page.Items[0].Token, Action: "trash"}
	renameRecoveryProject(t, s, "新修订")
	if _, err := s.ChangeSnapshotStorage(old); err == nil {
		t.Fatal("旧修订凭据仍执行")
	}
	// 通过路径直接打开版本文件时，清理不能破坏当前文件。
	var versionPath string
	entries, err := filepath.Glob(filepath.Join(dir, "*", v.ID, "project.aividcut"))
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	versionPath = entries[0]
	opened, err := s.OpenProjectPath(versionPath)
	if err != nil {
		t.Fatal(err)
	}
	page, err = s.ListSnapshotStorage(storageList(t, s, "version", "active"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeSnapshotStorage(SnapshotStorageChange{ProjectScope: versionScope(opened), Token: page.Items[0].Token, Action: "trash"}); err == nil {
		t.Fatal("当前打开的版本文件被移走")
	}
	if _, err := os.Stat(versionPath); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	token := s.storageTokens[page.Items[0].Token]
	token.created = time.Now().Add(-11 * time.Minute)
	s.mu.Unlock()
	if _, err := s.ChangeSnapshotStorage(SnapshotStorageChange{ProjectScope: versionScope(opened), Token: page.Items[0].Token, Action: "trash"}); err == nil {
		t.Fatal("超时身份未拒绝")
	}
}

func TestVersionTrashFreesFullActiveCapacityIncludingDamagedSlots(t *testing.T) {
	dir := t.TempDir()
	s := versionService(t, dir)
	defer s.Close()
	b := s.GetBootstrap()
	v, err := s.CreateProjectVersion(CreateProjectVersionInput{ProjectVersionScope: versionScope(b), Name: "保留"})
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*", v.ID))
	if len(files) != 1 {
		t.Fatal(files)
	}
	folder := filepath.Dir(files[0])
	for i := 0; i < 199; i++ {
		if err := os.Mkdir(filepath.Join(folder, fmt.Sprintf("version-%024x", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateProjectVersion(CreateProjectVersionInput{ProjectVersionScope: versionScope(b), Name: "到上限"}); err == nil {
		t.Fatal("容量不实")
	}
	page, err := s.ListSnapshotStorage(storageList(t, s, "version", "active"))
	if err != nil {
		t.Fatal(err)
	}
	var damagedToken string
	for _, item := range page.Items {
		if !item.Readable {
			damagedToken = item.Token
			break
		}
	}
	if damagedToken == "" {
		t.Fatal("缺失摘要槽位不可管理")
	}
	if _, err := s.ChangeSnapshotStorage(SnapshotStorageChange{ProjectScope: versionScope(b), Token: damagedToken, Action: "trash"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProjectVersion(CreateProjectVersionInput{ProjectVersionScope: versionScope(b), Name: "释放后可写"}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryTrashFreesByteBudgetWithoutDeletingRawBytes(t *testing.T) {
	dir := t.TempDir()
	s := recoveryService(t, dir)
	b := s.GetBootstrap()
	id := editor.NewID("recovery")
	if _, err := s.recovery.store.Write(id, b.Project, "", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".json")
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(projectrecovery.MaxTotalBytes); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	current := renameRecoveryProject(t, s, "当前待保护")
	if _, err := s.CheckpointRecovery(); err == nil {
		t.Fatal("字节预算没有阻止新点")
	}
	page, err := s.ListSnapshotStorage(storageList(t, s, "recovery", "active"))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Readable {
		t.Fatal("超限损坏内容被错误解释", page)
	}
	if _, err := s.ChangeSnapshotStorage(SnapshotStorageChange{ProjectScope: versionScope(current), Token: page.Items[0].Token, Action: "trash"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".trash", id+".json"))
	if err != nil || info.Size() != projectrecovery.MaxTotalBytes {
		t.Fatal("回收未保留原文件", err)
	}
	if _, err := s.CheckpointRecovery(); err != nil {
		t.Fatal("释放活动预算后仍不能保护", err)
	}
}
