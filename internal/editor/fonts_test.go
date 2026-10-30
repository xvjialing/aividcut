package editor

import (
	"reflect"
	"strings"
	"testing"
)

func TestFontSchemaMigrationAndDeepClone(t *testing.T) {
	for _, version := range []int{1, 2} {
		p := NewProject("旧工程")
		p.SchemaVersion = version
		original := CloneProject(p)
		got, err := UpgradeProject(p)
		if err != nil || got.SchemaVersion != SchemaVersion {
			t.Fatal(got, err)
		}
		got.SchemaVersion = version
		if !reflect.DeepEqual(got, original) {
			t.Fatal("旧工程迁移改变了数据")
		}
		p.Assets = []Asset{{ID: "font", Kind: "font", Name: "Font", Path: "/font.ttf", Fingerprint: "sha256:" + strings.Repeat("a", 64), Font: &FontInfo{Family: "Go", Style: "Regular", FullName: "Go Regular", Weight: 400}}}
		if _, err = UpgradeProject(p); err == nil {
			t.Fatal("旧schema偷带字体资源")
		}
	}
	p := NewProject("字体引用")
	p.Assets = []Asset{{ID: "f", Kind: "font", Name: "Font", Path: "/font.ttf", Fingerprint: "sha256:" + strings.Repeat("a", 64), Font: &FontInfo{Family: "Go", Style: "Regular", FullName: "Go Regular", Weight: 400}}}
	c := DefaultClip("title", "track-text-1", "text")
	c.TextStyle.FontAssetID = "f"
	p.Clips = []Clip{c}
	copy := CloneProject(p)
	copy.Assets[0].Font.Family = "Changed"
	if p.Assets[0].Font.Family != "Go" {
		t.Fatal("字体元信息浅拷贝")
	}
	p.Clips[0].TextStyle.FontAssetID = "missing"
	if ValidateProject(p) == nil {
		t.Fatal("字体引用不存在仍通过")
	}
}

func TestManualCaptionFontSurvivesRegeneration(t *testing.T) {
	engine := transcriptFixture(t)
	font := Asset{ID: "caption-font", Kind: "font", Name: "字幕字体", Path: "/font.ttf", Fingerprint: "sha256:" + strings.Repeat("a", 64), Font: &FontInfo{Family: "Go", Style: "Regular", FullName: "Go Regular", Weight: 400}}
	apply(t, engine, Command{Type: "add_asset", Asset: &font})
	caption := boundByWord(t, engine.Snapshot(), "speech-clip", "hello")
	caption.TextStyle.FontAssetID = font.ID
	updated := apply(t, engine, Command{Type: "update_clip", Clip: &caption})
	if !boundByWord(t, updated, "speech-clip", "hello").CaptionSource.StyleOverride {
		t.Fatal("选用字体未登记人工样式")
	}
	generated := apply(t, engine, Command{Type: "generate_bound_captions", TranscriptID: "transcript", TrackID: "track-caption-1"})
	if boundByWord(t, generated, "speech-clip", "hello").TextStyle.FontAssetID != font.ID {
		t.Fatal("重新生成字幕丢失人工字体")
	}
}
