package editor

import (
	"math/big"
	"reflect"
)

// AudioFade记录输出包络；派生段只选择原曲线窗口，不能重新淡入。
type AudioFade struct {
	InUs         int64 `json:"inUs"`
	OutUs        int64 `json:"outUs"`
	SpanFrames   int64 `json:"spanFrames"`
	OffsetFrames int64 `json:"offsetFrames"`
}

func CloneAudioFade(f *AudioFade) *AudioFade {
	if f == nil {
		return nil
	}
	v := *f
	return &v
}
func NewAudioFade(clip Clip, in, out int64) *AudioFade {
	if in == 0 && out == 0 {
		return nil
	}
	return &AudioFade{InUs: in, OutUs: out, SpanFrames: clip.DurationFrames}
}

// FittedAudioFade沿用核心重建时的同比缩短规则，供明确选择缩短的工作流使用。
func FittedAudioFade(clip Clip, settings Settings, in, out int64) (*AudioFade, error) {
	if err := ValidateSettings(settings); err != nil {
		return nil, err
	}
	if in < 0 || out < 0 || in > 86_400_000_000 || out > 86_400_000_000 {
		return nil, problem("audio_fade", "淡化时长需为0到24小时的整数微秒")
	}
	f := NewAudioFade(clip, in, out)
	if f == nil {
		return nil, nil
	}
	if err := fitAudioFadeDuration(f, settings); err != nil {
		return nil, err
	}
	copy := cloneClip(clip)
	copy.AudioFade = f
	if err := ValidateAudioFade(copy, Asset{HasAudio: true}, settings); err != nil {
		return nil, err
	}
	return f, nil
}

func fitAudioFadeDuration(f *AudioFade, settings Settings) error {
	span, err := floorRat(frameTimeRat(f.SpanFrames, settings))
	if err != nil || span < 1 {
		return problem("audio_fade", "淡化范围需要有效的正时长")
	}
	if total := f.InUs + f.OutUs; total > span && total > 0 {
		part := new(big.Int).Mul(big.NewInt(span), big.NewInt(f.InUs))
		part.Quo(part, big.NewInt(total))
		f.InUs = part.Int64()
		f.OutUs = span - f.InUs
	}
	return nil
}
func ValidateAudioFade(clip Clip, asset Asset, settings Settings) error {
	f := clip.AudioFade
	if f == nil {
		return nil
	}
	if !mediaSourceKind(clip) || !asset.HasAudio || f.InUs < 0 || f.OutUs < 0 || f.InUs > 86_400_000_000 || f.OutUs > 86_400_000_000 || f.InUs+f.OutUs == 0 || f.SpanFrames < 1 || f.OffsetFrames < 0 || clip.DurationFrames > f.SpanFrames || f.OffsetFrames > f.SpanFrames-clip.DurationFrames {
		return problem("audio_fade", "淡化需要有声片段、非负时长与有效的原包络保留窗")
	}
	span := frameTimeRat(f.SpanFrames, settings)
	if span.Cmp(new(big.Rat).SetInt64(86_400_000_000)) > 0 || new(big.Rat).SetInt64(f.InUs+f.OutUs).Cmp(span) > 0 {
		return problem("audio_fade", "淡入和淡出总时长不能超过片段包络范围（最多24小时）")
	}
	return nil
}

// AudioFadeSampleBounds使用真实采样时钟，虚拟起点允许在当前工程0之前。
func AudioFadeSampleBounds(clip Clip, settings Settings, rate int) (start, end, in, out float64) {
	f := clip.AudioFade
	if f == nil {
		return
	}
	start = float64(clip.StartFrame-f.OffsetFrames) * float64(rate) * float64(settings.FPSDen) / float64(settings.FPSNum)
	end = start + float64(f.SpanFrames)*float64(rate)*float64(settings.FPSDen)/float64(settings.FPSNum)
	in = float64(f.InUs) * float64(rate) / 1e6
	out = float64(f.OutUs) * float64(rate) / 1e6
	return
}
func finishAudioFadeUpdate(before Clip, after *Clip, settings Settings) {
	if before.AudioFade == nil || !reflect.DeepEqual(before.AudioFade, after.AudioFade) {
		return
	}
	f := CloneAudioFade(before.AudioFade)
	if before.AssetID == after.AssetID && before.Kind == after.Kind && before.SpeedNum == after.SpeedNum && before.SpeedDen == after.SpeedDen {
		head := int64(0)
		same := !sourceTimingChanged(before, *after)
		if before.SourcePhase != nil && after.SourcePhase != nil && reflect.DeepEqual(before.SourcePhase.Recipe, after.SourcePhase.Recipe) {
			head = after.SourcePhase.OffsetFrames - before.SourcePhase.OffsetFrames
			same = true
		}
		if same && head >= -f.OffsetFrames && head <= f.SpanFrames-f.OffsetFrames && after.DurationFrames <= f.SpanFrames-f.OffsetFrames-head {
			f.OffsetFrames += head
			after.AudioFade = f
			return
		}
	}
	// 处理配方重建/越出原包络时按当前长度重设；不足时同比缩短两端。
	f.OffsetFrames, f.SpanFrames = 0, after.DurationFrames
	_ = fitAudioFadeDuration(f, settings) // 无效几何仍由本次核心事务的完整校验拒绝。
	after.AudioFade = f
}
