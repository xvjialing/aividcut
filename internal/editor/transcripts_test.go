package editor

import (
	"reflect"
	"testing"
)

func transcriptFixture(t *testing.T) *Engine {
	t.Helper()
	project := NewProject("源转写绑定")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	project.Assets = []Asset{{ID: "speech", Name: "原始口播.mp4", Path: "/tmp/speech.mp4", Kind: "video", DurationUs: 20_000_000, Fingerprint: "sha256:original", HasAudio: true}}
	source := DefaultClip("speech-clip", "track-video-1", "video")
	source.AssetID, source.StartFrame, source.DurationFrames, source.SourceInUs = "speech", 30, 120, 1_000_000
	source.SpeedNum = 2
	project.Clips = []Clip{source}
	engine, err := NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	transcript := Transcript{ID: "transcript", AssetID: "speech", SourceFingerprint: "sha256:original", Language: "zh", RawWords: []SourceWord{
		{ID: "before", StartUs: 0, EndUs: 900_000, Text: "片段之外"},
		{ID: "cut", StartUs: 900_000, EndUs: 1_200_000, Text: "左边界", SpeakerID: "speaker-1"},
		{ID: "hello", StartUs: 2_000_000, EndUs: 3_000_000, Text: "你好"},
		{ID: "world", StartUs: 4_000_000, EndUs: 5_000_000, Text: "世界"},
		{ID: "tail", StartUs: 8_900_000, EndUs: 9_100_000, Text: "右边界"},
	}}
	apply(t, engine, Command{Type: "accept_transcript", Transcript: &transcript}, Command{Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"})
	return engine
}

func boundByWord(t *testing.T, project Project, sourceID, wordID string) Clip {
	t.Helper()
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.SourceClipID == sourceID && len(clip.CaptionSource.WordIDs) == 1 && clip.CaptionSource.WordIDs[0] == wordID {
			return clip
		}
	}
	t.Fatalf("未找到 %s/%s 的绑定字幕", sourceID, wordID)
	return Clip{}
}

func boundCount(project Project) int {
	count := 0
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil {
			count++
		}
	}
	return count
}

func TestTranscriptMappingMoveTrimSpeedAndImmutableRaw(t *testing.T) {
	engine := transcriptFixture(t)
	project := engine.Snapshot()
	left := boundByWord(t, project, "speech-clip", "cut")
	if left.StartFrame != 30 || left.DurationFrames != 3 || !left.CaptionSource.Partial {
		t.Fatalf("29.97fps 左截断和向外取整错误：%+v", left)
	}
	hello := boundByWord(t, project, "speech-clip", "hello")
	if hello.StartFrame != 44 || hello.DurationFrames != 16 {
		t.Fatalf("有理数二倍速映射错误：%+v", hello)
	}
	source := project.Clips[clipIndex(&project, "speech-clip")]
	source.StartFrame = 90
	project = apply(t, engine, Command{Type: "update_clip", Clip: &source})
	moved := boundByWord(t, project, "speech-clip", "hello")
	if moved.ID != hello.ID || moved.StartFrame != hello.StartFrame+60 {
		t.Fatal("移动没有稳定更新同一个字幕")
	}
	source.SourceInUs, source.DurationFrames, source.SpeedNum = 2_000_000, 60, 1
	project = apply(t, engine, Command{Type: "update_clip", Clip: &source})
	trimmed := boundByWord(t, project, "speech-clip", "hello")
	if trimmed.StartFrame != 90 || trimmed.DurationFrames != 30 {
		t.Fatalf("修剪/变速映射错误：%+v", trimmed)
	}
	if project.Transcripts[0].RawWords[2].Text != "你好" || project.Transcripts[0].RawWords[2].StartUs != 2_000_000 {
		t.Fatal("编辑源片段改写了原始识别证据")
	}
}

