package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/playback"
	"aividcut/internal/projectfonts"
	"aividcut/internal/projectstore"
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

func TestClipboardReopenSameProjectRejectsDifferentFontContent(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	regular, mono := filepath.Join(dir, "regular.ttf"), filepath.Join(dir, "mono.ttf")
	for path, data := range map[string][]byte{regular: goregular.TTF, mono: gomono.TTF} {
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	state, err := s.ImportPaths(context.Background(), []string{regular})
	if err != nil {
		t.Fatal(err)
	}
	c := editor.DefaultClip("font-title", "track-text-1", "text")
	c.Text, c.DurationFrames, c.TextStyle.FontAssetID = "Frozen Font", 30, state.Project.Assets[0].ID
	state, err = s.ApplyEdit(editor.EditRequest{BaseRevision: state.Project.Revision, IdempotencyKey: "font-title", Commands: []editor.Command{{Type: "add_clip", Clip: &c}}})
	if err != nil {
		t.Fatal(err)
	}
	copy, err := s.CopyTimelineSelection(clipboardSelection(state, c.ID))
	if err != nil {
		t.Fatal(err)
	}
	// 同一工程的另一份有效文件保持素材ID，使用实际不同的字体内容。
	alternate := editor.CloneProject(state.Project)
	alternate.Clips = nil
	font, err := projectfonts.Read(mono)
	if err != nil {
		t.Fatal(err)
	}
	alternate.Assets[0].Path, alternate.Assets[0].Fingerprint, alternate.Assets[0].Font = mono, "sha256:"+font.SHA256, &font.Info
	path := filepath.Join(dir, "alternate.aividcut")
	if err = projectstore.Save(path, alternate); err != nil {
		t.Fatal(err)
	}
	opened, err := s.OpenProjectPath(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PasteTimelineSelection(ClipboardPasteInput{ProjectID: opened.Project.ID, SourceEpoch: opened.SourceEpoch, BaseRevision: opened.Project.Revision, ClipboardID: copy.Clipboard.ID, IdempotencyKey: "paste-other-font", AtFrame: 60})
	if err == nil {
		t.Fatal("打开同ID工程后，旧剪贴板静默使用了新字体内容")
	}
	if !reflect.DeepEqual(opened.Project, s.GetBootstrap().Project) || s.GetClipboardStatus().ID != copy.Clipboard.ID {
		t.Fatal("拒绝后改变工程或丢掉旧剪贴板")
	}
	if _, err = s.Undo(); err == nil {
		t.Fatal("失败粘贴进入了新引擎的撤销历史")
	}
}

func TestClipboardActualFontRelinkPastePersistenceAndPixels(t *testing.T) {
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" {
		t.Skip("需要显式原生worker；跳过不计为真实字体画面验证")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	original := filepath.Join(dir, "原字体.ttf")
	if err = os.WriteFile(original, goregular.TTF, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := s.ImportPaths(ctx, []string{original})
	if err != nil {
		t.Fatal(err)
	}
	font := state.Project.Assets[0]
	settings := state.Project.Settings
	settings.Width, settings.Height = 320, 180
	c := editor.DefaultClip("real-font-title", "track-text-1", "text")
	c.Text, c.DurationFrames, c.TextStyle.FontAssetID, c.TextStyle.FontSize = "Frozen Font", 30, font.ID, 24
	state, err = s.ApplyEdit(editor.EditRequest{BaseRevision: state.Project.Revision, IdempotencyKey: "font-pixel-fixture", Commands: []editor.Command{{Type: "update_project", Settings: &settings}, {Type: "add_clip", Clip: &c}}})
	if err != nil {
		t.Fatal(err)
	}
	collect := func(p editor.Project, start int64) []byte {
		t.Helper()
		session, err := playback.Start(ctx, PlaybackEnvironment())
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if _, err = session.Load(ctx, p); err != nil {
			t.Fatal(err)
		}
		if _, err = session.Preview(ctx, start); err != nil {
			t.Fatal(err)
		}
		if err = session.Play(ctx); err != nil {
			t.Fatal(err)
		}
		var pixels []byte
		for i := int64(0); i < 30; i++ {
			frame, err := session.Next(ctx)
			if err != nil || frame.Position != start+i {
				t.Fatal("实际字体画面帧序错误", err)
			}
			pixels = append(pixels, frame.RGBA...)
		}
		return pixels
	}
	before := collect(state.Project, 0)
	hasGlyph := false
	for offset := 4; offset < 320*180*4; offset += 4 {
		if !bytes.Equal(before[:4], before[offset:offset+4]) {
			hasGlyph = true
			break
		}
	}
	if !hasGlyph {
		t.Fatal("基线只有纯色画面，不能用空白帧证明字体保持")
	}
	copy, err := s.CopyTimelineSelection(clipboardSelection(state, c.ID))
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(dir, "迁移后 相同字体.ttf")
	if err = os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	state, err = s.RelinkAsset(ctx, font.ID, moved)
	if err != nil {
		t.Fatal(err)
	}
	state, err = s.ApplyEdit(editor.EditRequest{BaseRevision: state.Project.Revision, IdempotencyKey: "delete-font-source", Commands: []editor.Command{{Type: "delete_clip", ClipID: c.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	pasted, err := s.PasteTimelineSelection(ClipboardPasteInput{ProjectID: state.Project.ID, SourceEpoch: state.SourceEpoch, BaseRevision: state.Project.Revision, ClipboardID: copy.Clipboard.ID, IdempotencyKey: "paste-moved-font", AtFrame: 60})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, collect(pasted.Bootstrap.Project, 60)) {
		t.Fatal("同内容字体迁移/删除原片段后粘贴的实际字形改变")
	}
	undone, err := s.Undo()
	if err != nil || len(undone.Project.Clips) != 0 {
		t.Fatal("字体粘贴不能一次撤销", err)
	}
	redone, err := s.Redo()
	if err != nil || !reflect.DeepEqual(redone.Project.Clips, pasted.Bootstrap.Project.Clips) {
		t.Fatal("重做没有保留字体和副本身份", err)
	}
	path := filepath.Join(dir, "pasted-font.aividcut")
	if _, err = s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	reopened, err := s.OpenProjectPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, collect(reopened.Project, 60)) {
		t.Fatal("保存重开丢失复制时字体")
	}
	t.Logf("实际30帧字体RGBA共%d字节：迁移、删除后粘贴、Undo/Redo及保存重开一致，SHA256=%x", len(before), sha256.Sum256(before))
}
