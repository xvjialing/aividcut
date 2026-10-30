package editor

import (
	"crypto/sha256"
	"fmt"
	"reflect"
)

// rippleDeleteRange只接受明确的全轨时间区间；源窗构造和字幕继承复用原核心。
func rippleDeleteRange(project *Project, command Command) error {
	extra := command
	extra.Type = ""
	extra.FromFrame = 0
	extra.ToFrame = 0
	extra.inheritance = nil
	extra.rippleNamespace = ""
	if !reflect.ValueOf(extra).IsZero() || command.rippleNamespace == "" {
		return problem("ripple_range", "全轨波纹删除只接受起止帧，不能夹带选区、轨道或替换字段")
	}
	from, to := command.FromFrame, command.ToFrame
	if from < 0 || to <= from || to > DurationFrames(*project) {
		return problem("ripple_range", "请选择工程内有效的半开帧区间，终点必须大于起点")
	}
	// 每个原链接组在左右存续时分成两组，右侧未跨切口成员也要跟随右组。
	type sides struct{ left, right bool }
	groups := map[string]sides{}
	affected := false
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil {
			continue
		}
		end := clip.StartFrame + clip.DurationFrames
		if end > from {
			affected = true
			if err := editableTrack(project, clip.TrackID); err != nil {
				return err
			}
		}
		if clip.LinkGroup != "" {
			value := groups[clip.LinkGroup]
			value.left = value.left || clip.StartFrame < from
			value.right = value.right || end > to
			groups[clip.LinkGroup] = value
		}
	}
	if !affected {
		return problem("ripple_range", "这个区间没有可删除或前移的内容")
	}
	rightGroups := map[string]string{}
	for id, value := range groups {
		if value.left && value.right {
			rightGroups[id] = rippleDerivedID(command.rippleNamespace, id, "right-link")
		}
	}
	result := make([]Clip, 0, len(project.Clips))
	removed := to - from
	shift := func(original Clip) (Clip, error) {
		after := cloneClip(original)
		after.StartFrame -= removed
		if group := rightGroups[original.LinkGroup]; group != "" {
			after.LinkGroup = group
		}
		_, ready, err := prepareClipUpdate(project, original, after)
		return ready, err
	}
	for _, clip := range project.Clips {
		// 绑定字幕保留给reconcile决定真实源词与人工覆盖的存续，不能按外层几何直接切断。
		if clip.CaptionSource != nil {
			result = append(result, clip)
			continue
		}
		start, end := clip.StartFrame, clip.StartFrame+clip.DurationFrames
		switch {
		case end <= from:
			result = append(result, clip)
		case start >= to:
			after, err := shift(clip)
			if err != nil {
				return err
			}
			result = append(result, after)
		case start >= from && end <= to:
			// 完全删除；原始/接受转写仍属于素材，不删除源词。
		case start < from && end <= to:
			left, _, err := splitClipParts(project, clip, from)
			if err != nil {
				return err
			}
			result = append(result, left)
		case start >= from && end > to:
			_, right, err := splitClipParts(project, clip, to)
			if err != nil {
				return err
			}
			right.ID = clip.ID
			right, err = shift(right)
			if err != nil {
				return err
			}
			result = append(result, right)
		default:
			left, tail, err := splitClipParts(project, clip, from)
			if err != nil {
				return err
			}
			_, right, err := splitClipParts(project, tail, to)
			if err != nil {
				return err
			}
			right, err = shift(right)
			if err != nil {
				return err
			}
			right.ID = rippleDerivedID(command.rippleNamespace, clip.ID, "right-clip")
			result = append(result, left, right)
			command.inheritance.parents[right.ID] = clip.ID
		}
	}
	project.Clips = result
	return nil
}

func rippleDerivedID(namespace, parent, role string) string {
	return fmt.Sprintf("ripple-%x", sha256.Sum256([]byte(namespace+"\x00"+parent+"\x00"+role)))
}
