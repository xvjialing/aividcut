package workflows

import (
	"errors"
	"fmt"

	"aividcut/internal/editor"
)

func previewBroll(project editor.Project, parameters map[string]any, plan *Plan) error {
	asset, err := assetByID(project, stringValue(parameters, "assetId"))
	if err != nil {
		return err
	}
	if asset.Kind != "image" && asset.Kind != "video" {
		return errors.New("补充镜头需要已导入图片或视频")
	}
	if asset.Kind == "image" && integerValue(parameters, "sourceInUs") != 0 {
		return errors.New("静态图片不能设置源入点")
	}
	if boolValue(parameters, "muted") {
		for _, transcript := range project.Transcripts {
			if transcript.AssetID == asset.ID && transcript.CaptionTrackID != "" {
				return errors.New("该素材启用了持续源字幕；请使用独立补充素材，避免静音B-roll意外派生口播字幕")
			}
		}
	}
	trackID := stringValue(parameters, "trackId")
	if trackID == "" {
		track := editor.Track{ID: editor.NewID("broll-track"), Name: "补充镜头", Kind: "video"}
		before := ""
		for _, existing := range project.Tracks {
			if existing.Kind == "video" {
				before = existing.ID
				break
			}
		}
		plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "add_track", Track: &track, BeforeTrackID: before})
		trackID = track.ID
	} else {
		target, err := trackByID(project, trackID, "video")
		if err != nil {
			return err
		}
		if target.Hidden {
			return errors.New("目标视频轨已隐藏，请先显式显示它或使用自动上层轨")
		}
	}
	clip := editor.DefaultClip(editor.NewID("broll"), trackID, asset.Kind)
	clip.AssetID, clip.Name, clip.StartFrame, clip.DurationFrames, clip.SourceInUs = asset.ID, asset.Name, integerValue(parameters, "startFrame"), integerValue(parameters, "durationFrames"), integerValue(parameters, "sourceInUs")
	clip.Muted, clip.Transform.Scale, clip.Transform.X, clip.Transform.Y = boolValue(parameters, "muted"), numberValue(parameters, "scale"), numberValue(parameters, "offsetX"), numberValue(parameters, "offsetY")
	if asset.Kind == "video" && !editor.SourceRangeFits(clip, asset, project.Settings) {
		return errors.New("补充视频的真实源长度不足，请缩短区间或调整入点")
	}
	if asset.Kind == "video" {
		clip, err = editor.CaptureSourcePhase(clip, asset, project.Settings)
		if err != nil {
			return err
		}
	}
	plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "add_clip", Clip: &clip})
	if stringValue(parameters, "trackId") != "" {
		order := map[string]int{}
		for i, track := range project.Tracks {
			order[track.ID] = i
		}
		for _, existing := range project.Clips {
			if (existing.Kind == "video" || existing.Kind == "image") && order[existing.TrackID] < order[trackID] && existing.StartFrame < clip.StartFrame+clip.DurationFrames && existing.StartFrame+existing.DurationFrames > clip.StartFrame {
				plan.Warnings = append(plan.Warnings, "显式选择的视频轨位于其它重叠画面下方，部分区间可能被上层遮挡；原轨没有移动")
				break
			}
		}
	}
	plan.AffectedClipIDs = append(plan.AffectedClipIDs, clip.ID)
	plan.Effects = append(plan.Effects, Effect{Kind: "broll-insert", ClipID: clip.ID, TrackID: trackID, StartFrame: clip.StartFrame, EndFrame: clip.StartFrame + clip.DurationFrames, Description: "新增独立补充画面；原视频和音频片段保持不动"})
	endUs := int64(0)
	if asset.Kind == "video" {
		used, _ := editor.SourceDurationUs(clip.DurationFrames, project.Settings, 1, 1)
		endUs = clip.SourceInUs + used
	}
	plan.Evidence = append(plan.Evidence, Evidence{Kind: "imported-visual", ClipID: clip.ID, AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, SourceStartUs: clip.SourceInUs, SourceEndUs: endUs, StartFrame: clip.StartFrame, EndFrame: clip.StartFrame + clip.DurationFrames, Description: "当前已导入素材及明确源入点/工程帧区间，没有生成新素材"})
	if !clip.Muted && asset.Kind == "video" && asset.HasAudio {
		plan.Warnings = append(plan.Warnings, "已明确启用补充镜头原声，会与原音轨混合")
	}
	plan.Summary = fmt.Sprintf("在第%d到%d帧加入已导入补充画面，原音画保留", clip.StartFrame, clip.StartFrame+clip.DurationFrames)
	return nil
}