func TestCorrectTranscriptPreservesManualTextAndStyleOverrides(t *testing.T) {
	engine := transcriptFixture(t)
	project := engine.Snapshot()
	manual := boundByWord(t, project, "speech-clip", "hello")
	manual.Text, manual.TextStyle.FontSize, manual.Transform.Y = "人工文案", 68, -100
	project = apply(t, engine, Command{Type: "update_clip", Clip: &manual})
	manual = boundByWord(t, project, "speech-clip", "hello")
	if !manual.CaptionSource.TextOverride || !manual.CaptionSource.StyleOverride {
		t.Fatal("人工覆盖没有显式保存")
	}
	words := append([]SourceWord{}, project.Transcripts[0].AcceptedWords...)
	words[2].Text, words[3].Text = "接受纠错", "新世界"
	project = apply(t, engine, Command{Type: "correct_transcript", TranscriptID: "transcript", TranscriptRevision: 1, Words: words})
	if got := boundByWord(t, project, "speech-clip", "hello"); got.Text != "人工文案" || got.TextStyle.FontSize != 68 || got.Transform.Y != -100 {
		t.Fatal("纠错覆盖了人工字幕文本/样式")
	}
	if got := boundByWord(t, project, "speech-clip", "world"); got.Text != "新世界" {
		t.Fatal("没有人工覆盖的字幕未更新接受版本")
	}
	if project.Transcripts[0].RawWords[2].Text != "你好" || project.Transcripts[0].AcceptedWords[2].Text != "接受纠错" || project.Transcripts[0].Revision != 2 {
		t.Fatal("原始识别和接受修订未分离")
	}
	before := engine.Snapshot()
	_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: "old-correction", Commands: []Command{{Type: "correct_transcript", TranscriptID: "transcript", TranscriptRevision: 1, Words: words}}})
	assertCode(t, err, "transcript_revision")
	if !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("过期接受修订造成部分变更")
	}
	undone, err := engine.Undo()
	if err != nil || undone.Transcripts[0].Revision != 1 || boundByWord(t, undone, "speech-clip", "world").Text != "世界" {
		t.Fatal("纠错撤销未整体恢复", err)
	}
}

func TestSplitAndDuplicateSourceInheritOverridesDeleteUndoWholeGroup(t *testing.T) {
	engine := transcriptFixture(t)
	project := engine.Snapshot()
	manual := boundByWord(t, project, "speech-clip", "hello")
	manual.Text, manual.TextStyle.FontSize = "保留人工", 66
	apply(t, engine, Command{Type: "update_clip", Clip: &manual})
	project = apply(t, engine, Command{Type: "split_clip", ClipID: "speech-clip", AtFrame: 50})
	var right Clip
	for _, clip := range project.Clips {
		if clip.Kind == "video" && clip.ID != "speech-clip" {
			right = clip
		}
	}
	if right.ID == "" {
		t.Fatal("源分割没有产生右片段")
	}
	leftCaption, rightCaption := boundByWord(t, project, "speech-clip", "hello"), boundByWord(t, project, right.ID, "hello")
	if leftCaption.Text != "保留人工" || rightCaption.Text != "保留人工" || rightCaption.TextStyle.FontSize != 66 || leftCaption.StartFrame+leftCaption.DurationFrames != 50 || rightCaption.StartFrame != 50 || !leftCaption.CaptionSource.Partial || !rightCaption.CaptionSource.Partial {
		t.Fatal("切口词截断丢失、越界或人工覆盖未继承")
	}
	copy := right
	copy.ID, copy.StartFrame = "source-copy", 200
	project = apply(t, engine, Command{Type: "duplicate_clip", ClipID: right.ID, Clip: &copy})
	if got := boundByWord(t, project, copy.ID, "hello"); got.Text != "保留人工" || got.TextStyle.FontSize != 66 || got.StartFrame != 200 {
		t.Fatal("复制源没有派生并继承人工覆盖")
	}
	count := boundCount(project)
	project = apply(t, engine, Command{Type: "delete_clip", ClipID: copy.ID})
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.SourceClipID == copy.ID {
			t.Fatal("删除源留下绑定字幕")
		}
	}
	undone, err := engine.Undo()
	if err != nil || boundCount(undone) != count {
		t.Fatal("删除源的整组撤销不完整", err)
	}
	redone, err := engine.Redo()
	if err != nil || boundCount(redone) != boundCount(project) {
		t.Fatal("删除源的整组重做不完整", err)
	}
}

func TestLockedDerivedCaptionPreventsImplicitMoveCorrectionAndDeletion(t *testing.T) {
	for _, operation := range []string{"move", "correct", "delete", "fingerprint"} {
		t.Run(operation, func(t *testing.T) {
			engine := transcriptFixture(t)
			project := engine.Snapshot()
			track := project.Tracks[trackIndex(&project, "track-caption-1")]
			track.Locked = true
			before := apply(t, engine, Command{Type: "update_track", Track: &track})
			var command Command
			switch operation {
			case "move":
				source := before.Clips[clipIndex(&before, "speech-clip")]
				source.StartFrame += 30
				command = Command{Type: "update_clip", Clip: &source}
			case "correct":
				words := append([]SourceWord{}, before.Transcripts[0].AcceptedWords...)
				words[2].Text = "不应通过"
				command = Command{Type: "correct_transcript", TranscriptID: "transcript", TranscriptRevision: 1, Words: words}
			case "delete":
				command = Command{Type: "delete_clip", ClipID: "speech-clip"}
			case "fingerprint":
				asset := before.Assets[0]
				asset.Fingerprint = "sha256:changed"
				command = Command{Type: "update_asset", Asset: &asset}
			}
			_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: operation, Commands: []Command{{Type: "update_project", Name: "不能部分保留"}, command}})
			assertCode(t, err, "track_locked")
			if !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("隐式锁轨失败的批次部分写入")
			}
		})
	}
}

