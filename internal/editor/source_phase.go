package editor

import "math"

// SourceRecipe记录首次派生前的处理输入；外观、音量与字幕覆盖仍由当前片段控制。
type SourceRecipe struct {
	AssetID           string      `json:"assetId"`
	SourceFingerprint string      `json:"sourceFingerprint,omitempty"`
	Kind              string      `json:"kind"`
	StartFrame        int64       `json:"startFrame"`
	DurationFrames    int64       `json:"durationFrames"`
	SourceInUs        int64       `json:"sourceInUs"`
	SpeedNum          int         `json:"speedNum"`
	SpeedDen          int         `json:"speedDen"`
	FPSNum            int         `json:"fpsNum"`
	FPSDen            int         `json:"fpsDen"`
	AudioRange        *AudioRange `json:"audioRange,omitempty"`
}

type SourcePhase struct {
	Recipe       SourceRecipe `json:"recipe"`
	OffsetFrames int64        `json:"offsetFrames"`
}

type ResolvedSourcePhase struct {
	Root                Clip  `json:"root"`
	WindowStartFrame    int64 `json:"windowStartFrame"`
	WindowEndFrame      int64 `json:"windowEndFrame"`
	TimelineShiftFrames int64 `json:"timelineShiftFrames"`
}

type ResolvedSourceAudioPhase struct {
	Root                    ResolvedAudioRange `json:"root"`
	Current                 ResolvedAudioRange `json:"current"`
	ProcessingTimelineStart AudioTime          `json:"processingTimelineStart"`
	ProcessingTimelineEnd   AudioTime          `json:"processingTimelineEnd"`
}

func CloneSourcePhase(value *SourcePhase) *SourcePhase {
	if value == nil {
		return nil
	}
	copy := *value
	if value.Recipe.AudioRange != nil {
		rangeCopy := *value.Recipe.AudioRange
		copy.Recipe.AudioRange = &rangeCopy
	}
	return &copy
}

// CaptureSourcePhase同时materialize有声video，不能只修画面而让其原声继续逐段重启处理。
func CaptureSourcePhase(clip Clip, asset Asset, settings Settings) (Clip, error) {
	clip = cloneClip(clip)
	if err := ValidateSettings(settings); err != nil {
		return Clip{}, err
	}
	if clip.SpeedNum <= 0 || clip.SpeedDen <= 0 || clip.SpeedNum > 1_000_000 || clip.SpeedDen > 1_000_000 || int64(clip.SpeedNum) > 100*int64(clip.SpeedDen) || int64(clip.SpeedDen) > 100*int64(clip.SpeedNum) || clip.DurationFrames < 1 || clip.SourceInUs < 0 {
		return Clip{}, problem("validation", "片段速度、时长或素材入点无效")
	}
	if !SourceRangeFits(clip, asset, settings) {
		return Clip{}, problem("source_out_of_range", "片段选区超出素材余量")
	}
	if clip.SourcePhase != nil {
		clip.SourcePhase = CloneSourcePhase(clip.SourcePhase)
		return clip, ValidateSourcePhase(clip, asset, settings)
	}
	if !validKind(clip.Kind, "audio", "video") || clip.AssetID != asset.ID {
		return Clip{}, problem("source_phase", "处理配方只用于同一已登记视频或音频")
	}
	var err error
	if asset.HasAudio && clip.AudioRange == nil {
		clip, err = materializeAudioWindow(clip, asset, settings)
		if err != nil {
			return Clip{}, err
		}
	}
	clip.SourcePhase = &SourcePhase{Recipe: SourceRecipe{AssetID: asset.ID, SourceFingerprint: asset.Fingerprint, Kind: clip.Kind, StartFrame: clip.StartFrame, DurationFrames: clip.DurationFrames, SourceInUs: clip.SourceInUs, SpeedNum: clip.SpeedNum, SpeedDen: clip.SpeedDen, FPSNum: settings.FPSNum, FPSDen: settings.FPSDen}}
	if clip.AudioRange != nil {
		value := *clip.AudioRange
		clip.SourcePhase.Recipe.AudioRange = &value
	}
	return clip, ValidateSourcePhase(clip, asset, settings)
}

func sourcePhaseRoot(clip Clip, phase *SourcePhase) Clip {
	root := cloneClip(clip)
	root.SourcePhase = nil
	r := phase.Recipe
	root.Kind, root.AssetID, root.StartFrame, root.DurationFrames, root.SourceInUs = r.Kind, r.AssetID, r.StartFrame, r.DurationFrames, r.SourceInUs
	root.SpeedNum, root.SpeedDen = r.SpeedNum, r.SpeedDen
	root.AudioRange = nil
	if r.AudioRange != nil {
		value := *r.AudioRange
		root.AudioRange = &value
	}
	return root
}

