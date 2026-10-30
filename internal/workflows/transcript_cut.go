package workflows

import (
	"aividcut/internal/editor"
	"errors"
	"fmt"
)

type TranscriptCutInput struct {
	RippleAll          bool     `json:"rippleAll,omitempty"`
	ProjectID          string   `json:"projectId"`
	ProjectRevision    int64    `json:"projectRevision"`
	TranscriptID       string   `json:"transcriptId"`
	TranscriptRevision int64    `json:"transcriptRevision"`
	ClipID             string   `json:"clipId"`
	WordIDs            []string `json:"wordIds"`
	CloseGap           bool     `json:"closeGap"`
}
type TranscriptWordSelection struct {
	ProjectID          string   `json:"projectId"`
	ProjectRevision    int64    `json:"projectRevision"`
	TranscriptID       string   `json:"transcriptId"`
	TranscriptRevision int64    `json:"transcriptRevision"`
	ClipID             string   `json:"clipId"`
	AssetID            string   `json:"assetId"`
	WordIDs            []string `json:"wordIds"`
	Text               string   `json:"text"`
	SourceStartUs      int64    `json:"sourceStartUs"`
	SourceEndUs        int64    `json:"sourceEndUs"`
	StartFrame         int64    `json:"startFrame"`
	EndFrame           int64    `json:"endFrame"`
	Warnings           []string `json:"warnings"`
}

