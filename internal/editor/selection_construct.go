package editor

import "math"

// 单个与成组分割共用取样配方，调用方负责链接关系及锁轨。
func splitClipParts(project *Project, original Clip, frame int64) (Clip, Clip, error) {
	if frame <= original.StartFrame || frame >= original.StartFrame+original.DurationFrames {
		return Clip{}, Clip{}, problem("invalid_split", "分割点必须位于片段内部")
	}
	left, right := cloneClip(original), cloneClip(original)
	leftFrames := frame - original.StartFrame
	right.ID, right.StartFrame, right.DurationFrames = NewID("clip"), frame, original.DurationFrames-leftFrames
	if validKind(original.Kind, "video", "audio") {
		offset, err := SourceDurationUs(leftFrames, project.Settings, original.SpeedNum, original.SpeedDen)
		if err != nil {
			return Clip{}, Clip{}, err
		}
		if right.SourceInUs > math.MaxInt64-offset {
			return Clip{}, Clip{}, problem("validation", "分割后的源入点超出可表示范围")
		}
		right.SourceInUs += offset
	}
	left.DurationFrames = leftFrames
	if mediaSourceKind(original) {
		asset, _ := transcriptAsset(project, original.AssetID)
		if err := splitSourcePhase(original, &left, &right, asset, project.Settings, leftFrames); err != nil {
			return Clip{}, Clip{}, err
		}
	}
	return left, right, nil
}

func constructClipSelection(project *Project, command Command) error {
	ids, err := selectionIDs(project, command)
	if err != nil {
		return err
	}
	if command.AtFrame < 0 || command.FrameDelta != 0 || (command.Type == "split_clips" && command.TrackDelta != 0) {
		return problem("selection", "分割/复制需要非负目标帧，不能夹带无关位移")
	}
	for _, c := range project.Clips {
		if !ids[c.ID] {
			continue
		}
		if err := editableTrack(project, c.TrackID); err != nil {
			return err
		}
		if c.CaptionSource != nil && !ids[c.CaptionSource.SourceClipID] {
			return problem("caption_binding", "绑定字幕需随其源片段分割或复制，独立操作请先解除绑定")
		}
	}
	if command.Type == "split_clips" {
		return splitSelection(project, command, ids)
	}
	return duplicateSelection(project, command, ids)
}

func splitSelection(project *Project, command Command, ids map[string]bool) error {
	rightGroups := map[string]string{}
	count := 0
	for _, c := range project.Clips {
		if ids[c.ID] && c.CaptionSource == nil && c.StartFrame < command.AtFrame && c.StartFrame+c.DurationFrames > command.AtFrame {
			count++
			if linked(command) && c.LinkGroup != "" && rightGroups[c.LinkGroup] == "" {
				rightGroups[c.LinkGroup] = NewID("link")
			}
		}
	}
	if count == 0 {
		return problem("invalid_split", "请把播放头放在至少一个所选片段内部，再进行分割")
	}
	rights := make([]Clip, 0, count)
	for i := range project.Clips {
		c := project.Clips[i]
		if !ids[c.ID] || c.CaptionSource != nil {
			continue
		}
		if c.StartFrame >= command.AtFrame {
			if group := rightGroups[c.LinkGroup]; group != "" {
				project.Clips[i].LinkGroup = group
			}
			continue
		}
		if c.StartFrame+c.DurationFrames <= command.AtFrame {
			continue
		}
		left, right, err := splitClipParts(project, c, command.AtFrame)
		if err != nil {
			return err
		}
		if c.LinkGroup != "" {
			if linked(command) {
				right.LinkGroup = rightGroups[c.LinkGroup]
			} else {
				left.LinkGroup, right.LinkGroup = "", ""
			}
		}
		project.Clips[i] = left
		rights = append(rights, right)
		command.inheritance.parents[right.ID] = c.ID
	}
	project.Clips = append(project.Clips, rights...)
	return nil
}

func duplicateSelection(project *Project, command Command, ids map[string]bool) error {
	start := int64(math.MaxInt64)
	tracks := map[string]int{}
	for i, t := range project.Tracks {
		tracks[t.ID] = i
	}
	for _, c := range project.Clips {
		if ids[c.ID] && c.CaptionSource == nil && c.StartFrame < start {
			start = c.StartFrame
		}
	}
	if start == math.MaxInt64 {
		return problem("selection", "选区没有可以复制的源片段")
	}
	groups := map[string]string{}
	copies := make([]Clip, 0, len(ids))
	for _, c := range project.Clips {
		if !ids[c.ID] || c.CaptionSource != nil {
			continue
		}
		relative := c.StartFrame - start
		if command.AtFrame > math.MaxInt64-relative || command.AtFrame+relative > math.MaxInt64-c.DurationFrames {
			return problem("validation", "副本位置超出时间范围")
		}
		track := tracks[c.TrackID]
		if command.TrackDelta < -track || command.TrackDelta >= len(project.Tracks)-track {
			return problem("track", "副本目标轨道超出范围")
		}
		copy := cloneClip(c)
		copy.ID, copy.StartFrame, copy.TrackID = NewID("clip"), command.AtFrame+relative, project.Tracks[track+command.TrackDelta].ID
		_, copy, err := prepareDuplicateClip(project, c, copy)
		if err != nil {
			return err
		}
		copy.LinkGroup = ""
		if c.LinkGroup != "" && linked(command) {
			if groups[c.LinkGroup] == "" {
				groups[c.LinkGroup] = NewID("link")
			}
			copy.LinkGroup = groups[c.LinkGroup]
		}
		command.inheritance.parents[copy.ID] = c.ID
		copies = append(copies, copy)
	}
	project.Clips = append(project.Clips, copies...)
	return nil
}
