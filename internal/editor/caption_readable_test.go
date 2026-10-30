package editor

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

func readableFixture(t *testing.T, words []SourceWord, layout *CaptionLayout) (*Engine, Project) {
	t.Helper()
	project := NewProject("阅读分句语义")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	project.Assets = []Asset{{ID: "read-source", Name: "讲话.mp4", Kind: "video", Path: "/fixture/read-source.mp4", DurationUs: 20_000_000, HasAudio: true, Fingerprint: "sha256:reading"}}
	source := DefaultClip("read-clip", "track-video-1", "video")
	source.AssetID, source.StartFrame, source.DurationFrames = "read-source", 30, 180
	project.Clips = []Clip{source}
	engine, err := NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	transcript := Transcript{ID: "read-transcript", AssetID: "read-source", SourceFingerprint: "sha256:reading", Origin: "asr", RawWords: words}
	project = apply(t, engine, Command{Type: "accept_transcript", Transcript: &transcript}, Command{Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1", CaptionLayout: layout})
	return engine, project
}

func timedWords(texts ...string) []SourceWord {
	words := make([]SourceWord, len(texts))
	for i, text := range texts {
		words[i] = SourceWord{ID: NewID("word"), StartUs: int64(i) * 100_000, EndUs: int64(i+1) * 100_000, Text: text, SpeakerID: "speaker"}
	}
	return words
}
func readingClips(project Project, sourceID string) []Clip {
	clips := []Clip{}
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil && (sourceID == "" || clip.CaptionSource.SourceClipID == sourceID) {
			clips = append(clips, clip)
		}
	}
	sort.SliceStable(clips, func(i, j int) bool { return clips[i].StartFrame < clips[j].StartFrame })
	return clips
}
func rawTexts(clips []Clip) []string {
	values := make([]string, len(clips))
	for i, clip := range clips {
		values[i] = clip.Text
	}
	return values
}

func TestReadableGroupsKeepChinesePunctuationAndActualEnglishSpaces(t *testing.T) {
	words := timedWords("你", "好", "，", "世", "界", "。", "Hello  ", "world", "!", "Nice", "to", "meet", "you.")
	_, project := readableFixture(t, words, &CaptionLayout{Mode: "readable", MaxCharsPerLine: 40})
	clips := readingClips(project, "read-clip")
	if !reflect.DeepEqual(rawTexts(clips), []string{"你好，世界。", "Hello  world!", "Nice to meet you."}) {
		t.Fatalf("中文或英文原始空格被破坏：%q", rawTexts(clips))
	}
	if len(clips[0].CaptionSource.WordIDs) != 6 || len(clips[1].CaptionSource.WordIDs) != 3 || len(clips[2].CaptionSource.WordIDs) != 4 {
		t.Fatal("阅读文字未持续绑定真实有序词")
	}
	source := project.Clips[clipIndex(&project, "read-clip")]
	for _, clip := range clips {
		first, last := SourceWord{}, SourceWord{}
		for _, word := range words {
			if word.ID == clip.CaptionSource.WordIDs[0] {
				first = word
			}
			if word.ID == clip.CaptionSource.WordIDs[len(clip.CaptionSource.WordIDs)-1] {
				last = word
			}
		}
		left, _, _ := MapWordToClip(first, source, project.Settings)
		right, _, _ := MapWordToClip(last, source, project.Settings)
		if clip.StartFrame != left.StartFrame || clip.StartFrame+clip.DurationFrames != right.StartFrame+right.DurationFrames {
			t.Fatal("阅读组时序不是实际首/末词映射")
		}
	}
}

func TestReadableWrappingAvoidsLeadingPunctuationAndSplittingEnglishWords(t *testing.T) {
	_, project := readableFixture(t, timedWords("今", "天", "一", "起", "剪", "辑", "视", "频", "。"), &CaptionLayout{Mode: "readable", MaxCharsPerLine: 4, MaxLines: 2})
	clips := readingClips(project, "read-clip")
	if len(clips) != 2 || clips[1].Text != "频。" {
		t.Fatalf("不能让标点单独成组：%q", rawTexts(clips))
	}
	for _, clip := range clips {
		for _, line := range strings.Split(clip.Text, "\n") {
			if line != "" {
				first, _ := utf8.DecodeRuneInString(line)
				if closingPunctuation(first) {
					t.Fatal("阅读行以关闭标点开头")
				}
			}
			if visibleLineLength([]rune(line)) > 4 {
				t.Fatal("行宽超界")
			}
		}
	}
	project = NewProject("不能猜时间拆长词")
	project.Assets = []Asset{{ID: "a", Name: "a.wav", Kind: "audio", Path: "/fixture/a.wav", DurationUs: 10_000_000, HasAudio: true, Fingerprint: "sha256:a"}}
	source := DefaultClip("s", "track-audio-1", "audio")
	source.AssetID = "a"
	project.Clips = []Clip{source}
	engine, err := NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	transcript := Transcript{ID: "t", AssetID: "a", SourceFingerprint: "sha256:a", RawWords: timedWords("extraordinarylongtoken")}
	project = apply(t, engine, Command{Type: "accept_transcript", Transcript: &transcript})
	before := engine.Snapshot()
	_, err = engine.Apply(EditRequest{BaseRevision: project.Revision, IdempotencyKey: "long-word", Commands: []Command{{Type: "generate_bound_captions", TranscriptID: "t", TrackID: "track-caption-1", CaptionLayout: &CaptionLayout{Mode: "readable", MaxCharsPerLine: 8, MaxLines: 2}}}})
	assertCode(t, err, "caption_layout")
	if !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("长词失败部分改写工程")
	}
}

