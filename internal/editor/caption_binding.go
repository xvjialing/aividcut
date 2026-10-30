package editor

import (
	"fmt"
	"math"
	"math/big"
	"sort"
)

type WordSpan struct {
	StartFrame     int64 `json:"startFrame"`
	DurationFrames int64 `json:"durationFrames"`
	Partial        bool  `json:"partial"`
}

// MapWordToClip 先在源有理数区间求交，再将工程起点向下、终点向上取整到帧。
// 每个可见词至少保留一帧，并裁在源片段的工程边界内；重叠词由派生层分轨。
func MapWordToClip(word SourceWord, clip Clip, settings Settings) (WordSpan, bool, error) {
	mapper, err := newWordMapper(clip, settings)
	if err != nil {
		return WordSpan{}, false, err
	}
	return mapper.mapWord(word)
}

// MapWordToClipWithAsset 使用实测声音覆盖；旧纯clip helper仍只解释clip自带范围。
func MapWordToClipWithAsset(word SourceWord, clip Clip, asset Asset, settings Settings) (WordSpan, bool, error) {
	mapper, err := newCaptionWordMapper(clip, asset, settings)
	if err != nil {
		return WordSpan{}, false, err
	}
	return mapper.mapWord(word)
}

func newCaptionWordMapper(clip Clip, asset Asset, settings Settings) (*wordMapper, error) {
	if clip.AudioRange == nil && asset.HasAudio && (asset.AudioSampleCount != nil || asset.AudioStartUs > 0) {
		var err error
		clip, err = materializeAudioWindow(clip, asset, settings)
		if err != nil {
			return nil, err
		}
	}
	return newWordMapper(clip, settings)
}

// 映射器仅在一次派生/校验内按源片段使用；不共享可变工作区，不进入工程快照。
type wordMapper struct {
	clip                   Clip
	frameSourceUs, in, out big.Rat
	start, end             big.Rat
	offsetFrames           big.Rat
	first, last, remainder big.Int
}

func newWordMapper(clip Clip, settings Settings) (*wordMapper, error) {
	if err := ValidateSettings(settings); err != nil {
		return nil, err
	}
	if clip.StartFrame < 0 || clip.DurationFrames <= 0 || clip.StartFrame > math.MaxInt64-clip.DurationFrames || clip.SourceInUs < 0 || clip.SpeedNum <= 0 || clip.SpeedDen <= 0 {
		return nil, problem("caption_mapping", "源词或片段时间、速度无效")
	}
	mapper := &wordMapper{clip: clip}
	mapper.frameSourceUs.SetFrac(new(big.Int).Mul(big.NewInt(1_000_000*int64(settings.FPSDen)), big.NewInt(int64(clip.SpeedNum))), new(big.Int).Mul(big.NewInt(int64(settings.FPSNum)), big.NewInt(int64(clip.SpeedDen))))
	mapper.in.SetInt64(clip.SourceInUs)
	mapper.out.Mul(new(big.Rat).SetInt64(clip.DurationFrames), &mapper.frameSourceUs).Add(&mapper.in, &mapper.out)
	if clip.AudioRange != nil {
		start, end, err := audioRangeSource(clip.AudioRange)
		if err != nil {
			return nil, err
		}
		offset, err := audioOffset(clip.AudioRange)
		if err != nil {
			return nil, err
		}
		mapper.in.Set(start)
		mapper.out.Set(end)
		mapper.offsetFrames.Quo(offset, frameTimeRat(1, settings))
	}
	return mapper, nil
}

