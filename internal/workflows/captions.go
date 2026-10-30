package workflows

import (
	"errors"
	"fmt"
	"regexp"

	"aividcut/internal/editor"
)

var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$`)

func previewCaptions(project editor.Project, selection []string, parameters map[string]any, plan *Plan) error {
	color, background := stringValue(parameters, "color"), stringValue(parameters, "background")
	if !colorPattern.MatchString(color) || background != "transparent" && !colorPattern.MatchString(background) {
		return errors.New("字幕颜色需要#RRGGBB/#RRGGBBAA，背景可为transparent")
	}
	selected, sources := map[string]bool{}, map[string]bool{}
	for _, id := range selection {
		for _, clip := range project.Clips {
			if clip.ID != id {
				continue
			}
			if clip.Kind == "caption" {
				selected[id] = true
				continue
			}
			if clip.Kind != "video" && clip.Kind != "audio" {
				return errors.New("字幕整理只处理字幕或所选源的绑定字幕，不改写标题")
			}
			group, err := selectedMediaGroup(project, clip)
			if err != nil {
				return err
			}
			for _, source := range group {
				sources[source.ID] = true
			}
		}
	}
	for _, clip := range project.Clips {
		if clip.Kind == "caption" && (stringValue(parameters, "scope") == "all_captions" || clip.CaptionSource != nil && sources[clip.CaptionSource.SourceClipID]) {
			selected[clip.ID] = true
		}
	}
	if len(selected) == 0 {
		return errors.New("请选中已有字幕或带绑定字幕的源，或明确选择全部字幕")
	}
	count := 0
	stale := false
	for _, before := range project.Clips {
		if !selected[before.ID] {
			continue
		}
		after := ownedClip(before)
		after.TextStyle.FontSize, after.TextStyle.Color, after.TextStyle.Background, after.TextStyle.Align = int(integerValue(parameters, "fontSize")), color, background, stringValue(parameters, "align")
		after.Transform.X, after.Transform.Y = numberValue(parameters, "offsetX"), numberValue(parameters, "offsetY")
		if after.TextStyle == before.TextStyle && after.Transform == before.Transform {
			continue
		}
		plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "update_clip", Clip: &after, IncludeLinked: boolPointer(false)})
		plan.Effects = append(plan.Effects, Effect{Kind: "caption-style", ClipID: before.ID, TrackID: before.TrackID, StartFrame: before.StartFrame, EndFrame: before.StartFrame + before.DurationFrames, Description: "修改明确字号、颜色、对齐和像素偏移，文字及词来源保留"})
		plan.Evidence = append(plan.Evidence, Evidence{Kind: "existing-caption", ClipID: before.ID, StartFrame: before.StartFrame, EndFrame: before.StartFrame + before.DurationFrames, Description: "已有字幕帧区间；本次只修改样式与布局"})
		if before.CaptionSource != nil && before.CaptionSource.Stale {
			stale = true
		}
		count++
	}
	if stale {
		plan.Warnings = append(plan.Warnings, "部分字幕的源转写已陈旧，本次仅整理已有文字样式，没有复用它生成新识别")
	}
	plan.Summary = fmt.Sprintf("整理%d条已有字幕，保留人工文字与源绑定", count)
	return nil
}
