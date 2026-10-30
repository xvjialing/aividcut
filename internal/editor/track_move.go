package editor

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func validateNewTrackName(name string) error {
	if !utf8.ValidString(name) || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 120 {
		return problem("validation", "轨道名称需为1–120个字符")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return problem("validation", "轨道名称不能包含控制字符")
		}
	}
	return nil
}

// moveTrack 交换相邻的同类轨道，不改变片段、源相位或其它类型轨道的位置。
func moveTrack(project *Project, command Command) error {
	if command.TrackDelta != -1 && command.TrackDelta != 1 {
		return problem("validation", "轨道每次只能上移或下移一级")
	}
	index := trackIndex(project, command.TrackID)
	if index < 0 {
		return problem("not_found", "目标轨道不存在")
	}
	if err := editableTrack(project, command.TrackID); err != nil {
		return err
	}
	for next := index + command.TrackDelta; next >= 0 && next < len(project.Tracks); next += command.TrackDelta {
		if project.Tracks[next].Kind != project.Tracks[index].Kind {
			continue
		}
		if err := editableTrack(project, project.Tracks[next].ID); err != nil {
			return err
		}
		project.Tracks[index], project.Tracks[next] = project.Tracks[next], project.Tracks[index]
		return nil
	}
	return problem("track_boundary", "已经是该类型的最上或最下轨道")
}
