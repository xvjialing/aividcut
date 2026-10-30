package editor

import (
	"reflect"
	"testing"
)

func TestUpdateAssetStableIDAndAllSourceReferencesAreValidatedAtomically(t *testing.T) {
	project := fixture(t).Snapshot()
	second := project.Clips[0]
	second.ID, second.StartFrame, second.DurationFrames, second.SourceInUs = "later-reference", 600, 180, 12_000_000
	project.Clips = append(project.Clips, second)
	engine, _ := NewEngine(project)
	replacement := project.Assets[0]
	replacement.Path, replacement.DurationUs = "/new/source.mp4", 15_000_000
	_, err := engine.Apply(EditRequest{BaseRevision: project.Revision, IdempotencyKey: "short-source", Commands: []Command{{Type: "update_project", Name: "不应部分保存"}, {Type: "update_asset", Asset: &replacement}}})
	assertCode(t, err, "source_out_of_range")
	if !reflect.DeepEqual(engine.Snapshot(), project) || engine.CanUndo() {
		t.Fatal("后一个引用越界时仍提交了部分素材或名称更新")
	}
	replacement.DurationUs = 25_000_000
	updated := apply(t, engine, Command{Type: "update_asset", Asset: &replacement})
	if updated.Assets[0].ID != project.Assets[0].ID || updated.Assets[0].Path != replacement.Path || !reflect.DeepEqual(updated.Clips, project.Clips) {
		t.Fatal("素材更新改变了稳定标识或片段编排")
	}
	undone, err := engine.Undo()
	if err != nil || undone.Assets[0].Path != project.Assets[0].Path {
		t.Fatal("素材更新不可撤销", err)
	}
	replacement.ID = "changed-id"
	_, err = engine.Apply(EditRequest{BaseRevision: undone.Revision, IdempotencyKey: "wrong-id", Commands: []Command{{Type: "update_asset", Asset: &replacement}}})
	assertCode(t, err, "not_found")
}

func TestLockedReferenceAllowsOnlyKnownFingerprintPureRelink(t *testing.T) {
	project := fixture(t).Snapshot()
	project.Assets[0].Fingerprint = "sha256:known-content"
	project.Assets[0].Missing = true
	project.Tracks[2].Locked = true
	engine, _ := NewEngine(project)
	replacement := project.Assets[0]
	replacement.Path, replacement.Missing = "/new/same-content.mp4", false
	replacement.URL, replacement.ThumbnailURL = "http://127.0.0.1/new-session", "http://127.0.0.1/new-thumb"
	updated := apply(t, engine, Command{Type: "update_asset", Asset: &replacement})
	if updated.Assets[0].Path != replacement.Path || !updated.Tracks[2].Locked || !reflect.DeepEqual(updated.Clips, project.Clips) {
		t.Fatal("纯重定位未保持锁轨与编排")
	}
	for name, mutate := range map[string]func(*Asset){
		"名称":   func(a *Asset) { a.Name = "不同名称" },
		"时长":   func(a *Asset) { a.DurationUs++ },
		"帧率":   func(a *Asset) { a.FPSNum++ },
		"尺寸":   func(a *Asset) { a.Width++ },
		"声音":   func(a *Asset) { a.HasAudio = !a.HasAudio },
		"不同指纹": func(a *Asset) { a.Fingerprint = "sha256:different-content" },
		"删除指纹": func(a *Asset) { a.Fingerprint = "" },
	} {
		t.Run(name, func(t *testing.T) {
			before := engine.Snapshot()
			changed := before.Assets[0]
			mutate(&changed)
			_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: NewID("locked"), Commands: []Command{{Type: "update_asset", Asset: &changed}}})
			assertCode(t, err, "track_locked")
			if !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("锁轨例外放宽成了通用素材编辑")
			}
		})
	}
}

func TestLegacyFingerprintAndSharedLockedLUTReferences(t *testing.T) {
	project := fixture(t).Snapshot()
	project.Tracks[2].Locked = true
	engine, _ := NewEngine(project)
	replacement := project.Assets[0]
	replacement.Fingerprint, replacement.Path = "sha256:newly-verified", "/new/source.mp4"
	_, err := engine.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "legacy-locked", Commands: []Command{{Type: "update_asset", Asset: &replacement}}})
	assertCode(t, err, "track_locked")
	track := project.Tracks[2]
	track.Locked = false
	apply(t, engine, Command{Type: "update_track", Track: &track})
	apply(t, engine, Command{Type: "update_asset", Asset: &replacement})

	project = fixture(t).Snapshot()
	project.Clips[0].LUT = &LUT{AssetID: "lut", Intensity: .7}
	project.Tracks[2].Locked = true
	project.Assets[2].Fingerprint = "sha256:lut-content"
	engine, _ = NewEngine(project)
	lut := project.Assets[2]
	lut.Path = "/moved/style.cube"
	apply(t, engine, Command{Type: "update_asset", Asset: &lut})
	lut.Name = "新名字"
	_, err = engine.Apply(EditRequest{BaseRevision: engine.Snapshot().Revision, IdempotencyKey: "locked-lut", Commands: []Command{{Type: "update_asset", Asset: &lut}}})
	assertCode(t, err, "track_locked")
}

func TestAssetTypeCannotChangeAndUnrelatedLocksDoNotBlockUpdate(t *testing.T) {
	engine := fixture(t)
	project := engine.Snapshot()
	track := project.Tracks[0]
	track.Locked = true
	apply(t, engine, Command{Type: "update_track", Track: &track})
	replacement := project.Assets[0]
	replacement.Name = "更新素材名称"
	apply(t, engine, Command{Type: "update_asset", Asset: &replacement})
	replacement.Kind = "image"
	_, err := engine.Apply(EditRequest{BaseRevision: engine.Snapshot().Revision, IdempotencyKey: "wrong-kind", Commands: []Command{{Type: "update_asset", Asset: &replacement}}})
	assertCode(t, err, "validation")
}