func LocateTranscriptWords(project editor.Project, input TranscriptCutInput) (TranscriptWordSelection, error) {
	out := TranscriptWordSelection{ProjectID: project.ID, ProjectRevision: project.Revision, TranscriptID: input.TranscriptID, TranscriptRevision: input.TranscriptRevision, ClipID: input.ClipID, Warnings: []string{}, WordIDs: []string{}}
	if project.ID != input.ProjectID || project.Revision != input.ProjectRevision || len(input.WordIDs) < 1 || len(input.WordIDs) > 1000 {
		return out, errors.New("请在当前工程版本选择1–1000个连续词")
	}
	var clip editor.Clip
	found := false
	for _, c := range project.Clips {
		if c.ID == input.ClipID {
			clip = c
			found = true
			break
		}
	}
	if !found || (clip.Kind != "video" && clip.Kind != "audio") {
		return out, errors.New("请选择一个已上轨的音频/视频实例")
	}
	asset, err := assetByID(project, clip.AssetID)
	if err != nil {
		return out, err
	}
	out.AssetID = asset.ID
	var transcript editor.Transcript
	found = false
	for _, t := range project.Transcripts {
		if t.ID == input.TranscriptID {
			transcript = t
			found = true
			break
		}
	}
	if !found || transcript.Stale || transcript.Revision != input.TranscriptRevision || transcript.AssetID != asset.ID || transcript.SourceFingerprint != asset.Fingerprint || transcript.SourceDurationUs != asset.DurationUs {
		return out, errors.New("接受版本或源素材已变化，请重新选择")
	}
	words, _, _ := editor.SourceTextParts(transcript.AcceptedWords)
	selected := map[string]bool{}
	for _, id := range input.WordIDs {
		if selected[id] {
			return out, errors.New("词标识重复")
		}
		selected[id] = true
	}
	first, last := -1, -1
	for i, w := range words {
		if selected[w.ID] {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return out, errors.New("所选词不在接受版本")
	}
	if last-first+1 != len(selected) {
		return out, errors.New("请选择源时间顺序连续的词，不隐式删掉未选文字")
	}
	selectedWords := words[first : last+1]
	out.SourceStartUs = selectedWords[0].StartUs
	out.StartFrame = clip.StartFrame + clip.DurationFrames
	for _, word := range selectedWords {
		if !selected[word.ID] {
			return out, errors.New("选择中含缺失或不连续的词")
		}
		span, visible, err := editor.MapWordToClipWithAsset(word, clip, asset, project.Settings)
		if err != nil {
			return out, err
		}
		if !visible {
			return out, errors.New("所选词不在这个时间线实例内，请选择其它实例或词")
		}
		out.StartFrame = min(out.StartFrame, span.StartFrame)
		out.EndFrame = max(out.EndFrame, span.StartFrame+span.DurationFrames)
		out.SourceEndUs = max(out.SourceEndUs, word.EndUs)
		out.WordIDs = append(out.WordIDs, word.ID)
		if span.Partial {
			out.Warnings = append(out.Warnings, "部分所选词已被片段边缘裁切，只操作当前实例仍可见部分")
		}
	}
	_, out.Text, _ = editor.SourceTextParts(selectedWords)
	for _, word := range words {
		if selected[word.ID] {
			continue
		}
		span, visible, err := editor.MapWordToClipWithAsset(word, clip, asset, project.Settings)
		if err != nil {
			return out, err
		}
		if visible && span.StartFrame < out.EndFrame && span.StartFrame+span.DurationFrames > out.StartFrame {
			out.Warnings = append(out.Warnings, "剪切边缘与邻词的帧量化范围重叠，请试听边界；词时间不是音素精确证明")
			break
		}
	}
	if out.EndFrame <= out.StartFrame {
		return out, errors.New("选择没有有效工程帧范围")
	}
	out.Warnings = uniqueText(out.Warnings)
	return out, nil
}
func uniqueText(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
func PreviewTranscriptCut(project editor.Project, input TranscriptCutInput) (Plan, error) {
	selection, err := LocateTranscriptWords(project, input)
	if err != nil {
		return Plan{}, err
	}
	if input.RippleAll {
		if input.CloseGap {
			return Plan{}, errors.New("全轨波纹与仅当前组闭合不能同时指定")
		}
		plan, err := PreviewRippleDelete(project, RippleInput{ProjectID: project.ID, ProjectRevision: project.Revision, FromFrame: selection.StartFrame, ToFrame: selection.EndFrame})
		if err != nil {
			return Plan{}, err
		}
		plan.Warnings = append(plan.Warnings, selection.Warnings...)
		plan.Evidence = append(plan.Evidence, Evidence{Kind: "accepted-word-cut", ClipID: selection.ClipID, AssetID: selection.AssetID, TranscriptID: selection.TranscriptID, WordIDs: selection.WordIDs, SourceStartUs: selection.SourceStartUs, SourceEndUs: selection.SourceEndUs, StartFrame: selection.StartFrame, EndFrame: selection.EndFrame, Description: selection.Text})
		return plan, nil
	}
	var primary editor.Clip
	for _, clip := range project.Clips {
		if clip.ID == input.ClipID {
			primary = clip
			break
		}
	}
	members, err := selectedMediaGroup(project, primary)
	if err != nil {
		return Plan{}, err
	}
	for _, member := range members {
		if member.StartFrame != primary.StartFrame || member.DurationFrames != primary.DurationFrames || int64(member.SpeedNum)*int64(primary.SpeedDen) != int64(primary.SpeedNum)*int64(member.SpeedDen) {
			return Plan{}, errors.New("linked音画组不同步，请先整理后按文字删减")
		}
		if _, err := trackByID(project, member.TrackID, member.Kind); err != nil {
			return Plan{}, err
		}
	}
	id := editor.NewID("transcript-cut")
	plan := Plan{ID: id, WorkflowID: "transcript-cut", WorkflowVersion: "1.0.0", ProjectID: project.ID, Request: editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: id, Label: "按文字删减", Commands: []editor.Command{}}, Effects: []Effect{}, Evidence: []Evidence{}, Warnings: append([]string{}, selection.Warnings...), AffectedClipIDs: []string{}}
	start, end := selection.StartFrame-primary.StartFrame, selection.EndFrame-primary.StartFrame
	removed := end - start
	if end < primary.DurationFrames && start > 0 {
		right := ownedClip(primary)
		right.ID = editor.NewID("word-keep")
		right.StartFrame = primary.StartFrame + end
		if input.CloseGap {
			right.StartFrame -= removed
		}
		right.DurationFrames = primary.DurationFrames - end
		offset, err := editor.SourceDurationUs(end, project.Settings, right.SpeedNum, right.SpeedDen)
		if err != nil {
			return Plan{}, err
		}
		right.SourceInUs += offset
		plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "duplicate_clip", ClipID: primary.ID, Clip: &right, IncludeLinked: boolPointer(true)})
		plan.Effects = append(plan.Effects, Effect{Kind: "word-keep", ClipID: right.ID, TrackID: right.TrackID, StartFrame: right.StartFrame, EndFrame: right.StartFrame + right.DurationFrames, Description: "保留选择之后的源段，linked音画与字幕继承"})
	}
	if start == 0 && end == primary.DurationFrames {
		plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "delete_clip", ClipID: primary.ID, IncludeLinked: boolPointer(true)})
	} else {
		kept := ownedClip(primary)
		if start > 0 {
			kept.DurationFrames = start
		} else {
			offset, err := editor.SourceDurationUs(end, project.Settings, kept.SpeedNum, kept.SpeedDen)
			if err != nil {
				return Plan{}, err
			}
			kept.SourceInUs += offset
			kept.DurationFrames -= end
			if !input.CloseGap {
				kept.StartFrame += end
			}
		}
		plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "update_clip", Clip: &kept, IncludeLinked: boolPointer(true)})
	}
	for _, member := range members {
		asset, err := assetByID(project, member.AssetID)
		if err != nil {
			return Plan{}, err
		}
		plan.AffectedClipIDs = append(plan.AffectedClipIDs, member.ID)
		plan.Evidence = append(plan.Evidence, Evidence{Kind: "selected-linked-source", ClipID: member.ID, AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, StartFrame: member.StartFrame, EndFrame: member.StartFrame + member.DurationFrames, Description: "明确的当前实例与完整linked组；其它实例不变"})
	}
	plan.Evidence = append(plan.Evidence, Evidence{Kind: "accepted-word-cut", ClipID: primary.ID, AssetID: primary.AssetID, TranscriptID: selection.TranscriptID, SourceFingerprint: membersSourceFingerprint(project, primary.AssetID), WordIDs: selection.WordIDs, SourceStartUs: selection.SourceStartUs, SourceEndUs: selection.SourceEndUs, StartFrame: selection.StartFrame, EndFrame: selection.EndFrame, Description: selection.Text})
	plan.Effects = append(plan.Effects, Effect{Kind: "word-delete", ClipID: primary.ID, TrackID: primary.TrackID, StartFrame: selection.StartFrame, EndFrame: selection.EndFrame, Description: "删除所选词覆盖的工程帧，原始/接受转写保留"})
	mode := "保留空隙"
	if input.CloseGap {
		mode = "仅闭合这个完整音画组"
	}
	plan.Summary = fmt.Sprintf("删减%d个接受词覆盖的%d帧，%s；其它实例/轨道不移动", len(selection.WordIDs), removed, mode)
	plan.Warnings = append(plan.Warnings, "按工程帧覆盖选择，边缘受ASR时间与量化影响，请试听；不自动判断口癖或语义，也不波纹移动未选片段")
	selectedRemoved := int64(0)
	if input.CloseGap {
		selectedRemoved = removed
	}
	return finalizePreview(project, plan, selectedRemoved)
}
func membersSourceFingerprint(project editor.Project, id string) string {
	asset, _ := assetByID(project, id)
	return asset.Fingerprint
}
