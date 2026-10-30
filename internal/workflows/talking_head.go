package workflows

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"unicode/utf8"

	"aividcut/internal/editor"
)

type gapCut struct {
	start, end             int64
	sourceStart, sourceEnd int64
	leftID, rightID        string
}
type keepSpan struct{ start, end int64 }
type speechGroup struct {
	primary    editor.Clip
	members    []editor.Clip
	asset      editor.Asset
	transcript editor.Transcript
	cuts       []gapCut
}

func previewTalkingHead(project editor.Project, selection []string, parameters map[string]any, plan *Plan) (int64, error) {
	if len(selection) == 0 {
		return 0, errors.New("口播粗剪需要明确选中源片段，会一并保护完整linked音画组")
	}
	if scope := stringValue(parameters, "rippleScope"); scope != "selection" && scope != "all_tracks" {
		return 0, errors.New("请选择选中完整源组或明确的全轨闭合范围")
	}
	if integerValue(parameters, "maxGapMs") < integerValue(parameters, "gapThresholdMs") {
		return 0, errors.New("最大词间间隔不能小于候选阈值")
	}
	clips := map[string]editor.Clip{}
	for _, clip := range project.Clips {
		clips[clip.ID] = clip
	}
	groups := []speechGroup{}
	seenGroups := map[string]bool{}
	expanded := map[string]bool{}
	for _, id := range selection {
		clip := clips[id]
		if clip.Kind != "video" && clip.Kind != "audio" {
			continue
		}
		key := clip.ID
		if clip.LinkGroup != "" {
			key = "linked:" + clip.LinkGroup
		}
		if seenGroups[key] {
			continue
		}
		seenGroups[key] = true
		members, err := selectedMediaGroup(project, clip)
		if err != nil {
			return 0, err
		}
		if len(groups) >= 32 {
			return 0, errors.New("首轮口播每次最多处理32个完整源组，请分段预览")
		}
		primary := clip
		var asset editor.Asset
		var accepted editor.Transcript
		hasAccepted := false
		for _, member := range members {
			if member.StartFrame != clip.StartFrame || member.DurationFrames != clip.DurationFrames || int64(member.SpeedNum)*int64(clip.SpeedDen) != int64(clip.SpeedNum)*int64(member.SpeedDen) {
				return 0, errors.New("linked组需具有相同工程区间与速度；不同步的复杂组请先整理")
			}
			if _, err := trackByID(project, member.TrackID, member.Kind); err != nil {
				return 0, err
			}
			media, err := assetByID(project, member.AssetID)
			if err != nil {
				return 0, err
			}
			expanded[member.ID] = true
			if tr, err := currentTranscript(project, media, ""); err == nil && len(tr.AcceptedWords) >= 2 {
				if !hasAccepted || member.Kind == "video" && primary.Kind != "video" {
					primary, asset, accepted, hasAccepted = member, media, tr, true
				}
			}
		}
		if !hasAccepted {
			asset, _ = assetByID(project, primary.AssetID)
			plan.Warnings = append(plan.Warnings, "部分所选源没有可用接受词：不从该段推导切口，仍按所选闭合范围处理")
		}
		groups = append(groups, speechGroup{primary: primary, members: members, asset: asset, transcript: accepted})
	}
	if len(groups) == 0 {
		return 0, errors.New("请选中音频/视频源，静态字幕不能作为口播删减证据")
	}
	for _, id := range selection {
		clip := clips[id]
		if expanded[id] {
			continue
		}
		if clip.Kind == "caption" && clip.CaptionSource != nil && expanded[clip.CaptionSource.SourceClipID] {
			continue
		}
		return 0, errors.New("口播选区包含未关联的标题/字幕/图片，首轮不改动这些内容")
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].primary.StartFrame < groups[j].primary.StartFrame })
	for i := 1; i < len(groups); i++ {
		previous := groups[i-1].primary
		if previous.StartFrame+previous.DurationFrames > groups[i].primary.StartFrame {
			return 0, errors.New("首轮口播仅支持时间上不重叠的所选完整源组；请先选择主口播，避免误删并行内容")
		}
	}
	cutCount := 0
	for i := range groups {
		if groups[i].transcript.ID == "" {
			continue
		}
		cuts, skipped, err := acceptedGapCuts(project.Settings, groups[i].primary, groups[i].transcript, parameters)
		if err != nil {
			return 0, err
		}
		groups[i].cuts = cuts
		cutCount += len(cuts)
		if skipped > 0 {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("保留%d处过长、说话人切换或与未接受原始词重叠的间隔，没有猜测其内容", skipped))
		}
		if cutCount > 128 {
			return 0, errors.New("本次候选停顿超过128处，请缩小选区或提高阈值后分段预览")
		}
	}
	if cutCount == 0 {
		return 0, errors.New("所选范围没有满足词间阈值、保护边缘及证据条件的可压缩区间")
	}
	if stringValue(parameters, "rippleScope") == "all_tracks" {
		return previewTalkingHeadRipple(project, groups, plan), nil
	}
	removedBefore := int64(0)
	for _, group := range groups {
		keeps := []keepSpan{}
		cursor := int64(0)
		for _, cut := range group.cuts {
			keeps = append(keeps, keepSpan{cursor, cut.start})
			cursor = cut.end
		}
		keeps = append(keeps, keepSpan{cursor, group.primary.DurationFrames})
		withinRemoved := int64(0)
		// 必须先duplicate所有后续保留段，再修剪原ID，才能继承跨gap的人工字幕覆盖。
		for i, keep := range keeps {
			if i == 0 {
				continue
			}
			withinRemoved += group.cuts[i-1].end - group.cuts[i-1].start
			copy := ownedClip(group.primary)
			copy.ID = editor.NewID("speech-keep")
			copy.StartFrame = group.primary.StartFrame + keep.start - removedBefore - withinRemoved
			copy.DurationFrames = keep.end - keep.start
			offset, err := editor.SourceDurationUs(keep.start, project.Settings, copy.SpeedNum, copy.SpeedDen)
			if err != nil {
				return 0, err
			}
			copy.SourceInUs += offset
			plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "duplicate_clip", ClipID: group.primary.ID, Clip: &copy, IncludeLinked: boolPointer(true)})
			plan.AffectedClipIDs = append(plan.AffectedClipIDs, copy.ID)
			plan.Effects = append(plan.Effects, Effect{Kind: "speech-keep", ClipID: copy.ID, TrackID: copy.TrackID, StartFrame: copy.StartFrame, EndFrame: copy.StartFrame + copy.DurationFrames, Description: "从明确父源复制保留段，同linked音画与字幕覆盖继承"})
		}
		if len(group.cuts) > 0 || removedBefore > 0 {
			first := ownedClip(group.primary)
			first.StartFrame -= removedBefore
			first.DurationFrames = keeps[0].end
			plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "update_clip", Clip: &first, IncludeLinked: boolPointer(true)})
			plan.Effects = append(plan.Effects, Effect{Kind: "speech-ripple", ClipID: first.ID, TrackID: first.TrackID, StartFrame: first.StartFrame, EndFrame: first.StartFrame + first.DurationFrames, Description: "保留原源ID的首段，仅闭合所选完整组；未选轨不移动"})
			for _, member := range group.members {
				plan.AffectedClipIDs = append(plan.AffectedClipIDs, member.ID)
				asset, _ := assetByID(project, member.AssetID)
				used, _ := editor.SourceDurationUs(member.DurationFrames, project.Settings, member.SpeedNum, member.SpeedDen)
				plan.Evidence = append(plan.Evidence, Evidence{Kind: "selected-linked-source", ClipID: member.ID, AssetID: member.AssetID, SourceFingerprint: asset.Fingerprint, SourceStartUs: member.SourceInUs, SourceEndUs: member.SourceInUs + used, StartFrame: member.StartFrame, EndFrame: member.StartFrame + member.DurationFrames, Description: "用户选中源及其完整linked组，原素材内容保留"})
			}
		}
		for _, cut := range group.cuts {
			plan.Evidence = append(plan.Evidence, speechGapEvidence(group, cut))
			removedBefore += cut.end - cut.start
		}
	}
	plan.Warnings = append(plan.Warnings, "词间没有接受文字不等于静音，可能含未识别内容；请试听预览后应用。仅处理所选组，未选轨和长尾保持原位。")
	plan.Summary = fmt.Sprintf("压缩%d处有接受词边界证据的词间停顿，选区移除%d帧；linked音画同步，原始识别保留", cutCount, removedBefore)
	return removedBefore, nil
}