func previewVoiceover(project editor.Project, selection []string, parameters map[string]any, plan *Plan) error {
	asset, err := assetByID(project, stringValue(parameters, "assetId"))
	if err != nil {
		return err
	}
	if asset.Kind != "audio" || asset.DurationUs <= 0 {
		return errors.New("配音编排需要已保存、导入且有真实时长的音频")
	}
	trackID := stringValue(parameters, "trackId")
	if trackID == "" {
		track := editor.Track{ID: editor.NewID("voiceover-track"), Name: "配音编排", Kind: "audio"}
		plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "add_track", Track: &track})
		trackID = track.ID
	} else {
		if _, err := trackByID(project, trackID, "audio"); err != nil {
			return err
		}
	}
	clip := editor.DefaultClip(editor.NewID("voiceover"), trackID, "audio")
	clip.AssetID, clip.Name, clip.StartFrame, clip.Volume = asset.ID, asset.Name, integerValue(parameters, "startFrame"), numberValue(parameters, "volume")
	clip, err = editor.AudioClipToSourceEnd(clip, asset, project.Settings)
	if err != nil {
		return err
	}
	frames := clip.DurationFrames
	plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "add_clip", Clip: &clip})
	if boolValue(parameters, "generateCaptions") {
		transcript, err := currentTranscript(project, asset, stringValue(parameters, "transcriptId"))
		if err != nil {
			return err
		}
		if len(transcript.AcceptedWords) == 0 {
			return errors.New("配音没有已接受的实际词时间，不按文案长度猜字幕")
		}
		captionTrack := stringValue(parameters, "captionTrackId")
		if transcript.CaptionTrackID != "" {
			if captionTrack != "" && captionTrack != transcript.CaptionTrackID {
				return errors.New("该配音已有源字幕轨，换轨会影响其它复用；请保持原轨或单独预览换轨")
			}
		} else {
			selected := map[string]bool{}
			for _, id := range selection {
				selected[id] = true
			}
			for _, existing := range project.Clips {
				if existing.AssetID == asset.ID && !selected[existing.ID] {
					return errors.New("启用持续字幕还会影响该素材的其它复用，请一并选中这些源或先单独生成字幕")
				}
			}
			if captionTrack == "" {
				track := editor.Track{ID: editor.NewID("voice-caption-track"), Name: "配音字幕", Kind: "caption"}
				plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "add_track", Track: &track})
				captionTrack = track.ID
			} else {
				if _, err := trackByID(project, captionTrack, "caption"); err != nil {
					return err
				}
			}
			plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: captionTrack})
		}
		plan.Evidence = append(plan.Evidence, Evidence{Kind: "accepted-voice-words", AssetID: asset.ID, TranscriptID: transcript.ID, SourceFingerprint: transcript.SourceFingerprint, SourceEndUs: asset.DurationUs, StartFrame: clip.StartFrame, EndFrame: clip.StartFrame + frames, Description: "字幕只使用该音频已有的接受词时间，原始识别不变"})
	} else {
		for _, transcript := range project.Transcripts {
			if transcript.AssetID == asset.ID && transcript.CaptionTrackID != "" {
				plan.Warnings = append(plan.Warnings, "此音频已有持续字幕规则，新复用会按已有规则派生字幕；本工作流没有关闭或更改旧规则")
				break
			}
		}
	}
	plan.AffectedClipIDs = append(plan.AffectedClipIDs, clip.ID)
	plan.Effects = append(plan.Effects, Effect{Kind: "voiceover-place", ClipID: clip.ID, TrackID: trackID, StartFrame: clip.StartFrame, EndFrame: clip.StartFrame + frames, Description: "按已导入音频真实时长编排，原音轨不删改"})
	plan.Evidence = append(plan.Evidence, Evidence{Kind: "imported-audio-duration", ClipID: clip.ID, AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, SourceEndUs: asset.DurationUs, StartFrame: clip.StartFrame, EndFrame: clip.StartFrame + frames, Description: "使用已导入音频时长及内容指纹；不足一帧的尾数按工程源边界保留在素材中"})
	plan.Summary = fmt.Sprintf("把已导入配音放到第%d帧，使用真实长度%d帧", clip.StartFrame, frames)
	return nil
}
