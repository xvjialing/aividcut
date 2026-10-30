package editor

import (
	"reflect"
	"strings"
	"testing"
)

func clipboardFontAsset() Asset {
	return Asset{ID: "clipboard-font", Kind: "font", Name: "复制字体", Path: "/font.ttf", Fingerprint: "sha256:" + strings.Repeat("a", 64), Font: &FontInfo{Family: "Go", Style: "Regular", FullName: "Go Regular", Weight: 400}}
}

func fontClipboardFixture(t *testing.T, kind string) (*Engine, *TimelineClipboard, Asset) {
	t.Helper()
	var e *Engine
	var ids []string
	font := clipboardFontAsset()
	if kind == "text" {
		p := NewProject("文字字体快照")
		c := DefaultClip("title", "track-text-1", "text")
		c.Text, c.DurationFrames, c.TextStyle.FontAssetID = "Frozen Font", 30, font.ID
		p.Assets, p.Clips = []Asset{font}, []Clip{c}
		var err error
		e, err = NewEngine(p)
		if err != nil {
			t.Fatal(err)
		}
		ids = []string{c.ID}
	} else {
		e = transcriptFixture(t)
		apply(t, e, Command{Type: "add_asset", Asset: &font})
		caption := boundByWord(t, e.Snapshot(), "speech-clip", "hello")
		caption.TextStyle.FontAssetID = font.ID
		apply(t, e, Command{Type: "update_clip", Clip: &caption})
		ids = []string{caption.ID}
		if kind == "bound-source" {
			ids = []string{"speech-clip"}
		}
	}
	c, err := CaptureTimelineClipboard(e.Snapshot(), ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e, c, font
}

func TestClipboardRejectsChangedFontIdentityForTextAndCaptions(t *testing.T) {
	for _, kind := range []string{"text", "standalone-caption", "bound-source"} {
		t.Run(kind, func(t *testing.T) {
			e, c, font := fontClipboardFixture(t, kind)
			font.Fingerprint = "sha256:" + strings.Repeat("b", 64)
			font.Font = &FontInfo{Family: "Go Mono", Style: "Regular", FullName: "Go Mono Regular", Weight: 400}
			before := apply(t, e, Command{Type: "update_asset", Asset: &font})
			_, err := e.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: "changed-font-paste", Commands: []Command{ClipboardPasteCommand(c, c.Info().ID, 600, 0)}})
			assertCode(t, err, "clipboard_asset")
			if !reflect.DeepEqual(before, e.Snapshot()) {
				t.Fatal("字体身份变化后留下了部分粘贴")
			}
			undone, err := e.Undo()
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range undone.Assets {
				if a.ID == font.ID && a.Fingerprint != clipboardFontAsset().Fingerprint {
					t.Fatal("失败粘贴占用了撤销历史")
				}
			}
		})
	}
}

func TestClipboardFontRelinkKeepsStyleAndSingleUndoRedo(t *testing.T) {
	for _, kind := range []string{"text", "standalone-caption", "bound-source"} {
		t.Run(kind, func(t *testing.T) {
			e, c, font := fontClipboardFixture(t, kind)
			font.Path, font.Name, font.URL = "/moved/same.ttf", "同内容新名称", "http://127.0.0.1/new-font-session"
			before := apply(t, e, Command{Type: "update_asset", Asset: &font})
			request := EditRequest{BaseRevision: before.Revision, IdempotencyKey: "same-font-paste", Commands: []Command{ClipboardPasteCommand(c, c.Info().ID, 600, 0)}}
			pasted, err := e.Apply(request)
			if err != nil {
				t.Fatal("字体同内容重定位被阻止", err)
			}
			ids := ClipboardPasteSelection(pasted, c.Info().ID, request.IdempotencyKey)
			foundFont := false
			for _, clip := range pasted.Clips {
				for _, id := range ids {
					if clip.ID == id && clip.TextStyle.FontAssetID == font.ID {
						foundFont = true
					}
				}
			}
			if !foundFont {
				t.Fatal("粘贴没有保留复制时字体引用")
			}
			undone, err := e.Undo()
			if err != nil || !reflect.DeepEqual(undone.Clips, before.Clips) {
				t.Fatal("字体粘贴不能整组撤销", err)
			}
			redone, err := e.Redo()
			if err != nil || !reflect.DeepEqual(redone.Clips, pasted.Clips) {
				t.Fatal("重做改变字体或字幕副本", err)
			}
		})
	}
}

func TestClipboardMissingFontIsRejectedBeforeCreatingAnyClip(t *testing.T) {
	e, c, _ := fontClipboardFixture(t, "text")
	p := e.Snapshot()
	p.Clips, p.Assets = nil, nil
	e, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	before := e.Snapshot()
	_, err = e.Apply(EditRequest{BaseRevision: p.Revision, IdempotencyKey: "missing-font", Commands: []Command{ClipboardPasteCommand(c, c.Info().ID, 0, 0)}})
	assertCode(t, err, "clipboard_asset")
	if !reflect.DeepEqual(before, e.Snapshot()) || e.CanUndo() {
		t.Fatal("缺字体仍留下粘贴或撤销记录")
	}
}