func acceptedGapCuts(settings editor.Settings, clip editor.Clip, transcript editor.Transcript, parameters map[string]any) ([]gapCut, int, error) {
	words := append([]editor.SourceWord{}, transcript.AcceptedWords...)
	sort.SliceStable(words, func(i, j int) bool {
		if words[i].StartUs == words[j].StartUs {
			return words[i].EndUs < words[j].EndUs
		}
		return words[i].StartUs < words[j].StartUs
	})
	frameUs := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(1_000_000*int64(settings.FPSDen)), big.NewInt(int64(clip.SpeedNum))), new(big.Int).Mul(big.NewInt(int64(settings.FPSNum)), big.NewInt(int64(clip.SpeedDen))))
	out := new(big.Rat).Mul(new(big.Rat).SetInt64(clip.DurationFrames), frameUs)
	out.Add(out, new(big.Rat).SetInt64(clip.SourceInUs))
	visible := words[:0]
	for _, word := range words {
		if word.EndUs > clip.SourceInUs && new(big.Rat).SetInt64(word.StartUs).Cmp(out) < 0 {
			visible = append(visible, word)
		}
	}
	if len(visible) < 2 {
		return nil, 0, nil
	}
	acceptedIDs := map[string]bool{}
	for _, word := range transcript.AcceptedWords {
		acceptedIDs[word.ID] = true
	}
	missingRaw := []editor.SourceWord{}
	for _, raw := range transcript.RawWords {
		if !acceptedIDs[raw.ID] {
			missingRaw = append(missingRaw, raw)
		}
	}
	cuts := []gapCut{}
	skipped := 0
	left := visible[0]
	for _, right := range visible[1:] {
		if right.StartUs <= left.EndUs {
			if right.EndUs > left.EndUs {
				left = right
			}
			continue
		}
		gap := right.StartUs - left.EndUs
		if gap < integerValue(parameters, "gapThresholdMs")*1000 {
			left = right
			continue
		}
		if gap > integerValue(parameters, "maxGapMs")*1000 || boolValue(parameters, "preserveSpeakerChanges") && left.SpeakerID != "" && right.SpeakerID != "" && left.SpeakerID != right.SpeakerID {
			skipped++
			left = right
			continue
		}
		keep := integerValue(parameters, "keepGapMs") * 1000
		text := strings.TrimRight(strings.TrimSpace(left.Text), "\"'”’」』）)]")
		last, _ := utf8.DecodeLastRuneInString(text)
		if strings.ContainsRune("。！？.!?；;؟", last) {
			keep = max(keep, integerValue(parameters, "sentencePauseMs")*1000)
		}
		protect := integerValue(parameters, "edgeProtectionMs") * 1000
		before, after := max(protect, keep/2), max(protect, keep-keep/2)
		if before+after >= gap {
			left = right
			continue
		}
		startUs, endUs := left.EndUs+before, right.StartUs-after
		unaccepted := false
		for _, word := range missingRaw {
			if word.StartUs < endUs && word.EndUs > startUs {
				unaccepted = true
				break
			}
		}
		if unaccepted {
			skipped++
			left = right
			continue
		}
		start, err := sourceFrame(startUs, clip, settings, true)
		if err != nil {
			return nil, skipped, err
		}
		end, err := sourceFrame(endUs, clip, settings, false)
		if err != nil {
			return nil, skipped, err
		}
		if start > 0 && end < clip.DurationFrames && end > start {
			cuts = append(cuts, gapCut{start: start, end: end, sourceStart: startUs, sourceEnd: endUs, leftID: left.ID, rightID: right.ID})
		}
		left = right
	}
	return cuts, skipped, nil
}

func sourceFrame(sourceUs int64, clip editor.Clip, settings editor.Settings, ceil bool) (int64, error) {
	if sourceUs < clip.SourceInUs {
		return 0, errors.New("词间证据超出当前源窗口")
	}
	numerator := new(big.Int).Mul(big.NewInt(sourceUs-clip.SourceInUs), big.NewInt(int64(settings.FPSNum)))
	numerator.Mul(numerator, big.NewInt(int64(clip.SpeedDen)))
	denominator := new(big.Int).Mul(big.NewInt(1_000_000*int64(settings.FPSDen)), big.NewInt(int64(clip.SpeedNum)))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if ceil && remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, errors.New("词间帧换算超出范围")
	}
	return quotient.Int64(), nil
}
