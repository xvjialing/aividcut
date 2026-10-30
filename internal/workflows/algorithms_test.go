package workflows

import (
	"reflect"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func algorithmProject(t *testing.T, music bool) editor.Project {
	t.Helper()
	project := editor.NewProject("真实编辑工作流fixture")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	project.Assets = []editor.Asset{
		{ID: "source-video", Name: "主口播.mp4", Kind: "video", Path: "/fixture/source.mp4", DurationUs: 20_000_000, Width: 1920, Height: 1080, HasAudio: true, Fingerprint: "sha256:source-video"},
		{ID: "source-audio", Name: "主口播.wav", Kind: "audio", Path: "/fixture/source.wav", DurationUs: 20_000_000, HasAudio: true, Fingerprint: "sha256:source-audio"},
		{ID: "visual", Name: "已导入补充画面.png", Kind: "image", Path: "/fixture/broll.png", Width: 1920, Height: 1080, Fingerprint: "sha256:visual"},
		{ID: "voice", Name: "已保存配音.mp3", Kind: "audio", Path: "/fixture/voice.mp3", DurationUs: 1_500_001, HasAudio: true, Fingerprint: "sha256:voice"},
	}
	video := editor.DefaultClip("video", "track-video-1", "video")
	video.AssetID, video.DurationFrames, video.LinkGroup = "source-video", 180, "av"
	audio := editor.DefaultClip("audio", "track-audio-1", "audio")
	audio.AssetID, audio.DurationFrames, audio.LinkGroup = "source-audio", 180, "av"
	project.Clips = []editor.Clip{video, audio}
	if music {
		project.Tracks = append(project.Tracks, editor.Track{ID: "music-track", Name: "未选配乐", Kind: "audio", Locked: true})
		bgm := editor.DefaultClip("music", "music-track", "audio")
		bgm.AssetID, bgm.DurationFrames, bgm.Volume = "source-audio", 300, 0.3
		project.Clips = append(project.Clips, bgm)
	}
	transcript := editor.Transcript{ID: "accepted-source", AssetID: "source-video", SourceFingerprint: "sha256:source-video", Language: "zh", RawWords: []editor.SourceWord{
		{ID: "word-1", StartUs: 100_000, EndUs: 500_000, Text: "第一词", SpeakerID: "speaker"},
		{ID: "word-2", StartUs: 2_500_000, EndUs: 3_000_000, Text: "第二句。", SpeakerID: "speaker"},
		{ID: "word-3", StartUs: 5_000_000, EndUs: 5_500_000, Text: "第三词", SpeakerID: "speaker"},
	}}
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	project, err = engine.Apply(editor.EditRequest{BaseRevision: 0, IdempotencyKey: "accept-source", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range project.Clips {
		if value.CaptionSource != nil && value.CaptionSource.WordIDs[0] == "word-2" {
			value.Text, value.TextStyle.FontSize, value.Transform.Y = "人工第二句", 68, -123
			project, err = engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "manual-caption", Commands: []editor.Command{{Type: "update_clip", Clip: &value}}})
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	return project
}

func applyAlgorithm(t *testing.T, project editor.Project, plan Plan) (*editor.Engine, editor.Project) {
	t.Helper()
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	after, err := engine.Apply(plan.Request)
	if err != nil {
		t.Fatal(err)
	}
	return engine, after
}
func clipWithID(project editor.Project, id string) editor.Clip {
	for _, clip := range project.Clips {
		if clip.ID == id {
			return clip
		}
	}
	return editor.Clip{}
}
func noRevision(project editor.Project) editor.Project { project.Revision = 0; return project }

func TestTalkingHeadActuallyCutsLinkedSourcesAndKeepsCaptionOverrides(t *testing.T) {
	before := algorithmProject(t, true)
	untouched := editor.CloneProject(before)
	plan, err := Preview(before, Input{WorkflowID: "talking-head", Selection: []string{"video"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, untouched) {
		t.Fatal("预览修改了调用方工程")
	}
	engine, after := applyAlgorithm(t, before, plan)
	if plan.SavedFrames != 0 || !strings.Contains(plan.Summary, "94帧") {
		t.Fatalf("有配乐长尾时不能把linked双计或谎报工程缩短：%d %s", plan.SavedFrames, plan.Summary)
	}
	if !strings.Contains(strings.Join(plan.Warnings, " "), "工程总长仅减少0") {
		t.Fatal("没有明确说明选区压缩与工程总长差异")
	}
	if !reflect.DeepEqual(clipWithID(before, "music"), clipWithID(after, "music")) {
		t.Fatal("口播粗剪改动了未选配乐或其锁轨")
	}
	videoCount, audioCount := 0, 0
	videoEnd := int64(0)
	for _, clip := range after.Clips {
		if clip.AssetID == "source-video" {
			videoCount++
			videoEnd = max(videoEnd, clip.StartFrame+clip.DurationFrames)
		}
		if clip.AssetID == "source-audio" && clip.ID != "music" {
			audioCount++
			matching := false
			for _, visual := range after.Clips {
				if visual.Kind == "video" && visual.LinkGroup == clip.LinkGroup && visual.StartFrame == clip.StartFrame && visual.DurationFrames == clip.DurationFrames {
					matching = true
				}
			}
			if !matching {
				t.Fatal("切剪后音画链接或帧区间不同步")
			}
		}
	}
	if videoCount != 3 || audioCount != 3 || videoEnd != 86 {
		t.Fatalf("真实保留段没有压缩成预期帧范围：video=%d audio=%d end=%d", videoCount, audioCount, videoEnd)
	}
	manualFound := false
	for _, clip := range after.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.WordIDs[0] == "word-2" {
			manualFound = true
			if clip.CaptionSource.SourceClipID == "video" || clip.Text != "人工第二句" || clip.TextStyle.FontSize != 68 || clip.Transform.Y != -123 || !clip.CaptionSource.TextOverride || !clip.CaptionSource.StyleOverride {
				t.Fatal("跨gap保留段丢失明确父源的人工字幕覆盖")
			}
		}
	}
	if !manualFound || !reflect.DeepEqual(before.Transcripts[0].RawWords, after.Transcripts[0].RawWords) {
		t.Fatal("没有保留绑定字幕或改写原始识别")
	}
	undone, err := engine.Undo()
	if err != nil || !reflect.DeepEqual(noRevision(before), noRevision(undone)) {
		t.Fatal("工作流不是整批可撤销编辑", err)
	}
	redone, err := engine.Redo()
	if err != nil || !reflect.DeepEqual(noRevision(after), noRevision(redone)) {
		t.Fatal("工作流重做未恢复同一组实际编辑", err)
	}
}

func TestTalkingHeadSavedFramesUsesFinalTimelineAndPreciseWordEvidence(t *testing.T) {
	project := algorithmProject(t, false)
	plan, err := Preview(project, Input{WorkflowID: "talking-head", Selection: []string{"audio", "video"}})
	if err != nil {
		t.Fatal(err)
	}
	_, after := applyAlgorithm(t, project, plan)
	if plan.SavedFrames != 94 || editor.DurationFrames(project)-editor.DurationFrames(after) != 94 {
		t.Fatal("重复选择linked音画造成帧数双计")
	}
	gaps := []Evidence{}
	for _, evidence := range plan.Evidence {
		if evidence.Kind == "accepted-word-gap" {
			gaps = append(gaps, evidence)
			if evidence.SourceFingerprint != "sha256:source-video" || evidence.TranscriptID != "accepted-source" || len(evidence.WordIDs) != 2 {
				t.Fatal("缺少当前接受词和源指纹证据")
			}
		}
	}
	if len(gaps) != 2 || gaps[0].StartFrame != 20 || gaps[0].EndFrame != 70 || gaps[1].StartFrame != 98 || gaps[1].EndFrame != 142 {
		t.Fatalf("29.97fps内缩保护/句末保留换算不正确：%+v", gaps)
	}
}

func TestTalkingHeadRejectsRelevantLocksAndOverlappingGroups(t *testing.T) {
	for _, trackID := range []string{"track-video-1", "track-audio-1", "track-caption-1"} {
		t.Run(trackID, func(t *testing.T) {
			project := algorithmProject(t, false)
			for i := range project.Tracks {
				if project.Tracks[i].ID == trackID {
					project.Tracks[i].Locked = true
				}
			}
			if _, err := Preview(project, Input{WorkflowID: "talking-head", Selection: []string{"video"}}); err == nil {
				t.Fatal("相关源/链接/隐式字幕锁没有阻止粗剪")
			}
		})
	}
	project := algorithmProject(t, false)
	other := clipWithID(project, "video")
	other.ID, other.LinkGroup, other.TrackID = "other-video", "", "other-video-track"
	project.Tracks = append(project.Tracks, editor.Track{ID: other.TrackID, Name: "另一并行源", Kind: "video"})
	project.Clips = append(project.Clips, other)
	if _, err := Preview(project, Input{WorkflowID: "talking-head", Selection: []string{"video", other.ID}}); err == nil {
		t.Fatal("不支持的并行源不应猜测主口播并删减")
	}
}

func TestTalkingHeadPreservesSpeakerTurnAndUnacceptedRecognizedContent(t *testing.T) {
	project := algorithmProject(t, false)
	project.Transcripts[0].AcceptedWords[1].SpeakerID = "other-speaker"
	project.Transcripts[0].AcceptedWords[2].SpeakerID = "other-speaker"
	plan, err := Preview(project, Input{WorkflowID: "talking-head", Selection: []string{"video"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SavedFrames != 44 {
		t.Fatal("说话人切换停顿被自动压缩")
	}
	project = algorithmProject(t, false)
	project.Transcripts[0].RawWords = append(project.Transcripts[0].RawWords, editor.SourceWord{ID: "not-accepted", StartUs: 1_000_000, EndUs: 1_200_000, Text: "原始识别但未接受的内容"})
	plan, err = Preview(project, Input{WorkflowID: "talking-head", Selection: []string{"video"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SavedFrames != 44 || !strings.Contains(strings.Join(plan.Warnings, " "), "未接受原始词") {
		t.Fatal("把已识别但未接受的实际词误当静音删掉")
	}
}

func TestCaptionStyleWorkflowChangesRealClipsKeepsTextSourceAndUndo(t *testing.T) {
	before := algorithmProject(t, false)
	plan, err := Preview(before, Input{WorkflowID: "captions", Selection: []string{"video"}, Parameters: map[string]any{"fontSize": float64(72), "color": "#ffcc00", "background": "#00000099", "align": "left", "offsetX": float64(20), "offsetY": float64(-50)}})
	if err != nil {
		t.Fatal(err)
	}
	engine, after := applyAlgorithm(t, before, plan)
	count := 0
	for _, clip := range after.Clips {
		if clip.Kind != "caption" {
			continue
		}
		count++
		old := clipWithID(before, clip.ID)
		if clip.Text != old.Text || clip.TextStyle.FontSize != 72 || clip.TextStyle.Color != "#ffcc00" || clip.Transform.X != 20 || clip.Transform.Y != -50 || clip.CaptionSource == nil || clip.CaptionSource.TranscriptID != old.CaptionSource.TranscriptID {
			t.Fatal("样式工作流丢失文本/绑定或只是名字占位")
		}
	}
	if count != 3 || plan.SavedFrames != 0 {
		t.Fatal("字幕整理范围或时长改变错误")
	}
	undone, err := engine.Undo()
	if err != nil || !reflect.DeepEqual(noRevision(before), noRevision(undone)) {
		t.Fatal("样式工作流不能整批撤销", err)
	}
}

func TestBrollCreatesVisibleMutedLayerWithoutEditingOriginalPictureSound(t *testing.T) {
	before := algorithmProject(t, false)
	plan, err := Preview(before, Input{WorkflowID: "broll", Parameters: map[string]any{"assetId": "visual", "startFrame": 30, "durationFrames": 30}})
	if err != nil {
		t.Fatal(err)
	}
	_, after := applyAlgorithm(t, before, plan)
	var insert editor.Clip
	for _, clip := range after.Clips {
		if clip.AssetID == "visual" {
			insert = clip
		}
	}
	if insert.ID == "" || !insert.Muted || insert.StartFrame != 30 || insert.DurationFrames != 30 {
		t.Fatal("补充画面没有按明确范围创建静音片段")
	}
	index := func(id string) int {
		for i, track := range after.Tracks {
			if track.ID == id {
				return i
			}
		}
		return -1
	}
	if index(insert.TrackID) >= index("track-video-1") {
		t.Fatal("新增B-roll会被原视频遮住，没有真正可见的上层轨")
	}
	if !reflect.DeepEqual(clipWithID(before, "video"), clipWithID(after, "video")) || !reflect.DeepEqual(clipWithID(before, "audio"), clipWithID(after, "audio")) {
		t.Fatal("B-roll插入删除或改动原音画")
	}
}

func TestVoiceoverUsesImportedDurationAndAcceptedWordTimesOnly(t *testing.T) {
	project := algorithmProject(t, false)
	plan, err := Preview(project, Input{WorkflowID: "voiceover", Parameters: map[string]any{"assetId": "voice", "startFrame": 200}})
	if err != nil {
		t.Fatal(err)
	}
	_, after := applyAlgorithm(t, project, plan)
	var voice editor.Clip
	for _, clip := range after.Clips {
		if clip.AssetID == "voice" {
			voice = clip
		}
	}
	if voice.DurationFrames != 45 || voice.StartFrame != 200 || plan.SavedFrames != -65 || voice.AudioRange == nil || voice.AudioRange.Unit != "us" || voice.AudioRange.End != 1_500_001 {
		t.Fatal("配音长度不是来自真实音频时长，或工程延长被误报为节省")
	}
	resolved, err := editor.ResolveAudioRange(voice, project.Assets[3], project.Settings)
	if err != nil || resolved.SourceEnd != (editor.AudioTime{Num: 1_500_001, Den: 1}) || resolved.MeasuredSamples {
		t.Fatal("声明微秒末端被帧占位延长或伪造成实测samples", err)
	}
	if _, err := Preview(project, Input{WorkflowID: "voiceover", Parameters: map[string]any{"assetId": "voice", "generateCaptions": true}}); err == nil {
		t.Fatal("没有词时间却按文案猜字幕")
	}
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	transcript := editor.Transcript{ID: "voice-transcript", AssetID: "voice", SourceFingerprint: "sha256:voice", RawWords: []editor.SourceWord{{ID: "voice-word-1", StartUs: 0, EndUs: 500_000, Text: "旁白一"}, {ID: "voice-word-2", StartUs: 800_000, EndUs: 1_300_000, Text: "旁白二"}}}
	project, err = engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "accepted-voice", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &transcript}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = Preview(project, Input{WorkflowID: "voiceover", Parameters: map[string]any{"assetId": "voice", "startFrame": 200, "generateCaptions": true, "transcriptId": transcript.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, after = applyAlgorithm(t, project, plan)
	bound := 0
	for _, clip := range after.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.TranscriptID == transcript.ID {
			bound++
			if clip.StartFrame < 200 || clip.StartFrame+clip.DurationFrames > 244 {
				t.Fatal("配音字幕没有跟随实际词时间映射")
			}
		}
	}
	if bound != 2 {
		t.Fatal("真实词字幕没有形成可编辑绑定")
	}
}

func TestWorkflowPreconditionsAndReturnedPlanDoNotMutateCaller(t *testing.T) {
	project := algorithmProject(t, false)
	before := editor.CloneProject(project)
	plan, err := Preview(project, Input{WorkflowID: "captions", Selection: []string{"video"}, Parameters: map[string]any{"fontSize": 80}})
	if err != nil {
		t.Fatal(err)
	}
	plan.Request.Commands[0].Clip.CaptionSource.WordIDs[0] = "caller-mutation"
	if !reflect.DeepEqual(before, project) {
		t.Fatal("返回计划与调用方工程共享可写绑定指针")
	}
	if _, err := Preview(project, Input{WorkflowID: "broll", Parameters: map[string]any{"assetId": "not-imported"}}); err == nil {
		t.Fatal("工作流伪造了尚未生成/导入的素材")
	}
	if _, err := Preview(project, Input{WorkflowID: "talking-head", Selection: []string{"video"}, Parameters: map[string]any{"paidModel": "anything"}}); err == nil {
		t.Fatal("不支持参数不能绕到收费模型调用")
	}
}
