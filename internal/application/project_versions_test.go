package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/projectstore"
)

func versionScope(b Bootstrap) ProjectVersionScope {
	return ProjectVersionScope{ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision, SourceEpoch: b.SourceEpoch}
}

func versionService(t *testing.T, directory string) *Service {
	t.Helper()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureProjectVersions(directory); err != nil {
		s.Close()
		t.Fatal(err)
	}
	return s
}

func TestProjectVersionsRestoreReturnAndPersistentReopen(t *testing.T) {
	directory := t.TempDir()
	s := versionService(t, directory)
	b, err := s.ImportPaths(context.Background(), []string{fixtureImage(t)})
	if err != nil {
		t.Fatal(err)
	}
	clip := editor.DefaultClip("title", "track-text-1", "text")
	clip.Text = "人工保留文本"
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "version-title", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "saved.aividcut")
	b, err = s.SaveProjectTo(path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.CreateProjectVersion(CreateProjectVersionInput{ProjectVersionScope: versionScope(b), Name: "初稿"})
	if err != nil {
		t.Fatal(err)
	}
	if after := s.GetBootstrap(); after.Dirty || after.Project.Revision != b.Project.Revision || after.CanUndo != b.CanUndo {
		t.Fatal("创建版本修改了编辑状态")
	}
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "version-rename", Commands: []editor.Command{{Type: "update_project", Name: "当前精修"}}})
	if err != nil {
		t.Fatal(err)
	}
	session := mcpApplicationClient(t, s, true)
	input := RestoreProjectVersionInput{ProjectVersionScope: versionScope(b), ID: version.ID, SHA256: version.SHA256}
	restored, err := s.RestoreProjectVersion(input)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Bootstrap.Project.Name != "未命名工程" || restored.Bootstrap.SourceEpoch == b.SourceEpoch || restored.Bootstrap.Project.Revision != b.Project.Revision+1 || restored.Bootstrap.CanUndo {
		t.Fatal("恢复状态或新实例错误", restored)
	}
	if s.GetMCPStatus().ScopeValid {
		t.Fatal("旧MCP授权未失效")
	}
	callApplicationMCP(t, session, "aividcut_get_project", map[string]any{}, true)
	if _, err := s.RestoreProjectVersion(input); err == nil {
		t.Fatal("重复旧请求未拒绝")
	}
	contents, _ := os.ReadFile(path)
	if !reflect.DeepEqual(original, contents) {
		t.Fatal("恢复覆盖了原工程文件")
	}
	returned, err := s.RestoreProjectVersion(RestoreProjectVersionInput{ProjectVersionScope: versionScope(restored.Bootstrap), ID: restored.PreviousVersion.ID, SHA256: restored.PreviousVersion.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if returned.Bootstrap.Project.Name != "当前精修" || returned.Bootstrap.Project.Clips[0].Text != "人工保留文本" || !returned.Bootstrap.Dirty {
		t.Fatal("未能返回恢复前状态")
	}
	s.Close()
	s = versionService(t, directory)
	defer s.Close()
	b, err = s.OpenProjectPath(path)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ListProjectVersions(ListProjectVersionsInput{ProjectVersionScope: versionScope(b), Limit: 20})
	if err != nil || page.Total != 3 {
		t.Fatal("重启版本未保留", page, err)
	}
	if _, err := s.RestoreProjectVersion(RestoreProjectVersionInput{ProjectVersionScope: versionScope(b), ID: version.ID, SHA256: version.SHA256}); err != nil {
		t.Fatal("重开无法恢复", err)
	}
}

func TestProjectVersionsOldScopeCorruptionAndBackupFailure(t *testing.T) {
	dir := t.TempDir()
	s := versionService(t, dir)
	defer s.Close()
	b := s.GetBootstrap()
	version, err := s.CreateProjectVersion(CreateProjectVersionInput{ProjectVersionScope: versionScope(b), Name: "原状态"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "same.aividcut")
	if err := projectstore.Save(path, b.Project); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProjectVersion(CreateProjectVersionInput{ProjectVersionScope: versionScope(b), Name: "旧实例"}); err == nil {
		t.Fatal("同ID重开接受旧实例")
	}
	b = s.GetBootstrap()
	if _, err := s.RestoreProjectVersion(RestoreProjectVersionInput{ProjectVersionScope: versionScope(b), ID: "../outside", SHA256: version.SHA256}); err == nil {
		t.Fatal("路径标识未拒绝")
	}
	if err := s.versions.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreProjectVersion(RestoreProjectVersionInput{ProjectVersionScope: versionScope(b), ID: version.ID, SHA256: version.SHA256}); err == nil {
		t.Fatal("关闭版本存储仍恢复")
	}
	if s.GetBootstrap().SourceEpoch != b.SourceEpoch || !reflect.DeepEqual(s.GetBootstrap().Project, b.Project) {
		t.Fatal("失败修改了工程")
	}
}

func TestProjectVersionBackupRefusalKeepsCurrentEngine(t *testing.T) {
	directory := t.TempDir()
	s := versionService(t, directory)
	defer s.Close()
	b := s.GetBootstrap()
	v, err := s.CreateProjectVersion(CreateProjectVersionInput{ProjectVersionScope: versionScope(b), Name: "目标"})
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "before-cap", Commands: []editor.Command{{Type: "update_project", Name: "必须保留"}}})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(b.Project.ID))
	folder := filepath.Join(directory, hex.EncodeToString(sum[:]))
	for index := 0; index < 199; index++ {
		if err := os.Mkdir(filepath.Join(folder, fmt.Sprintf("version-%024x", index)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	engine := s.engine
	_, err = s.RestoreProjectVersion(RestoreProjectVersionInput{ProjectVersionScope: versionScope(b), ID: v.ID, SHA256: v.SHA256})
	if err == nil {
		t.Fatal("恢复前版本被拒绝时不能切换工程")
	}
	if s.engine != engine || !reflect.DeepEqual(s.GetBootstrap().Project, b.Project) || s.GetBootstrap().SourceEpoch != b.SourceEpoch {
		t.Fatal("备份失败改变了当前工程")
	}
}