func (mapper *wordMapper) mapWord(word SourceWord) (WordSpan, bool, error) {
	if word.StartUs < 0 || word.EndUs <= word.StartUs {
		return WordSpan{}, false, problem("caption_mapping", "源词或片段时间、速度无效")
	}
	start, end := &mapper.start, &mapper.end
	start.SetInt64(word.StartUs)
	end.SetInt64(word.EndUs)
	if start.Cmp(&mapper.out) >= 0 || end.Cmp(&mapper.in) <= 0 {
		return WordSpan{}, false, nil
	}
	partial := false
	if start.Cmp(&mapper.in) < 0 {
		start.Set(&mapper.in)
		partial = true
	}
	if end.Cmp(&mapper.out) > 0 {
		end.Set(&mapper.out)
		partial = true
	}
	start.Sub(start, &mapper.in).Quo(start, &mapper.frameSourceUs)
	end.Sub(end, &mapper.in).Quo(end, &mapper.frameSourceUs)
	start.Add(start, &mapper.offsetFrames)
	end.Add(end, &mapper.offsetFrames)
	first, last, remainder := &mapper.first, &mapper.last, &mapper.remainder
	first.QuoRem(start.Num(), start.Denom(), remainder)
	if remainder.Sign() < 0 {
		first.Sub(first, big.NewInt(1))
	}
	last.QuoRem(end.Num(), end.Denom(), remainder)
	if remainder.Sign() > 0 {
		last.Add(last, big.NewInt(1))
	}
	if !first.IsInt64() || !last.IsInt64() {
		return WordSpan{}, false, problem("caption_mapping", "字幕帧范围超出可表示范围")
	}
	left, right := max(int64(0), first.Int64()), min(mapper.clip.DurationFrames, last.Int64())
	if right <= left {
		return WordSpan{}, false, nil
	}
	return WordSpan{StartFrame: mapper.clip.StartFrame + left, DurationFrames: right - left, Partial: partial}, true, nil
}

func bindingKey(transcriptID, sourceID, wordID string) string {
	return transcriptID + "\x00" + sourceID + "\x00" + wordID
}

type boundCandidate struct {
	clip          Clip
	baseTrack     string
	exact         bool
	preserveTrack bool
	strictLayout  bool
}

func sourceParents(before, after []Clip, command Command) map[string]string {
	if command.inheritance != nil {
		return command.inheritance.parents
	}
	old := make(map[string]*Clip, len(before))
	for i := range before {
		old[before[i].ID] = &before[i]
	}
	parents := map[string]string{}
	eligible := map[string]bool{}
	if target, exists := old[command.ClipID]; exists && (command.Type == "split_clip" || command.Type == "duplicate_clip") {
		eligible[target.ID] = true
		if linked(command) && target.LinkGroup != "" {
			for _, clip := range before {
				if clip.LinkGroup == target.LinkGroup {
					eligible[clip.ID] = true
				}
			}
		}
	}
	for _, clip := range after {
		if _, exists := old[clip.ID]; exists || !validKind(clip.Kind, "video", "audio") {
			continue
		}
		if command.Type == "duplicate_clip" {
			if source, exists := old[command.ClipID]; exists && command.Clip != nil && clip.ID == command.Clip.ID && source.AssetID == clip.AssetID && source.Kind == clip.Kind {
				parents[clip.ID] = source.ID
				continue
			}
		}
		for _, source := range before {
			if (command.Type == "split_clip" || command.Type == "duplicate_clip") && !eligible[source.ID] {
				continue
			}
			if source.Kind != clip.Kind || source.AssetID != clip.AssetID {
				continue
			}
			if command.Type == "duplicate_clip" && command.Clip != nil {
				origin := old[command.ClipID]
				position, ok := addDelta(source.StartFrame, command.Clip.StartFrame, origin.StartFrame)
				if ok && source.TrackID == clip.TrackID && position == clip.StartFrame {
					parents[clip.ID] = source.ID
					break
				}
				continue
			}
			if command.Type == "split_clip" && clip.StartFrame == command.AtFrame && source.StartFrame < command.AtFrame && source.StartFrame+source.DurationFrames > command.AtFrame {
				if source.TrackID != clip.TrackID {
					continue
				}
				parents[clip.ID] = source.ID
				break
			}
			if source.SourceInUs == clip.SourceInUs && source.DurationFrames == clip.DurationFrames && source.SpeedNum == clip.SpeedNum && source.SpeedDen == clip.SpeedDen {
				parents[clip.ID] = source.ID
				break
			}
		}
	}
	return parents
}

