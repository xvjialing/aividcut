package editor

import (
	"math/big"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type captionWord struct {
	word SourceWord
	rank int
	span WordSpan
}
type captionGroup struct {
	words []captionWord
	span  WordSpan
	text  string
	old   *Clip
	exact bool
}

func orderedCaptionWords(words []SourceWord) []SourceWord {
	if sort.SliceIsSorted(words, func(i, j int) bool { return words[i].StartUs < words[j].StartUs }) {
		return words
	}
	values := append([]SourceWord{}, words...)
	sort.SliceStable(values, func(i, j int) bool { return values[i].StartUs < values[j].StartUs })
	return values
}

func captionGroupKey(transcriptID, sourceID string, ids []string) string {
	return bindingKey(transcriptID, sourceID, strings.Join(ids, "\x00"))
}
func captionSourceKey(transcriptID, sourceID string) string { return transcriptID + "\x00" + sourceID }
func captionIDs(words []captionWord) []string {
	ids := make([]string, len(words))
	for i, word := range words {
		ids[i] = word.word.ID
	}
	return ids
}

func cjkRune(value rune) bool {
	return unicode.In(value, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}
func latinWordRune(value rune) bool {
	return unicode.In(value, unicode.Latin) || unicode.IsDigit(value)
}
func closingPunctuation(value rune) bool {
	return strings.ContainsRune("，。、！？；：）》」』】〕〉”’!?.,:;)]}", value)
}
func openingPunctuation(value rune) bool {
	return strings.ContainsRune("（《「『【〔〈“‘([{", value)
}

// 只在拉丁单词边界补必要空格，原有空白逐字保留，中文词之间不凭空加空格。
func joinCaptionWords(words []SourceWord) string {
	text, _ := joinedCaptionParts(words)
	return text
}

func joinedCaptionParts(words []SourceWord) (string, []int) {
	var result strings.Builder
	boundaries := make([]int, 0, len(words))
	position := 0
	last := rune(0)
	lastBeforePunctuation := rune(0)
	for _, word := range words {
		text := word.Text
		first, _ := utf8.DecodeRuneInString(text)
		needsSpace := latinWordRune(last) && latinWordRune(first)
		if strings.ContainsRune(",;:.)]}”’", last) && latinWordRune(first) && latinWordRune(lastBeforePunctuation) {
			needsSpace = true
			if unicode.IsDigit(first) && unicode.IsDigit(lastBeforePunctuation) && (last == '.' || last == ',') {
				needsSpace = false
			}
		}
		if result.Len() > 0 && needsSpace && !unicode.IsSpace(last) && !unicode.IsSpace(first) {
			result.WriteByte(' ')
			position++
		}
		result.WriteString(text)
		position += utf8.RuneCountInString(text)
		boundaries = append(boundaries, position)
		for _, value := range text {
			if !closingPunctuation(value) && !unicode.IsSpace(value) {
				lastBeforePunctuation = value
			}
			last = value
		}
	}
	return result.String(), boundaries
}

// SourceTextParts 复用字幕拼接规则，返回源时间排序的词及每词末端的Unicode位置。
// 返回的词切片独立于调用方，用于只读检索证据，不修改源转写。
func SourceTextParts(words []SourceWord) ([]SourceWord, string, []int) {
	ordered := append([]SourceWord{}, words...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StartUs < ordered[j].StartUs })
	text, ends := joinedCaptionParts(ordered)
	return ordered, text, ends
}

func safeLineBoundary(values []rune, at int) bool {
	if at == len(values) {
		return true
	}
	left, right := values[at-1], values[at]
	if unicode.IsMark(right) || right == '\u200d' || left == '\u200d' || right >= 0x1f3fb && right <= 0x1f3ff {
		return false
	}
	if closingPunctuation(right) || openingPunctuation(left) {
		return false
	}
	if unicode.IsSpace(left) {
		return true
	}
	word := func(value rune) bool {
		return (unicode.IsLetter(value) || unicode.IsDigit(value) || unicode.IsMark(value)) && !cjkRune(value)
	}
	if word(left) && word(right) {
		return false
	}
	if strings.ContainsRune("'-’./_@", left) && word(right) && at > 1 && word(values[at-2]) {
		return false
	}
	if strings.ContainsRune("'-’./_@", right) && word(left) && at+1 < len(values) && word(values[at+1]) {
		return false
	}
	return true
}

func visibleLineLength(values []rune) int {
	end := len(values)
	for end > 0 && unicode.IsSpace(values[end-1]) {
		end--
	}
	return end
}

// 只插入换行，不删除已有空格或拆分拉丁词。单条带时间的长词放不下时明确拒绝。
func wrapCaptionText(text string, layout CaptionLayout, preferred map[int]bool) (string, bool) {
	lines := []string{}
	paragraphOffset := 0
	for _, paragraph := range strings.Split(text, "\n") {
		values := []rune(paragraph)
		if len(values) == 0 {
			lines = append(lines, "")
		}
		for start := 0; start < len(values); {
			if visibleLineLength(values[start:]) <= layout.MaxCharsPerLine {
				lines = append(lines, string(values[start:]))
				break
			}
			limit := min(len(values), start+layout.MaxCharsPerLine)
			best, space, wordBoundary := 0, 0, 0
			for at := start + 1; at <= limit; at++ {
				if !safeLineBoundary(values, at) {
					continue
				}
				end := at
				for end < len(values) && unicode.IsSpace(values[end]) {
					end++
				}
				if visibleLineLength(values[start:end]) <= layout.MaxCharsPerLine {
					best = end
					if preferred[paragraphOffset+end] {
						wordBoundary = end
					}
					if unicode.IsSpace(values[end-1]) {
						space = end
					}
				}
				if end > at {
					at = end - 1
				}
			}
			if wordBoundary > 0 {
				best = wordBoundary
			}
			if space > 0 {
				best = space
			}
			if best <= start {
				return "", false
			}
			lines = append(lines, string(values[start:best]))
			start = best
			if len(lines) > layout.MaxLines {
				return "", false
			}
		}
		if len(lines) > layout.MaxLines {
			return "", false
		}
		paragraphOffset += len(values) + 1
	}
	return strings.Join(lines, "\n"), len(lines) <= layout.MaxLines
}

func groupText(words []captionWord, layout CaptionLayout) (string, bool) {
	if layout.Mode == "word" && len(words) == 1 {
		return words[0].word.Text, true
	}
	values := make([]SourceWord, len(words))
	for i, word := range words {
		values[i] = word.word
	}
	text, boundaries := joinedCaptionParts(values)
	if layout.Mode == "word" {
		return text, true
	}
	preferred := map[int]bool{}
	// 已知词之间优先换行；超长中文词条仍只换行，不拆分其真实时间区间。
	for _, position := range boundaries {
		preferred[position] = true
	}
	return wrapCaptionText(text, layout, preferred)
}

func groupSpan(words []captionWord) WordSpan {
	first, last := words[0].span, words[len(words)-1].span
	partial := false
	for _, word := range words {
		partial = partial || word.span.Partial
	}
	return WordSpan{StartFrame: first.StartFrame, DurationFrames: last.StartFrame + last.DurationFrames - first.StartFrame, Partial: partial}
}

func durationWithin(span WordSpan, settings Settings, maximum int64) bool {
	left := new(big.Int).Mul(big.NewInt(span.DurationFrames), big.NewInt(1_000_000*int64(settings.FPSDen)))
	right := new(big.Int).Mul(big.NewInt(maximum), big.NewInt(int64(settings.FPSNum)))
	return left.Cmp(right) <= 0
}

func pauseBoundary(left, right SourceWord, source Clip, threshold int64) bool {
	if right.StartUs < left.EndUs {
		return true
	}
	gap := new(big.Int).Mul(big.NewInt(right.StartUs-left.EndUs), big.NewInt(int64(source.SpeedDen)))
	maximum := new(big.Int).Mul(big.NewInt(threshold), big.NewInt(int64(source.SpeedNum)))
	return gap.Cmp(maximum) >= 0
}

func sentenceBoundary(words []captionWord, next SourceWord) bool {
	if closingOnly(next.Text) {
		return false
	}
	text := ""
	for i := len(words) - 1; i >= 0 && text == ""; i-- {
		text = strings.TrimRight(strings.TrimSpace(words[i].word.Text), "\"'”’」』）)]")
	}
	lower := strings.ToLower(text)
	for _, abbreviation := range []string{"mr.", "mrs.", "ms.", "dr.", "prof.", "sr.", "jr.", "e.g.", "i.e."} {
		if lower == abbreviation {
			return false
		}
	}
	last, _ := utf8.DecodeLastRuneInString(text)
	if last == '.' && text == "." && len(words) > 1 {
		left, _ := utf8.DecodeLastRuneInString(words[len(words)-2].word.Text)
		right, _ := utf8.DecodeRuneInString(next.Text)
		if unicode.IsDigit(left) && unicode.IsDigit(right) {
			return false
		}
	}
	return strings.ContainsRune("。！？.!?；;؟", last)
}

func closingOnly(text string) bool {
	seen := false
	for _, value := range text {
		if unicode.IsSpace(value) {
			continue
		}
		if !closingPunctuation(value) {
			return false
		}
		seen = true
	}
	return seen
}

func speakerBoundary(left, right SourceWord) bool {
	return left.SpeakerID != right.SpeakerID && !closingOnly(right.Text)
}

func protectedCaption(clip Clip, transcript Transcript, trackOrder map[string]int) bool {
	if clip.CaptionSource == nil {
		return false
	}
	if clip.CaptionSource.TextOverride || clip.CaptionSource.StyleOverride {
		return true
	}
	return legacyCaptionLayout(clip, transcript, trackOrder)
}

func legacyCaptionLayout(clip Clip, transcript Transcript, trackOrder map[string]int) bool {
	// 兼容旧版未记布局标志的手动换轨；普通自动分轨仍可重新阅读分句。
	if clip.TrackID != transcript.CaptionTrackID {
		expected := -float64(trackOrder[clip.TrackID]-trackOrder[transcript.CaptionTrackID]) * float64(clip.TextStyle.FontSize) * 1.4
		return clip.Transform.Y != expected
	}
	return false
}

func buildCaptionGroups(transcript Transcript, source Clip, settings Settings, mapper *wordMapper, ordered []SourceWord, excluded map[string]bool, oldGroups map[string]*Clip, scoped []*Clip, parent string, trackOrder map[string]int, prior map[string]*Clip, priorSources map[string]Clip, protectionBase string, asset Asset) ([]captionGroup, error) {
	layout, err := effectiveCaptionLayout(transcript)
	if err != nil {
		return nil, err
	}
	protectionTranscript := transcript
	if protectionBase != "" {
		protectionTranscript.CaptionTrackID = protectionBase
	}
	if layout.Mode == "word" {
		multiProtected, hasProtected := false, false
		for _, old := range scoped {
			if protectedCaption(*old, protectionTranscript, trackOrder) {
				hasProtected = true
				if len(old.CaptionSource.WordIDs) > 1 {
					multiProtected = true
				}
			}
		}
		if !multiProtected {
			if hasProtected {
				accepted := make(map[string]bool, len(ordered))
				for _, word := range ordered {
					accepted[word.ID] = true
				}
				for _, old := range scoped {
					if protectedCaption(*old, protectionTranscript, trackOrder) && !accepted[old.CaptionSource.WordIDs[0]] {
						return nil, problem("caption_override_conflict", "词修订将移除手动字幕对应内容，请先解除该组绑定或保留这些词")
					}
				}
			}
			groups := make([]captionGroup, 0, len(ordered))
			for rank, word := range ordered {
				if excluded[word.ID] {
					continue
				}
				span, visible, err := mapper.mapWord(word)
				if err != nil {
					return nil, err
				}
				if !visible {
					continue
				}
				old, exact := oldGroups[bindingKey(transcript.ID, source.ID, word.ID)]
				if !exact && parent != "" {
					old, _ = oldGroups[bindingKey(transcript.ID, parent, word.ID)]
				}
				groups = append(groups, captionGroup{words: []captionWord{{word: word, rank: rank, span: span}}, span: span, text: word.Text, old: old, exact: exact})
			}
			return groups, nil
		}
	}
	visible := make([]captionWord, 0, len(ordered))
	visibleIndex := make(map[string]int, len(ordered))
	ranks := make(map[string]int, len(ordered))
	for rank, word := range ordered {
		ranks[word.ID] = rank
		if excluded[word.ID] {
			continue
		}
		span, shown, err := mapper.mapWord(word)
		if err != nil {
			return nil, err
		}
		if !shown {
			continue
		}
		visibleIndex[word.ID] = len(visible)
		visible = append(visible, captionWord{word: word, rank: rank, span: span})
	}
	protectedAt := map[int]captionGroup{}
	protectedWords := map[string]bool{}
	for _, old := range scoped {
		if !protectedCaption(*old, protectionTranscript, trackOrder) {
			continue
		}
		ids := old.CaptionSource.WordIDs
		members := []captionWord{}
		excludedCount := 0
		previousRank := -1
		for _, id := range ids {
			rank, exists := ranks[id]
			if !exists {
				return nil, problem("caption_override_conflict", "词修订将移除手动字幕对应内容，请先解除该组绑定或保留这些词")
			}
			if previousRank >= 0 && rank != previousRank+1 {
				return nil, problem("caption_override_conflict", "词修订插入或重排了手动字幕组，请先解除绑定后再重新分句")
			}
			previousRank = rank
			if excluded[id] {
				excludedCount++
			}
			if index, shown := visibleIndex[id]; shown {
				members = append(members, visible[index])
			}
		}
		if excludedCount == len(ids) || len(members) == 0 {
			continue
		}
		if excludedCount > 0 || len(members) != len(ids) {
			return nil, problem("caption_override_conflict", "修剪或分割只保留了手动字幕组的一部分，请调整切口或先解除该组绑定")
		}
		stablePartial := false
		if previous := prior[old.ID]; previous != nil && previous.CaptionSource != nil && previous.CaptionSource.Partial {
			if oldSource, found := priorSources[previous.CaptionSource.SourceClipID]; found && oldSource.AssetID == source.AssetID && oldSource.SourceInUs == source.SourceInUs && oldSource.DurationFrames == source.DurationFrames && int64(oldSource.SpeedNum)*int64(source.SpeedDen) == int64(source.SpeedNum)*int64(oldSource.SpeedDen) && (sameAudioRange(oldSource.AudioRange, source.AudioRange) || sameResolvedAudio(oldSource, source, asset, settings)) {
				span := groupSpan(members)
				stablePartial = previous.StartFrame-oldSource.StartFrame == span.StartFrame-source.StartFrame && previous.DurationFrames == span.DurationFrames
			}
		}
		for i, member := range members {
			if protectedWords[member.word.ID] {
				return nil, problem("caption_override_conflict", "多个手动字幕组引用了相同词，无法安全继承")
			}
			if (layout.Mode == "readable" || len(ids) > 1) && member.span.Partial && !stablePartial {
				return nil, problem("caption_override_conflict", "切口截断了手动字幕组中的词，请调整边界或先解除该组绑定")
			}
			if i > 0 && (speakerBoundary(members[i-1].word, member.word) || member.word.StartUs < members[i-1].word.EndUs) {
				return nil, problem("caption_override_conflict", "手动字幕组的说话人或词时间发生冲突，请先核对后解除或调整绑定")
			}
		}
		text := old.Text
		if !old.CaptionSource.TextOverride {
			var fits bool
			text, fits = groupText(members, layout)
			if !fits {
				return nil, problem("caption_override_conflict", "新行宽或行数无法容纳手动字幕组，请扩大范围或先解除该组绑定")
			}
		}
		for _, member := range members {
			protectedWords[member.word.ID] = true
		}
		exact := old.CaptionSource.SourceClipID == source.ID
		protectedAt[visibleIndex[ids[0]]] = captionGroup{words: members, span: groupSpan(members), text: text, old: old, exact: exact}
	}
	groups := []captionGroup{}
	for at := 0; at < len(visible); {
		if protected, found := protectedAt[at]; found {
			groups = append(groups, protected)
			at += len(protected.words)
			continue
		}
		members := []captionWord{visible[at]}
		at++
		text, fits := groupText(members, layout)
		if !fits || layout.Mode == "readable" && !durationWithin(groupSpan(members), settings, layout.MaxDurationUs) {
			return nil, problem("caption_layout", "单条真实词时间对应文字或时长超过分句上限，不能猜时间拆词；请提高行宽、行数或时长上限")
		}
		if layout.Mode == "readable" {
			for at < len(visible) {
				if _, protected := protectedAt[at]; protected {
					break
				}
				previous, next := members[len(members)-1], visible[at]
				if next.rank != previous.rank+1 || speakerBoundary(previous.word, next.word) || pauseBoundary(previous.word, next.word, source, layout.PauseThresholdUs) || sentenceBoundary(members, next.word) {
					break
				}
				trial := append(members, next)
				candidate, fits := groupText(trial, layout)
				if !fits || !durationWithin(groupSpan(trial), settings, layout.MaxDurationUs) {
					if closingOnly(next.word.Text) {
						if len(members) == 1 {
							return nil, problem("caption_layout", "单个词及其标点超过分句边界，不能拆词或让标点单独成组；请提高行宽或时长上限")
						}
						members = members[:len(members)-1]
						at--
						text, _ = groupText(members, layout)
					}
					break
				}
				members, text = trial, candidate
				at++
			}
		}
		ids := captionIDs(members)
		old, exact := oldGroups[captionGroupKey(transcript.ID, source.ID, ids)]
		if !exact && parent != "" {
			old, _ = oldGroups[captionGroupKey(transcript.ID, parent, ids)]
		}
		groups = append(groups, captionGroup{words: members, span: groupSpan(members), text: text, old: old, exact: exact})
	}
	return groups, nil
}
