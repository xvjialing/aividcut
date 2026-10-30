package workflows

import (
	"aividcut/internal/editor"
	"reflect"
	"strings"
	"testing"
)

func wordCutProject(t *testing.T) editor.Project {
	t.Helper()
	p := editor.NewProject("按文字删减夹具")
	p.Settings.FPSNum, p.Settings.FPSDen = 30, 1
	p.Assets = []editor.Asset{{ID: "v", Name: "源.mp4", Kind: "video", Path: "/fixture/v.mp4", Fingerprint: "sha256:v", DurationUs: 8_000_000, Width: 320, Height: 180, HasAudio: true}, {ID: "a", Name: "源.wav", Kind: "audio", Path: "/fixture/a.wav", Fingerprint: "sha256:a", DurationUs: 8_000_000, HasAudio: true}}
	v := editor.DefaultClip("v1", "track-video-1", "video")
	v.AssetID = "v"
	v.DurationFrames = 120
	v.LinkGroup = "av"
	v.Muted = true
	a := editor.DefaultClip("a1", "track-audio-1", "audio")
	a.AssetID = "a"
	a.DurationFrames = 120
	a.LinkGroup = "av"
	repeat := v
	repeat.ID = "v2"
	repeat.StartFrame = 180
	repeat.LinkGroup = ""
	p.Clips = []editor.Clip{v, a, repeat}
	tr := editor.Transcript{ID: "t", AssetID: "v", SourceFingerprint: "sha256:v", Origin: "asr", RawWords: []editor.SourceWord{{ID: "w1", Text: "第一句。", StartUs: 0, EndUs: 800_000}, {ID: "w2", Text: "嗯。", StartUs: 1_000_000, EndUs: 1_500_000}, {ID: "w3", Text: "保留。", StartUs: 2_000_000, EndUs: 2_600_000}, {ID: "w4", Text: "末句。", StartUs: 3_000_000, EndUs: 4_000_000}}}
	e, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	p, err = e.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "accept", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &tr}, {Type: "generate_bound_captions", TranscriptID: "t", TrackID: "track-caption-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func wordCutInput(p editor.Project, ids ...string) TranscriptCutInput {
	return TranscriptCutInput{ProjectID: p.ID, ProjectRevision: p.Revision, TranscriptID: "t", TranscriptRevision: p.Transcripts[0].Revision, ClipID: "v1", WordIDs: ids, CloseGap: true}
}
func TestTranscriptCutAllEdgesLinkedInstancesAndUndo(t *testing.T) {
	for _, test := range []struct {
		name               string
		ids                []string
		left, right, start int64
		close              bool
	}{
		{"中删闭合", []string{"w2"}, 30, 75, 30, true}, {"中删留隙", []string{"w2"}, 30, 75, 45, false}, {"首删闭合", []string{"w1"}, 0, 96, 0, true}, {"首删留隙", []string{"w1"}, 0, 96, 24, false}, {"尾删", []string{"w4"}, 90, 0, 0, true}, {"全删", []string{"w1", "w2", "w3", "w4"}, 0, 0, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := wordCutProject(t)
			original := editor.CloneProject(before)
			input := wordCutInput(before, test.ids...)
			input.CloseGap = test.close
			plan, err := PreviewTranscriptCut(before, input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, original) {
				t.Fatal("预览改变工程")
			}
			engine, after := applyAlgorithm(t, before, plan)
			if !reflect.DeepEqual(after.Transcripts, before.Transcripts) {
				t.Fatal("媒体删减改变原始/接受文字")
			}
			if !reflect.DeepEqual(clipWithID(after, "v2"), clipWithID(before, "v2")) {
				t.Fatal("改动同素材其它实例")
			}
			if test.left > 0 && clipWithID(after, "v1").DurationFrames != test.left {
				t.Fatal("左保留范围错误")
			}
			count := 0
			for _, clip := range after.Clips {
				if (clip.Kind == "audio" || clip.Kind == "video") && clip.ID != "v2" {
					count++
					if test.right > 0 && (test.left == 0 || clip.ID != "v1" && clip.ID != "a1") {
						if clip.StartFrame != test.start || clip.DurationFrames != test.right {
							t.Fatalf("右保留范围错误: %+v", clip)
						}
					}
				}
			}
			expected := 0
			if test.left > 0 {
				expected += 2
			}
			if test.right > 0 {
				expected += 2
			}
			if count != expected {
				t.Fatalf("音画成员数 %d != %d", count, expected)
			}
			undone, err := engine.Undo()
			if err != nil || !reflect.DeepEqual(noRevision(undone), noRevision(before)) {
				t.Fatal("不能一次撤销", err)
			}
			redone, err := engine.Redo()
			if err != nil || !reflect.DeepEqual(noRevision(redone), noRevision(after)) {
				t.Fatal("重做不一致", err)
			}
		})
	}
}
func TestTranscriptCutProtectsCaptionManualStyle(t *testing.T) {
	p := wordCutProject(t)
	e, _ := editor.NewEngine(p)
	for _, clip := range p.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.SourceClipID == "v1" && strings.Contains(clip.Text, "保留") {
			clip.Text = "人工保留"
			clip.TextStyle.FontSize = 72
			var err error
			p, err = e.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "manual", Commands: []editor.Command{{Type: "update_clip", Clip: &clip}}})
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	plan, err := PreviewTranscriptCut(p, wordCutInput(p, "w2"))
	if err != nil {
		t.Fatal(err)
	}
	_, after := applyAlgorithm(t, p, plan)
	found := false
	for _, clip := range after.Clips {
		if clip.Text == "人工保留" && clip.TextStyle.FontSize == 72 && clip.CaptionSource != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("右保留源丢失字幕手改")
	}
	for i := range p.Tracks {
		if p.Tracks[i].ID == "track-audio-1" {
			p.Tracks[i].Locked = true
		}
	}
	if _, err = PreviewTranscriptCut(p, wordCutInput(p, "w2")); err == nil {
		t.Fatal("linked锁轨仍可删减")
	}
}
func TestTranscriptWordsRejectStaleAndNoncontiguous(t *testing.T) {
	p := wordCutProject(t)
	for _, ids := range [][]string{{}, {"w1", "w3"}, {"w2", "w2"}, {"missing"}, {"w1", "missing"}} {
		if _, err := LocateTranscriptWords(p, wordCutInput(p, ids...)); err == nil {
			t.Fatalf("错误词选择被接受 %v", ids)
		}
	}
	in := wordCutInput(p, "w2")
	in.ProjectRevision--
	if _, err := LocateTranscriptWords(p, in); err == nil {
		t.Fatal("旧工程通过")
	}
	in = wordCutInput(p, "w2")
	in.TranscriptRevision--
	if _, err := LocateTranscriptWords(p, in); err == nil {
		t.Fatal("旧转写通过")
	}
	p.Transcripts[0].Stale = true
	if _, err := LocateTranscriptWords(p, wordCutInput(p, "w2")); err == nil {
		t.Fatal("陈旧源通过")
	}
}

