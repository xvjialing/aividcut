package captions

import (
	"aividcut/internal/editor"
	"fmt"
	"math"
	"strings"
)

type frameCue struct {
	cue      Cue
	start    int64
	duration int64
}

// ImportCommands 只产生静态字幕编辑命令，不修改工程，也不创建转写源绑定。
// 指定 trackID 时只使用该轨；为空时复用空字幕轨并为重叠字幕自动增加轨道。
func ImportCommands(project editor.Project, trackID string, cues []Cue) ([]editor.Command, error) {
	if err := editor.ValidateProject(project); err != nil {
		return nil, fmt.Errorf("导入字幕前工程校验失败：%w", err)
	}
	values, err := normalizedCues(cues)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, fail("empty", "没有可导入的字幕")
	}
	frames := make([]frameCue, len(values))
	maxLines := 1
	for i, cue := range values {
		start, duration, err := CueToFrames(cue, project.Settings)
		if err != nil {
			return nil, &Error{Code: "frame_range", Cue: i + 1, Message: err.Error()}
		}
		frames[i] = frameCue{cue: cue, start: start, duration: duration}
		maxLines = max(maxLines, strings.Count(cue.Text, "\n")+1)
	}
	working := editor.CloneProject(project)
	commands := make([]editor.Command, 0, len(frames)+1)
	fontSize := max(16, int(math.Round(float64(project.Settings.Height)*48/1080)))
	if trackID != "" {
		var target *editor.Track
		for i := range working.Tracks {
			if working.Tracks[i].ID == trackID {
				target = &working.Tracks[i]
			}
		}
		if target == nil || target.Kind != "caption" {
			return nil, fail("caption_track", "指定轨道不存在或不是字幕轨")
		}
		if target.Locked {
			return nil, fail("track_locked", "指定字幕轨已锁定")
		}
		for i, value := range frames {
			clip := captionClip(value, trackID, fontSize, 0, i)
			working.Clips = append(working.Clips, clip)
			commands = append(commands, editor.Command{Type: "add_clip", Clip: &clip})
		}
	} else {
		type lane struct {
			id  string
			end int64
		}
		used := make(map[string]bool)
		for _, clip := range working.Clips {
			used[clip.TrackID] = true
		}
		lanes := make([]lane, 0)
		captionTrackCount := 0
		for _, track := range working.Tracks {
			if track.Kind != "caption" {
				continue
			}
			captionTrackCount++
			if !track.Locked && !track.Hidden && !used[track.ID] {
				lanes = append(lanes, lane{id: track.ID})
			}
		}
		for i, value := range frames {
			chosen := -1
			for index, candidate := range lanes {
				if candidate.end <= value.start {
					chosen = index
					break
				}
			}
			if chosen < 0 {
				captionTrackCount++
				track := editor.Track{ID: editor.NewID("caption-track"), Name: fmt.Sprintf("字幕 %d", captionTrackCount), Kind: "caption"}
				working.Tracks = append(working.Tracks, track)
				commands = append(commands, editor.Command{Type: "add_track", Track: &track})
				lanes = append(lanes, lane{id: track.ID})
				chosen = len(lanes) - 1
			}
			// 同时字幕逐层上移，保留每条文本；大量同时字幕仍需用户检查布局。
			offset := -float64(chosen) * float64(fontSize) * float64(maxLines+1) * 1.4
			clip := captionClip(value, lanes[chosen].id, fontSize, offset, i)
			working.Clips = append(working.Clips, clip)
			commands = append(commands, editor.Command{Type: "add_clip", Clip: &clip})
			lanes[chosen].end = value.start + value.duration
		}
	}
	if err := editor.ValidateProject(working); err != nil {
		return nil, fmt.Errorf("字幕无法放入目标轨道，重叠字幕可使用自动分轨：%w", err)
	}
	return commands, nil
}

func captionClip(value frameCue, trackID string, fontSize int, y float64, index int) editor.Clip {
	clip := editor.DefaultClip(editor.NewID("caption"), trackID, "caption")
	clip.Name = fmt.Sprintf("字幕 %d", index+1)
	clip.StartFrame, clip.DurationFrames, clip.Text = value.start, value.duration, value.cue.Text
	clip.TextStyle.FontSize = fontSize
	clip.Transform.Y = y
	return clip
}

// ExportProjectSRT 按轨道数据导出；隐藏不等于删除，是否导出该源由调用方选择 trackID。
// trackID 为空表示所有字幕轨，包含重叠字幕，不将普通标题文字混入字幕。
func ExportProjectSRT(project editor.Project, trackID string) (string, error) {
	if err := editor.ValidateProject(project); err != nil {
		return "", fmt.Errorf("导出字幕前工程校验失败：%w", err)
	}
	tracks := make(map[string]bool)
	for _, track := range project.Tracks {
		if track.Kind == "caption" && (trackID == "" || track.ID == trackID) {
			tracks[track.ID] = true
		}
	}
	if trackID != "" && len(tracks) == 0 {
		return "", fail("caption_track", "指定轨道不存在或不是字幕轨")
	}
	cues := make([]Cue, 0)
	for _, clip := range project.Clips {
		if clip.Kind != "caption" || !tracks[clip.TrackID] {
			continue
		}
		cue, err := FramesToCue(clip.StartFrame, clip.DurationFrames, clip.Text, project.Settings)
		if err != nil {
			return "", fmt.Errorf("字幕片段 %s 无法导出：%w", clip.Name, err)
		}
		cues = append(cues, cue)
	}
	if len(cues) == 0 {
		return "", fail("empty", "所选范围没有字幕片段")
	}
	return FormatSRT(cues)
}
