package projectstore

import (
	"aividcut/internal/editor"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripAfterEditingAndRuntimeURLsAreNotSaved(t *testing.T) {
	dir := t.TempDir()
	mediaPath := filepath.Join(dir, "口播.mp4")
	if err := os.WriteFile(mediaPath, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	project := editor.NewProject("保存测试")
	project.Assets = []editor.Asset{{ID: "source", Name: "口播.mp4", Kind: "video", Path: mediaPath, DurationUs: 20_000_000, URL: "http://127.0.0.1/media?token=secret", ThumbnailURL: "http://127.0.0.1/thumb?token=secret"}}
	clip := editor.DefaultClip("clip", "track-video-1", "video")
	clip.AssetID, clip.DurationFrames = "source", 180
	project.Clips = []editor.Clip{clip}
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	project, err = engine.Apply(editor.EditRequest{BaseRevision: 0, IdempotencyKey: "split", Commands: []editor.Command{{Type: "split_clip", ClipID: "clip", AtFrame: 60}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "工程.aividcut")
	if err := Save(path, project); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte("secret")) || project.Assets[0].URL == "" {
		t.Fatal("临时地址泄露到工程，或保存污染了调用方状态")
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != project.Name || loaded.Revision != 1 || len(loaded.Clips) != 2 || loaded.Clips[1].SourceInUs != 2_000_000 || loaded.Assets[0].Missing || loaded.Assets[0].URL != "" {
		t.Fatalf("重开工程丢失剪辑信息：%+v", loaded)
	}
	if _, err := editor.NewEngine(loaded); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidSaveDoesNotReplaceExistingProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "工程.aividcut")
	project := editor.NewProject("可恢复版本")
	if err := Save(path, project); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	project.Settings.FPSDen = 0
	if err := Save(path, project); err == nil {
		t.Fatal("非法工程被保存")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("失败保存覆盖了已有工程")
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".aividcut-save-*"))
	if len(matches) != 0 {
		t.Fatal("临时文件没有清理")
	}
}

func TestLoadRejectsCorruptUnsupportedAndTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "工程.aividcut")
	if err := Save(path, editor.NewProject("有效工程")); err != nil {
		t.Fatal(err)
	}
	valid, _ := os.ReadFile(path)
	for name, data := range map[string][]byte{
		"损坏内容":  []byte(`{"schemaVersion":`),
		"不支持版本": bytes.Replace(valid, []byte(fmt.Sprintf(`"schemaVersion": %d`, editor.SchemaVersion)), []byte(`"schemaVersion": 999`), 1),
		"尾部内容":  append(append([]byte{}, valid...), []byte("{}")...),
		"未知字段":  bytes.Replace(valid, []byte(`"revision": 0`), []byte(`"revision": 0, "unknownSecret": true`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("非法工程被接受")
			}
		})
	}
}

func TestRelativeMediaPathsAndMissingMedia(t *testing.T) {
	dir := t.TempDir()
	project := editor.NewProject("相对路径")
	project.Assets = []editor.Asset{{ID: "image", Name: "照片.png", Kind: "image", Path: "assets/照片.png"}}
	path := filepath.Join(dir, "工程.aividcut")
	if err := Save(path, project); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Assets[0].Missing || loaded.Assets[0].Path != filepath.Join(dir, "assets/照片.png") {
		t.Fatalf("没有正确标记待重定位媒体：%+v", loaded.Assets[0])
	}
}

func TestLegacyLoadReportsMigrationAndPreservesOriginalFile(t *testing.T) {
	project := editor.NewProject("旧版工程")
	project.SchemaVersion = 1
	data, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy.aividcut")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, warnings, err := LoadWithWarnings(path)
	if err != nil || loaded.SchemaVersion != editor.SchemaVersion || len(warnings) != 1 {
		t.Fatal("旧版迁移未报告", err)
	}
	original, _ := os.ReadFile(path)
	if !bytes.Equal(data, original) {
		t.Fatal("打开旧工程就改写了原文件")
	}
	if err = Save(path, loaded); err != nil {
		t.Fatal(err)
	}
	stored, _ := os.ReadFile(path)
	if !bytes.Contains(stored, []byte(fmt.Sprintf(`"schemaVersion": %d`, editor.SchemaVersion))) {
		t.Fatal("明确保存未写入新格式")
	}
}
