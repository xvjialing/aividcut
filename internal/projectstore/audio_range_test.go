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

func TestExactAudioSamplesAndSubframeStartSurviveSaveReopen(t *testing.T) {
	project := editor.NewProject("精确音频快照")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	path := filepath.Join(t.TempDir(), "source.wav")
	if err := os.WriteFile(path, []byte("本测试只验证工程存储，不冒充媒体解码"), 0600); err != nil {
		t.Fatal(err)
	}
	count := int64(5777)
	asset := editor.Asset{ID: "audio", Name: "source.wav", Kind: "audio", Path: path, DurationUs: 120354, HasAudio: true, Fingerprint: "sha256:source", AudioSampleRate: 48000, AudioSampleCount: &count}
	clip := editor.DefaultClip("audio-clip", "track-audio-1", "audio")
	clip.AssetID = asset.ID
	var err error
	clip, err = editor.AudioClipToSourceEnd(clip, asset, project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	project.Assets, project.Clips = []editor.Asset{asset}, []editor.Clip{clip}
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	project, err = engine.Apply(editor.EditRequest{BaseRevision: 0, IdempotencyKey: "split-samples", Commands: []editor.Command{{Type: "split_clip", ClipID: clip.ID, AtFrame: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "audio.aividcut")
	if err := Save(snapshot, project); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project, loaded) {
		t.Fatal("保存重开丢失真实样本时钟或分数音轨起点")
	}
	if _, err := editor.NewEngine(loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Clips[1].AudioRange.OffsetNum != 25 || loaded.Clips[1].AudioRange.OffsetDen != 3 || *loaded.Assets[0].AudioSampleCount != 5777 {
		t.Fatal("持久化端点被当浮点或DurationFrames重建")
	}
	phase := loaded.Clips[1].SourcePhase
	if phase == nil || phase.OffsetFrames != 1 || phase.Recipe.DurationFrames != clip.DurationFrames || phase.Recipe.AudioRange.End != 5777 {
		t.Fatal("保存重开未保留原处理输入与输出窗")
	}
}

func TestLegacyProjectsOmitExactAudioFieldsAndStillLoad(t *testing.T) {
	project := editor.NewProject("旧帧音频兼容")
	data, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"audioRange", "audioSampleCount", "audioSampleRate", "audioStartUs", "sourcePhase"} {
		if strings.Contains(string(data), field) {
			t.Fatal("未使用时新精确字段必须省略")
		}
	}
	path := filepath.Join(t.TempDir(), "legacy.aividcut")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal("旧schema不可读", err)
	}
}
