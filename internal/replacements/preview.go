package replacements

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"aividcut/internal/editor"
)

func Preview(project editor.Project, input Input) (Plan, error) {
	if err := editor.ValidateProject(project); err != nil {
		return Plan{}, err
	}
	project = editor.CloneProject(project)
	if input.AudioMode == "" {
		input.AudioMode = KeepOriginal
	}
	if input.LUTMode == "" {
		input.LUTMode = KeepLUT
	}
	if input.SpeedMode == "" {
		input.SpeedMode = PreserveSpeed
	}
	if input.AudioMode != KeepOriginal && input.AudioMode != UseCandidate && input.AudioMode != Mute {
		return Plan{}, errors.New("请选择保留原声、使用候选声音或静音")
	}
	if input.LUTMode != KeepLUT && input.LUTMode != ClearLUT {
		return Plan{}, errors.New("请选择保留或清除原LUT调色")
	}
	if input.SpeedMode != PreserveSpeed && input.SpeedMode != NormalSpeed {
		return Plan{}, errors.New("请选择保持原播放速度或以1倍速播放候选")
	}
	if input.SourceInUs < 0 {
		return Plan{}, errors.New("候选源入点不能为负")
	}
	before, found := findClip(project, input.ClipID)
	if !found || before.Kind != "video" && before.Kind != "image" {
		return Plan{}, errors.New("请选择当前工程的视频或图片片段")
	}
	targetTrack, found := findTrack(project, before.TrackID)
	if !found || targetTrack.Locked {
		return Plan{}, errors.New("目标视频轨不存在或已锁定")
	}
	original, err := usableAsset(project, before.AssetID)
	if err != nil {
		return Plan{}, fmt.Errorf("原镜头：%w", err)
	}
	candidate, err := usableAsset(project, input.AssetID)
	if err != nil {
		return Plan{}, fmt.Errorf("候选：%w", err)
	}
	if candidate.Kind != "video" && candidate.Kind != "image" {
		return Plan{}, errors.New("请先将候选视频或图片导入当前工程")
	}
	for _, transcript := range project.Transcripts {
		if transcript.AssetID == candidate.ID && transcript.CaptionTrackID != "" {
			return Plan{}, errors.New("候选素材已关联字幕，请先确认要保留哪一份讲话字幕后再替换")
		}
	}
	if input.AudioMode == UseCandidate && (candidate.Kind != "video" || !candidate.HasAudio || before.Volume <= 0 || targetTrack.Muted) {
		return Plan{}, errors.New("使用候选声音需要带声音的视频。请同时确认片段音量大于0、视频轨未静音")
	}
	id := editor.NewID("replacement")
	plan := Plan{ID: id, ProjectID: project.ID, ClipID: before.ID, AssetID: candidate.ID, AudioMode: input.AudioMode, Request: editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: id, Label: "替换镜头", Commands: []editor.Command{}}, Effects: []Effect{}, Evidence: []Evidence{}, Warnings: []string{}, AffectedClipIDs: []string{before.ID}}
	after := before
	after.AssetID, after.Kind, after.SourceInUs = candidate.ID, candidate.Kind, input.SourceInUs
	after.SourcePhase = nil
	after.AudioFade = nil
	if before.AudioFade != nil {
		plan.Warnings = append(plan.Warnings, "原声保留时沿用其淡化曲线；新候选声音不继承旧源淡化，请在替换后单独设置。")
	}
	if input.Transform != nil {
		after.Transform = *input.Transform
	}
	if input.LUTMode == ClearLUT {
		after.LUT = nil
	}
	if input.SpeedMode == NormalSpeed || candidate.Kind == "image" || before.Kind == "image" {
		after.SpeedNum, after.SpeedDen = 1, 1
	}
	if candidate.Kind == "image" {
		if input.SourceInUs != 0 {
			return Plan{}, errors.New("静态图片不能使用视频源入点")
		}
		if before.SpeedNum != before.SpeedDen {
			plan.Warnings = append(plan.Warnings, "图片按静态画面使用，原声播放速度保持不变")
		}
	}
	if before.Kind == "image" && candidate.Kind == "video" && input.SpeedMode == PreserveSpeed {
		plan.Warnings = append(plan.Warnings, "原画面是图片，候选视频将以1倍速播放")
	}
	if candidate.Kind == "video" && !editor.SourceRangeFits(after, candidate, project.Settings) {
		return Plan{}, errors.New("候选视频长度不足以按所选速度填满当前镜头，请调整入点或更换更长的候选")
	}
	after.Muted = input.AudioMode != UseCandidate
	related := []editor.Clip{}
	if before.LinkGroup != "" {
		for _, clip := range project.Clips {
			if clip.ID == before.ID || clip.LinkGroup != before.LinkGroup {
				continue
			}
			if clip.Kind != "audio" {
				return Plan{}, errors.New("该镜头还链接了其它画面或文字，请先整理链接关系后再替换")
			}
			if _, err := usableAsset(project, clip.AssetID); err != nil {
				return Plan{}, fmt.Errorf("原链接声音：%w", err)
			}
			related = append(related, clip)
		}
	}
	bound := []editor.Clip{}
	needsSpeechCarrier := false
	for _, clip := range project.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.SourceClipID == before.ID {
			bound = append(bound, clip)
			needsSpeechCarrier = true
		}
	}
	for _, transcript := range project.Transcripts {
		for _, exclusion := range transcript.CaptionExclusions {
			if exclusion.SourceClipID == before.ID {
				needsSpeechCarrier = true
			}
		}
	}
	inlineAudible := original.Kind == "video" && original.HasAudio && !before.Muted && !targetTrack.Muted && before.Volume > 0
	if input.AudioMode == KeepOriginal && !inlineAudible && len(related) == 0 {
		plan.Warnings = append(plan.Warnings, "原镜头当前没有可听原声，替换后保持无声，候选声音不会自动开启")
	}
	needsCarrier := needsSpeechCarrier || input.AudioMode == KeepOriginal && inlineAudible
	if needsCarrier && (!original.HasAudio || original.Kind != "video") {
		return Plan{}, errors.New("原字幕对应的讲话音轨暂时不可用，请先确认原声音频，或解除字幕关联后再替换")
	}
	if needsCarrier {
		for _, transcript := range project.Transcripts {
			if transcript.CaptionTrackID == "" || transcript.AssetID == original.ID {
				continue
			}
			for _, linked := range related {
				if linked.AssetID == transcript.AssetID {
					return Plan{}, errors.New("链接音频各自关联了不同讲话字幕，请先确定要保留的字幕来源")
				}
			}
		}
	}
	for i := range related {
		clip := related[i]
		if input.AudioMode != KeepOriginal && !clip.Muted {
			track, _ := findTrack(project, clip.TrackID)
			if track.Locked {
				return Plan{}, errors.New("需要静音的原链接音轨已锁定，请先解锁")
			}
			clip.Muted = true
			related[i] = clip
			plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "update_clip", Clip: &clip, IncludeLinked: boolPointer(false)})
			plan.AffectedClipIDs = append(plan.AffectedClipIDs, clip.ID)
			plan.Effects = append(plan.Effects, Effect{Kind: "linked-audio-mute", ClipID: clip.ID, TrackID: clip.TrackID, AssetID: clip.AssetID, Description: "将已链接原声静音，保留原音频片段"})
		}
		asset, _ := usableAsset(project, clip.AssetID)
		plan.Evidence = append(plan.Evidence, evidenceFor(clip, asset))
	}
	var carrier *editor.Clip
	var newTrack *editor.Track
	if needsCarrier {
		if input.AudioMode != KeepOriginal || !inlineAudible {
			for _, existing := range related {
				if sameSourceMap(before, existing) {
					value := existing
					carrier = &value
					break
				}
			}
		}
		if carrier == nil {
			trackID := input.AudioTrackID
			if trackID == "" {
				track := editor.Track{ID: editor.NewID("original-audio-track"), Name: "原讲话声音", Kind: "audio"}
				newTrack = &track
				trackID = track.ID
			} else {
				track, found := findTrack(project, trackID)
				if !found || track.Kind != "audio" || track.Locked {
					return Plan{}, errors.New("原声目标轨必须是未锁定音频轨")
				}
			}
			value := editor.DefaultClip(editor.NewID("original-audio"), trackID, "audio")
			value.AssetID, value.Name, value.StartFrame, value.DurationFrames, value.SourceInUs = before.AssetID, before.Name+" · 原声", before.StartFrame, before.DurationFrames, before.SourceInUs
			value.SpeedNum, value.SpeedDen, value.Volume = before.SpeedNum, before.SpeedDen, before.Volume
			value.AudioFade = editor.CloneAudioFade(before.AudioFade)
			value.Muted = input.AudioMode != KeepOriginal || before.Muted || targetTrack.Muted
			if after.LinkGroup == "" {
				after.LinkGroup = editor.NewID("replacement-link")
			}
			value.LinkGroup = after.LinkGroup
			carrier = &value
			plan.AffectedClipIDs = append(plan.AffectedClipIDs, value.ID)
			state := "将原声保留在音频轨，播放位置、速度和音量不变"
			if value.Muted {
				state = "保留原音频片段供字幕继续跟随，原声保持静音"
			}
			plan.Effects = append(plan.Effects, Effect{Kind: "original-audio-carrier", ClipID: value.ID, TrackID: value.TrackID, AssetID: value.AssetID, Description: state})
			if input.AudioMode == KeepOriginal && inlineAudible && len(related) > 0 {
				plan.Warnings = append(plan.Warnings, "原视频和已链接音频都保留各自的声音，保持原来的混音；不会再复制已链接音频")
			}
		} else {
			plan.AffectedClipIDs = append(plan.AffectedClipIDs, carrier.ID)
			plan.Effects = append(plan.Effects, Effect{Kind: "reuse-original-audio", ClipID: carrier.ID, TrackID: carrier.TrackID, AssetID: carrier.AssetID, Description: "继续使用已有的原音频片段，不添加重复原声"})
		}
		for _, caption := range bound {
			plan.AffectedClipIDs = append(plan.AffectedClipIDs, caption.ID)
		}
		if needsSpeechCarrier {
			plan.Effects = append(plan.Effects, Effect{Kind: "caption-origin-transfer", ClipID: carrier.ID, AssetID: original.ID, Description: "字幕继续跟随原讲话音频，保留人工文字、样式和布局；已删除或已解绑的字幕不重建"})
		}
		if needsSpeechCarrier && input.AudioMode != KeepOriginal {
			warning := "字幕仍对应原讲话，原声已静音；字幕文字和人工修改会保留"
			if input.AudioMode == UseCandidate {
				warning = "当前字幕仍对应原讲话；原声已静音，字幕尚未与候选声音对齐，请检查字幕，必要时解除关联"
			}
			plan.Warnings = append(plan.Warnings, warning)
		}
	}
	plan.Request.Commands = append(plan.Request.Commands, editor.Command{Type: "replace_visual", Clip: &after, AudioCarrier: carrier, AudioMode: input.AudioMode, Track: newTrack})
	visualDescription := "替换画面，保留原位置、长度和画面变换"
	if input.Transform != nil {
		visualDescription = "替换画面并使用指定的画面变换，镜头位置和长度不变"
	}
	plan.Effects = append(plan.Effects, Effect{Kind: "visual-replace", ClipID: after.ID, TrackID: after.TrackID, AssetID: candidate.ID, Description: visualDescription})
	if input.LUTMode == ClearLUT && before.LUT != nil {
		plan.Effects = append(plan.Effects, Effect{Kind: "lut-clear", ClipID: after.ID, Description: "清除镜头原有的LUT调色"})
	}
	plan.Evidence = append(plan.Evidence, evidenceFor(before, original), evidenceFor(after, candidate))
	engine, err := editor.NewEngine(project)
	if err != nil {
		return Plan{}, err
	}
	result, err := engine.Apply(plan.Request)
	if err != nil {
		return Plan{}, fmt.Errorf("暂时无法替换，工程未作修改：%w", err)
	}
	for _, clip := range project.Clips {
		if next, found := findClip(result, clip.ID); !found || !reflect.DeepEqual(clip, next) {
			plan.AffectedClipIDs = append(plan.AffectedClipIDs, clip.ID)
		}
	}
	set := map[string]bool{}
	for _, id := range plan.AffectedClipIDs {
		set[id] = true
	}
	plan.AffectedClipIDs = nil
	for id := range set {
		plan.AffectedClipIDs = append(plan.AffectedClipIDs, id)
	}
	sort.Strings(plan.AffectedClipIDs)
	mediaName := "视频"
	if candidate.Kind == "image" {
		mediaName = "图片"
	}
	audioName := map[string]string{KeepOriginal: "保留原声", UseCandidate: "使用候选声音", Mute: "静音"}[input.AudioMode]
	plan.Summary = fmt.Sprintf("使用%s替换画面，镜头位置和长度不变；%s", mediaName, audioName)
	return plan, nil
}

