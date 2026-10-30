package contentindex

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func fixture() editor.Project {
	p := editor.NewProject("检索")
	p.Assets = []editor.Asset{{ID: "asset", Name: "访谈", Kind: "video", Fingerprint: "sha256:source", DurationUs: 30_000_000}}
	p.Transcripts = []editor.Transcript{{ID: "transcript", Revision: 2, AssetID: "asset", SourceFingerprint: "sha256:source", SourceDurationUs: 30_000_000, Origin: "asr", AcceptedWords: []editor.SourceWord{
		{ID: "one", Text: "你好", StartUs: 0, EndUs: 1_000_000}, {ID: "two", Text: "世界", StartUs: 1_000_000, EndUs: 2_000_000}, {ID: "three", Text: "Hello", StartUs: 2_000_000, EndUs: 3_000_000}, {ID: "four", Text: "World", StartUs: 3_000_000, EndUs: 4_000_000},
	}}}
	return p
}
func TestAcrossWordsSourceEvidenceUnicodeAndCase(t *testing.T) {
	p := fixture()
	before, _ := json.Marshal(p)
	index, err := Build(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		text       string
		ids        []string
		start, end int64
	}{{"好世界", []string{"one", "two"}, 0, 2_000_000}, {"hello world", []string{"three", "four"}, 2_000_000, 4_000_000}} {
		result, err := index.Search(context.Background(), Query{Text: q.text})
		if err != nil || result.Total != 1 || len(result.Hits) != 1 {
			t.Fatalf("未跨词命中: %+v %v", result, err)
		}
		h := result.Hits[0]
		if !reflect.DeepEqual(h.WordIDs, q.ids) || h.StartUs != q.start || h.EndUs != q.end || h.TranscriptRevision != 2 || !strings.Contains(h.Text, "Hello World") {
			t.Fatalf("时间或原文证据不一致: %+v", h)
		}
	}
	after, _ := json.Marshal(p)
	if string(after) != string(before) {
		t.Fatal("检索改变源工程")
	}
	p.Transcripts[0].AcceptedWords[0].Text = "后改"
	r, _ := index.Search(context.Background(), Query{Text: "你好"})
	if r.Total != 1 {
		t.Fatal("索引别名被调用方修改")
	}
}
func TestSpeakerGapOriginsAndOverlappingEnd(t *testing.T) {
	p := fixture()
	p.Transcripts[0].AcceptedWords = []editor.SourceWord{{ID: "a", Text: "甲", StartUs: 0, EndUs: 8_000_000}, {ID: "b", Text: "乙", StartUs: 1_000_000, EndUs: 2_000_000}, {ID: "c", Text: "丙", StartUs: 6_000_000, EndUs: 7_000_000}, {ID: "d", Text: "丁", StartUs: 7_000_000, EndUs: 8_000_000, SpeakerID: "other"}}
	spoken := p.Transcripts[0]
	spoken.ID = "generated"
	spoken.Origin = "tts"
	p.Transcripts = append(p.Transcripts, spoken)
	index, err := Build(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := index.Search(context.Background(), Query{Text: "甲乙", Origin: "asr"})
	if r.Total != 1 || r.Hits[0].EndUs != 8_000_000 || r.Hits[0].Origin != "asr" {
		t.Fatal("重叠词结尾或来源丢失")
	}
	for _, text := range []string{"乙丙", "丙丁"} {
		r, _ := index.Search(context.Background(), Query{Text: text})
		if r.Total != 0 {
			t.Fatal("跨长停顿或说话人拼成短语")
		}
	}
	r, _ = index.Search(context.Background(), Query{Text: "甲乙", Origin: "tts"})
	if r.Total != 1 || r.Hits[0].TranscriptID != "generated" {
		t.Fatal("配音冒充原话")
	}
}
func TestStaleMissingFingerprintDurationPaginationAndCancel(t *testing.T) {
	p := fixture()
	for i := 0; i < 4; i++ {
		v := p.Transcripts[0]
		v.ID = fmt.Sprint("bad", i)
		switch i {
		case 0:
			v.Stale = true
		case 1:
			v.SourceFingerprint = "changed"
		case 2:
			v.SourceDurationUs--
		case 3:
			v.AssetID = "missing"
		}
		p.Transcripts = append(p.Transcripts, v)
	}
	index, err := Build(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := index.Search(context.Background(), Query{Text: "世界", Offset: 1, Limit: 1})
	if r.Total != 1 || len(r.Hits) != 0 || r.Stats.SkippedStale != 3 || r.Stats.SkippedMissing != 1 {
		t.Fatalf("陈旧或分页证据错误: %+v", r)
	}
	for _, q := range []Query{{Text: ""}, {Text: strings.Repeat("字", 101)}, {Text: "你好", Origin: "vision"}, {Text: "你好", Offset: -1}, {Text: "你好", Limit: 101}} {
		if _, err := index.Search(context.Background(), q); err == nil {
			t.Fatal("无效查询被接受")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, p, ""); err == nil {
		t.Fatal("取消未生效")
	}
	if _, err := index.Search(ctx, Query{Text: "世界"}); err == nil {
		t.Fatal("搜索忽略取消")
	}
}
func TestParagraphIDsAndLongWordExcerptIncludeActualMatch(t *testing.T) {
	p := fixture()
	p.Transcripts[0].AcceptedWords = []editor.SourceWord{{ID: "a", Text: "原话", StartUs: 0, EndUs: 1_000_000}, {ID: "b", Text: strings.Repeat("前", 5000) + "Needle" + strings.Repeat("后", 5000), StartUs: 5_000_000, EndUs: 6_000_000}, {ID: "c", Text: "原话", StartUs: 10_000_000, EndUs: 11_000_000}}
	index, err := Build(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := index.Search(context.Background(), Query{Text: "原话"})
	if r.Total != 2 || r.Hits[0].ID == r.Hits[1].ID {
		t.Fatal("不同段落结果ID碰撞")
	}
	r, _ = index.Search(context.Background(), Query{Text: "needle"})
	if r.Total != 1 || !strings.Contains(r.Hits[0].Text, "Needle") || len([]rune(r.Hits[0].Text)) > 242 {
		t.Fatal("长词上下文遗漏真实命中或超过界限")
	}
}
func TestHundredThousandWordsBoundedPage(t *testing.T) {
	p := fixture()
	p.Assets[0].DurationUs = 200_000_000
	p.Transcripts[0].SourceDurationUs = 200_000_000
	p.Transcripts[0].AcceptedWords = nil
	for i := 0; i < 100_000; i++ {
		p.Transcripts[0].AcceptedWords = append(p.Transcripts[0].AcceptedWords, editor.SourceWord{ID: fmt.Sprint(i), Text: "检索", StartUs: int64(i) * 1000, EndUs: int64(i+1) * 1000})
	}
	index, err := Build(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := index.Search(context.Background(), Query{Text: "检索", Offset: 99960, Limit: 40})
	if err != nil || r.Total != 100000 || len(r.Hits) != 40 || r.Hits[39].WordIDs[0] != "99999" {
		t.Fatal("大转写检索或分页丢失证据")
	}
}

func TestLegacyOriginIsNotSilentlyClaimedAsOriginalSpeech(t *testing.T) {
	p := fixture()
	p.Transcripts[0].Origin = ""
	index, err := Build(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	all, _ := index.Search(context.Background(), Query{Text: "你好"})
	if all.Total != 1 || all.Hits[0].Origin != "" {
		t.Fatal("旧版未知来源被伪报为原话")
	}
	spoken, _ := index.Search(context.Background(), Query{Text: "你好", Origin: "asr"})
	if spoken.Total != 0 {
		t.Fatal("原话筛选混入未知来源")
	}
}

func TestEmptyIndexHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, editor.NewProject("空工程"), ""); err == nil {
		t.Fatal("空工程构建忽略取消")
	}
	index, _ := Build(context.Background(), editor.NewProject("空工程"), "")
	if _, err := index.Search(ctx, Query{Text: "词"}); err == nil {
		t.Fatal("空索引检索忽略取消")
	}
}

func TestAcceptedVisualNotesSearchTagsAndPreservePointEvidence(t *testing.T) {
	p := fixture()
	asset := p.Assets[0]
	frame := editor.SourceFrameEvidence{AssetID: asset.ID, AssetName: asset.Name, SourceFingerprint: "sha256:" + strings.Repeat("a", 64), ImageSHA256: strings.Repeat("b", 64), PositionUs: 500000, RequestedPositionUs: 500000, Width: 320, Height: 180, Profile: "overview", RecipeVersion: "overview-jpeg-v1"}
	frame.ID = editor.SourceFrameEvidenceID(frame)
	p.Assets[0].Fingerprint = frame.SourceFingerprint
	p.Transcripts = nil
	p.VisualAnalyses = []editor.VisualAnalysis{{ID: "visual", AssetID: asset.ID, SourceKind: "video", SourceFingerprint: frame.SourceFingerprint, SourceDurationUs: asset.DurationUs, Producer: "external-mcp", Frames: []editor.SourceFrameEvidence{frame}, Notes: []editor.VisualNote{{ID: "note", Kind: "inference", Text: "可能是海边场景（索引夹具）", Tags: []string{"相机"}, EvidenceIDs: []string{frame.ID}}}}}
	index, err := Build(context.Background(), p, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := index.Search(context.Background(), Query{Text: "相机", Origin: "visual"})
	if err != nil || result.Total != 1 || result.Hits[0].NoteKind != "inference" || result.Hits[0].StartUs != 500000 || result.Hits[0].EndUs != 500000 || len(result.Hits[0].Frames) != 1 {
		t.Fatal("画面记录被伪造为连续区间或丢失类别", err)
	}
	p.VisualAnalyses[0].Stale = true
	index, _ = Build(context.Background(), p, "")
	result, _ = index.Search(context.Background(), Query{Text: "相机"})
	if result.Total != 0 || result.Stats.SkippedStale != 1 {
		t.Fatal("失效分析仍进入检索")
	}
}
