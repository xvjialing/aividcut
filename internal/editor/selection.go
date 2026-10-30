package editor

import "math"

func selectionIDs(project *Project, command Command) (map[string]bool, error) {
	if len(command.ClipIDs) == 0 || len(command.ClipIDs) > 100000 {
		return nil, problem("selection", "请提供1到100000个片段标识")
	}
	if command.Clip != nil || command.ClipID != "" || command.TrackID != "" {
		return nil, problem("selection", "批量编辑不能夹带单片段替换参数")
	}
	if command.Track != nil || command.Asset != nil || command.Settings != nil || command.Name != "" || (command.AtFrame != 0 && command.Type != "split_clips" && command.Type != "duplicate_clips") {
		return nil, problem("selection", "批量编辑不能夹带无关字段")
	}
	known := make(map[string]Clip, len(project.Clips))
	groups := map[string]bool{}
	ids := map[string]bool{}
	for _, c := range project.Clips {
		known[c.ID] = c
	}
	for _, id := range command.ClipIDs {
		c, ok := known[id]
		if !ok {
			return nil, problem("not_found", "选中的片段不存在")
		}
		if ids[id] {
			return nil, problem("selection", "选中片段标识不能重复")
		}
		ids[id] = true
		if linked(command) && c.LinkGroup != "" {
			groups[c.LinkGroup] = true
		}
	}
	for _, c := range project.Clips {
		if groups[c.LinkGroup] {
			ids[c.ID] = true
		}
	}
	return ids, nil
}
func editClipSelection(project *Project, command Command) error {
	ids, err := selectionIDs(project, command)
	if err != nil {
		return err
	}
	for _, c := range project.Clips {
		if ids[c.ID] {
			if err := editableTrack(project, c.TrackID); err != nil {
				return err
			}
		}
	}
	if command.Type == "delete_clips" {
		if command.FrameDelta != 0 || command.TrackDelta != 0 {
			return problem("selection", "批量删除不能携带位移")
		}
		for _, c := range project.Clips {
			if ids[c.ID] {
				suppressCaption(project, c)
			}
		}
		kept := project.Clips[:0]
		for _, c := range project.Clips {
			if !ids[c.ID] {
				kept = append(kept, c)
			}
		}
		project.Clips = kept
		return nil
	}
	indices := map[string]int{}
	for i, t := range project.Tracks {
		indices[t.ID] = i
	}
	updates := map[string]Clip{}
	for _, c := range project.Clips {
		if !ids[c.ID] {
			continue
		}
		if c.CaptionSource != nil {
			if !ids[c.CaptionSource.SourceClipID] {
				return problem("caption_binding", "绑定字幕应随其源片段移动，或先明确解除绑定")
			}
			continue
		}
		if command.FrameDelta > 0 && c.StartFrame > math.MaxInt64-command.FrameDelta || command.FrameDelta < 0 && command.FrameDelta < -c.StartFrame {
			return problem("validation", "整组位移超出非负时间范围")
		}
		position := c.StartFrame + command.FrameDelta
		if position > math.MaxInt64-c.DurationFrames {
			return problem("validation", "整组末端超出时间范围")
		}
		start := indices[c.TrackID]
		if command.TrackDelta < -start || command.TrackDelta >= len(project.Tracks)-start {
			return problem("track", "整组目标轨道超出范围")
		}
		after := cloneClip(c)
		after.StartFrame = position
		after.TrackID = project.Tracks[start+command.TrackDelta].ID
		_, ready, err := prepareClipUpdate(project, c, after)
		if err != nil {
			return err
		}
		updates[c.ID] = ready
	}
	for i := range project.Clips {
		if after, ok := updates[project.Clips[i].ID]; ok {
			project.Clips[i] = after
		}
	}
	return nil
}