func TestReadablePauseSpeakerAndDurationUseActualPlaybackRate(t *testing.T) {
	words := []SourceWord{{ID: "a", StartUs: 0, EndUs: 200_000, Text: "one", SpeakerID: "a"}, {ID: "b", StartUs: 300_000, EndUs: 500_000, Text: "two", SpeakerID: "a"}, {ID: "c", StartUs: 1_500_000, EndUs: 1_700_000, Text: "three", SpeakerID: "a"}, {ID: "d", StartUs: 1_800_000, EndUs: 2_000_000, Text: "four", SpeakerID: "b"}}
	engine, project := readableFixture(t, words, &CaptionLayout{Mode: "readable", MaxCharsPerLine: 30, MaxDurationUs: 1_000_000})
	if !reflect.DeepEqual(rawTexts(readingClips(project, "read-clip")), []string{"one two", "three", "four"}) {
		t.Fatal("真实停顿或换说话人没有形成边界")
	}
	source := project.Clips[clipIndex(&project, "read-clip")]
	source.SpeedNum = 2
	project = apply(t, engine, Command{Type: "update_clip", Clip: &source})
	if !reflect.DeepEqual(rawTexts(readingClips(project, "read-clip")), []string{"one two three", "four"}) {
		t.Fatal("变速后停顿/时长没有按实际播放映射重新分组")
	}
}

