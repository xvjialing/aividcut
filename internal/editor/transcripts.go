package editor

import (
	"math"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxTranscriptWords = 100_000

func cloneTranscript(value Transcript) Transcript {
	if value.CaptionLayout != nil {
		copy := *value.CaptionLayout
		value.CaptionLayout = &copy
	}
	value.RawWords = append([]SourceWord{}, value.RawWords...)
	value.AcceptedWords = append([]SourceWord{}, value.AcceptedWords...)
	value.CaptionExclusions = append([]CaptionExclusion(nil), value.CaptionExclusions...)
	return value
}

func transcriptIndex(project *Project, id string) int {
	for index, transcript := range project.Transcripts {
		if transcript.ID == id {
			return index
		}
	}
	return -1
}

func transcriptAsset(project *Project, id string) (Asset, bool) {
	for _, asset := range project.Assets {
		if asset.ID == id {
			return asset, true
		}
	}
	return Asset{}, false
}

func validTranscriptText(value string, limit int, required bool) bool {
	if !utf8.ValidString(value) || len(value) > limit || required && strings.TrimSpace(value) == "" {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) && char != '\n' && char != '\t' {
			return false
		}
	}
	return true
}

func validTranscriptID(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || len(value) > 256 {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

// ValidateSourceWords 校验源词的稳定身份和有效源区间，不接纳或修改工程数据。
func ValidateSourceWords(words []SourceWord, duration int64) error {
	if duration <= 0 {
		return problem("transcript_word", "源转写需要有效素材时长")
	}
	if len(words) > MaxTranscriptWords {
		return problem("transcript_limit", "单份转写超过 100000 个词条限制")
	}
	ids, textBytes := make(map[string]bool, len(words)), 0
	for _, word := range words {
		if !validTranscriptID(word.ID) || ids[word.ID] || word.StartUs < 0 || word.EndUs <= word.StartUs || word.EndUs > duration || !validTranscriptText(word.Text, 1<<20, true) || !validTranscriptText(word.SpeakerID, 256, false) {
			return problem("transcript_word", "转写词标识、文本或源时间区间无效/重复")
		}
		ids[word.ID] = true
		textBytes += len(word.Text)
		if textBytes > 16<<20 {
			return problem("transcript_limit", "单份转写文本超过 16MiB")
		}
	}
	return nil
}

func validateTranscripts(project Project, assets map[string]Asset, tracks map[string]Track) error {
	transcripts := map[string]Transcript{}
	wordIDs := map[string]map[string]int{}
	wordRanks := map[string]map[string]int{}
	excludedBySource := map[string]map[string]bool{}
	trackOrder := map[string]int{}
	for i, track := range project.Tracks {
		trackOrder[track.ID] = i
	}
	if len(project.Transcripts) > 1000 {
		return problem("transcript_limit", "工程转写份数超过 1000 份限制")
	}
	activeAssets := map[string]bool{}
	total := 0
	for _, transcript := range project.Transcripts {
		asset, exists := assets[transcript.AssetID]
		if !validTranscriptID(transcript.ID) || !exists || !validKind(asset.Kind, "video", "audio") || transcript.SourceFingerprint == "" || transcript.SourceDurationUs <= 0 || transcript.Revision < 1 || !validTranscriptText(transcript.Language, 128, false) {
			return problem("transcript", "源转写身份、源指纹、时长或修订号无效")
		}
		if err := validateTranscriptOrigin(transcript); err != nil {
			return err
		}
		if _, err := NormalizeCaptionLayout(transcript.CaptionLayout); err != nil {
			return err
		}
		if _, duplicate := transcripts[transcript.ID]; duplicate {
			return problem("transcript", "源转写标识重复")
		}
		if transcript.Stale != (transcript.SourceFingerprint != asset.Fingerprint) {
			return problem("transcript_stale", "转写源指纹变化必须明确标记为陈旧")
		}
		if err := ValidateSourceWords(transcript.RawWords, transcript.SourceDurationUs); err != nil {
			return err
		}
		if err := ValidateSourceWords(transcript.AcceptedWords, transcript.SourceDurationUs); err != nil {
			return err
		}
		total += len(transcript.RawWords) + len(transcript.AcceptedWords)
		if total > 1_000_000 {
			return problem("transcript_limit", "工程转写词条总数超过当前限制")
		}
		if transcript.CaptionTrackID != "" {
			track, ok := tracks[transcript.CaptionTrackID]
			if !ok || track.Kind != "caption" || activeAssets[transcript.AssetID] {
				return problem("caption_binding", "字幕派生目标轨道无效或同一素材启用了多份转写")
			}
			activeAssets[transcript.AssetID] = true
		}
		transcripts[transcript.ID] = transcript
		wordIDs[transcript.ID] = make(map[string]int, len(transcript.AcceptedWords))
		for i, word := range transcript.AcceptedWords {
			wordIDs[transcript.ID][word.ID] = i
		}
		wordRanks[transcript.ID] = make(map[string]int, len(transcript.AcceptedWords))
		for i, word := range orderedCaptionWords(transcript.AcceptedWords) {
			wordRanks[transcript.ID][word.ID] = i
		}
		for _, exclusion := range transcript.CaptionExclusions {
			key := captionSourceKey(transcript.ID, exclusion.SourceClipID)
			if excludedBySource[key] == nil {
				excludedBySource[key] = map[string]bool{}
			}
			excludedBySource[key][exclusion.WordID] = true
		}
	}
	clips := make(map[string]*Clip, len(project.Clips))
	for i := range project.Clips {
		clips[project.Clips[i].ID] = &project.Clips[i]
	}
	mappers := map[string]*wordMapper{}
	usedWords := map[string]map[string]bool{}
	for _, clip := range project.Clips {
		binding := clip.CaptionSource
		if binding == nil {
			continue
		}
		transcript, ok := transcripts[binding.TranscriptID]
		source, exists := clips[binding.SourceClipID]
		if clip.Kind != "caption" || !ok || !exists || !validKind(source.Kind, "video", "audio") || source.AssetID != transcript.AssetID || transcript.CaptionTrackID == "" || len(binding.WordIDs) == 0 || len(binding.WordIDs) > MaxTranscriptWords || binding.Stale != transcript.Stale || clip.StartFrame < source.StartFrame || clip.StartFrame+clip.DurationFrames > source.StartFrame+source.DurationFrames {
			return problem("caption_binding", "绑定字幕的转写、源片段或时间区间无效")
		}
		key := captionSourceKey(transcript.ID, source.ID)
		if usedWords[key] == nil {
			usedWords[key] = map[string]bool{}
		}
		words := make([]captionWord, 0, len(binding.WordIDs))
		previousRank := -1
		for _, id := range binding.WordIDs {
			index, found := wordIDs[transcript.ID][id]
			if !found || usedWords[key][id] || excludedBySource[key][id] {
				return problem("caption_binding", "字幕词引用不存在、重复或已排除")
			}
			rank := wordRanks[transcript.ID][id]
			if previousRank >= 0 && rank != previousRank+1 {
				return problem("caption_binding", "阅读字幕必须引用真实时间顺序中连续的词")
			}
			previousRank = rank
			usedWords[key][id] = true
			word := transcript.AcceptedWords[index]
			if len(words) > 0 {
				previous := words[len(words)-1].word
				if word.StartUs < previous.EndUs || speakerBoundary(previous, word) {
					return problem("caption_binding", "一组阅读字幕不能混合重叠词或不同说话人")
				}
			}
			words = append(words, captionWord{word: word, rank: rank})
		}
		if !transcript.Stale {
			mapper := mappers[source.ID]
			if mapper == nil {
				var err error
				mapper, err = newCaptionWordMapper(*source, assets[source.AssetID], project.Settings)
				if err != nil {
					return err
				}
				mappers[source.ID] = mapper
			}
			for i := range words {
				span, visible, err := mapper.mapWord(words[i].word)
				if err != nil || !visible {
					return problem("caption_binding", "字幕引用的词不在当前源片段可见区间")
				}
				words[i].span = span
			}
			span := groupSpan(words)
			layout, err := effectiveCaptionLayout(transcript)
			if err != nil {
				return err
			}
			protected := protectedCaption(clip, transcript, trackOrder)
			if layout.Mode == "word" && len(words) > 1 && !protected {
				return problem("caption_binding", "逐词模式不能产生未保护的多词组")
			}
			if layout.Mode == "readable" && !protected && !durationWithin(span, project.Settings, layout.MaxDurationUs) {
				return problem("caption_layout", "阅读字幕超过声明的持续时间上限")
			}
			text, fits := groupText(words, layout)
			if span.StartFrame != clip.StartFrame || span.DurationFrames != clip.DurationFrames || span.Partial != binding.Partial || !binding.TextOverride && (!fits || clip.Text != text) {
				return problem("caption_binding", "绑定字幕未按当前接受词和源片段映射派生")
			}
		}
	}
	for _, transcript := range project.Transcripts {
		if len(transcript.CaptionExclusions) > 100_000 {
			return problem("caption_limit", "字幕排除条目过多")
		}
		seen := map[CaptionExclusion]bool{}
		for _, exclusion := range transcript.CaptionExclusions {
			source, exists := clips[exclusion.SourceClipID]
			_, wordExists := wordIDs[transcript.ID][exclusion.WordID]
			if !exists || !validKind(source.Kind, "video", "audio") || source.AssetID != transcript.AssetID || !wordExists || seen[exclusion] {
				return problem("caption_binding", "字幕排除引用的源片段或接受词无效/重复")
			}
			seen[exclusion] = true
		}
	}
	return nil
}

func applyTranscriptCommand(project *Project, command Command) error {
	switch command.Type {
	case "accept_transcript":
		if command.Transcript == nil {
			return problem("transcript", "接纳转写需要原始识别词")
		}
		value := cloneTranscript(*command.Transcript)
		if err := validateTranscriptOrigin(value); err != nil {
			return err
		}
		layout, err := NormalizeCaptionLayout(value.CaptionLayout)
		if err != nil {
			return err
		}
		value.CaptionLayout = layout
		if value.ID == "" || transcriptIndex(project, value.ID) >= 0 {
			return problem("transcript", "接纳转写需要新的稳定标识，不能覆盖原始识别结果")
		}
		asset, exists := transcriptAsset(project, value.AssetID)
		if !exists || !validKind(asset.Kind, "video", "audio") || asset.Fingerprint == "" || value.SourceFingerprint != asset.Fingerprint {
			return problem("transcript_stale", "转写必须对应当前已核对指纹的音视频源")
		}
		if err := ValidateSourceWords(value.RawWords, asset.DurationUs); err != nil {
			return err
		}
		value.AcceptedWords = append([]SourceWord{}, value.RawWords...)
		value.Revision, value.SourceDurationUs, value.CaptionTrackID, value.Stale = 1, asset.DurationUs, "", false
		value.CaptionExclusions = nil
		project.Transcripts = append(project.Transcripts, value)
	case "correct_transcript":
		index := transcriptIndex(project, command.TranscriptID)
		if index < 0 {
			return problem("not_found", "需要纠错的转写不存在")
		}
		value := &project.Transcripts[index]
		if value.Stale {
			return problem("transcript_stale", "源内容已改变，请重新取得原始转写，不能静默复用旧词时间")
		}
		if value.Revision != command.TranscriptRevision {
			return problem("transcript_revision", "转写接受版本已变化，请刷新后再纠错")
		}
		if value.Revision == math.MaxInt64 {
			return problem("transcript_revision", "转写修订号超出范围")
		}
		if err := ValidateSourceWords(command.Words, value.SourceDurationUs); err != nil {
			return err
		}
		value.AcceptedWords = append([]SourceWord{}, command.Words...)
		value.Revision++
	case "generate_bound_captions":
		index := transcriptIndex(project, command.TranscriptID)
		if index < 0 {
			return problem("not_found", "需要生成字幕的源转写不存在")
		}
		if project.Transcripts[index].Stale {
			return problem("transcript_stale", "转写已陈旧，请重新接纳对应当前素材的原始转写")
		}
		if command.CaptionLayout != nil {
			layout, err := NormalizeCaptionLayout(command.CaptionLayout)
			if err != nil {
				return err
			}
			project.Transcripts[index].CaptionLayout = layout
		}
		trackID := command.TrackID
		if trackID == "" {
			for _, track := range project.Tracks {
				if track.Kind == "caption" && !track.Locked && !track.Hidden {
					trackID = track.ID
					break
				}
			}
			if trackID == "" {
				track := Track{ID: NewID("caption-track"), Name: "源字幕", Kind: "caption"}
				if err := applyCommand(project, Command{Type: "add_track", Track: &track}); err != nil {
					return err
				}
				trackID = track.ID
			}
		}
		track := trackIndex(project, trackID)
		if track < 0 || project.Tracks[track].Kind != "caption" {
			return problem("caption_binding", "派生字幕需要有效的字幕轨")
		}
		if err := editableTrack(project, trackID); err != nil {
			return err
		}
		for i := range project.Transcripts {
			if project.Transcripts[i].AssetID == project.Transcripts[index].AssetID {
				project.Transcripts[i].CaptionTrackID = ""
			}
		}
		project.Transcripts[index].CaptionTrackID = trackID
	case "detach_caption":
		index := clipIndex(project, command.ClipID)
		if index < 0 || project.Clips[index].CaptionSource == nil {
			return problem("caption_binding", "目标不是绑定字幕")
		}
		if err := editableTrack(project, project.Clips[index].TrackID); err != nil {
			return err
		}
		suppressCaption(project, project.Clips[index])
		project.Clips[index].CaptionSource = nil
	}
	return nil
}

func suppressCaption(project *Project, clip Clip) {
	if clip.CaptionSource == nil {
		return
	}
	index := transcriptIndex(project, clip.CaptionSource.TranscriptID)
	if index < 0 {
		return
	}
	value := &project.Transcripts[index]
	for _, wordID := range clip.CaptionSource.WordIDs {
		exclusion := CaptionExclusion{SourceClipID: clip.CaptionSource.SourceClipID, WordID: wordID}
		found := false
		for _, old := range value.CaptionExclusions {
			if old == exclusion {
				found = true
				break
			}
		}
		if !found {
			value.CaptionExclusions = append(value.CaptionExclusions, exclusion)
		}
	}
}

func protectBoundCaption(before Clip, after *Clip) error {
	if before.CaptionSource == nil {
		if after.CaptionSource != nil {
			return problem("caption_binding", "不能通过普通片段命令伪造字幕源绑定")
		}
		return nil
	}
	if before.Kind != after.Kind || before.AssetID != after.AssetID || before.StartFrame != after.StartFrame || before.DurationFrames != after.DurationFrames || before.SpeedNum != after.SpeedNum || before.SpeedDen != after.SpeedDen || before.SourceInUs != after.SourceInUs {
		return problem("caption_binding", "绑定字幕时间由源片段派生；请编辑源片段，或先解除字幕绑定")
	}
	copy := cloneClip(before)
	after.CaptionSource = copy.CaptionSource
	if after.Text != before.Text {
		after.CaptionSource.TextOverride = true
	}
	if after.TextStyle != before.TextStyle || after.Transform != before.Transform || after.TrackID != before.TrackID {
		after.CaptionSource.StyleOverride = true
	}
	return nil
}

func prepareDuplicateClip(project *Project, original, proposed Clip) (Clip, Clip, error) {
	copy := cloneClip(proposed)
	if err := editableTrack(project, copy.TrackID); err != nil {
		return Clip{}, Clip{}, err
	}
	copy.CaptionSource = nil
	explicitRange := copy.AudioRange != nil && !sameAudioRange(original.AudioRange, copy.AudioRange)
	if mediaSourceKind(original) {
		asset, _ := transcriptAsset(project, original.AssetID)
		if copy.SourcePhase != nil && !sameSourcePhase(original.SourcePhase, copy.SourcePhase) {
			return Clip{}, Clip{}, problem("source_phase", "复制不能直接改写源处理配方")
		}
		previous := original
		var err error
		original, err = CaptureSourcePhase(original, asset, project.Settings)
		if err != nil {
			return Clip{}, Clip{}, err
		}
		if sameAudioRange(previous.AudioRange, copy.AudioRange) {
			copy.AudioRange = cloneClip(original).AudioRange
		}
	}
	if original.AudioRange != nil {
		asset, _ := transcriptAsset(project, copy.AssetID)
		if err := adaptAudioDuplicate(original, &copy, asset, project.Settings); err != nil {
			return Clip{}, Clip{}, err
		}
	}
	if mediaSourceKind(copy) {
		asset, _ := transcriptAsset(project, copy.AssetID)
		if err := finishPhaseUpdate(original, &copy, asset, project.Settings, true, explicitRange); err != nil {
			return Clip{}, Clip{}, err
		}
	}
	return original, copy, nil
}

func duplicateClip(project *Project, command Command) error {
	index := clipIndex(project, command.ClipID)
	if index < 0 || command.Clip == nil {
		return problem("not_found", "复制需要原片段与新片段信息")
	}
	original, copy := project.Clips[index], cloneClip(*command.Clip)
	if copy.ID == "" || clipIndex(project, copy.ID) >= 0 || copy.Kind != original.Kind || copy.AssetID != original.AssetID {
		return problem("validation", "复制必须使用新的片段标识且保留素材归属")
	}
	original, copy, err := prepareDuplicateClip(project, original, copy)
	if err != nil {
		return err
	}
	ids := selectedIDs(project, original, linked(command))
	group := ""
	if original.LinkGroup != "" && linked(command) {
		group = NewID("link")
	}
	copy.LinkGroup = group
	var copies []Clip
	for _, source := range project.Clips {
		if !ids[source.ID] || source.ID == original.ID {
			continue
		}
		if err := editableTrack(project, source.TrackID); err != nil {
			return err
		}
		other := cloneClip(source)
		other.ID, other.LinkGroup, other.CaptionSource = NewID("clip"), group, nil
		position, ok := addDelta(other.StartFrame, copy.StartFrame, original.StartFrame)
		if !ok {
			return problem("validation", "复制关联片段的位置超出范围")
		}
		other.StartFrame = position
		if other.DurationFrames, ok = addDelta(other.DurationFrames, copy.DurationFrames, original.DurationFrames); !ok {
			return problem("validation", "复制关联片段的长度超出范围")
		}
		if validKind(other.Kind, "video", "audio") {
			if other.SourceInUs, ok = linkedSourceIn(other, original, copy); !ok {
				return problem("validation", "复制关联片段的素材入点超出范围")
			}
			if original.SpeedNum != copy.SpeedNum || original.SpeedDen != copy.SpeedDen {
				if other.SpeedNum, other.SpeedDen, ok = linkedSpeed(other, original, copy); !ok {
					return problem("validation", "复制关联片段的变速超出范围")
				}
			}
		}
		if mediaSourceKind(source) {
			asset, _ := transcriptAsset(project, source.AssetID)
			var err error
			source, err = CaptureSourcePhase(source, asset, project.Settings)
			if err != nil {
				return err
			}
		}
		if source.AudioRange != nil {
			asset, _ := transcriptAsset(project, other.AssetID)
			if err := adaptAudioDuplicate(source, &other, asset, project.Settings); err != nil {
				return err
			}
		}
		if mediaSourceKind(other) {
			asset, _ := transcriptAsset(project, other.AssetID)
			if err := finishPhaseUpdate(source, &other, asset, project.Settings, true, false); err != nil {
				return err
			}
		}
		copies = append(copies, other)
	}
	project.Clips = append(project.Clips, copy)
	project.Clips = append(project.Clips, copies...)
	return nil
}

func sameSourceTiming(before, after Clip) bool {
	return before.AssetID == after.AssetID && before.Kind == after.Kind && before.StartFrame == after.StartFrame && before.DurationFrames == after.DurationFrames && before.SourceInUs == after.SourceInUs && before.SpeedNum == after.SpeedNum && before.SpeedDen == after.SpeedDen && sameAudioRange(before.AudioRange, after.AudioRange)
}

func sameCaption(before, after Clip) bool {
	beforeBinding, afterBinding := before.CaptionSource, after.CaptionSource
	beforeLUT, afterLUT := before.LUT, after.LUT
	beforeAudio, afterAudio := before.AudioRange, after.AudioRange
	beforePhase, afterPhase := before.SourcePhase, after.SourcePhase
	beforeFade, afterFade := before.AudioFade, after.AudioFade
	before.CaptionSource, after.CaptionSource = nil, nil
	before.LUT, after.LUT = nil, nil
	before.AudioRange, after.AudioRange = nil, nil
	before.SourcePhase, after.SourcePhase = nil, nil
	before.AudioFade, after.AudioFade = nil, nil
	// 先比较可比较的值字段，避免每个字幕把整个大 Clip 装箱到反射接口。
	if before != after {
		return false
	}
	if !sameAudioRange(beforeAudio, afterAudio) || !sameSourcePhase(beforePhase, afterPhase) {
		return false
	}
	if (beforeLUT == nil) != (afterLUT == nil) || beforeLUT != nil && *beforeLUT != *afterLUT {
		return false
	}
	if (beforeFade == nil) != (afterFade == nil) || beforeFade != nil && *beforeFade != *afterFade {
		return false
	}
	return reflect.DeepEqual(beforeBinding, afterBinding)
}
