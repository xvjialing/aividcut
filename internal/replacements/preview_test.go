package replacements

import (
	"reflect"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func replacementProject(t *testing.T) editor.Project {
	t.Helper()
	project := editor.NewProject("替换语义fixture")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	project.Assets = []editor.Asset{
		{ID: "original", Name: "原讲话.mp4", Kind: "video", Path: "/fixture/original.mp4", DurationUs: 20_000_000, Width: 1920, Height: 1080, FPSNum: 30000, FPSDen: 1001, HasAudio: true, Fingerprint: "sha256:original"},
		{ID: "candidate", Name: "已保存候选.mp4", Kind: "video", Path: "/fixture/candidate.mp4", DurationUs: 20_000_000, Width: 1280, Height: 720, HasAudio: true, Fingerprint: "sha256:candidate"},
		{ID: "silent", Name: "无声候选.mp4", Kind: "video", Path: "/fixture/silent.mp4", DurationUs: 20_000_000, Width: 1280, Height: 720, Fingerprint: "sha256:silent"},
		{ID: "short", Name: "余量不足.mp4", Kind: "video", Path: "/fixture/short.mp4", DurationUs: 1_000_000, Width: 1280, Height: 720, HasAudio: true, Fingerprint: "sha256:short"},
		{ID: "still", Name: "已保存图片.png", Kind: "image", Path: "/fixture/still.png", Width: 1920, Height: 1080, Fingerprint: "sha256:still"},
		{ID: "music", Name: "未选背景.wav", Kind: "audio", Path: "/fixture/music.wav", DurationUs: 20_000_000, HasAudio: true, Fingerprint: "sha256:music"},
		{ID: "lut", Name: "原色彩.cube", Kind: "lut", Path: "/fixture/look.cube"},
	}
	shot := editor.DefaultClip("shot", "track-video-1", "video")
	shot.AssetID, shot.StartFrame, shot.DurationFrames, shot.SourceInUs, shot.SpeedNum, shot.Volume = "original", 30, 90, 1_000_000, 2, 0.6
	shot.Transform = editor.Transform{X: 12, Y: -24, Scale: 1.2, Rotation: 5}
	shot.LUT = &editor.LUT{AssetID: "lut", Intensity: 0.4}
	other := editor.DefaultClip("unselected-picture", "track-video-1", "video")
	other.AssetID, other.StartFrame, other.DurationFrames, other.SourceInUs = "original", 300, 30, 8_000_000
	music := editor.DefaultClip("unselected-music", "track-audio-1", "audio")
	music.AssetID, music.DurationFrames, music.Volume = "music", 450, 0.2
	project.Clips = []editor.Clip{shot, other, music}
	transcript := editor.Transcript{ID: "speech", AssetID: "original", SourceFingerprint: "sha256:original", Language: "zh", RawWords: []editor.SourceWord{{ID: "word-1", StartUs: 1_200_000, EndUs: 1_800_000, Text: "原讲话一"}, {ID: "word-2", StartUs: 3_000_000, EndUs: 3_800_000, Text: "原讲话二"}, {ID: "word-3", StartUs: 5_000_000, EndUs: 5_800_000, Text: "原讲话三"}, {ID: "word-4", StartUs: 6_200_000, EndUs: 6_800_000, Text: "原讲话四"}}}
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	project, err = engine.Apply(editor.EditRequest{BaseRevision: 0, IdempotencyKey: "accept-speech", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	commands := []editor.Command{}
	for _, caption := range project.Clips {
		if caption.CaptionSource == nil {
			continue
		}
		switch caption.CaptionSource.WordIDs[0] {
		case "word-2":
			caption.Text, caption.TextStyle.FontSize, caption.Transform.Y = "人工原讲话", 70, -80
			commands = append(commands, editor.Command{Type: "update_clip", Clip: &caption})
		case "word-3":
			commands = append(commands, editor.Command{Type: "detach_caption", ClipID: caption.ID})
		case "word-4":
			commands = append(commands, editor.Command{Type: "delete_clip", ClipID: caption.ID})
		}
	}
	project, err = engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "manual-caption-state", Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func getClip(project editor.Project, id string) editor.Clip {
	clip, _ := findClip(project, id)
	return clip
}
func resetRevision(project editor.Project) editor.Project { project.Revision = 0; return project }
func applyPlan(t *testing.T, project editor.Project, plan Plan) (*editor.Engine, editor.Project) {
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
func carrierFor(project editor.Project, shotID string) editor.Clip {
	for _, clip := range project.Clips {
		if clip.Kind == "audio" && clip.AssetID == "original" && clip.ID != shotID {
			return clip
		}
	}
	return editor.Clip{}
}

func TestKeepOriginalMovesRealSpeechBindingsAndExclusionsWithoutChangingShotIdentity(t *testing.T) {
	before := replacementProject(t)
	inputCopy := editor.CloneProject(before)
	plan, err := Preview(before, Input{ClipID: "shot", AssetID: "candidate", SourceInUs: 2_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, inputCopy) {
		t.Fatal("预览改写输入工程")
	}
	engine, after := applyPlan(t, before, plan)
	original, replacement := getClip(before, "shot"), getClip(after, "shot")
	if replacement.ID != original.ID || replacement.TrackID != original.TrackID || replacement.StartFrame != original.StartFrame || replacement.DurationFrames != original.DurationFrames || replacement.AssetID != "candidate" || replacement.SpeedNum != 2 || replacement.SourceInUs != 2_000_000 || !replacement.Muted || replacement.Transform != original.Transform || !reflect.DeepEqual(replacement.LUT, original.LUT) {
		t.Fatal("替换没有保留镜头身份/时间/速度策略/默认变换LUT")
	}
	carrier := carrierFor(after, "shot")
	if carrier.ID == "" || carrier.Kind != "audio" || carrier.AssetID != original.AssetID || carrier.SourceInUs != original.SourceInUs || carrier.StartFrame != original.StartFrame || carrier.DurationFrames != original.DurationFrames || carrier.SpeedNum != original.SpeedNum || carrier.Volume != original.Volume || carrier.Muted || carrier.LinkGroup == "" || carrier.LinkGroup != replacement.LinkGroup {
		t.Fatal("内嵌原音未成为同真实video Asset的audio轨片段")
	}
	if len(after.Assets) != len(before.Assets) {
		t.Fatal("保留原音不应伪造另一份audio素材或源指纹")
	}
	for _, old := range before.Clips {
		if old.CaptionSource != nil && old.CaptionSource.SourceClipID == "shot" {
			current := getClip(after, old.ID)
			if current.ID == "" || current.CaptionSource == nil || current.CaptionSource.SourceClipID != carrier.ID || current.CaptionSource.TranscriptID != old.CaptionSource.TranscriptID || current.Text != old.Text || current.TextStyle != old.TextStyle || current.Transform != old.Transform || current.StartFrame != old.StartFrame || current.DurationFrames != old.DurationFrames {
				t.Fatal("讲话字幕迁移丢失ID、人工覆盖或真实时间")
			}
		}
		if old.ID == "unselected-picture" || old.ID == "unselected-music" || old.Kind == "caption" && old.CaptionSource == nil {
			if !reflect.DeepEqual(old, getClip(after, old.ID)) {
				t.Fatal("替换改了未选片段或已解绑静态字幕")
			}
		}
	}
	for _, exclusion := range after.Transcripts[0].CaptionExclusions {
		if exclusion.SourceClipID != carrier.ID {
			t.Fatal("手动删除/解绑的词排除未迁至真实讲话承载")
		}
	}
	if len(after.Transcripts[0].CaptionExclusions) != 2 || !reflect.DeepEqual(before.Transcripts[0].RawWords, after.Transcripts[0].RawWords) || !reflect.DeepEqual(before.Transcripts[0].AcceptedWords, after.Transcripts[0].AcceptedWords) {
		t.Fatal("字幕排除或识别接受版本被改写")
	}
	undone, err := engine.Undo()
	if err != nil || !reflect.DeepEqual(resetRevision(before), resetRevision(undone)) {
		t.Fatal("替换没有整组撤销原镜头/原字幕/新音轨", err)
	}
	redone, err := engine.Redo()
	if err != nil || !reflect.DeepEqual(resetRevision(after), resetRevision(redone)) {
		t.Fatal("替换重做未恢复同一组来源", err)
	}
	revision := redone.Revision
	repeated, err := engine.Apply(plan.Request)
	if err != nil || repeated.Revision != revision {
		t.Fatal("同一预览幂等重试产生重复声音或历史", err)
	}
}

func TestReplacementKeepsOriginalFadeOnRealCarrierAndClearsCandidate(t *testing.T) {
	p := replacementProject(t)
	for i := range p.Clips {
		if p.Clips[i].ID == "shot" {
			p.Clips[i].AudioFade = &editor.AudioFade{InUs: 200000, OutUs: 300000, SpanFrames: 180, OffsetFrames: 30}
		}
	}
	for _, asset := range []string{"candidate", "still"} {
		plan, err := Preview(p, Input{ClipID: "shot", AssetID: asset, AudioMode: KeepOriginal})
		if err != nil {
			t.Fatal(err)
		}
		_, after := applyPlan(t, p, plan)
		carrier := carrierFor(after, "shot")
		if !reflect.DeepEqual(carrier.AudioFade, getClip(p, "shot").AudioFade) || getClip(after, "shot").AudioFade != nil {
			t.Fatal("原声淡化丢失或被带到新画面/候选声音")
		}
	}
}

func TestCandidateAndMuteKeepOldCaptionOriginButReallyMuteOldSpeech(t *testing.T) {
	for _, mode := range []string{UseCandidate, Mute} {
		t.Run(mode, func(t *testing.T) {
			before := replacementProject(t)
			plan, err := Preview(before, Input{ClipID: "shot", AssetID: "candidate", AudioMode: mode})
			if err != nil {
				t.Fatal(err)
			}
			_, after := applyPlan(t, before, plan)
			carrier := carrierFor(after, "shot")
			shot := getClip(after, "shot")
			if carrier.ID == "" || !carrier.Muted || carrier.AssetID != "original" || shot.Muted != (mode == Mute) {
				t.Fatal("声音模式假装原讲话仍在播放或没有启用真实候选音轨")
			}
			warning := strings.Join(plan.Warnings, " ")
			if mode == UseCandidate && !strings.Contains(warning, "字幕尚未与候选声音对齐") || mode == Mute && (!strings.Contains(warning, "字幕仍对应原讲话") || !strings.Contains(warning, "原声已静音")) {
				t.Fatal("没有明确说明旧讲话字幕与新声音未对齐")
			}
			for _, caption := range after.Clips {
				if caption.CaptionSource != nil && caption.CaptionSource.TranscriptID == "speech" && caption.CaptionSource.SourceClipID != carrier.ID {
					t.Fatal("原字幕错误跟到了新画面源")
				}
			}
		})
	}
}

func TestCrossImageVideoKindsLUTAndSpeedStrategiesAreExplicit(t *testing.T) {
	before := replacementProject(t)
	plan, err := Preview(before, Input{ClipID: "shot", AssetID: "still", LUTMode: ClearLUT})
	if err != nil {
		t.Fatal(err)
	}
	_, after := applyPlan(t, before, plan)
	still := getClip(after, "shot")
	carrier := carrierFor(after, "shot")
	if still.Kind != "image" || still.SpeedNum != 1 || still.SpeedDen != 1 || still.LUT != nil || still.Transform != getClip(before, "shot").Transform || carrier.SpeedNum != 2 || len(plan.Warnings) == 0 {
		t.Fatal("视频到图片没有固定静态速度或保留原讲话速度")
	}
	plan, err = Preview(before, Input{ClipID: "shot", AssetID: "candidate", SpeedMode: NormalSpeed, AudioMode: Mute})
	if err != nil {
		t.Fatal(err)
	}
	_, after = applyPlan(t, before, plan)
	if getClip(after, "shot").SpeedNum != 1 || carrierFor(after, "shot").SpeedNum != 2 {
		t.Fatal("1倍候选策略错误修改原讲话的源速度")
	}
	imageProject := editor.NewProject("图到视频")
	imageProject.Assets = before.Assets
	image := editor.DefaultClip("image-shot", "track-video-1", "image")
	image.AssetID, image.StartFrame, image.DurationFrames, image.SpeedNum, image.SpeedDen = "still", 30, 90, 3, 2
	image.Transform = editor.Transform{Scale: 1.3, X: 5}
	imageProject.Clips = []editor.Clip{image}
	plan, err = Preview(imageProject, Input{ClipID: image.ID, AssetID: "candidate", SourceInUs: 500_000})
	if err != nil {
		t.Fatal(err)
	}
	_, after = applyPlan(t, imageProject, plan)
	video := getClip(after, image.ID)
	if video.Kind != "video" || video.SpeedNum != 1 || video.SpeedDen != 1 || video.SourceInUs != 500_000 || video.Transform != image.Transform || !video.Muted {
		t.Fatal("图片没有真实源速度，转视频应明确1倍且保持原无声状态")
	}
}

func TestReuseExistingLinkedOriginalAudioWithoutDuplicatingIt(t *testing.T) {
	before := replacementProject(t)
	engine, err := editor.NewEngine(before)
	if err != nil {
		t.Fatal(err)
	}
	shot := getClip(before, "shot")
	shot.Muted, shot.LinkGroup = true, "existing-av"
	linked := editor.DefaultClip("existing-audio", "track-audio-1", "audio")
	linked.AssetID, linked.StartFrame, linked.DurationFrames, linked.SourceInUs, linked.SpeedNum, linked.Volume, linked.LinkGroup = "original", shot.StartFrame, shot.DurationFrames, shot.SourceInUs, shot.SpeedNum, 0.8, shot.LinkGroup
	track := editor.Track{ID: "linked-audio-track", Name: "已有原声", Kind: "audio"}
	linked.TrackID = track.ID
	before, err = engine.Apply(editor.EditRequest{BaseRevision: before.Revision, IdempotencyKey: "linked-original", Commands: []editor.Command{{Type: "update_clip", Clip: &shot, IncludeLinked: boolPointer(false)}, {Type: "add_track", Track: &track}, {Type: "add_clip", Clip: &linked}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Preview(before, Input{ClipID: "shot", AssetID: "still"})
	if err != nil {
		t.Fatal(err)
	}
	_, after := applyPlan(t, before, plan)
	count := 0
	for _, clip := range after.Clips {
		if clip.Kind == "audio" && clip.AssetID == "original" {
			count++
		}
	}
	if count != 1 || !reflect.DeepEqual(linked, getClip(after, linked.ID)) {
		t.Fatal("已经存在的linked原音被复制或改变音量")
	}
	for _, caption := range after.Clips {
		if caption.CaptionSource != nil && caption.CaptionSource.TranscriptID == "speech" && caption.CaptionSource.SourceClipID != linked.ID {
			t.Fatal("旧字幕没有转到真实已链接讲话")
		}
	}
}

func TestShortSilentAndUnregisteredCandidatesNeverFakeSuccess(t *testing.T) {
	project := replacementProject(t)
	for _, input := range []Input{{ClipID: "shot", AssetID: "short"}, {ClipID: "shot", AssetID: "silent", AudioMode: UseCandidate}, {ClipID: "shot", AssetID: "still", AudioMode: UseCandidate}, {ClipID: "shot", AssetID: "missing"}, {ClipID: "shot", AssetID: "still", SourceInUs: 1}, {ClipID: "shot", AssetID: "candidate", SourceInUs: 19_000_000}} {
		before := editor.CloneProject(project)
		if _, err := Preview(project, input); err == nil {
			t.Fatalf("无效候选/源余量/声音仍成功：%+v", input)
		}
		if !reflect.DeepEqual(before, project) {
			t.Fatal("失败预览改变工程")
		}
	}
}

func TestTargetCarrierCaptionLocksAndOverlapRejectWholeReplacement(t *testing.T) {
	for _, scenario := range []string{"visual", "caption", "carrier-lock", "carrier-overlap"} {
		t.Run(scenario, func(t *testing.T) {
			project := replacementProject(t)
			input := Input{ClipID: "shot", AssetID: "still"}
			switch scenario {
			case "visual":
				for i := range project.Tracks {
					if project.Tracks[i].ID == "track-video-1" {
						project.Tracks[i].Locked = true
					}
				}
			case "caption":
				for i := range project.Tracks {
					if project.Tracks[i].ID == "track-caption-1" {
						project.Tracks[i].Locked = true
					}
				}
			case "carrier-lock":
				for i := range project.Tracks {
					if project.Tracks[i].ID == "track-audio-1" {
						project.Tracks[i].Locked = true
					}
				}
				input.AudioTrackID = "track-audio-1"
			case "carrier-overlap":
				input.AudioTrackID = "track-audio-1"
			}
			before := editor.CloneProject(project)
			if _, err := Preview(project, input); err == nil {
				t.Fatal("锁轨或原声目标重叠没有原子拒绝")
			}
			if !reflect.DeepEqual(before, project) {
				t.Fatal("拒绝替换仍保留部分修改")
			}
		})
	}
}

func TestNewCandidateCannotBorrowAnotherActiveSpeechSource(t *testing.T) {
	project := replacementProject(t)
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	transcript := editor.Transcript{ID: "candidate-words", AssetID: "candidate", SourceFingerprint: "sha256:candidate", RawWords: []editor.SourceWord{{ID: "new-word", StartUs: 0, EndUs: 1_000_000, Text: "另一个讲话"}}}
	project, err = engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "candidate-rule", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Preview(project, Input{ClipID: "shot", AssetID: "candidate"}); err == nil {
		t.Fatal("候选的全局字幕规则会误把新画面当原讲话，应明确拒绝")
	}
}

func TestReplacementPlanIsIsolatedFromCallerAndKeepsStableIDWithinPreview(t *testing.T) {
	project := replacementProject(t)
	before := editor.CloneProject(project)
	plan, err := Preview(project, Input{ClipID: "shot", AssetID: "still"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ID == "" || plan.Request.IdempotencyKey != plan.ID || plan.ProjectID != project.ID {
		t.Fatal("预览身份与幂等键没有稳定关联")
	}
	last := plan.Request.Commands[len(plan.Request.Commands)-1]
	if last.Clip.LUT == nil {
		t.Fatal("默认LUT没有保留")
	}
	last.Clip.LUT.Intensity = 0.9
	if !reflect.DeepEqual(before, project) {
		t.Fatal("计划与调用方LUT/绑定状态共享了可写指针")
	}
}

func TestSeparateLinkedAudioStaysSingleAndOnlyExplicitSoundModeMutesIt(t *testing.T) {
	project := editor.NewProject("已有独立链接原声")
	project.Assets = []editor.Asset{{ID: "visual-original", Name: "原视频", Kind: "video", Path: "/fixture/original.mp4", DurationUs: 10_000_000, HasAudio: true, Fingerprint: "sha256:original"}, {ID: "dialogue", Name: "独立原声", Kind: "audio", Path: "/fixture/dialogue.wav", DurationUs: 10_000_000, HasAudio: true, Fingerprint: "sha256:dialogue"}, {ID: "new-picture", Name: "候选视频", Kind: "video", Path: "/fixture/new.mp4", DurationUs: 10_000_000, HasAudio: true, Fingerprint: "sha256:new"}}
	visual := editor.DefaultClip("visual", "track-video-1", "video")
	visual.AssetID, visual.DurationFrames, visual.Muted, visual.LinkGroup = "visual-original", 90, true, "av"
	audio := editor.DefaultClip("dialogue-clip", "track-audio-1", "audio")
	audio.AssetID, audio.DurationFrames, audio.Volume, audio.LinkGroup = "dialogue", 90, 0.8, "av"
	project.Clips = []editor.Clip{visual, audio}
	plan, err := Preview(project, Input{ClipID: visual.ID, AssetID: "new-picture"})
	if err != nil {
		t.Fatal(err)
	}
	_, after := applyPlan(t, project, plan)
	if len(after.Clips) != 2 || !reflect.DeepEqual(audio, getClip(after, audio.ID)) {
		t.Fatal("已有独立linked原声被复制或改写")
	}
	plan, err = Preview(project, Input{ClipID: visual.ID, AssetID: "new-picture", AudioMode: UseCandidate})
	if err != nil {
		t.Fatal(err)
	}
	_, after = applyPlan(t, project, plan)
	muted := getClip(after, audio.ID)
	if !muted.Muted || muted.Volume != audio.Volume || muted.AssetID != audio.AssetID || len(after.Clips) != 2 || getClip(after, visual.ID).Muted {
		t.Fatal("明确候选声策略没有仅静音旧linked原声，或重新复制了音频")
	}
	for i := range project.Tracks {
		if project.Tracks[i].ID == audio.TrackID {
			project.Tracks[i].Locked = true
		}
	}
	if _, err := Preview(project, Input{ClipID: visual.ID, AssetID: "new-picture", AudioMode: UseCandidate}); err == nil {
		t.Fatal("需要改变的linked音频已锁定却仍部分替换")
	}
}
