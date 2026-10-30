package localagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func TestLargeAcceptedTranscriptIsSummarizedWithoutInventingEmptyEvidence(t *testing.T) {
	p := editor.NewProject("长口播")
	asset := editor.Asset{ID: "video", Name: "口播", Kind: "video", Path: "/private/source.mp4", DurationUs: 2_000_000_000, Fingerprint: "sha256:" + strings.Repeat("a", 64), HasAudio: true}
	p.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("source", "track-video-1", "video")
	clip.AssetID = asset.ID
	clip.DurationFrames = 60_000
	p.Clips = []editor.Clip{clip}
	words := make([]editor.SourceWord, 10000)
	for i := range words {
		words[i] = editor.SourceWord{ID: fmt.Sprintf("w%d", i), StartUs: int64(i) * 100_000, EndUs: int64(i+1) * 100_000, Text: "仅原始语音记录不应复制到结构规划"}
	}
	p.Transcripts = []editor.Transcript{{ID: "transcript", AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, SourceDurationUs: asset.DurationUs, RawWords: words, AcceptedWords: append([]editor.SourceWord{}, words...), Revision: 1}}
	data, err := buildPrompt(PlanInput{Project: p, Selection: []string{clip.ID}, Instruction: "移动所选片段"}, Workflow{ID: "manual-plan"}, "key")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 256<<10 || bytes.Contains(data, []byte("仅原始语音记录")) {
		t.Fatal("未按结构任务限制原始词内容")
	}
	start := bytes.IndexByte(data, '{')
	var value map[string]any
	if err := json.Unmarshal(data[start:], &value); err != nil {
		t.Fatal(err)
	}
	coverage := value["coverage"].(map[string]any)
	if coverage["partial"] != true || coverage["acceptedWordsIncluded"] != false {
		t.Fatal("遗漏内容没有明确声明")
	}
	summaries := value["transcripts"].([]any)
	if len(summaries) != 1 || summaries[0].(map[string]any)["wordCount"] != float64(10000) {
		t.Fatal("把未内联词伪造成没有转写")
	}
}

func TestSequencePromptOmitsOtherTimelineContent(t *testing.T) {
	p := editor.NewProject("序列上下文")
	hidden := editor.DefaultClip("hidden", "track-text-1", "text")
	hidden.Text = "其它序列专用原文不得进入此任务"
	p.OtherSequences = []editor.Sequence{{ID: "other", Name: "其它版本", Settings: p.Settings, Tracks: p.Tracks, Clips: []editor.Clip{hidden}}}
	p.SequenceOrder = []string{editor.MainSequenceID, "other"}
	data, err := buildPrompt(PlanInput{Project: p, Instruction: "检查当前序列"}, Workflow{ID: "manual-plan"}, "key")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(hidden.Text)) || !bytes.Contains(data, []byte(`"otherSequencesOmitted":1`)) {
		t.Fatal("序列投影泄漏或没有标记省略")
	}
}
