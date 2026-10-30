package projectstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func TestTranscriptBindingsAndCorrectionsSurviveSaveReopen(t *testing.T) {
	project := editor.NewProject("保存源转写")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	sourcePath := filepath.Join(t.TempDir(), "speech.mp4")
	if err := os.WriteFile(sourcePath, []byte("这里只测试工程快照，不冒充真实媒体解码"), 0600); err != nil {
		t.Fatal(err)
	}
	project.Assets = []editor.Asset{{ID: "source", Name: "speech.mp4", Kind: "video", Path: sourcePath, DurationUs: 10_000_000, HasAudio: true, Fingerprint: "sha256:source"}}
	clip := editor.DefaultClip("take", "track-video-1", "video")
	clip.AssetID, clip.DurationFrames = "source", 60
	project.Clips = []editor.Clip{clip}
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	raw := editor.Transcript{ID: "transcript", AssetID: "source", SourceFingerprint: "sha256:source", RawWords: []editor.SourceWord{{ID: "word", StartUs: 100_000, EndUs: 900_000, Text: "识别原文", SpeakerID: "speaker-1"}}}
	project, err = engine.Apply(editor.EditRequest{BaseRevision: 0, IdempotencyKey: "transcribe", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &raw}, {Type: "generate_bound_captions", TranscriptID: raw.ID, TrackID: "track-caption-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	accepted := append([]editor.SourceWord{}, project.Transcripts[0].AcceptedWords...)
	accepted[0].Text = "接受纠错"
	project, err = engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "correct", Commands: []editor.Command{{Type: "correct_transcript", TranscriptID: raw.ID, TranscriptRevision: 1, Words: accepted}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range project.Clips {
		if value.CaptionSource == nil {
			continue
		}
		value.Text, value.TextStyle.FontSize = "人工覆盖", 72
		project, err = engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "override", Commands: []editor.Command{{Type: "update_clip", Clip: &value}}})
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	path := filepath.Join(t.TempDir(), "transcript.aividcut")
	if err := Save(path, project); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project.Transcripts, loaded.Transcripts) || loaded.Transcripts[0].RawWords[0].Text != "识别原文" || loaded.Transcripts[0].AcceptedWords[0].Text != "接受纠错" {
		t.Fatal("保存重开混淆了原始识别与接受修订")
	}
	reopened, err := editor.NewEngine(loaded)
	if err != nil {
		t.Fatal(err)
	}
	clip = loaded.Clips[0]
	clip.StartFrame = 90
	moved, err := reopened.Apply(editor.EditRequest{BaseRevision: loaded.Revision, IdempotencyKey: "move-reopened", Commands: []editor.Command{{Type: "update_clip", Clip: &clip}}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, value := range moved.Clips {
		if value.CaptionSource != nil {
			found = true
			if value.Text != "人工覆盖" || value.TextStyle.FontSize != 72 || value.StartFrame < 90 {
				t.Fatal("重开后绑定跟随或人工覆盖丢失")
			}
		}
	}
	if !found {
		t.Fatal("重开后绑定字幕消失")
	}
}

func TestLegacyProjectOmitsNewOptionalTranscriptFields(t *testing.T) {
	project := editor.NewProject("旧工程兼容")
	data, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "transcripts") || strings.Contains(string(data), "captionSource") {
		t.Fatal("旧工程未使用新字段时应保持省略")
	}
	path := filepath.Join(t.TempDir(), "legacy.aividcut")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || len(loaded.Transcripts) != 0 {
		t.Fatal("旧 schema=1 工程不能读取", err)
	}
}
