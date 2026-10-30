package editor

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func visualFixture(t *testing.T) (Project, VisualAnalysis) {
	t.Helper()
	p := NewProject("分析记录")
	fp := "sha256:" + strings.Repeat("a", 64)
	asset := Asset{ID: "visual-asset", Name: "画面", Kind: "image", Path: "/fixture/source.png", Width: 100, Height: 60, Fingerprint: fp}
	p.Assets = []Asset{asset}
	image := DefaultClip("image-clip", "track-video-1", "image")
	image.AssetID = asset.ID
	p.Clips = []Clip{image}
	frame := SourceFrameEvidence{AssetID: asset.ID, AssetName: asset.Name, SourceFingerprint: fp, ImageSHA256: strings.Repeat("b", 64), Width: 100, Height: 60, Profile: "detail", RecipeVersion: "detail-jpeg-v1"}
	frame.ID = SourceFrameEvidenceID(frame)
	value := VisualAnalysis{ID: "visual-analysis", AssetID: asset.ID, SourceKind: "image", SourceFingerprint: fp, Producer: "codex", MethodVersion: "visual-notes-v1", Coverage: "sampled_frames", Frames: []SourceFrameEvidence{frame}, Notes: []VisualNote{{ID: "note", Kind: "observation", Text: "红色画面（核心夹具，不是真实模型）", Tags: []string{"红色"}, EvidenceIDs: []string{frame.ID}}}}
	return p, value
}

func TestOCRSchemaMigrationAndProducerMethodBinding(t *testing.T) {
	for version := 1; version <= 6; version++ {
		p := NewProject("旧工程")
		p.SchemaVersion = version
		got, err := UpgradeProject(p)
		if err != nil || got.SchemaVersion != SchemaVersion {
			t.Fatal(version, err)
		}
	}
	p, value := visualFixture(t)
	value.Producer = "apple-vision"
	value.MethodVersion = "apple-vision-ocr-v1"
	if err := ValidateVisualAnalysis(value); err != nil {
		t.Fatal(err)
	}
	value.AcceptedAt = time.Now().UTC()
	p.VisualAnalyses = []VisualAnalysis{value}
	p.SchemaVersion = 5
	if _, err := UpgradeProject(p); err == nil {
		t.Fatal("旧工程夹带OCR来源未拒绝")
	}
	value.Producer = "codex"
	if ValidateVisualAnalysis(value) == nil {
		t.Fatal("CLI来源冒充OCR方法")
	}
}

func TestWindowsOCRRejectsLegacySchemaAndMismatchedMethods(t *testing.T) {
	p, value := visualFixture(t)
	value.Producer = "windows-media-ocr"
	value.MethodVersion = "windows-media-ocr-v1"
	value.AcceptedAt = time.Now().UTC()
	if err := ValidateVisualAnalysis(value); err != nil {
		t.Fatal(err)
	}
	for version := 2; version <= 6; version++ {
		legacy := p
		legacy.SchemaVersion = version
		legacy.VisualAnalyses = []VisualAnalysis{value}
		if _, err := UpgradeProject(legacy); err == nil {
			t.Fatal("旧格式夹带Windows来源", version)
		}
	}
	value.Producer = "apple-vision"
	if ValidateVisualAnalysis(value) == nil {
		t.Fatal("Apple来源接受了Windows方法")
	}
}
func TestVisualAnalysisAcceptUndoRedoStaleAndDeepCopies(t *testing.T) {
	p, value := visualFixture(t)
	engine, err := NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	next, err := engine.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "accept", Commands: []Command{{Type: "accept_visual_analysis", VisualAnalysis: &value}}})
	if err != nil || len(next.VisualAnalyses) != 1 || next.VisualAnalyses[0].AcceptedAt.IsZero() {
		t.Fatalf("未接纳: %+v %v", next, err)
	}
	next.VisualAnalyses[0].Notes[0].Tags[0] = "外部修改"
	value.Notes[0].EvidenceIDs[0] = "修改"
	if !reflect.DeepEqual(engine.Snapshot().Clips, p.Clips) {
		t.Fatal("分析接纳改变了剪辑属性")
	}
	if engine.Snapshot().VisualAnalyses[0].Notes[0].Tags[0] != "红色" {
		t.Fatal("分析记录未深拷贝")
	}
	if p, err := engine.Undo(); err != nil || len(p.VisualAnalyses) != 0 {
		t.Fatal("撤销未移除记录")
	}
	if p, err := engine.Redo(); err != nil || len(p.VisualAnalyses) != 1 {
		t.Fatal("重做未恢复记录")
	}
	source := p.Assets[0]
	source.Fingerprint = "sha256:" + strings.Repeat("c", 64)
	stale, err := engine.Apply(EditRequest{BaseRevision: engine.Snapshot().Revision, IdempotencyKey: "source-change", Commands: []Command{{Type: "update_asset", Asset: &source}}})
	if err != nil || !stale.VisualAnalyses[0].Stale {
		t.Fatalf("源变更未失效: %v", err)
	}
	if p, err := engine.Undo(); err != nil || p.VisualAnalyses[0].Stale {
		t.Fatal("撤销源变更未恢复状态")
	}
}
func TestVisualAnalysisRejectsForgedEvidenceAndMixedCommandFields(t *testing.T) {
	p, value := visualFixture(t)
	for _, change := range []func(*VisualAnalysis){func(v *VisualAnalysis) { v.Frames[0].ID = "forged" }, func(v *VisualAnalysis) { v.Notes[0].EvidenceIDs = []string{"other"} }, func(v *VisualAnalysis) { v.Producer = "fake-model" }, func(v *VisualAnalysis) { v.Coverage = "entire_video" }, func(v *VisualAnalysis) { v.Notes[0].Kind = "fact" }} {
		engine, _ := NewEngine(p)
		candidate := CloneVisualAnalysis(value)
		change(&candidate)
		before := engine.Snapshot()
		if _, err := engine.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "bad", Commands: []Command{{Type: "accept_visual_analysis", VisualAnalysis: &candidate}}}); err == nil || !reflect.DeepEqual(before, engine.Snapshot()) {
			t.Fatal("无效分析被接纳或部分改变工程")
		}
	}
	engine, _ := NewEngine(p)
	if _, err := engine.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "mixed", Commands: []Command{{Type: "update_project", Name: "假写入", VisualAnalysis: &value}}}); err == nil {
		t.Fatal("普通命令忽略了分析写入参数")
	}
}
func TestProjectV1UpgradePreservesDataAndRejectsSmuggledAnalysis(t *testing.T) {
	p, value := visualFixture(t)
	p.SchemaVersion = 1
	upgraded, err := UpgradeProject(p)
	if err != nil || upgraded.SchemaVersion != SchemaVersion || !reflect.DeepEqual(upgraded.Assets, p.Assets) {
		t.Fatal("旧工程迁移丢数据")
	}
	p.VisualAnalyses = []VisualAnalysis{value}
	if _, err := UpgradeProject(p); err == nil {
		t.Fatal("旧schema静默接纳新字段")
	}
}
