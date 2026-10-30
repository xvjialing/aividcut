package editor

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func largeReadableEngine(tb testing.TB, wordCount int) (*Engine, Clip) {
	tb.Helper()
	project := NewProject("阅读组规模")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	duration := int64(wordCount) * 100_000
	project.Assets = []Asset{{ID: "large-source", Name: "large.mp4", Kind: "video", Path: "/fixture/large.mp4", DurationUs: duration + 1_000_000, HasAudio: true, Fingerprint: "sha256:large"}}
	source := DefaultClip("large-clip", "track-video-1", "video")
	source.AssetID = "large-source"
	source.DurationFrames = (duration*30000 + 1_000_000*1001 - 1) / (1_000_000 * 1001)
	project.Clips = []Clip{source}
	words := make([]SourceWord, wordCount)
	for i := range words {
		words[i] = SourceWord{ID: "large-word-" + strconv.Itoa(i), StartUs: int64(i) * 100_000, EndUs: int64(i+1) * 100_000, Text: "测试词条"}
	}
	transcript := Transcript{ID: "large-transcript", AssetID: source.AssetID, SourceFingerprint: "sha256:large", Origin: "asr", RawWords: words}
	engine, err := NewEngine(project)
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := engine.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "large-accept", Commands: []Command{{Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1", CaptionLayout: &CaptionLayout{Mode: "readable"}}}}); err != nil {
		tb.Fatal(err)
	}
	return engine, source
}

func TestReadableSupportsExisting100kWordLimitWithoutLosingBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式不运行100k词规模校验")
	}
	engine, _ := largeReadableEngine(t, MaxTranscriptWords)
	project := engine.Snapshot()
	seen := make(map[string]bool, MaxTranscriptWords)
	groups := 0
	for _, clip := range project.Clips {
		if clip.CaptionSource == nil {
			continue
		}
		groups++
		if len(clip.CaptionSource.WordIDs) < 2 {
			t.Fatal("规模fixture没有真正生成多词阅读组")
		}
		for _, id := range clip.CaptionSource.WordIDs {
			if seen[id] {
				t.Fatal("规模派生重复引用源词")
			}
			seen[id] = true
		}
		lines := strings.Split(clip.Text, "\n")
		if len(lines) > 2 {
			t.Fatal("规模派生超出行数")
		}
		for _, line := range lines {
			if visibleLineLength([]rune(line)) > 18 {
				t.Fatal("规模派生超出行宽")
			}
		}
	}
	if len(seen) != MaxTranscriptWords || groups >= MaxTranscriptWords {
		t.Fatalf("规模词绑定丢失或仍为逐词占位：words=%d groups=%d", len(seen), groups)
	}
	t.Logf("实际绑定%d词，派生%d条阅读字幕", len(seen), groups)
	if err := ValidateProject(project); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkMoveReadableBoundSource(b *testing.B) {
	for _, wordCount := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("words_%d", wordCount), func(b *testing.B) {
			engine, source := largeReadableEngine(b, wordCount)
			_, revision := engine.Identity()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				source.StartFrame = int64(i%2 + 1)
				if _, err := engine.Apply(EditRequest{BaseRevision: revision, IdempotencyKey: "readable-move-" + strconv.Itoa(i), Commands: []Command{{Type: "update_clip", Clip: &source}}}); err != nil {
					b.Fatal(err)
				}
				revision++
			}
		})
	}
}
