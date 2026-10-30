package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/media"
	"context"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"os"
	"path/filepath"
	"testing"
)

func TestFontImportSelectionPersistenceUndoAndLockedRelink(t *testing.T) {
	ctx := context.Background()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "font.ttf")
	os.WriteFile(path, goregular.TTF, 0600)
	b, err := s.ImportPaths(ctx, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	a := b.Project.Assets[0]
	if a.URL != "" {
		t.Fatal("未使用字体不应开放字节读取")
	}
	if a.Kind != "font" || a.Font == nil || a.Font.Family != "Go" {
		t.Fatal(a)
	}
	c := editor.DefaultClip("title", "track-text-1", "text")
	c.Text = "Font File"
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "text", Commands: []editor.Command{{Type: "add_clip", Clip: &c}}})
	if err != nil {
		t.Fatal(err)
	}
	c.TextStyle.FontAssetID = a.ID
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "font", Commands: []editor.Command{{Type: "update_clip", Clip: &c}}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Project.Assets[0].URL == "" {
		t.Fatal("已使用字体缺少经过验证的会话读取能力")
	}
	b, err = s.Undo()
	if err != nil || b.Project.Clips[0].TextStyle.FontAssetID != "" {
		t.Fatal("字体不能撤销", err)
	}
	b, err = s.Redo()
	if err != nil {
		t.Fatal(err)
	}
	save := filepath.Join(t.TempDir(), "project.aividcut")
	s.SaveProjectTo(save)
	b, err = s.OpenProjectPath(save)
	if err != nil || b.Project.SchemaVersion != editor.SchemaVersion || b.Project.Clips[0].TextStyle.FontAssetID != a.ID {
		t.Fatal(b, err)
	}
	track := b.Project.Tracks[1]
	track.Locked = true
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "lock", Commands: []editor.Command{{Type: "update_track", Track: &track}}})
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "same.ttf")
	os.WriteFile(moved, goregular.TTF, 0600)
	b, err = s.RelinkAsset(ctx, a.ID, moved)
	if err != nil || b.Project.Assets[0].Path != moved {
		t.Fatal("同字体锁轨纯重定位失败", err)
	}
	other := filepath.Join(t.TempDir(), "different.ttf")
	os.WriteFile(other, gomono.TTF, 0600)
	if _, err = s.RelinkAsset(ctx, a.ID, other); err == nil {
		t.Fatal("同名不同字体被当作原文件")
	}
}
