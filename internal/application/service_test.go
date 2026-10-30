package application

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/media"
)

func fixtureImage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "真实图片.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	im := image.NewRGBA(image.Rect(0, 0, 32, 18))
	im.Set(2, 2, color.RGBA{R: 200, A: 255})
	if err := png.Encode(f, im); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func TestRealImportEditSaveReopenAndDirtyHistory(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	initial := s.GetBootstrap()
	if initial.Dirty || len(initial.Project.Assets) != 0 || initial.Capabilities["exportVideo"].Available {
		t.Fatalf("空工程不应虚构素材或能力：%+v", initial)
	}
	imported, err := s.ImportPaths(context.Background(), []string{fixtureImage(t)})
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Project.Assets) != 1 || !imported.Dirty {
		t.Fatal("真实图片未进入工程")
	}
	asset := imported.Project.Assets[0]
	response, err := http.Get(asset.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("导入后素材 URL 不可用")
	}
	clip := editor.DefaultClip("clip-image", "track-video-1", "image")
	clip.AssetID = asset.ID
	edited, err := s.ApplyEdit(editor.EditRequest{BaseRevision: imported.Project.Revision, IdempotencyKey: "add-image", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "剪辑工程.aividcut")
	saved, err := s.SaveProjectTo(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Dirty || saved.ProjectPath != path {
		t.Fatal("保存状态不正确")
	}
	undone, err := s.Undo()
	if err != nil || !undone.Dirty || len(undone.Project.Clips) != 0 {
		t.Fatal("撤销不能恢复编辑前状态", err)
	}
	redone, err := s.Redo()
	if err != nil || redone.Dirty || len(redone.Project.Clips) != 1 {
		t.Fatal("重做回已保存内容应为干净状态", err)
	}
	_, _ = s.NewProject("另一个工程")
	opened, err := s.OpenProjectPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Project.ID != edited.Project.ID || len(opened.Project.Clips) != 1 || opened.Dirty || opened.CanUndo {
		t.Fatal("重开工程的状态不完整")
	}
}

func TestImportBatchFailureDoesNotPartiallyMutateProject(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.ImportPaths(context.Background(), []string{fixtureImage(t), filepath.Join(t.TempDir(), "不存在.mp4")})
	if err == nil {
		t.Fatal("缺失素材被误认为导入成功")
	}
	if p := s.GetBootstrap().Project; len(p.Assets) != 0 || p.Revision != 0 {
		t.Fatal("失败的导入批次留下部分工程变更")
	}
}

func TestProjectSaveCannotOverwriteSource(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := fixtureImage(t)
	original, _ := os.ReadFile(path)
	if _, err := s.ImportPaths(context.Background(), []string{path}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProjectTo(path); err == nil {
		t.Fatal("工程保存允许覆盖源素材")
	}
	after, _ := os.ReadFile(path)
	if string(original) != string(after) {
		t.Fatal("源素材被修改")
	}
}
