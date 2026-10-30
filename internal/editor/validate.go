package editor

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const SchemaVersion = 7

// Error 的 Code 可供桌面层分类展示；Message 保留可读中文原因。
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func problem(code, message string) error { return &Error{Code: code, Message: message} }

func validKind(kind string, values ...string) bool {
	for _, value := range values {
		if kind == value {
			return true
		}
	}
	return false
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func ValidateSettings(settings Settings) error {
	if settings.Width < 1 || settings.Height < 1 || settings.Width > 16384 || settings.Height > 16384 {
		return problem("validation", "画布宽高必须在 1 到 16384 像素之间")
	}
	if settings.FPSNum <= 0 || settings.FPSDen <= 0 || settings.FPSNum > 1_000_000 || settings.FPSDen > 1_000_000 || int64(settings.FPSNum) > int64(settings.FPSDen)*240 {
		return problem("validation", "工程帧率必须为有效正有理数且不超过 240fps")
	}
	if settings.SampleRate < 8000 || settings.SampleRate > 192000 {
		return problem("validation", "采样率必须在 8000 到 192000Hz 之间")
	}
	return nil
}

func ValidateProject(project Project) error {
	if err := validateSequenceSet(project); err != nil {
		return err
	}
	if err := validateSingleTimeline(project); err != nil {
		return err
	}
	for _, s := range project.OtherSequences {
		view := SequenceProject(project, s)
		if err := validateSingleTimeline(view); err != nil {
			return fmt.Errorf("序列%s：%w", s.Name, err)
		}
	}
	return nil
}

func validateSingleTimeline(project Project) error {
	if project.SchemaVersion != SchemaVersion {
		return problem("unsupported_schema", fmt.Sprintf("不支持工程版本 %d，当前支持版本为 %d", project.SchemaVersion, SchemaVersion))
	}
	if strings.TrimSpace(project.ID) == "" || strings.TrimSpace(project.Name) == "" || project.Revision < 0 {
		return problem("validation", "工程标识、名称或修订号无效")
	}
	if err := ValidateSettings(project.Settings); err != nil {
		return err
	}
	if len(project.Tracks) == 0 {
		return problem("validation", "工程至少需要一条轨道")
	}
	assets := make(map[string]Asset, len(project.Assets))
	for _, asset := range project.Assets {
		if err := validateAudioAsset(asset); err != nil {
			return err
		}
		if strings.TrimSpace(asset.ID) == "" || strings.TrimSpace(asset.Name) == "" || strings.TrimSpace(asset.Path) == "" {
			return problem("validation", "素材缺少标识、名称或文件路径")
		}
		if _, exists := assets[asset.ID]; exists {
			return problem("validation", "素材标识重复："+asset.ID)
		}
		if !validKind(asset.Kind, "video", "audio", "image", "lut", "font") || asset.DurationUs < 0 || asset.Width < 0 || asset.Height < 0 || asset.FPSNum < 0 || asset.FPSDen < 0 {
			return problem("validation", "素材类型或元信息无效："+asset.Name)
		}
		if validKind(asset.Kind, "video", "audio") && asset.DurationUs <= 0 {
			return problem("validation", "音视频素材缺少有效时长："+asset.Name)
		}
		if asset.Kind == "font" {
			if asset.Font == nil || strings.TrimSpace(asset.Font.Family) == "" || len(asset.Font.Family) > 512 || len(asset.Font.Style) > 512 || asset.Font.Weight < 1 || asset.Font.Weight > 1000 || len(asset.Fingerprint) != 71 || !strings.HasPrefix(asset.Fingerprint, "sha256:") {
				return problem("validation", "字体资源元信息无效："+asset.Name)
			}
		} else if asset.Font != nil {
			return problem("validation", "非字体素材不能携带字体元信息")
		}
		assets[asset.ID] = asset
	}
	tracks := make(map[string]Track, len(project.Tracks))
	for _, track := range project.Tracks {
		if strings.TrimSpace(track.ID) == "" || strings.TrimSpace(track.Name) == "" || !validKind(track.Kind, "video", "audio", "text", "caption") {
			return problem("validation", "轨道标识、名称或类型无效")
		}
		if _, exists := tracks[track.ID]; exists {
			return problem("validation", "轨道标识重复："+track.ID)
		}
		tracks[track.ID] = track
	}
	clipIDs := make(map[string]struct{}, len(project.Clips))
	byTrack := make(map[string][]*Clip)
	for index, clip := range project.Clips {
		if strings.TrimSpace(clip.ID) == "" || strings.TrimSpace(clip.Name) == "" || !validKind(clip.Kind, "video", "audio", "image", "text", "caption") {
			return problem("validation", "片段标识、名称或类型无效")
		}
		if _, exists := clipIDs[clip.ID]; exists {
			return problem("validation", "片段标识重复："+clip.ID)
		}
		clipIDs[clip.ID] = struct{}{}
		track, exists := tracks[clip.TrackID]
		if !exists {
			return problem("validation", "片段引用的轨道不存在："+clip.Name)
		}
		if (track.Kind == "video" && !validKind(clip.Kind, "video", "image")) || (track.Kind != "video" && clip.Kind != track.Kind) {
			return problem("validation", "片段与目标轨道类型不兼容："+clip.Name)
		}
		if clip.StartFrame < 0 || clip.DurationFrames <= 0 || clip.StartFrame > math.MaxInt64-clip.DurationFrames || clip.SourceInUs < 0 {
			return problem("validation", "片段位置、长度或素材入点无效："+clip.Name)
		}
		if clip.SpeedNum <= 0 || clip.SpeedDen <= 0 || clip.SpeedNum > 1_000_000 || clip.SpeedDen > 1_000_000 || int64(clip.SpeedNum) > int64(clip.SpeedDen)*100 || int64(clip.SpeedDen) > int64(clip.SpeedNum)*100 {
			return problem("validation", "片段速度必须在 0.01 到 100 倍之间："+clip.Name)
		}
		if !finite(clip.Volume) || clip.Volume < 0 || clip.Volume > 10 || !finite(clip.Transform.X) || !finite(clip.Transform.Y) || !finite(clip.Transform.Scale) || clip.Transform.Scale < 0 || clip.Transform.Scale > 100 || !finite(clip.Transform.Rotation) {
			return problem("validation", "片段音量或变换数值无效："+clip.Name)
		}
		if clip.TextStyle.FontSize < 0 || clip.TextStyle.FontSize > 4096 {
			return problem("validation", "文字字号超出有效范围："+clip.Name)
		}
		if validKind(clip.Kind, "video", "audio", "image") {
			asset, exists := assets[clip.AssetID]
			compatibleAudio := clip.Kind == "audio" && asset.Kind == "video" && asset.HasAudio
			if !exists || asset.Kind != clip.Kind && !compatibleAudio {
				return problem("validation", "片段素材不存在或类型不匹配："+clip.Name)
			}
			if validKind(clip.Kind, "video", "audio") && !SourceRangeFits(clip, asset, project.Settings) {
				return problem("source_out_of_range", "片段使用范围超出素材时长："+clip.Name)
			}
			if clip.Kind == "image" && clip.SourceInUs != 0 {
				return problem("validation", "静态图片不能设置素材入点："+clip.Name)
			}
			if clip.AudioRange != nil {
				if _, err := ResolveAudioRange(clip, asset, project.Settings); err != nil {
					return err
				}
			}
			if err := validateSourcePhaseState(clip, asset, project.Settings); err != nil {
				return err
			}
		} else if clip.AssetID != "" || clip.SourceInUs != 0 {
			return problem("validation", "文字或字幕片段不能引用音视频素材区间："+clip.Name)
		}
		if clip.AudioRange != nil && !validKind(clip.Kind, "audio", "video") {
			return problem("audio_range", "静态图与字幕不能携带声音范围")
		}
		if clip.SourcePhase != nil && !validKind(clip.Kind, "audio", "video") {
			return problem("source_phase", "静态图与字幕不能携带源处理配方")
		}
		if err := ValidateAudioFade(clip, assets[clip.AssetID], project.Settings); err != nil {
			return err
		}
		if clip.TextStyle.FontAssetID != "" {
			font, ok := assets[clip.TextStyle.FontAssetID]
			if !validKind(clip.Kind, "text", "caption") || !ok || font.Kind != "font" {
				return problem("validation", "文字引用的工程字体不存在或类型无效："+clip.Name)
			}
		}
		if clip.LUT != nil {
			asset, exists := assets[clip.LUT.AssetID]
			if !validKind(clip.Kind, "video", "image") || !exists || asset.Kind != "lut" || !finite(clip.LUT.Intensity) || clip.LUT.Intensity < 0 || clip.LUT.Intensity > 1 {
				return problem("validation", "LUT 资源、强度或目标类型无效："+clip.Name)
			}
		}
		byTrack[clip.TrackID] = append(byTrack[clip.TrackID], &project.Clips[index])
	}
	for _, clips := range byTrack {
		if len(clips) > 0 && clips[0].Kind == "audio" {
			if err := validateAudioTrackOverlap(clips, assets, project.Settings); err != nil {
				return err
			}
			continue
		}
		sort.Slice(clips, func(i, j int) bool { return clips[i].StartFrame < clips[j].StartFrame })
		for i := 1; i < len(clips); i++ {
			if clips[i].StartFrame < clips[i-1].StartFrame+clips[i-1].DurationFrames {
				return problem("overlap", "同一轨道上的片段不能重叠，请移到另一轨道")
			}
		}
	}
	if err := validateTranscripts(project, assets, tracks); err != nil {
		return err
	}
	return validateVisualAnalyses(project)
}
