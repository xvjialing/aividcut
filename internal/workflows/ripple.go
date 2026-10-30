package workflows

import (
	"aividcut/internal/editor"
	"errors"
	"fmt"
	"reflect"
)

type RippleInput struct {
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	FromFrame       int64  `json:"fromFrame"`
	ToFrame         int64  `json:"toFrame"`
}
type RippleSummary struct {
	FromFrame          int64 `json:"fromFrame"`
	ToFrame            int64 `json:"toFrame"`
	BeforeFrames       int64 `json:"beforeFrames"`
	AfterFrames        int64 `json:"afterFrames"`
	Deleted            int   `json:"deleted"`
	Split              int   `json:"split"`
	Trimmed            int   `json:"trimmed"`
	Moved              int   `json:"moved"`
	CaptionChanges     int   `json:"captionChanges"`
	AffectedTracks     int   `json:"affectedTracks"`
	TotalAffectedClips int   `json:"totalAffectedClips"`
}

func PreviewRippleDelete(project editor.Project, input RippleInput) (Plan, error) {
	if project.ID != input.ProjectID || project.Revision != input.ProjectRevision {
		return Plan{}, errors.New("工程版本已变化，请重新核对时间区间")
	}
	id := editor.NewID("ripple-preview")
	p := Plan{ID: id, WorkflowID: "ripple-delete-range", WorkflowVersion: "1.0.0", ProjectID: project.ID, Request: editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: id, Label: "全轨波纹删除", Commands: []editor.Command{{Type: "ripple_delete_range", FromFrame: input.FromFrame, ToFrame: input.ToFrame}}}, Effects: []Effect{}, Evidence: []Evidence{}, Warnings: []string{"将影响所有轨道（含未选中、隐藏和静音轨）；受影响锁轨或不能安全拆分的人工整句会整体拒绝"}, RangeSummary: &RippleSummary{FromFrame: input.FromFrame, ToFrame: input.ToFrame, BeforeFrames: editor.DurationFrames(project)}}
	return finalizePreview(project, p, 0)
}
func describeRipple(project, after editor.Project, plan *Plan) {
	s := plan.RangeSummary
	s.AfterFrames = editor.DurationFrames(after)
	known := map[string]editor.Clip{}
	for _, c := range after.Clips {
		known[c.ID] = c
	}
	tracks := map[string]bool{}
	sources := map[string]bool{}
	detailCount := 0
	for _, c := range project.Clips {
		next, exists := known[c.ID]
		if exists && reflect.DeepEqual(c, next) {
			continue
		}
		tracks[c.TrackID] = true
		detailCount++
		kind, label := "move", "前移"
		switch {
		case c.CaptionSource != nil:
			s.CaptionChanges++
			kind, label = "caption-follow", "绑定字幕随源重新映射"
		case !exists:
			s.Deleted++
			kind, label = "delete", "删除区间内片段"
		case c.StartFrame < s.FromFrame && c.StartFrame+c.DurationFrames > s.ToFrame:
			s.Split++
			kind, label = "split", "保留切口左右两段"
		case c.StartFrame < s.ToFrame && c.StartFrame+c.DurationFrames > s.FromFrame:
			s.Trimmed++
			kind, label = "trim", "保留区间外源段"
		default:
			s.Moved++
		}
		if len(plan.Effects) < 200 {
			plan.Effects = append(plan.Effects, Effect{Kind: kind, ClipID: c.ID, TrackID: c.TrackID, StartFrame: c.StartFrame, EndFrame: c.StartFrame + c.DurationFrames, Description: label + " · " + c.Name})
		}
		// 只有被裁切/分割的媒体需要再读源证明；纯移动/删除也允许离线占位几何编辑。
		if (kind == "split" || kind == "trim") && c.AssetID != "" && !sources[c.AssetID] {
			for _, a := range project.Assets {
				if a.ID == c.AssetID {
					sources[a.ID] = true
					plan.Evidence = append(plan.Evidence, Evidence{Kind: "ripple-retained-source", AssetID: a.ID, SourceFingerprint: a.Fingerprint, ClipID: c.ID, StartFrame: c.StartFrame, EndFrame: c.StartFrame + c.DurationFrames, Description: "裁切源窗的来源核对"})
					break
				}
			}
		}
	}
	s.AffectedTracks = len(tracks)
	s.TotalAffectedClips = len(plan.AffectedClipIDs)
	plan.Summary = fmt.Sprintf("删除工程[%d,%d)的%d帧，所有后段统一前移；工程%d→%d帧，影响%d条轨道", s.FromFrame, s.ToFrame, s.ToFrame-s.FromFrame, s.BeforeFrames, s.AfterFrames, s.AffectedTracks)
	if len(plan.AffectedClipIDs) > 2000 {
		plan.AffectedClipIDs = plan.AffectedClipIDs[:2000]
		plan.Warnings = append(plan.Warnings, "受影响标识仅列前2000项，完整命令仍处理全轨范围；请以总数核对")
	}
	if detailCount > len(plan.Effects) {
		plan.Warnings = append(plan.Warnings, "明细最多显示200项，其余变化保留在完整统计中")
	}
}