func reconcileCaptions(project *Project, before []Clip, command Command, previousCaptionTrack string) error {
	if len(project.Transcripts) == 0 {
		return nil
	}
	if command.inheritance != nil && len(command.inheritance.templates) > 0 {
		before = append(append([]Clip{}, before...), command.inheritance.templates...)
	}
	parents := sourceParents(before, project.Clips, command)
	sources := map[string]Clip{}
	for _, clip := range project.Clips {
		if validKind(clip.Kind, "video", "audio") {
			sources[clip.ID] = clip
		}
	}
	oldSources := map[string]Clip{}
	// 只在本次派生内建立只读引用索引；最终快照和撤销历史仍使用深拷贝。
	prior := make(map[string]*Clip, len(before))
	oldBindings := make(map[string]*Clip, len(before))
	scopes := map[string]map[string]*Clip{}
	indexBinding := func(clip *Clip) {
		if clip.CaptionSource == nil || len(clip.CaptionSource.WordIDs) == 0 {
			return
		}
		binding := clip.CaptionSource
		oldBindings[captionGroupKey(binding.TranscriptID, binding.SourceClipID, binding.WordIDs)] = clip
		key := captionSourceKey(binding.TranscriptID, binding.SourceClipID)
		if scopes[key] == nil {
			scopes[key] = map[string]*Clip{}
		}
		scopes[key][clip.ID] = clip
	}
	for i := range before {
		clip := &before[i]
		prior[clip.ID] = clip
		if validKind(clip.Kind, "video", "audio") {
			oldSources[clip.ID] = *clip
		}
		indexBinding(clip)
	}
	// 当前显式编辑过的文本/样式覆盖优先于命令前的旧值。
	for i := range project.Clips {
		clip := &project.Clips[i]
		indexBinding(clip)
	}
	for i := range project.Transcripts {
		transcript := &project.Transcripts[i]
		asset, exists := transcriptAsset(project, transcript.AssetID)
		if !exists {
			return problem("transcript", "转写源素材不存在")
		}
		transcript.Stale = transcript.SourceFingerprint != asset.Fingerprint
		accepted := map[string]bool{}
		for _, word := range transcript.AcceptedWords {
			accepted[word.ID] = true
		}
		exclusions := make([]CaptionExclusion, 0, len(transcript.CaptionExclusions))
		for _, exclusion := range transcript.CaptionExclusions {
			if source, exists := sources[exclusion.SourceClipID]; exists && source.AssetID == transcript.AssetID && accepted[exclusion.WordID] {
				exclusions = append(exclusions, exclusion)
			}
		}
		inheritedExclusions := transcript.CaptionExclusions
		if command.inheritance != nil && len(command.inheritance.exclusions[transcript.ID]) > 0 {
			inheritedExclusions = append(append([]CaptionExclusion{}, inheritedExclusions...), command.inheritance.exclusions[transcript.ID]...)
		}
		for newID, parentID := range parents {
			for _, exclusion := range inheritedExclusions {
				if exclusion.SourceClipID == parentID && accepted[exclusion.WordID] {
					exclusions = append(exclusions, CaptionExclusion{SourceClipID: newID, WordID: exclusion.WordID})
				}
			}
		}
		sort.Slice(exclusions, func(i, j int) bool {
			if exclusions[i].SourceClipID == exclusions[j].SourceClipID {
				return exclusions[i].WordID < exclusions[j].WordID
			}
			return exclusions[i].SourceClipID < exclusions[j].SourceClipID
		})
		transcript.CaptionExclusions = exclusions
	}
	static := []Clip{}
	for _, clip := range project.Clips {
		if clip.CaptionSource == nil {
			static = append(static, clip)
		}
	}
	candidates := make([]boundCandidate, 0, len(project.Clips))
	// 相同 linked 源映射只选择一个字幕拥有者；视频优先，独立复用仍分别生成。
	type owner struct {
		transcript int
		clip       Clip
	}
	owners := []owner{}
	ordered := map[string][]SourceWord{}
	trackOrder := map[string]int{}
	for i, track := range project.Tracks {
		trackOrder[track.ID] = i
	}
	groups := map[string]int{}
	for ti, transcript := range project.Transcripts {
		if transcript.CaptionTrackID == "" {
			continue
		}
		for _, clip := range project.Clips {
			if clip.AssetID != transcript.AssetID || !validKind(clip.Kind, "video", "audio") {
				continue
			}
			asset, _ := transcriptAsset(project, clip.AssetID)
			if !SourceRangeFits(clip, asset, project.Settings) {
				return problem("source_out_of_range", "绑定字幕源片段超出有效范围")
			}
			if clip.LinkGroup != "" {
				// 分离出的音频文件指纹可以不同；明确的 linked 关系与相同映射才是去重依据。
				key := fmt.Sprintf("%s|%s|%d|%d|%d|%s", clip.LinkGroup, transcript.Language, clip.StartFrame, clip.DurationFrames, clip.SourceInUs, new(big.Rat).SetFrac64(int64(clip.SpeedNum), int64(clip.SpeedDen)).RatString())
				if index, found := groups[key]; found {
					if clip.Kind == "video" && owners[index].clip.Kind == "audio" {
						owners[index] = owner{ti, clip}
					}
					continue
				}
				groups[key] = len(owners)
			}
			owners = append(owners, owner{ti, clip})
		}
	}
	for _, own := range owners {
		transcript, source := project.Transcripts[own.transcript], own.clip
		if transcript.Stale {
			old, exists := oldSources[source.ID]
			if !exists || !sameSourceTiming(old, source) {
				return problem("transcript_stale", "源转写已陈旧，不能静默派生移动、修剪、复制或变速后的字幕")
			}
			for _, clip := range project.Clips {
				if clip.CaptionSource != nil && clip.CaptionSource.TranscriptID == transcript.ID && clip.CaptionSource.SourceClipID == source.ID {
					value := cloneClip(clip)
					value.CaptionSource.Stale = true
					candidates = append(candidates, boundCandidate{clip: value, baseTrack: transcript.CaptionTrackID, exact: true})
				}
			}
			continue
		}
		excluded := map[string]bool{}
		for _, exclusion := range transcript.CaptionExclusions {
			if exclusion.SourceClipID == source.ID {
				excluded[exclusion.WordID] = true
			}
		}
		sourceAsset, _ := transcriptAsset(project, source.AssetID)
		mapper, err := newCaptionWordMapper(source, sourceAsset, project.Settings)
		if err != nil {
			return err
		}
		if ordered[transcript.ID] == nil {
			ordered[transcript.ID] = orderedCaptionWords(transcript.AcceptedWords)
		}
		scope := scopes[captionSourceKey(transcript.ID, source.ID)]
		if len(scope) == 0 && parents[source.ID] != "" {
			scope = scopes[captionSourceKey(transcript.ID, parents[source.ID])]
		}
		oldScoped := make([]*Clip, 0, len(scope))
		for _, clip := range scope {
			oldScoped = append(oldScoped, clip)
		}
		protectionTranscript := transcript
		if command.Type == "generate_bound_captions" && command.TranscriptID == transcript.ID && previousCaptionTrack != "" {
			protectionTranscript.CaptionTrackID = previousCaptionTrack
		}
		groups, err := buildCaptionGroups(transcript, source, project.Settings, mapper, ordered[transcript.ID], excluded, oldBindings, oldScoped, parents[source.ID], trackOrder, prior, oldSources, protectionTranscript.CaptionTrackID, sourceAsset)
		if err != nil {
			return err
		}
		layout, _ := effectiveCaptionLayout(transcript)
		for _, group := range groups {
			old, exact := group.old, group.exact
			inherited := old != nil
			clip := DefaultClip("", transcript.CaptionTrackID, "caption")
			clip.Name = "源字幕"
			if inherited {
				clip = cloneClip(*old)
				if !exact {
					clip.ID = NewID("caption")
				}
			}
			if clip.ID == "" {
				clip.ID = NewID("caption")
			}
			if command.rippleNamespace != "" && (!inherited || !exact) {
				clip.ID = rippleDerivedID(command.rippleNamespace, captionGroupKey(transcript.ID, source.ID, captionIDs(group.words)), "caption")
			}
			clip.StartFrame, clip.DurationFrames = group.span.StartFrame, group.span.DurationFrames
			clip.CaptionSource = &CaptionBinding{TranscriptID: transcript.ID, SourceClipID: source.ID, WordIDs: captionIDs(group.words), Partial: group.span.Partial}
			if inherited {
				clip.CaptionSource.TextOverride, clip.CaptionSource.StyleOverride = old.CaptionSource.TextOverride, old.CaptionSource.StyleOverride
				if command.Type == "generate_bound_captions" && command.TranscriptID == transcript.ID && legacyCaptionLayout(*old, protectionTranscript, trackOrder) {
					clip.CaptionSource.StyleOverride = true
				}
			}
			if !clip.CaptionSource.TextOverride {
				clip.Text = group.text
			}
			preserve := inherited && protectedCaption(*old, protectionTranscript, trackOrder) && (old.CaptionSource.StyleOverride || old.TrackID != protectionTranscript.CaptionTrackID)
			candidates = append(candidates, boundCandidate{clip: clip, baseTrack: transcript.CaptionTrackID, exact: exact, preserveTrack: preserve, strictLayout: layout.Mode == "readable" && preserve})
			if len(candidates) > 100_000 {
				return problem("caption_limit", "派生字幕超过 100000 条，请分段处理源转写")
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].clip.StartFrame == candidates[j].clip.StartFrame {
			return candidates[i].clip.DurationFrames < candidates[j].clip.DurationFrames
		}
		return candidates[i].clip.StartFrame < candidates[j].clip.StartFrame
	})
	occupied := map[string][]Clip{}
	for _, clip := range static {
		if clip.Kind == "caption" {
			occupied[clip.TrackID] = append(occupied[clip.TrackID], clip)
		}
	}
	for id := range occupied {
		sort.Slice(occupied[id], func(i, j int) bool { return occupied[id][i].StartFrame < occupied[id][j].StartFrame })
	}
	ends := map[string]int64{}
	free := func(trackID string, clip Clip) bool {
		if ends[trackID] > clip.StartFrame {
			return false
		}
		clips := occupied[trackID]
		index := sort.Search(len(clips), func(i int) bool { return clips[i].StartFrame+clips[i].DurationFrames > clip.StartFrame })
		return index == len(clips) || clips[index].StartFrame >= clip.StartFrame+clip.DurationFrames
	}
	derived := make([]Clip, 0, len(candidates))
	for _, candidate := range candidates {
		clip := candidate.clip
		// 显式更换派生目标时，旧的exact展示轨不能压过新目标；普通重算仍保持原展示轨。
		retarget := command.Type == "generate_bound_captions" && clip.CaptionSource.TranscriptID == command.TranscriptID && previousCaptionTrack != "" && previousCaptionTrack != candidate.baseTrack
		if !candidate.exact {
			if err := editableTrack(project, candidate.baseTrack); err != nil {
				return err
			}
		}
		chosen := ""
		if (candidate.exact || candidate.preserveTrack) && !retarget && trackIndex(project, clip.TrackID) >= 0 && free(clip.TrackID, clip) {
			chosen = clip.TrackID
		}
		if chosen == "" && candidate.strictLayout && !retarget {
			return problem("caption_override_conflict", "手动字幕组的展示轨发生重叠，请先调整布局或解除该组绑定后再移动/复制")
		}
		if chosen == "" && free(candidate.baseTrack, clip) {
			chosen = candidate.baseTrack
		}
		if chosen == "" {
			for _, track := range project.Tracks {
				if track.Kind == "caption" && !track.Locked && !track.Hidden && free(track.ID, clip) {
					chosen = track.ID
					break
				}
			}
		}
		if chosen == "" {
			if len(project.Tracks) >= 1000 {
				return problem("caption_limit", "重叠字幕需要过多轨道，请先整理转写词时间")
			}
			track := Track{ID: NewID("caption-track"), Name: "源字幕分轨", Kind: "caption"}
			if command.rippleNamespace != "" {
				track.ID = rippleDerivedID(command.rippleNamespace, candidate.baseTrack, fmt.Sprintf("caption-track-%d", len(project.Tracks)))
			}
			if err := applyCommand(project, Command{Type: "add_track", Track: &track}); err != nil {
				return err
			}
			chosen = track.ID
		}
		clip.TrackID = chosen
		if (!candidate.exact || retarget) && !clip.CaptionSource.StyleOverride && (chosen != candidate.baseTrack || retarget) {
			clip.Transform.Y = -float64(trackIndex(project, chosen)-trackIndex(project, candidate.baseTrack)) * float64(clip.TextStyle.FontSize) * 1.4
		}
		ends[chosen] = clip.StartFrame + clip.DurationFrames
		if old, found := prior[clip.ID]; !found || !sameCaption(*old, clip) {
			if err := editableTrack(project, chosen); err != nil {
				return err
			}
		}
		derived = append(derived, clip)
	}
	finalIDs := make(map[string]*Clip, len(derived))
	for i := range derived {
		finalIDs[derived[i].ID] = &derived[i]
	}
	for _, old := range before {
		if old.CaptionSource == nil {
			continue
		}
		current, exists := finalIDs[old.ID]
		if !exists || !sameCaption(old, *current) {
			if err := editableTrack(project, old.TrackID); err != nil {
				return err
			}
		}
	}
	project.Clips = append(static, derived...)
	return nil
}