func TestReadableRegroupProtectsManualWordAndWholeGroupOverrides(t *testing.T) {
	engine, project := readableFixture(t, timedWords("你", "好", "世", "界"), nil)
	manual := readingClips(project, "read-clip")[1]
	manual.Text, manual.TextStyle.FontSize, manual.Transform.Y = "人工文案", 72, -88
	project = apply(t, engine, Command{Type: "update_clip", Clip: &manual})
	project = apply(t, engine, Command{Type: "generate_bound_captions", TranscriptID: "read-transcript", TrackID: "track-caption-1", CaptionLayout: &CaptionLayout{Mode: "readable"}})
	if len(readingClips(project, "read-clip")) != 3 {
		t.Fatal("手改词没有作为阅读组硬边界")
	}
	kept := project.Clips[clipIndex(&project, manual.ID)]
	if kept.Text != manual.Text || kept.Transform != manual.Transform || kept.TextStyle != manual.TextStyle || len(kept.CaptionSource.WordIDs) != 1 {
		t.Fatal("重新分句丢失手改文字/布局")
	}
	engine, project = readableFixture(t, timedWords("你", "好", "世", "界"), &CaptionLayout{Mode: "readable"})
	manual = readingClips(project, "read-clip")[0]
	manual.Text = "整组人工文案"
	project = apply(t, engine, Command{Type: "update_clip", Clip: &manual})
	project = apply(t, engine, Command{Type: "generate_bound_captions", TranscriptID: "read-transcript", TrackID: "track-caption-1", CaptionLayout: &CaptionLayout{Mode: "readable", MaxCharsPerLine: 2, MaxLines: 1}})
	kept = project.Clips[clipIndex(&project, manual.ID)]
	if kept.Text != manual.Text || len(kept.CaptionSource.WordIDs) != 4 {
		t.Fatal("更窄布局不能静默拆掉整组手改")
	}
}

func TestProtectedReadableGroupRejectsPartialTrimSplitAndWordCorrection(t *testing.T) {
	for _, operation := range []string{"trim", "split", "remove-word", "insert-word"} {
		t.Run(operation, func(t *testing.T) {
			engine, project := readableFixture(t, timedWords("你", "好", "世", "界"), &CaptionLayout{Mode: "readable"})
			manual := readingClips(project, "read-clip")[0]
			manual.Text = "不可随意拆分的人工字幕"
			project = apply(t, engine, Command{Type: "update_clip", Clip: &manual})
			before := engine.Snapshot()
			var command Command
			switch operation {
			case "trim":
				source := project.Clips[clipIndex(&project, "read-clip")]
				source.SourceInUs = 150_000
				command = Command{Type: "update_clip", Clip: &source}
			case "split":
				command = Command{Type: "split_clip", ClipID: "read-clip", AtFrame: 37}
			case "remove-word":
				words := append([]SourceWord{}, project.Transcripts[0].AcceptedWords[1:]...)
				command = Command{Type: "correct_transcript", TranscriptID: "read-transcript", TranscriptRevision: 1, Words: words}
			case "insert-word":
				words := append([]SourceWord{}, project.Transcripts[0].AcceptedWords...)
				words = append(words, SourceWord{ID: "new", StartUs: 150_000, EndUs: 160_000, Text: "插入", SpeakerID: "speaker"})
				command = Command{Type: "correct_transcript", TranscriptID: "read-transcript", TranscriptRevision: 1, Words: words}
			}
			_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: operation, Commands: []Command{{Type: "update_project", Name: "不得部分保存"}, command}})
			assertCode(t, err, "caption_override_conflict")
			if !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("手改组冲突没有原子回滚")
			}
		})
	}
}