func boolPointer(value bool) *bool { return &value }
func findClip(project editor.Project, id string) (editor.Clip, bool) {
	for _, clip := range project.Clips {
		if clip.ID == id {
			return clip, true
		}
	}
	return editor.Clip{}, false
}
func findTrack(project editor.Project, id string) (editor.Track, bool) {
	for _, track := range project.Tracks {
		if track.ID == id {
			return track, true
		}
	}
	return editor.Track{}, false
}
func usableAsset(project editor.Project, id string) (editor.Asset, error) {
	for _, asset := range project.Assets {
		if asset.ID == id {
			if asset.Missing || asset.Fingerprint == "" {
				return editor.Asset{}, errors.New("素材文件缺失或尚未完成内容核验，请重新定位或导入")
			}
			return asset, nil
		}
	}
	return editor.Asset{}, errors.New("素材尚未导入当前工程")
}
func sameSourceMap(visual, audio editor.Clip) bool {
	return audio.Kind == "audio" && visual.AssetID == audio.AssetID && visual.StartFrame == audio.StartFrame && visual.DurationFrames == audio.DurationFrames && visual.SourceInUs == audio.SourceInUs && int64(visual.SpeedNum)*int64(audio.SpeedDen) == int64(audio.SpeedNum)*int64(visual.SpeedDen)
}
func evidenceFor(clip editor.Clip, asset editor.Asset) Evidence {
	return Evidence{AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, ClipID: clip.ID, SourceInUs: clip.SourceInUs, DurationFrames: clip.DurationFrames, SpeedNum: clip.SpeedNum, SpeedDen: clip.SpeedDen}
}
