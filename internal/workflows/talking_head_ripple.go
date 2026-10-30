package workflows

import (
	"aividcut/internal/editor"
	"fmt"
	"reflect"
	"sort"
)

// SpeechRippleSummary统计完整事务；标识与明细的展示上限不改变实际影响数。
type SpeechRippleSummary struct {
	CutCount             int   `json:"cutCount"`
	RemovedFrames        int64 `json:"removedFrames"`
	BeforeFrames         int64 `json:"beforeFrames"`
	AfterFrames          int64 `json:"afterFrames"`
	AffectedTracks       int   `json:"affectedTracks"`
	TotalAffectedClips   int   `json:"totalAffectedClips"`
	ChangedOriginalClips int   `json:"changedOriginalClips"`
	NewClips             int   `json:"newClips"`
}

func speechGapEvidence(group speechGroup, cut gapCut) Evidence {
	return Evidence{Kind: "accepted-word-gap", ClipID: group.primary.ID, AssetID: group.asset.ID, TranscriptID: group.transcript.ID, SourceFingerprint: group.transcript.SourceFingerprint, WordIDs: []string{cut.leftID, cut.rightID}, SourceStartUs: cut.sourceStart, SourceEndUs: cut.sourceEnd, StartFrame: group.primary.StartFrame + cut.start, EndFrame: group.primary.StartFrame + cut.end, Description: "已接受词之间的中心区间，按帧向内量化；不是静音或语义识别证明"}
}

func previewTalkingHeadRipple(project editor.Project, groups []speechGroup, plan *Plan) int64 {
	summary := &SpeechRippleSummary{BeforeFrames: editor.DurationFrames(project)}
	var cuts []editor.Command
	for _, group := range groups {
		for _, cut := range group.cuts {
			from, to := group.primary.StartFrame+cut.start, group.primary.StartFrame+cut.end
			cuts = append(cuts, editor.Command{Type: "ripple_delete_range", FromFrame: from, ToFrame: to})
			plan.Evidence = append(plan.Evidence, speechGapEvidence(group, cut))
			plan.Effects = append(plan.Effects, Effect{Kind: "speech-gap-all-tracks", ClipID: group.primary.ID, TrackID: group.primary.TrackID, StartFrame: from, EndFrame: to, Description: "原工程词间候选切口 · 删除所有轨道该区间并闭合后段"})
			summary.RemovedFrames += to - from
		}
	}
	// 原工程坐标先删后方；前方切口不会受到此前删除的坐标变化影响。
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].FromFrame > cuts[j].FromFrame })
	plan.Request.Commands = cuts
	summary.CutCount = len(cuts)
	plan.SpeechRippleSummary = summary
	plan.Warnings = append(plan.Warnings, "全轨闭合会删除候选区间内的配乐、标题、补充镜头和其它实例，并前移所有后段，包括未选中、隐藏和静音轨；受影响锁轨或不能安全继承的人工字幕将整批拒绝。", "词间没有接受文字不等于静音，可能包含未识别内容；请核对所有切口及其它轨内容后应用。")
	plan.Request.Label = "口播停顿粗剪 · 全轨闭合"
	return summary.RemovedFrames
}

func describeSpeechRipple(project, after editor.Project, plan *Plan) error {
	summary := plan.SpeechRippleSummary
	summary.AfterFrames = editor.DurationFrames(after)
	summary.TotalAffectedClips = len(plan.AffectedClipIDs)
	afterByID := map[string]editor.Clip{}
	beforeIDs := map[string]bool{}
	tracks, sources := map[string]bool{}, map[string]bool{}
	for _, clip := range after.Clips {
		afterByID[clip.ID] = clip
	}
	for _, proof := range plan.Evidence {
		if proof.AssetID != "" {
			sources[proof.AssetID] = true
		}
	}
	details := len(plan.Effects)
	for _, clip := range project.Clips {
		beforeIDs[clip.ID] = true
		next, exists := afterByID[clip.ID]
		if exists && reflect.DeepEqual(clip, next) {
			continue
		}
		tracks[clip.TrackID] = true
		summary.ChangedOriginalClips++
		details++
		if len(plan.Effects) < 200 {
			label := "全轨裁剪或前移"
			if !exists {
				label = "原片段或绑定字幕标识不再保留，请核对存续段"
			} else if clip.CaptionSource != nil {
				label = "绑定字幕随源重新映射，保留可继承的人工覆盖"
			}
			plan.Effects = append(plan.Effects, Effect{Kind: "speech-all-tracks-change", ClipID: clip.ID, TrackID: clip.TrackID, StartFrame: clip.StartFrame, EndFrame: clip.StartFrame + clip.DurationFrames, Description: label + " · " + clip.Name})
		}
		// 全删或纯移动不必读取离线占位；裁切的未选媒体同样需要来源核对。
		if exists && clip.AssetID != "" && !sources[clip.AssetID] && (clip.DurationFrames != next.DurationFrames || clip.SourceInUs != next.SourceInUs || !reflect.DeepEqual(clip.SourcePhase, next.SourcePhase) || !reflect.DeepEqual(clip.AudioRange, next.AudioRange)) {
			asset, err := assetByID(project, clip.AssetID)
			if err != nil {
				return fmt.Errorf("全轨裁切的其它媒体需要可核对来源：%w", err)
			}
			sources[asset.ID] = true
			plan.Evidence = append(plan.Evidence, Evidence{Kind: "speech-ripple-retained-source", AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, ClipID: clip.ID, StartFrame: clip.StartFrame, EndFrame: clip.StartFrame + clip.DurationFrames, Description: "全轨裁切后存续媒体的来源核对"})
		}
	}
	for _, clip := range after.Clips {
		if !beforeIDs[clip.ID] {
			tracks[clip.TrackID] = true
			summary.NewClips++
		}
	}
	summary.AffectedTracks = len(tracks)
	plan.Summary = fmt.Sprintf("全轨闭合%d处词间候选，共删除%d帧；工程%d→%d帧，影响%d条轨道；变更%d个原片段，新增%d个存续片段", summary.CutCount, summary.RemovedFrames, summary.BeforeFrames, summary.AfterFrames, summary.AffectedTracks, summary.ChangedOriginalClips, summary.NewClips)
	if len(plan.AffectedClipIDs) > 2000 {
		plan.AffectedClipIDs = plan.AffectedClipIDs[:2000]
		plan.Warnings = append(plan.Warnings, "受影响标识仅列前2000项，完整命令仍处理全轨范围；请以总数核对")
	}
	if details > len(plan.Effects) {
		plan.Warnings = append(plan.Warnings, "影响明细最多显示200项，候选切口已全部列出，其余变化保留在完整统计中")
	}
	return nil
}