func TestReadableSourceMoveDuplicateSplitExclusionsAndReplaceStayBound(t *testing.T) {
	engine, project := readableFixture(t, timedWords("今", "天", "一", "起", "剪", "辑", "视", "频"), &CaptionLayout{Mode: "readable", MaxCharsPerLine: 4, MaxLines: 1})
	source := project.Clips[clipIndex(&project, "read-clip")]
	source.StartFrame = 90
	project = apply(t, engine, Command{Type: "update_clip", Clip: &source})
	for _, clip := range readingClips(project, source.ID) {
		if clip.StartFrame < 90 {
			t.Fatal("移动源没有持续映射阅读组")
		}
	}
	project = apply(t, engine, Command{Type: "split_clip", ClipID: source.ID, AtFrame: 102})
	var right Clip
	for _, clip := range project.Clips {
		if clip.Kind == "video" && clip.ID != source.ID {
			right = clip
		}
	}
	copy := right
	copy.ID, copy.StartFrame = "copy-reading", 300
	project = apply(t, engine, Command{Type: "duplicate_clip", ClipID: right.ID, Clip: &copy})
	if len(readingClips(project, copy.ID)) == 0 {
		t.Fatal("复制保留段没有真实多词绑定")
	}
	manual := readingClips(project, copy.ID)[0]
	manual.Text = "复制组的人工修改"
	project = apply(t, engine, Command{Type: "update_clip", Clip: &manual})
	image := Asset{ID: "image", Name: "替代.png", Kind: "image", Path: "/fixture/still.png", Fingerprint: "sha256:image"}
	project = apply(t, engine, Command{Type: "add_asset", Asset: &image})
	visual := copy
	visual.Kind, visual.AssetID, visual.SourceInUs, visual.SpeedNum, visual.SpeedDen, visual.Muted, visual.LinkGroup = "image", image.ID, 0, 1, 1, true, "reading-av"
	track := Track{ID: "reading-audio-track", Name: "原讲话", Kind: "audio"}
	carrier := DefaultClip("reading-carrier", track.ID, "audio")
	carrier.AssetID, carrier.StartFrame, carrier.DurationFrames, carrier.SourceInUs, carrier.SpeedNum, carrier.SpeedDen, carrier.Volume, carrier.LinkGroup = copy.AssetID, copy.StartFrame, copy.DurationFrames, copy.SourceInUs, copy.SpeedNum, copy.SpeedDen, copy.Volume, visual.LinkGroup
	project = apply(t, engine, Command{Type: "replace_visual", Clip: &visual, AudioCarrier: &carrier, AudioMode: "keep_original", Track: &track})
	kept := project.Clips[clipIndex(&project, manual.ID)]
	if kept.CaptionSource.SourceClipID != carrier.ID || kept.Text != manual.Text || len(kept.CaptionSource.WordIDs) < 2 {
		t.Fatal("旧讲话画面替换后阅读组或人工覆盖跟错来源")
	}
	beforeCount := len(readingClips(project, carrier.ID))
	project = apply(t, engine, Command{Type: "detach_caption", ClipID: manual.ID})
	project = apply(t, engine, Command{Type: "update_project", Name: "保持排除"})
	if len(readingClips(project, carrier.ID)) != beforeCount-1 {
		t.Fatal("多词解绑后全部词排除没有持续生效")
	}
}

func TestCaptionLayoutAndOriginAreStrictAndDeepCopied(t *testing.T) {
	zero, err := NormalizeCaptionLayout(&CaptionLayout{Mode: "readable"})
	if err != nil || *zero != (CaptionLayout{Mode: "readable", MaxCharsPerLine: 18, MaxLines: 2, MaxDurationUs: 4_000_000, PauseThresholdUs: 700_000}) {
		t.Fatal("阅读默认归一不一致", err)
	}
	for _, layout := range []*CaptionLayout{{Mode: "unknown"}, {Mode: "readable", MaxCharsPerLine: 1}, {Mode: "readable", MaxLines: 5}, {Mode: "readable", MaxDurationUs: -1}} {
		if _, err := NormalizeCaptionLayout(layout); err == nil {
			t.Fatal("无效阅读参数被接受")
		}
	}
	engine, project := readableFixture(t, timedWords("你", "好"), &CaptionLayout{Mode: "readable"})
	snapshot := engine.Snapshot()
	snapshot.Transcripts[0].CaptionLayout.MaxCharsPerLine = 100
	if engine.Snapshot().Transcripts[0].CaptionLayout.MaxCharsPerLine != 18 {
		t.Fatal("布局指针越过快照边界")
	}
	for _, metadata := range []Transcript{{Origin: "tts", TimingSource: "original"}, {Origin: "tts", TimingSource: "normalized"}, {Origin: "asr"}, {}} {
		metadata.ID = NewID("origin")
		metadata.AssetID = "read-source"
		metadata.SourceFingerprint = "sha256:reading"
		metadata.RawWords = timedWords("真实词")
		apply(t, engine, Command{Type: "accept_transcript", Transcript: &metadata})
	}
	for _, metadata := range []Transcript{{Origin: "manual"}, {Origin: "tts"}, {Origin: "asr", TimingSource: "normalized"}, {TimingSource: "original"}} {
		metadata.ID = NewID("bad-origin")
		metadata.AssetID = "read-source"
		metadata.SourceFingerprint = "sha256:reading"
		metadata.RawWords = timedWords("词")
		before := engine.Snapshot()
		_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: NewID("reject"), Commands: []Command{{Type: "accept_transcript", Transcript: &metadata}}})
		assertCode(t, err, "transcript_origin")
	}
	_, err = engine.Apply(EditRequest{BaseRevision: engine.Snapshot().Revision, IdempotencyKey: "layout-on-wrong-command", Commands: []Command{{Type: "update_project", Name: "不允许", CaptionLayout: &CaptionLayout{Mode: "word"}}}})
	assertCode(t, err, "caption_layout")
	data, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	var reopened Project
	if err := json.Unmarshal(data, &reopened); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(reopened); err != nil {
		t.Fatal("序列化阅读组后无法重开", err)
	}
}