func TestFingerprintChangeMarksStaleAndRejectsSilentReuse(t *testing.T) {
	engine := transcriptFixture(t)
	project := engine.Snapshot()
	asset := project.Assets[0]
	asset.Fingerprint = "sha256:new-content"
	project = apply(t, engine, Command{Type: "update_asset", Asset: &asset})
	if !project.Transcripts[0].Stale || !boundByWord(t, project, "speech-clip", "hello").CaptionSource.Stale {
		t.Fatal("源指纹变化没有暴露转写与字幕的陈旧状态")
	}
	source := project.Clips[clipIndex(&project, "speech-clip")]
	source.StartFrame += 20
	_, err := engine.Apply(EditRequest{BaseRevision: project.Revision, IdempotencyKey: "stale-move", Commands: []Command{{Type: "update_clip", Clip: &source}}})
	assertCode(t, err, "transcript_stale")
	_, err = engine.Apply(EditRequest{BaseRevision: project.Revision, IdempotencyKey: "stale-generate", Commands: []Command{{Type: "generate_bound_captions", TranscriptID: "transcript", TrackID: "track-caption-1"}}})
	assertCode(t, err, "transcript_stale")
	apply(t, engine, Command{Type: "delete_clip", ClipID: "speech-clip"})
	if boundCount(engine.Snapshot()) != 0 {
		t.Fatal("陈旧字幕也应能随源删除")
	}
}

func TestTranscriptSnapshotDeepCopyAndDetachDoesNotRegenerate(t *testing.T) {
	engine := transcriptFixture(t)
	snapshot := engine.Snapshot()
	snapshot.Transcripts[0].RawWords[0].Text = "外部篡改原始"
	snapshot.Transcripts[0].AcceptedWords[0].Text = "外部篡改接受"
	for i := range snapshot.Clips {
		if snapshot.Clips[i].CaptionSource != nil {
			snapshot.Clips[i].CaptionSource.WordIDs[0] = "外部篡改绑定"
		}
	}
	if engine.Snapshot().Transcripts[0].RawWords[0].Text != "片段之外" || engine.Snapshot().Transcripts[0].AcceptedWords[0].Text != "片段之外" {
		t.Fatal("转写词条越过快照边界")
	}
	project := engine.Snapshot()
	caption := boundByWord(t, project, "speech-clip", "hello")
	project = apply(t, engine, Command{Type: "detach_caption", ClipID: caption.ID})
	if project.Clips[clipIndex(&project, caption.ID)].CaptionSource != nil || len(project.Transcripts[0].CaptionExclusions) != 1 {
		t.Fatal("解绑没有形成可持久的派生排除")
	}
	source := project.Clips[clipIndex(&project, "speech-clip")]
	source.StartFrame += 30
	project = apply(t, engine, Command{Type: "update_clip", Clip: &source})
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.WordIDs[0] == "hello" {
			t.Fatal("解绑字幕在后续源编辑中重新生成")
		}
	}
}