func TestTranscriptCutRejectsPartialProtectedReadingCaption(t *testing.T) {
	p := wordCutProject(t)
	engine, err := editor.NewEngine(p)
	if err != nil {
		t.Fatal(err)
	}
	words := append([]editor.SourceWord{}, p.Transcripts[0].AcceptedWords...)
	for i := range words {
		words[i].Text = strings.TrimSuffix(words[i].Text, "。")
	}
	p, err = engine.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "joined-reading", Commands: []editor.Command{{Type: "correct_transcript", TranscriptID: "t", TranscriptRevision: p.Transcripts[0].Revision, Words: words}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err = engine.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "reading", Commands: []editor.Command{{Type: "generate_bound_captions", TranscriptID: "t", TrackID: "track-caption-1", CaptionLayout: &editor.CaptionLayout{Mode: "readable", MaxCharsPerLine: 32, MaxLines: 2, MaxDurationUs: 5_000_000, PauseThresholdUs: 5_000_000}}}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, clip := range p.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.SourceClipID == "v1" && len(clip.CaptionSource.WordIDs) > 1 {
			clip.Text = "不能静默拆分的人工整句"
			p, err = engine.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "protected", Commands: []editor.Command{{Type: "update_clip", Clip: &clip}}})
			if err != nil {
				t.Fatal(err)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("没有构造跨切口的手改阅读字幕")
	}
	before := editor.CloneProject(p)
	if _, err = PreviewTranscriptCut(p, wordCutInput(p, "w2")); err == nil {
		t.Fatal("拆分受保护人工整句仍通过")
	}
	if !reflect.DeepEqual(before, p) {
		t.Fatal("失败改变工程")
	}
}
func TestTranscriptWordsRationalMappingPartialAndNeighborWarning(t *testing.T) {
	p := wordCutProject(t)
	p.Settings.FPSNum, p.Settings.FPSDen = 30000, 1001
	p.Clips[0].StartFrame = 10
	p.Clips[0].SourceInUs = 1_100_000
	p.Clips[0].SpeedNum = 3
	p.Clips[0].SpeedDen = 2
	in := wordCutInput(p, "w2")
	selection, err := LocateTranscriptWords(p, in)
	if err != nil {
		t.Fatal(err)
	}
	span, ok, err := editor.MapWordToClipWithAsset(p.Transcripts[0].AcceptedWords[1], p.Clips[0], p.Assets[0], p.Settings)
	if err != nil || !ok || selection.StartFrame != span.StartFrame || selection.EndFrame != span.StartFrame+span.DurationFrames || len(selection.Warnings) == 0 {
		t.Fatal("变速非零入点/部分词映射错误", selection, err)
	}
	p = wordCutProject(t)
	p.Transcripts[0].AcceptedWords[1].EndUs = 1_500_001
	p.Transcripts[0].AcceptedWords[2].StartUs = 1_500_001
	selection, err = LocateTranscriptWords(p, wordCutInput(p, "w2"))
	if err != nil || !strings.Contains(strings.Join(selection.Warnings, ""), "邻词") {
		t.Fatal("未提示邻词帧量化", selection, err)
	}
}
