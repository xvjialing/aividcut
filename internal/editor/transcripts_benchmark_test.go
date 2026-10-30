package editor

import (
	"fmt"
	"strconv"
	"testing"
)

// 基准仅测工程核心，不含 ASR、媒体解码、前端渲染或 Wails 通信。
// 29.97fps 连续 100ms 词条会产生分数帧交界，实际派生为两条字幕 lane。
func benchmarkBoundSource(b *testing.B, wordCount int) (*Engine, Clip) {
	b.Helper()
	project := NewProject("长字幕核心基准")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	duration := int64(wordCount) * 100_000
	project.Assets = []Asset{{ID: "benchmark-source", Name: "speech.mp4", Kind: "video", Path: "/benchmark/speech.mp4", DurationUs: duration + 1_000_000, HasAudio: true, Fingerprint: "benchmark-source-fingerprint"}}
	source := DefaultClip("benchmark-clip", "track-video-1", "video")
	source.AssetID = project.Assets[0].ID
	source.DurationFrames = (duration*30_000 + 1_000_000*1001 - 1) / (1_000_000 * 1001)
	project.Clips = []Clip{source}
	engine, err := NewEngine(project)
	if err != nil {
		b.Fatal(err)
	}
	words := make([]SourceWord, wordCount)
	for i := range words {
		words[i] = SourceWord{ID: "word-" + strconv.Itoa(i), StartUs: int64(i) * 100_000, EndUs: int64(i+1) * 100_000, Text: "测试词条"}
	}
	transcript := Transcript{ID: "benchmark-transcript", AssetID: source.AssetID, SourceFingerprint: project.Assets[0].Fingerprint, RawWords: words}
	result, err := engine.Apply(EditRequest{BaseRevision: 0, IdempotencyKey: "accept-and-bind", Commands: []Command{{Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"}}})
	if err != nil {
		b.Fatal(err)
	}
	derived := 0
	lanes := map[string]bool{}
	for _, clip := range result.Clips {
		if clip.CaptionSource != nil {
			derived++
			lanes[clip.TrackID] = true
		}
	}
	if derived != wordCount {
		b.Fatalf("需要 %d 条实际绑定字幕，得到 %d", wordCount, derived)
	}
	if len(lanes) != 2 {
		b.Fatalf("基准要求实际使用两条字幕 lane，得到 %d", len(lanes))
	}
	return engine, source
}

// 包含统一命令、重新派生、校验、深拷贝返回及正常撤销历史记录的分配。
// 固定次数运行可避免不同机器的自动校准令撤销历史长度不一致。
func BenchmarkMoveBoundSource(b *testing.B) {
	for _, wordCount := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("words_%d", wordCount), func(b *testing.B) {
			engine, source := benchmarkBoundSource(b, wordCount)
			revision := int64(1)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				source.StartFrame = int64(i%2 + 1)
				_, err := engine.Apply(EditRequest{BaseRevision: revision, IdempotencyKey: "move-" + strconv.Itoa(i), Commands: []Command{{Type: "update_clip", Clip: &source}}})
				if err != nil {
					b.Fatal(err)
				}
				revision++
			}
		})
	}
}

func BenchmarkValidateBoundProject(b *testing.B) {
	for _, wordCount := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("words_%d", wordCount), func(b *testing.B) {
			engine, _ := benchmarkBoundSource(b, wordCount)
			project := engine.Snapshot()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := ValidateProject(project); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