func TestLinkedVideoAudioSameSourceDoNotDuplicateCaptions(t *testing.T) {
	engine := transcriptFixture(t)
	project := engine.Snapshot()
	video := project.Clips[clipIndex(&project, "speech-clip")]
	video.LinkGroup = "av"
	audioAsset := project.Assets[0]
	audioAsset.ID, audioAsset.Kind, audioAsset.Name, audioAsset.Path = "speech-audio", "audio", "原始声音", "/tmp/speech.wav"
	audioAsset.Fingerprint = "sha256:demuxed-audio"
	audio := video
	audio.ID, audio.AssetID, audio.TrackID, audio.Kind = "audio-clip", audioAsset.ID, "track-audio-1", "audio"
	transcript := Transcript{ID: "audio-transcript", AssetID: audioAsset.ID, SourceFingerprint: audioAsset.Fingerprint, Language: "zh", RawWords: append([]SourceWord{}, project.Transcripts[0].RawWords...)}
	project = apply(t, engine, Command{Type: "add_asset", Asset: &audioAsset}, Command{Type: "update_clip", Clip: &video}, Command{Type: "add_clip", Clip: &audio}, Command{Type: "accept_transcript", Transcript: &transcript}, Command{Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"})
	if boundCount(project) != 4 {
		t.Fatalf("linked 音视频重复产生字幕：%d", boundCount(project))
	}
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.SourceClipID != "speech-clip" {
			t.Fatal("同源 linked 字幕拥有者不稳定")
		}
	}
}

func TestExplicitSplitKeepsOverridesFromTheSelectedOccurrence(t *testing.T) {
	engine := transcriptFixture(t)
	project := engine.Snapshot()
	otherTrack := Track{ID: "video-other", Name: "另一份复用", Kind: "video"}
	other := project.Clips[clipIndex(&project, "speech-clip")]
	other.ID, other.TrackID = "other-occurrence", otherTrack.ID
	project = apply(t, engine, Command{Type: "add_track", Track: &otherTrack}, Command{Type: "add_clip", Clip: &other})
	first := boundByWord(t, project, "speech-clip", "hello")
	first.Text = "第一份"
	second := boundByWord(t, project, other.ID, "hello")
	second.Text = "第二份"
	project = apply(t, engine, Command{Type: "update_clip", Clip: &first}, Command{Type: "update_clip", Clip: &second})
	if boundCount(project) != 8 {
		t.Fatal("非 linked 的复用被错误去重")
	}
	project = apply(t, engine, Command{Type: "split_clip", ClipID: other.ID, AtFrame: 50})
	var right Clip
	for _, clip := range project.Clips {
		if clip.Kind == "video" && clip.TrackID == other.TrackID && clip.ID != other.ID {
			right = clip
		}
	}
	if right.ID == "" || boundByWord(t, project, right.ID, "hello").Text != "第二份" || boundByWord(t, project, "speech-clip", "hello").Text != "第一份" {
		t.Fatal("分割错误继承了另一份相同素材的人工覆盖")
	}
}

func TestPureRelinkAllowedWithLockedSourceAndCaptionTracks(t *testing.T) {
	engine := transcriptFixture(t)
	project := engine.Snapshot()
	video, caption := project.Tracks[trackIndex(&project, "track-video-1")], project.Tracks[trackIndex(&project, "track-caption-1")]
	video.Locked, caption.Locked = true, true
	project = apply(t, engine, Command{Type: "update_track", Track: &video}, Command{Type: "update_track", Track: &caption})
	before := boundByWord(t, project, "speech-clip", "hello")
	asset := project.Assets[0]
	asset.Path = "/tmp/relocated-speech.mp4"
	project = apply(t, engine, Command{Type: "update_asset", Asset: &asset})
	if project.Transcripts[0].Stale || !reflect.DeepEqual(before, boundByWord(t, project, "speech-clip", "hello")) {
		t.Fatal("同指纹重定位不应改变词映射或触发字幕陈旧")
	}
}

func TestWordMapperReuseKeepsIndependentResultsAcrossUnsortedBoundaries(t *testing.T) {
	settings := NewProject("映射工作区").Settings
	settings.FPSNum, settings.FPSDen = 30000, 1001
	words := []SourceWord{{StartUs: 8_900_000, EndUs: 9_100_000}, {StartUs: 900_000, EndUs: 1_200_000}, {StartUs: 2_000_000, EndUs: 3_000_000}, {StartUs: 0, EndUs: 1}, {StartUs: 2_345_678, EndUs: 2_345_679}, {StartUs: 30_000_000, EndUs: 30_000_001}}
	for _, speed := range [][2]int{{1, 1}, {2, 1}, {1, 3}} {
		source := DefaultClip("take", "track-video-1", "video")
		source.StartFrame, source.SourceInUs, source.DurationFrames, source.SpeedNum, source.SpeedDen = 30, 1_000_000, 120, speed[0], speed[1]
		mapper, err := newWordMapper(source, settings)
		if err != nil {
			t.Fatal(err)
		}
		for round := 0; round < 3; round++ {
			for _, word := range words {
				span, visible, err := mapper.mapWord(word)
				independent, independentVisible, independentErr := MapWordToClip(word, source, settings)
				if err != nil || independentErr != nil || span != independent || visible != independentVisible {
					t.Fatalf("映射工作区残留了上一次词边界：%+v %+v %v", span, independent, err)
				}
			}
		}
	}
}