func TestReadableLinkedSourceStaleLocksAndInvalidWordSequences(t *testing.T) {
	engine, project := readableFixture(t, timedWords("你", "好", "世", "界"), &CaptionLayout{Mode: "readable"})
	video := project.Clips[clipIndex(&project, "read-clip")]
	video.LinkGroup = "reading-linked"
	audio := DefaultClip("linked-reading-audio", "track-audio-1", "audio")
	audio.AssetID, audio.StartFrame, audio.DurationFrames, audio.LinkGroup = video.AssetID, video.StartFrame, video.DurationFrames, video.LinkGroup
	project = apply(t, engine, Command{Type: "update_clip", Clip: &video}, Command{Type: "add_clip", Clip: &audio})
	if len(readingClips(project, "")) != 1 {
		t.Fatal("linked音画重复派生同一阅读组")
	}
	video.StartFrame += 60
	project = apply(t, engine, Command{Type: "update_clip", Clip: &video})
	if readingClips(project, video.ID)[0].StartFrame < 90 {
		t.Fatal("linked源移动没有带阅读字幕")
	}
	track := project.Tracks[trackIndex(&project, "track-caption-1")]
	track.Locked = true
	before := apply(t, engine, Command{Type: "update_track", Track: &track})
	_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: "locked-reading-layout", Commands: []Command{{Type: "generate_bound_captions", TranscriptID: "read-transcript", TrackID: track.ID, CaptionLayout: &CaptionLayout{Mode: "readable", MaxCharsPerLine: 2, MaxLines: 1}}}})
	assertCode(t, err, "track_locked")
	if !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("锁轨阅读重排没有原子回滚")
	}
	track.Locked = false
	project = apply(t, engine, Command{Type: "update_track", Track: &track})
	bad := CloneProject(project)
	for i := range bad.Clips {
		if bad.Clips[i].CaptionSource != nil {
			ids := bad.Clips[i].CaptionSource.WordIDs
			ids[0], ids[1] = ids[1], ids[0]
			break
		}
	}
	if err := ValidateProject(bad); err == nil {
		t.Fatal("倒置多词绑定没有拒绝")
	}
	asset := project.Assets[0]
	asset.Fingerprint = "sha256:changed-reading"
	project = apply(t, engine, Command{Type: "update_asset", Asset: &asset})
	if !project.Transcripts[0].Stale || !readingClips(project, video.ID)[0].CaptionSource.Stale {
		t.Fatal("阅读组未保留明确陈旧状态")
	}
	_, err = engine.Apply(EditRequest{BaseRevision: project.Revision, IdempotencyKey: "stale-reading-layout", Commands: []Command{{Type: "generate_bound_captions", TranscriptID: "read-transcript", TrackID: "track-caption-1", CaptionLayout: &CaptionLayout{Mode: "readable"}}}})
	assertCode(t, err, "transcript_stale")
}

