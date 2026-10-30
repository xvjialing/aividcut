package editor

import (
	"math/big"
	"reflect"
	"testing"
)

func sampleAsset(id string, count int64, rate int) Asset {
	return Asset{ID: id, Name: id + ".wav", Kind: "audio", Path: "/fixture/" + id + ".wav", DurationUs: count * 1_000_000 / int64(rate), HasAudio: true, Fingerprint: "sha256:" + id, AudioSampleRate: rate, AudioSampleCount: &count}
}
func preciseAudioFixture(t *testing.T, count int64, rate int) (*Engine, Project, Clip) {
	t.Helper()
	project := NewProject("精确声音尾端")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	asset := sampleAsset("precise", count, rate)
	project.Assets = []Asset{asset}
	clip := DefaultClip("precise-clip", "track-audio-1", "audio")
	clip.AssetID = asset.ID
	var err error
	clip, err = AudioClipToSourceEnd(clip, asset, project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	project.Clips = []Clip{clip}
	engine, err := NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	return engine, project, clip
}
func equalAudioTime(a, b AudioTime) bool {
	left, _ := a.Rat()
	right, _ := b.Rat()
	return left.Cmp(right) == 0
}

func TestFullAudioKeepsMeasuredLastSampleAndCeilsOnlyDisplaySlot(t *testing.T) {
	_, project, clip := preciseAudioFixture(t, 5777, 48000)
	if clip.DurationFrames != 4 || clip.AudioRange == nil || clip.AudioRange.Unit != "samples" || clip.AudioRange.Start != 0 || clip.AudioRange.End != 5777 {
		t.Fatal("完整导入没有保留最后一个实际样本")
	}
	oldFloor, _ := USToFrames(project.Assets[0].DurationUs, project.Settings)
	if oldFloor >= clip.DurationFrames {
		t.Fatal("fixture没有覆盖旧floor截尾")
	}
	resolved, err := ResolveAudioRange(clip, project.Assets[0], project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.MeasuredSamples || resolved.EndSample != 5777 || resolved.SampleRate != 48000 || !equalAudioTime(resolved.TimelineEnd, AudioTime{722125, 6}) {
		t.Fatalf("末端不是真实分数sample时刻：%+v", resolved)
	}
	if end, _ := resolved.TimelineEnd.Rat(); end.Cmp(frameTimeRat(clip.DurationFrames, project.Settings)) >= 0 {
		t.Fatal("把ceil占位当声音继续播放")
	}
	if !SourceRangeFits(clip, project.Assets[0], project.Settings) {
		t.Fatal("样本实测末端略超过floor微秒元数据时不应吞样本")
	}
}

func TestUnknownSampleCountUsesDeclaredMicrosecondEndWithoutFabricatingMeasurement(t *testing.T) {
	project := NewProject("未知samples")
	asset := Asset{ID: "us", Name: "us.wav", Kind: "audio", Path: "/fixture/us.wav", DurationUs: 100001, HasAudio: true, Fingerprint: "sha256:us", AudioSampleRate: 48000}
	clip := DefaultClip("us-clip", "track-audio-1", "audio")
	clip.AssetID = asset.ID
	clip, err := AudioClipToSourceEnd(clip, asset, project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveAudioRange(clip, asset, project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if clip.DurationFrames != 4 || clip.AudioRange.Unit != "us" || clip.AudioRange.End != 100001 || resolved.MeasuredSamples || resolved.EndSample != 0 || asset.AudioSampleCount != nil {
		t.Fatal("把duration反推成实测samples或仍然floor截尾")
	}
}

func TestSampleSplitPartitionsExactlyAndRecursiveCutsDoNotAccumulateMicrosecondLoss(t *testing.T) {
	engine, project, original := preciseAudioFixture(t, 5777, 48000)
	before, _ := ResolveAudioRange(original, project.Assets[0], project.Settings)
	project = apply(t, engine, Command{Type: "split_clip", ClipID: original.ID, AtFrame: 1})
	left, right := project.Clips[0], project.Clips[1]
	if left.AudioRange.End != 1602 || right.AudioRange.Start != 1602 || right.AudioRange.End != 5777 || left.AudioRange.End-left.AudioRange.Start+right.AudioRange.End-right.AudioRange.Start != 5777 {
		t.Fatal("29.97切口丢失或重复sample")
	}
	if right.AudioRange.OffsetNum != 25 || right.AudioRange.OffsetDen != 3 {
		t.Fatalf("右段未保存真实子帧起点：%+v", right.AudioRange)
	}
	l, _ := ResolveAudioRange(left, project.Assets[0], project.Settings)
	r, _ := ResolveAudioRange(right, project.Assets[0], project.Settings)
	if !equalAudioTime(l.TimelineEnd, r.TimelineStart) || !equalAudioTime(r.TimelineEnd, before.TimelineEnd) {
		t.Fatal("共同sample切口产生真实声音缝隙或拖长尾端")
	}
	project = apply(t, engine, Command{Type: "split_clip", ClipID: right.ID, AtFrame: 2})
	var last Clip
	total := int64(0)
	for _, clip := range project.Clips {
		total += clip.AudioRange.End - clip.AudioRange.Start
		if clip.StartFrame == 2 {
			last = clip
		}
	}
	if total != 5777 || last.AudioRange.Start != 3204 || last.SourceInUs != 66733 {
		t.Fatal("连续分割把帧的分数微秒不断截断或丢样本")
	}
	if err := ValidateProject(project); err != nil {
		t.Fatal(err)
	}
}

func TestDirectLeftTrimMatchesSplitThenDeleteAndKeepsTrueTail(t *testing.T) {
	engine, project, clip := preciseAudioFixture(t, 5777, 48000)
	trimmed := clip
	trimmed.StartFrame = 1
	trimmed.DurationFrames--
	trimmed.SourceInUs, _ = SourceDurationUs(1, project.Settings, 1, 1)
	project = apply(t, engine, Command{Type: "update_clip", Clip: &trimmed})
	trimmed = project.Clips[0]
	other, splitProject, source := preciseAudioFixture(t, 5777, 48000)
	splitProject = apply(t, other, Command{Type: "split_clip", ClipID: source.ID, AtFrame: 1})
	right := splitProject.Clips[1]
	a, _ := ResolveAudioRange(trimmed, project.Assets[0], project.Settings)
	b, _ := ResolveAudioRange(right, splitProject.Assets[0], splitProject.Settings)
	if trimmed.AudioRange.Start != right.AudioRange.Start || trimmed.AudioRange.End != 5777 || !equalAudioTime(a.TimelineStart, b.TimelineStart) || !equalAudioTime(a.TimelineEnd, b.TimelineEnd) {
		t.Fatal("直接trim与split后删左选择了不同sample或吞尾")
	}
}

func TestPreciseMoveCopySpeedAndRightTrimPreserveExplicitSourceMeaning(t *testing.T) {
	engine, project, clip := preciseAudioFixture(t, 5777, 48000)
	clip.StartFrame = 30
	project = apply(t, engine, Command{Type: "update_clip", Clip: &clip})
	moved := project.Clips[0]
	if moved.AudioRange.End != 5777 || moved.AudioRange.Start != 0 {
		t.Fatal("移动改变源样本范围")
	}
	copy := moved
	copy.ID, copy.StartFrame = "precise-copy", 60
	project = apply(t, engine, Command{Type: "duplicate_clip", ClipID: moved.ID, Clip: &copy})
	if project.Clips[1].AudioRange.End != 5777 || project.Clips[1].AudioRange.Start != 0 {
		t.Fatal("复制把真实尾端扩成帧边界")
	}
	fast := project.Clips[0]
	fast.SpeedNum = 2
	project = apply(t, engine, Command{Type: "update_clip", Clip: &fast})
	fast = project.Clips[0]
	if fast.DurationFrames != 2 || fast.AudioRange.End != 5777 {
		t.Fatal("音频改速没有保持完整源样本并重新ceil显示长度")
	}
	engine, project, clip = preciseAudioFixture(t, 5777, 48000)
	clip.DurationFrames = 3
	project = apply(t, engine, Command{Type: "update_clip", Clip: &clip})
	if project.Clips[0].AudioRange.End != 4805 {
		t.Fatal("显式右trim没有按所选帧界选择真实sample")
	}
}

func TestLateAudioCoverageAndEmptySplitDoNotMoveSoundToZero(t *testing.T) {
	project := NewProject("晚起真实音轨")
	asset := sampleAsset("late", 48000, 48000)
	asset.Kind = "video"
	asset.DurationUs = 3_000_000
	asset.AudioStartUs = 500_000
	project.Assets = []Asset{asset}
	clip := DefaultClip("late-clip", "track-audio-1", "audio")
	clip.AssetID = asset.ID
	clip, err := AudioClipToSourceEnd(clip, asset, project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	project.Clips = []Clip{clip}
	resolved, err := ResolveAudioRange(clip, asset, project.Settings)
	if err != nil || resolved.TimelineStart != (AudioTime{500000, 1}) || resolved.TimelineEnd != (AudioTime{1500000, 1}) {
		t.Fatal("把late音轨搬到了0或补齐至视频时长", err)
	}
	engine, err := NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	project = apply(t, engine, Command{Type: "split_clip", ClipID: clip.ID, AtFrame: 3})
	left, right := project.Clips[0], project.Clips[1]
	empty, _ := ResolveAudioRange(left, asset, project.Settings)
	remaining, _ := ResolveAudioRange(right, asset, project.Settings)
	if !empty.Empty || remaining.TimelineStart != (AudioTime{500000, 1}) || right.AudioRange.End != 48000 {
		t.Fatal("无覆盖的左段不应制造音频，右段仍从实际late起点开始")
	}
}

func TestAudioOverlapUsesTrueRangeNotCeilPlaceholder(t *testing.T) {
	_, project, first := preciseAudioFixture(t, 5777, 48000)
	secondAsset := sampleAsset("second", 5777, 48000)
	secondAsset.AudioStartUs = 40000
	secondAsset.DurationUs += 40000
	project.Assets = append(project.Assets, secondAsset)
	second := DefaultClip("second-clip", "track-audio-1", "audio")
	second.AssetID, second.StartFrame = secondAsset.ID, 3
	var err error
	second, err = AudioClipToSourceEnd(second, secondAsset, project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	project.Clips = append(project.Clips, second)
	if first.StartFrame+first.DurationFrames <= second.StartFrame {
		t.Fatal("fixture没有覆盖显示占位重叠")
	}
	if err := ValidateProject(project); err != nil {
		t.Fatal("真实音频不重叠时不能仅因ceil占位拒绝", err)
	}
	project.Clips[1].StartFrame = 2
	if err := ValidateProject(project); err == nil {
		t.Fatal("真实音频重叠没有拒绝")
	}
}

func TestPreciseCaptionBindingStopsAtActualAudioEnd(t *testing.T) {
	engine, project, clip := preciseAudioFixture(t, 5777, 48000)
	asset := project.Assets[0]
	asset.DurationUs = 150000
	project = apply(t, engine, Command{Type: "update_asset", Asset: &asset})
	transcript := Transcript{ID: "tail-words", AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, RawWords: []SourceWord{{ID: "spoken", StartUs: 90000, EndUs: 110000, Text: "实际说完"}, {ID: "outside", StartUs: 125000, EndUs: 130000, Text: "仅在ceil占位中"}}}
	project = apply(t, engine, Command{Type: "accept_transcript", Transcript: &transcript}, Command{Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1", CaptionLayout: &CaptionLayout{Mode: "readable"}})
	clips := readingClips(project, clip.ID)
	if len(clips) != 1 || len(clips[0].CaptionSource.WordIDs) != 1 || clips[0].CaptionSource.WordIDs[0] != "spoken" {
		t.Fatal("字幕把ceil占位误当真实音频范围")
	}
}

func TestMeasuredClockClaimsInvalidRangesAndLockedTrimAreAtomic(t *testing.T) {
	engine, project, clip := preciseAudioFixture(t, 5777, 48000)
	for _, scenario := range []string{"count", "rate", "clock", "anchor"} {
		t.Run(scenario, func(t *testing.T) {
			before := engine.Snapshot()
			value := cloneClip(clip)
			switch scenario {
			case "count":
				value.AudioRange.End = 5778
			case "rate":
				value.AudioRange.SampleRate = 44100
			case "clock":
				value.AudioRange.SourceOriginUs = 1
			case "anchor":
				value.AudioRange.OffsetNum = 1000
				value.AudioRange.OffsetDen = 1
			}
			_, err := engine.Apply(EditRequest{BaseRevision: before.Revision, IdempotencyKey: scenario, Commands: []Command{{Type: "update_clip", Clip: &value}}})
			if err == nil || !reflect.DeepEqual(before, engine.Snapshot()) {
				t.Fatal("伪造采样/源锚点没有整批拒绝")
			}
		})
	}
	track := project.Tracks[trackIndex(&project, clip.TrackID)]
	track.Locked = true
	project = apply(t, engine, Command{Type: "update_track", Track: &track})
	before := engine.Snapshot()
	clip.DurationFrames--
	_, err := engine.Apply(EditRequest{BaseRevision: project.Revision, IdempotencyKey: "locked-precise-trim", Commands: []Command{{Type: "update_clip", Clip: &clip}}})
	assertCode(t, err, "track_locked")
	if !reflect.DeepEqual(before, engine.Snapshot()) {
		t.Fatal("锁音轨精确trim部分改变状态")
	}
}

func TestAudioClockAndRangeSnapshotPointersAreIsolated(t *testing.T) {
	engine, _, _ := preciseAudioFixture(t, 5777, 48000)
	snapshot := engine.Snapshot()
	*snapshot.Assets[0].AudioSampleCount = 1
	snapshot.Clips[0].AudioRange.End = 1
	current := engine.Snapshot()
	if *current.Assets[0].AudioSampleCount != 5777 || current.Clips[0].AudioRange.End != 5777 {
		t.Fatal("采样指针越过核心快照边界")
	}
}

func TestLinkedPictureKeepsFrameSemanticsWhileAudioTrimsAtExactClock(t *testing.T) {
	project := NewProject("画面帧与真实原声")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	asset := sampleAsset("av", 5777, 48000)
	asset.Kind, asset.DurationUs = "video", 2_000_000
	project.Assets = []Asset{asset}
	visual := DefaultClip("visual", "track-video-1", "video")
	visual.AssetID, visual.DurationFrames, visual.LinkGroup = "av", 4, "av-link"
	audio := DefaultClip("carrier", "track-audio-1", "audio")
	audio.AssetID, audio.LinkGroup = "av", "av-link"
	var err error
	audio, err = AudioClipToSourceEnd(audio, asset, project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	project.Clips = []Clip{visual, audio}
	engine, err := NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	visual.StartFrame, visual.DurationFrames = 1, 3
	visual.SourceInUs, _ = SourceDurationUs(1, project.Settings, 1, 1)
	project = apply(t, engine, Command{Type: "update_clip", Clip: &visual})
	if project.Clips[0].SourceInUs != 33366 || project.Clips[0].DurationFrames != 3 {
		t.Fatal("音频精度修改了原画面帧裁剪语义")
	}
	carrier := project.Clips[1]
	if carrier.AudioRange.Start != 1602 || carrier.AudioRange.End != 5777 || carrier.AudioRange.OffsetNum != 25 || carrier.AudioRange.OffsetDen != 3 {
		t.Fatal("linked原声没有跟随真实ceil样本切口")
	}
	transcript := Transcript{ID: "av-words", AssetID: "av", SourceFingerprint: asset.Fingerprint, RawWords: []SourceWord{{ID: "real", StartUs: 90000, EndUs: 110000, Text: "原声范围"}, {ID: "not-real", StartUs: 125000, EndUs: 130000, Text: "无声ceil区"}}}
	project = apply(t, engine, Command{Type: "accept_transcript", Transcript: &transcript}, Command{Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"})
	for _, clip := range readingClips(project, "") {
		for _, id := range clip.CaptionSource.WordIDs {
			if id == "not-real" {
				t.Fatal("video/audio linked字幕没有遵守实测声音EOS")
			}
		}
	}
}

func TestExactAudioSplitUndoRedoRestoresAllSampleEndpoints(t *testing.T) {
	engine, before, clip := preciseAudioFixture(t, 5777, 44100)
	after := apply(t, engine, Command{Type: "split_clip", ClipID: clip.ID, AtFrame: 1})
	if after.Clips[0].AudioRange.End != 1472 || after.Clips[1].AudioRange.Start != 1472 {
		t.Fatal("44.1kHz分数帧切口不正确")
	}
	left, _ := ResolveAudioRange(after.Clips[0], after.Assets[0], after.Settings)
	right, _ := ResolveAudioRange(after.Clips[1], after.Assets[0], after.Settings)
	if !equalAudioTime(left.TimelineEnd, right.TimelineStart) {
		t.Fatal("不同采样率声明不应产生物理缝隙")
	}
	undone, err := engine.Undo()
	if err != nil || !reflect.DeepEqual(audioNoRevision(before), audioNoRevision(undone)) {
		t.Fatal("Undo未恢复精确源末端", err)
	}
	redone, err := engine.Redo()
	if err != nil || !reflect.DeepEqual(audioNoRevision(after), audioNoRevision(redone)) {
		t.Fatal("Redo未恢复同一共享sample边界", err)
	}
}

func audioNoRevision(project Project) Project { project.Revision = 0; return project }

// 本文件的分数断言不用浮点近似。
func assertRat(t *testing.T, value AudioTime, numerator, denominator int64) {
	t.Helper()
	actual, _ := value.Rat()
	if actual.Cmp(new(big.Rat).SetFrac64(numerator, denominator)) != 0 {
		t.Fatalf("分数不符：%+v", value)
	}
}
