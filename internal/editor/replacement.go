package editor

import (
	"reflect"
	"strings"
)

// replaceVisual 是单个原子领域步骤：图像身份不变，旧讲话只迁移到同一真实音轨源。
// 声音载体不会复制素材、创建识别结果或借用其它文件的字幕时间。
func replaceVisual(project *Project, command Command) error {
	if command.Clip == nil {
		return problem("replacement", "镜头替换缺少目标片段")
	}
	index := clipIndex(project, command.Clip.ID)
	if index < 0 {
		return problem("not_found", "替换目标片段不存在")
	}
	before, after := project.Clips[index], cloneClip(*command.Clip)
	// 新画面以自己的输入重新建立配方；旧声音另在同源载体中保留处理相位。
	after.SourcePhase = nil
	if after.Kind == "image" || after.AssetID != before.AssetID && sameAudioRange(after.AudioRange, before.AudioRange) {
		after.AudioRange = nil
	}
	if !validKind(before.Kind, "video", "image") || !validKind(after.Kind, "video", "image") || before.TrackID != after.TrackID || before.StartFrame != after.StartFrame || before.DurationFrames != after.DurationFrames || after.CaptionSource != nil {
		return problem("replacement", "替换必须保留视觉片段ID、轨道、起点和时长")
	}
	if err := editableTrack(project, before.TrackID); err != nil {
		return err
	}
	original, found := transcriptAsset(project, before.AssetID)
	if !found {
		return problem("replacement", "原镜头素材不存在")
	}
	candidate, found := transcriptAsset(project, after.AssetID)
	if !found || candidate.Kind != after.Kind || candidate.Missing {
		return problem("replacement", "候选必须为已注册且类型一致的可用视觉素材")
	}
	mode := command.AudioMode
	if !validKind(mode, "keep_original", "use_candidate", "mute") {
		return problem("replacement", "替换必须明确原声、候选声或静音策略")
	}
	if mode == "use_candidate" && (after.Kind != "video" || !candidate.HasAudio || after.Muted) {
		return problem("replacement", "候选声音策略需要真实音频流且视觉片段不能静音")
	}
	if mode == "use_candidate" {
		window, err := materializeAudioWindow(after, candidate, project.Settings)
		if err != nil {
			return err
		}
		if window.AudioRange.Start == window.AudioRange.End {
			return problem("replacement", "候选在所选真实源区间内没有音频覆盖，不能声称使用了候选声音")
		}
	}
	if mode != "use_candidate" && !after.Muted {
		return problem("replacement", "保留原声/静音时新画面自身必须静音")
	}
	if before.LinkGroup != "" && after.LinkGroup != before.LinkGroup {
		return problem("replacement", "替换不能改变原有链接组")
	}
	if before.LinkGroup == "" && after.LinkGroup != "" {
		for _, clip := range project.Clips {
			if clip.LinkGroup == after.LinkGroup {
				return problem("replacement", "替换不能借新链接关联未选片段")
			}
		}
	}
	if mode == "use_candidate" && (after.Volume <= 0 || project.Tracks[trackIndex(project, before.TrackID)].Muted) {
		return problem("replacement", "目标片段音量为0或视频轨静音，不能声称使用了候选声音")
	}
	if mode != "keep_original" && before.LinkGroup != "" {
		for _, linked := range project.Clips {
			if linked.ID != before.ID && linked.LinkGroup == before.LinkGroup && linked.Kind == "audio" && !linked.Muted {
				return problem("replacement", "候选声/静音策略须先明确静音已链接原音频")
			}
		}
	}
	requiresCarrier := false
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.SourceClipID == before.ID {
			requiresCarrier = true
		}
	}
	for _, transcript := range project.Transcripts {
		for _, exclusion := range transcript.CaptionExclusions {
			if exclusion.SourceClipID == before.ID {
				requiresCarrier = true
			}
		}
	}
	if command.AudioCarrier == nil {
		if requiresCarrier {
			return problem("caption_binding", "原讲话字幕/排除记录需要同源真实音频承载，不能绑定新画面")
		}
		if mode == "keep_original" && original.HasAudio && !before.Muted && before.Volume > 0 && !project.Tracks[trackIndex(project, before.TrackID)].Muted {
			return problem("replacement", "原镜头内嵌声音可听，必须保留为真实音频片段")
		}
		if command.Track != nil {
			return problem("replacement", "没有音频承载时不能创建附带轨道")
		}
	} else {
		carrier := cloneClip(*command.AudioCarrier)
		if before.Kind != "video" || original.Kind != "video" || !original.HasAudio || original.Missing || carrier.Kind != "audio" || strings.TrimSpace(carrier.ID) == "" || carrier.ID == before.ID || carrier.AssetID != before.AssetID || carrier.StartFrame != before.StartFrame || carrier.DurationFrames != before.DurationFrames || carrier.SpeedNum <= 0 || carrier.SpeedDen <= 0 || carrier.SpeedNum > 1_000_000 || carrier.SpeedDen > 1_000_000 || int64(carrier.SpeedNum)*int64(before.SpeedDen) != int64(before.SpeedNum)*int64(carrier.SpeedDen) || carrier.CaptionSource != nil || carrier.LUT != nil {
			return problem("replacement", "音频承载必须引用原视频已有的音轨与完全相同源/工程映射")
		}
		if carrier.LinkGroup != after.LinkGroup {
			return problem("replacement", "原声承载必须与替换画面保持同一链接关系")
		}
		if mode != "keep_original" && !carrier.Muted {
			return problem("replacement", "原字幕保留来源时，候选声/静音模式的旧讲话承载必须静音")
		}
		carrierIndex := clipIndex(project, carrier.ID)
		if before.AudioRange == nil && carrier.SourceInUs != before.SourceInUs {
			return problem("replacement", "原声承载不能改变原源入点")
		}
		if !sameResolvedAudio(before, carrier, original, project.Settings) {
			return problem("replacement", "原声承载必须保留真实声音的完整源/工程区间")
		}
		if carrierIndex >= 0 {
			existing := project.Clips[carrierIndex]
			if before.LinkGroup == "" || existing.LinkGroup != before.LinkGroup || !sameCaption(existing, carrier) || command.Track != nil {
				return problem("replacement", "复用承载只能使用未经修改的现有同源linked音频")
			}
		} else {
			if carrier.AudioFade != nil && !reflect.DeepEqual(carrier.AudioFade, before.AudioFade) {
				return problem("replacement", "分离原声不能改变原淡化曲线")
			}
			carrier.AudioFade = CloneAudioFade(before.AudioFade)
			if after.LinkGroup == "" {
				return problem("replacement", "新原声承载需要明确的新/原链接组")
			}
			if carrier.Volume != before.Volume {
				return problem("replacement", "分离的原声音量必须保持原片段值")
			}
			if carrier.AudioRange == nil {
				window, err := materializeAudioWindow(before, original, project.Settings)
				if err != nil {
					return err
				}
				anchor, err := audioAnchor(window)
				if err != nil {
					return err
				}
				if err := installAudioRange(&carrier, *window.AudioRange, anchor); err != nil {
					return err
				}
			}
			owned, err := CaptureSourcePhase(before, original, project.Settings)
			if err != nil {
				return err
			}
			carrier.SourcePhase = CloneSourcePhase(owned.SourcePhase)
			carrier, err = recipeWindow(carrier, original, project.Settings)
			if err != nil {
				return err
			}
			oldTrack := project.Tracks[trackIndex(project, before.TrackID)]
			expectedMute := mode != "keep_original" || before.Muted || oldTrack.Muted
			if carrier.Muted != expectedMute {
				return problem("replacement", "分离的原声必须保留原静音状态，不能暗中打开声音")
			}
			if command.Track != nil {
				if command.Track.Kind != "audio" || command.Track.ID != carrier.TrackID || trackIndex(project, command.Track.ID) >= 0 {
					return problem("replacement", "原声新轨必须是新的匹配音频轨")
				}
				track := *command.Track
				if err := applyCommand(project, Command{Type: "add_track", Track: &track}); err != nil {
					return err
				}
			}
			track := trackIndex(project, carrier.TrackID)
			if track < 0 || project.Tracks[track].Kind != "audio" {
				return problem("replacement", "原声承载需要真实音频轨")
			}
			if err := editableTrack(project, carrier.TrackID); err != nil {
				return err
			}
			if !carrier.Muted && carrier.Volume > 0 && project.Tracks[trackIndex(project, carrier.TrackID)].Muted {
				return problem("replacement", "目标原声轨静音，无法保持原可听状态")
			}
			project.Clips = append(project.Clips, carrier)
		}
		if err := editableTrack(project, carrier.TrackID); err != nil {
			return err
		}
		// 先迁移真实源引用，随后一次reconcile只看到新画面与原声载体的最终关系。
		for i := range project.Clips {
			clip := &project.Clips[i]
			if clip.CaptionSource == nil || clip.CaptionSource.SourceClipID != before.ID {
				continue
			}
			if err := editableTrack(project, clip.TrackID); err != nil {
				return err
			}
			clip.CaptionSource = cloneClip(*clip).CaptionSource
			clip.CaptionSource.SourceClipID = carrier.ID
		}
		for i := range project.Transcripts {
			seen := map[CaptionExclusion]bool{}
			exclusions := []CaptionExclusion{}
			for _, value := range project.Transcripts[i].CaptionExclusions {
				if value.SourceClipID == before.ID {
					value.SourceClipID = carrier.ID
				}
				if !seen[value] {
					seen[value] = true
					exclusions = append(exclusions, value)
				}
			}
			project.Transcripts[i].CaptionExclusions = exclusions
		}
	}
	// 不对linked旧音频做隐式delta：其保留/静音已经在显式请求中声明。
	project.Clips[index] = after
	return nil
}
