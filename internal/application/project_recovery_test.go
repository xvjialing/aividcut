package application

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/projectrecovery"
)

func recoveryService(t *testing.T, dir string) *Service {
	t.Helper()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureRecovery(RecoveryOptions{Directory: dir, Interval: time.Hour}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func renameRecoveryProject(t *testing.T, s *Service, name string) Bootstrap {
	t.Helper()
	b := s.GetBootstrap()
	result, err := s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: editor.NewID("rename"), Commands: []editor.Command{{Type: "update_project", Name: name}}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type recoveryBlockingStore struct {
	recoveryStorage
	entered, release chan struct{}
	once             sync.Once
	after            bool
}

func (s *recoveryBlockingStore) Write(id string, p editor.Project, path string, before bool) (projectrecovery.Summary, error) {
	wait := func() { s.once.Do(func() { close(s.entered); <-s.release }) }
	if !s.after {
		wait()
	}
	v, err := s.recoveryStorage.Write(id, p, path, before)
	if s.after {
		wait()
	}
	return v, err
}

type recoveryFailStore struct{ recoveryStorage }

func (s recoveryFailStore) Write(string, editor.Project, string, bool) (projectrecovery.Summary, error) {
	return projectrecovery.Summary{}, errors.New("模拟磁盘写入失败")
}

func TestRecoverySaveAndSameIDOpenCannotReviveInflightSnapshot(t *testing.T) {
	for _, after := range []bool{false, true} {
		for _, action := range []string{"save", "open"} {
			t.Run(action+map[bool]string{false: "-before", true: "-after"}[after], func(t *testing.T) {
				s := recoveryService(t, t.TempDir())
				path := filepath.Join(t.TempDir(), "original.aividcut")
				if _, err := s.SaveProjectTo(path); err != nil {
					t.Fatal(err)
				}
				renameRecoveryProject(t, s, "旧未保存点")
				wrapper := &recoveryBlockingStore{recoveryStorage: s.recovery.store, entered: make(chan struct{}), release: make(chan struct{}), after: after}
				s.recovery.store = wrapper
				done := make(chan error, 1)
				go func() { _, err := s.CheckpointRecovery(); done <- err }()
				select {
				case <-wrapper.entered:
				case <-time.After(time.Second):
					t.Fatal("写入没有进入受控阶段")
				}
				// 周期写盘期间仍允许提交编辑；保存/切换则退役同一个旧写入身份。
				renameRecoveryProject(t, s, "最新编辑")
				if action == "save" {
					if _, err := s.SaveProjectTo(path); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := s.OpenProjectPath(path); err != nil {
						t.Fatal(err)
					}
				}
				close(wrapper.release)
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				page, err := s.recovery.store.List("", 0, 20)
				if err != nil || page.Total != 0 {
					t.Fatal("旧写入在保存或同ID重开后复活", page, err)
				}
			})
		}
	}
}

func TestRecoveryCloseCaptureDiscardAndUnchangedCoalescing(t *testing.T) {
	dir := t.TempDir()
	s := recoveryService(t, dir)
	b := renameRecoveryProject(t, s, "最后状态")
	if _, err := s.CheckpointRecovery(); err != nil {
		t.Fatal(err)
	}
	first, err := s.recovery.store.List("", 0, 20)
	if err != nil || first.Total != 1 {
		t.Fatal(first, err)
	}
	if _, err := s.CheckpointRecovery(); err != nil {
		t.Fatal(err)
	}
	second, _ := s.recovery.store.List("", 0, 20)
	if !first.Records[0].UpdatedAt.Equal(second.Records[0].UpdatedAt) {
		t.Fatal("无变化重复写盘")
	}
	status := s.GetRecoveryStatus()
	if status.Pending != 0 || status.ProtectedRevision != b.Project.Revision {
		t.Fatal(status)
	}
	*status.ProtectedAt = time.Time{}
	if s.GetRecoveryStatus().ProtectedAt.IsZero() {
		t.Fatal("读模型改变了内部时间")
	}
	s.Close()
	next := recoveryService(t, dir)
	if next.GetRecoveryStatus().StartupPending != 1 {
		t.Fatal("重启未发现恢复点")
	}
	page, err := next.ListProjectRecoveries(RecoveryListInput{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	current := renameRecoveryProject(t, next, "恢复前另一个工作")
	session := mcpApplicationClient(t, next, true)
	result, err := next.RestoreProjectRecovery(RestoreRecoveryInput{ProjectScope: versionScope(current), ID: page.Records[0].ID, SHA256: page.Records[0].SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if result.Project.Name != "最后状态" || result.ProjectPath != "" || !result.Dirty || result.CanUndo || result.SourceEpoch == current.SourceEpoch {
		t.Fatal("恢复没有成为新实例未保存工程", result)
	}
	callApplicationMCP(t, session, "aividcut_get_project", map[string]any{}, true)
	if status := next.GetRecoveryStatus(); status.Pending != 1 || status.StartupPending != 0 {
		t.Fatal("恢复前保留不应触发异常启动提示", status)
	}
	save := filepath.Join(t.TempDir(), "recovered.aividcut")
	if _, err := next.SaveProjectTo(save); err != nil {
		t.Fatal(err)
	}
	if status := next.GetRecoveryStatus(); status.Pending != 1 {
		t.Fatal(status)
	}
	thirdDir := t.TempDir()
	third := recoveryService(t, thirdDir)
	renameRecoveryProject(t, third, "关闭时最后捕获")
	third.Close()
	fourth := recoveryService(t, thirdDir)
	if fourth.GetRecoveryStatus().Pending != 1 {
		t.Fatal("正常关闭未作最后捕获")
	}
	discardDir := t.TempDir()
	discard := recoveryService(t, discardDir)
	renameRecoveryProject(t, discard, "明确放弃")
	if _, err := discard.CheckpointRecovery(); err != nil {
		t.Fatal(err)
	}
	if err := discard.DiscardCurrentRecovery(); err != nil {
		t.Fatal(err)
	}
	discard.Close()
	final := recoveryService(t, discardDir)
	if final.GetRecoveryStatus().Pending != 0 {
		t.Fatal("放弃后关闭又生成恢复副本")
	}
}
func TestRecoveryFailurePreservesCurrentAndOriginalFile(t *testing.T) {
	dir := t.TempDir()
	first := recoveryService(t, dir)
	b := first.GetBootstrap()
	original := filepath.Join(t.TempDir(), "original.aividcut")
	if _, err := first.SaveProjectTo(original); err != nil {
		t.Fatal(err)
	}
	oldBytes, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	renameRecoveryProject(t, first, "应恢复但不覆盖原文件")
	if _, err := first.CheckpointRecovery(); err != nil {
		t.Fatal(err)
	}
	first.Close()
	s := recoveryService(t, dir)
	page, err := s.ListProjectRecoveries(RecoveryListInput{Limit: 20})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
	current := renameRecoveryProject(t, s, "当前必须保留")
	base := s.recovery.store
	s.recovery.store = recoveryFailStore{base}
	_, err = s.RestoreProjectRecovery(RestoreRecoveryInput{ProjectScope: versionScope(current), ID: page.Records[0].ID, SHA256: page.Records[0].SHA256})
	if err == nil {
		t.Fatal("当前备份失败还切换工程")
	}
	if after := s.GetBootstrap(); !reflect.DeepEqual(after.Project, current.Project) || after.SourceEpoch != current.SourceEpoch {
		t.Fatal("恢复失败修改了工程")
	}
	if _, err := s.CheckpointRecovery(); err == nil || s.GetRecoveryStatus().Error == "" {
		t.Fatal("自动保护写入失败未展示状态")
	}
	s.recovery.store = base
	result, err := s.RestoreProjectRecovery(RestoreRecoveryInput{ProjectScope: versionScope(current), ID: page.Records[0].ID, SHA256: page.Records[0].SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if result.Project.ID != b.Project.ID || result.ProjectPath != "" || !result.Dirty {
		t.Fatal(result)
	}
	newBytes, err := os.ReadFile(original)
	if err != nil || !reflect.DeepEqual(oldBytes, newBytes) {
		t.Fatal("恢复覆盖原文件")
	}
}

func TestRecoveryReadableRecordCanRestoreWhenUpdatesFail(t *testing.T) {
	dir := t.TempDir()
	first := recoveryService(t, dir)
	renameRecoveryProject(t, first, "可读但目录暂不可写")
	if _, err := first.CheckpointRecovery(); err != nil {
		t.Fatal(err)
	}
	first.Close()
	s := recoveryService(t, dir)
	page, err := s.ListProjectRecoveries(RecoveryListInput{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	base := s.recovery.store
	s.recovery.store = recoveryFailStore{base}
	result, err := s.RestoreProjectRecovery(RestoreRecoveryInput{ProjectScope: versionScope(s.GetBootstrap()), ID: page.Records[0].ID, SHA256: page.Records[0].SHA256})
	if err != nil || result.Project.Name != "可读但目录暂不可写" || result.ProjectPath != "" || !result.Dirty {
		t.Fatal("可读恢复点应允许恢复到内存", result, err)
	}
	if s.GetRecoveryStatus().Error == "" || len(result.Warnings) < 2 {
		t.Fatal("未明确提示自动保护更新失败")
	}
	if _, _, _, err := base.Load(page.Records[0].ID, page.Records[0].SHA256); err != nil {
		t.Fatal("原恢复点被更新失败破坏", err)
	}
	s.recovery.store = base
	if _, err := s.SaveProjectTo(filepath.Join(t.TempDir(), "rescued.aividcut")); err != nil {
		t.Fatal("不能另存恢复结果", err)
	}
}