func TestReadableRepartitionAndNewBaseDoNotTurnOldAutomaticLanesIntoManualGroups(t *testing.T) {
	engine, project := readableFixture(t, timedWords("今", "天", "一", "起", "剪", "辑", "视", "频"), nil)
	target := Track{ID: "new-readable-base", Name: "新阅读字幕轨", Kind: "caption"}
	project = apply(t, engine, Command{Type: "add_track", Track: &target})
	project = apply(t, engine, Command{Type: "generate_bound_captions", TranscriptID: "read-transcript", TrackID: target.ID, CaptionLayout: &CaptionLayout{Mode: "readable", MaxCharsPerLine: 4, MaxLines: 1}})
	clips := readingClips(project, "read-clip")
	if len(clips) != 2 {
		t.Fatalf("旧自动逐词轨被误认手改，重分句仍有%d组", len(clips))
	}
	for _, clip := range clips {
		if len(clip.CaptionSource.WordIDs) != 4 || clip.CaptionSource.StyleOverride {
			t.Fatal("自动组被错误添加了人工覆盖标记")
		}
	}
	if project.Transcripts[0].CaptionTrackID != target.ID {
		t.Fatal("最终目标没有使用新基准轨")
	}
}

func TestNewReadableBaseKeepsRealManualOverridesAndLegacyManualPlacement(t *testing.T) {
	engine, project := readableFixture(t, timedWords("你", "好", "世", "界"), nil)
	manual := readingClips(project, "read-clip")[1]
	manual.Text, manual.TextStyle.FontSize, manual.Transform.Y = "手改保留", 70, -90
	target := Track{ID: "new-protected-readable", Name: "新字幕轨", Kind: "caption"}
	project = apply(t, engine, Command{Type: "update_clip", Clip: &manual}, Command{Type: "add_track", Track: &target})
	project = apply(t, engine, Command{Type: "generate_bound_captions", TranscriptID: "read-transcript", TrackID: target.ID, CaptionLayout: &CaptionLayout{Mode: "readable"}})
	kept := project.Clips[clipIndex(&project, manual.ID)]
	if kept.Text != manual.Text || kept.TextStyle != manual.TextStyle || kept.Transform != manual.Transform || !kept.CaptionSource.StyleOverride {
		t.Fatal("原基准切换误丢真实人工覆盖")
	}
	// 模拟旧版只换展示轨但没有置StyleOverride的已保存布局。
	legacy := CloneProject(project)
	extra := Track{ID: "legacy-manual-lane", Name: "旧手动布局", Kind: "caption"}
	legacy.Tracks = append(legacy.Tracks, extra)
	for i := range legacy.Clips {
		if legacy.Clips[i].CaptionSource != nil {
			legacy.Clips[i].TrackID = extra.ID
			legacy.Clips[i].Transform.Y = -77
			legacy.Clips[i].CaptionSource.StyleOverride = false
			break
		}
	}
	legacyEngine, err := NewEngine(legacy)
	if err != nil {
		t.Fatal(err)
	}
	lastTarget := Track{ID: "last-reading-base", Name: "最终轨", Kind: "caption"}
	legacy = apply(t, legacyEngine, Command{Type: "add_track", Track: &lastTarget})
	oldID := ""
	for _, clip := range legacy.Clips {
		if clip.TrackID == extra.ID {
			oldID = clip.ID
			break
		}
	}
	legacy = apply(t, legacyEngine, Command{Type: "generate_bound_captions", TranscriptID: "read-transcript", TrackID: lastTarget.ID, CaptionLayout: &CaptionLayout{Mode: "readable"}})
	retained := legacy.Clips[clipIndex(&legacy, oldID)]
	if retained.Transform.Y != -77 || !retained.CaptionSource.StyleOverride {
		t.Fatal("旧手动展示布局没有按原基准识别并保护")
	}
}
