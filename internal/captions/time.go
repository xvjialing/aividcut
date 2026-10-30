package captions

import (
	"aividcut/internal/editor"
	"math"
	"math/big"
)

func validRate(settings editor.Settings) bool {
	return settings.FPSNum > 0 && settings.FPSDen > 0 && settings.FPSNum <= 1_000_000 && settings.FPSDen <= 1_000_000 && int64(settings.FPSNum) <= int64(settings.FPSDen)*240
}

// 两端均采用四舍五入，恰好半帧向后取整；相邻字幕的共享边界保持相同。
// 不把不足一帧的字幕偷偷延长，区间坍缩时由调用方明确修正或合并。
func CueToFrames(cue Cue, settings editor.Settings) (startFrame, durationFrames int64, err error) {
	if err := validateCue(Cue{StartMS: cue.StartMS, EndMS: cue.EndMS, Text: normalizeNewlines(cue.Text)}); err != nil {
		return 0, 0, err
	}
	if !validRate(settings) {
		return 0, 0, fail("frame_rate", "工程帧率无效")
	}
	denominator := new(big.Int).Mul(big.NewInt(1000), big.NewInt(int64(settings.FPSDen)))
	start, err := roundedRatio(new(big.Int).Mul(big.NewInt(cue.StartMS), big.NewInt(int64(settings.FPSNum))), denominator)
	if err != nil {
		return 0, 0, err
	}
	end, err := roundedRatio(new(big.Int).Mul(big.NewInt(cue.EndMS), big.NewInt(int64(settings.FPSNum))), denominator)
	if err != nil {
		return 0, 0, err
	}
	if end <= start {
		return 0, 0, fail("precision_loss", "字幕短于当前工程可表示的帧区间，请延长或合并字幕")
	}
	return start, end - start, nil
}

func FramesToCue(startFrame, durationFrames int64, text string, settings editor.Settings) (Cue, error) {
	if !validRate(settings) || startFrame < 0 || durationFrames <= 0 || startFrame > math.MaxInt64-durationFrames {
		return Cue{}, fail("frame_range", "字幕帧率、位置或长度无效")
	}
	denominator := big.NewInt(int64(settings.FPSNum))
	multiplier := new(big.Int).Mul(big.NewInt(1000), big.NewInt(int64(settings.FPSDen)))
	start, err := roundedRatio(new(big.Int).Mul(big.NewInt(startFrame), multiplier), denominator)
	if err != nil {
		return Cue{}, err
	}
	end, err := roundedRatio(new(big.Int).Mul(big.NewInt(startFrame+durationFrames), multiplier), denominator)
	if err != nil {
		return Cue{}, err
	}
	cue := Cue{StartMS: start, EndMS: end, Text: normalizeNewlines(text)}
	if err := validateCue(cue); err != nil {
		return Cue{}, err
	}
	return cue, nil
}

func roundedRatio(numerator, denominator *big.Int) (int64, error) {
	if numerator.Sign() < 0 || denominator.Sign() <= 0 {
		return 0, fail("time_range", "字幕时间不能为负数，时间基准必须为正数")
	}
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Lsh(remainder, 1).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, fail("time_overflow", "字幕时间换算超出可表示范围")
	}
	return quotient.Int64(), nil
}