func ValidateSourcePhase(clip Clip, asset Asset, settings Settings) error {
	phase := clip.SourcePhase
	if phase == nil {
		return nil
	}
	r := phase.Recipe
	if phase.OffsetFrames < 0 || clip.DurationFrames <= 0 || phase.OffsetFrames > math.MaxInt64-clip.DurationFrames || phase.OffsetFrames+clip.DurationFrames > r.DurationFrames || r.StartFrame < 0 || r.StartFrame > math.MaxInt64-r.DurationFrames || r.DurationFrames < 1 || r.SourceInUs < 0 || r.SpeedNum < 1 || r.SpeedDen < 1 || r.SpeedNum > 1_000_000 || r.SpeedDen > 1_000_000 || clip.SpeedNum < 1 || clip.SpeedDen < 1 || clip.SpeedNum > 1_000_000 || clip.SpeedDen > 1_000_000 || r.FPSNum < 1 || r.FPSDen < 1 || r.FPSNum > 1_000_000 || r.FPSDen > 1_000_000 {
		return problem("source_phase", "原处理范围或派生输出窗口无效；延长选区需要重建配方")
	}
	if !validKind(r.Kind, "audio", "video") || !validKind(clip.Kind, "audio", "video") || r.AssetID != clip.AssetID || r.AssetID != asset.ID || r.SourceFingerprint != "" && r.SourceFingerprint != asset.Fingerprint {
		return problem("source_phase", "原处理配方与当前素材来源不一致，不能复用旧相位")
	}
	if r.Kind != clip.Kind && !(r.Kind == "video" && clip.Kind == "audio" && asset.Kind == "video" && asset.HasAudio) {
		return problem("source_phase", "原处理种类不能迁移到不同的素材载体")
	}
	if int64(r.SpeedNum)*int64(clip.SpeedDen) != int64(clip.SpeedNum)*int64(r.SpeedDen) || int64(r.FPSNum)*int64(settings.FPSDen) != int64(settings.FPSNum)*int64(r.FPSDen) {
		return problem("source_phase", "速度或工程帧率已改变，需要按新输入重建处理配方")
	}
	root := sourcePhaseRoot(clip, phase)
	if !SourceRangeFits(root, asset, settings) {
		return problem("source_phase", "原处理完整输入超出源素材余量")
	}
	if r.AudioRange != nil {
		if _, err := ResolveAudioRange(root, asset, settings); err != nil {
			return problem("source_phase", "原处理配方的音频范围无效："+err.Error())
		}
	}
	if asset.HasAudio && r.AudioRange == nil {
		return problem("source_phase", "有声素材的原处理配方缺少真实音频范围")
	}
	return nil
}

func ResolveSourcePhase(clip Clip, asset Asset, settings Settings) (ResolvedSourcePhase, error) {
	owned, err := CaptureSourcePhase(clip, asset, settings)
	if err != nil {
		return ResolvedSourcePhase{}, err
	}
	phase := owned.SourcePhase
	return ResolvedSourcePhase{Root: sourcePhaseRoot(owned, phase), WindowStartFrame: phase.OffsetFrames, WindowEndFrame: phase.OffsetFrames + clip.DurationFrames, TimelineShiftFrames: clip.StartFrame - phase.Recipe.StartFrame - phase.OffsetFrames}, nil
}

// ResolveSourceAudioPhase把原完整处理范围与当前真实声音窗口分开；处理参考原点可在工程0以前。
func ResolveSourceAudioPhase(clip Clip, asset Asset, settings Settings) (ResolvedSourceAudioPhase, error) {
	owned, err := CaptureSourcePhase(clip, asset, settings)
	if err != nil {
		return ResolvedSourceAudioPhase{}, err
	}
	phase, err := ResolveSourcePhase(owned, asset, settings)
	if err != nil {
		return ResolvedSourceAudioPhase{}, err
	}
	root, err := ResolveAudioRange(phase.Root, asset, settings)
	if err != nil {
		return ResolvedSourceAudioPhase{}, err
	}
	current, err := ResolveAudioRange(owned, asset, settings)
	if err != nil {
		return ResolvedSourceAudioPhase{}, err
	}
	start, _ := root.TimelineStart.Rat()
	end, _ := root.TimelineEnd.Rat()
	shift := frameTimeRat(phase.TimelineShiftFrames, settings)
	start.Add(start, shift)
	end.Add(end, shift)
	result := ResolvedSourceAudioPhase{Root: root, Current: current}
	if result.ProcessingTimelineStart, err = audioTime(start); err != nil {
		return result, err
	}
	if result.ProcessingTimelineEnd, err = audioTime(end); err != nil {
		return result, err
	}
	// 源单位的共同切口可有不足一个样本的量化差额；派生段选择的是原处理
	// 输出帧窗，不能把每个源切口分别映射后在闭合停顿时产生重叠或空洞。
	if owned.SourcePhase.OffsetFrames > 0 {
		boundary := frameTimeRat(owned.StartFrame, settings)
		if start.Cmp(boundary) < 0 {
			start.Set(boundary)
		}
	}
	if owned.SourcePhase.OffsetFrames+owned.DurationFrames < owned.SourcePhase.Recipe.DurationFrames {
		boundary := frameTimeRat(owned.StartFrame+owned.DurationFrames, settings)
		if end.Cmp(boundary) > 0 {
			end.Set(boundary)
		}
	}
	if end.Cmp(start) < 0 {
		end.Set(start)
	}
	result.Current.Empty = root.Empty || end.Cmp(start) == 0
	if result.Current.TimelineStart, err = audioTime(start); err != nil {
		return result, err
	}
	if result.Current.TimelineEnd, err = audioTime(end); err != nil {
		return result, err
	}
	return result, nil
}
