package editor

import (
	"fmt"
	"math/big"
)

// 时间换算统一采用整数除法向零截断，避免 29.97fps 被误写为浮点数后不断累积误差。
func FramesToMicroseconds(frames int64, settings Settings) (int64, error) {
	return scaledTime(frames, []int64{1_000_000, int64(settings.FPSDen)}, []int64{int64(settings.FPSNum)})
}

func FramesToUS(frames int64, settings Settings) (int64, error) {
	return FramesToMicroseconds(frames, settings)
}

func MicrosecondsToFrames(us int64, settings Settings) (int64, error) {
	return scaledTime(us, []int64{int64(settings.FPSNum)}, []int64{1_000_000, int64(settings.FPSDen)})
}

func USToFrames(us int64, settings Settings) (int64, error) {
	return MicrosecondsToFrames(us, settings)
}

func SourceDurationUs(frames int64, settings Settings, speedNum, speedDen int) (int64, error) {
	return scaledTime(frames, []int64{1_000_000, int64(settings.FPSDen), int64(speedNum)}, []int64{int64(settings.FPSNum), int64(speedDen)})
}

func scaledTime(value int64, numerator, denominator []int64) (int64, error) {
	if value < 0 {
		return 0, problem("validation", "时间不能为负数")
	}
	n := big.NewInt(value)
	d := big.NewInt(1)
	for _, v := range numerator {
		if v <= 0 {
			return 0, problem("validation", "帧率和速度必须为正数")
		}
		n.Mul(n, big.NewInt(v))
	}
	for _, v := range denominator {
		if v <= 0 {
			return 0, problem("validation", "帧率和速度必须为正数")
		}
		d.Mul(d, big.NewInt(v))
	}
	n.Quo(n, d)
	if !n.IsInt64() {
		return 0, problem("validation", "时间换算超出可表示范围")
	}
	return n.Int64(), nil
}

// SourceRangeFits 按有理数比较区间，不能先把不足一微秒的消耗截断再放宽素材边界。
func SourceRangeFits(clip Clip, asset Asset, settings Settings) bool {
	if clip.Kind == "audio" && clip.AudioRange != nil {
		_, err := ResolveAudioRange(clip, asset, settings)
		return err == nil
	}
	if clip.SourceInUs < 0 || asset.DurationUs <= 0 || clip.SourceInUs > asset.DurationUs || clip.SpeedNum <= 0 || clip.SpeedDen <= 0 || settings.FPSNum <= 0 || settings.FPSDen <= 0 {
		return false
	}
	used := new(big.Int).Mul(big.NewInt(clip.DurationFrames), big.NewInt(1_000_000))
	used.Mul(used, big.NewInt(int64(settings.FPSDen)))
	used.Mul(used, big.NewInt(int64(clip.SpeedNum)))
	available := new(big.Int).Mul(big.NewInt(asset.DurationUs-clip.SourceInUs), big.NewInt(int64(settings.FPSNum)))
	available.Mul(available, big.NewInt(int64(clip.SpeedDen)))
	return used.Sign() > 0 && used.Cmp(available) <= 0
}

func DurationFrames(project Project) int64 {
	var last int64
	for _, clip := range project.Clips {
		if end := clip.StartFrame + clip.DurationFrames; end > last {
			last = end
		}
	}
	return last
}

func Timecode(frame int64, settings Settings) string {
	if frame < 0 || settings.FPSNum <= 0 || settings.FPSDen <= 0 {
		return "00:00:00:00"
	}
	// 此显示为非丢帧时间码；精确媒体时间仍通过上述有理数函数换算。
	nominal := (settings.FPSNum + settings.FPSDen/2) / settings.FPSDen
	if nominal < 1 {
		nominal = 1
	}
	seconds := frame / int64(nominal)
	return fmt.Sprintf("%02d:%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60, frame%int64(nominal))
}
