package media

import (
	"errors"

	"aividcut/internal/editor"
)

type renderVideoSampling struct {
	root                   editor.Clip
	windowStart, windowEnd int64
	sourceDuration         float64
}

// 派生段先重建原recipe的fps处理，再裁输出帧窗；选区不成为新的input seek锚。
func resolveRenderVideoSampling(original editor.Clip, asset editor.Asset, settings editor.Settings, left, right int64) (renderVideoSampling, error) {
	resolved, err := editor.ResolveSourcePhase(original, asset, settings)
	if err != nil {
		return renderVideoSampling{}, err
	}
	start := resolved.WindowStartFrame + left - original.StartFrame
	end := resolved.WindowStartFrame + right - original.StartFrame
	if left < original.StartFrame || right > original.StartFrame+original.DurationFrames || start < resolved.WindowStartFrame || end > resolved.WindowEndFrame || start >= end {
		return renderVideoSampling{}, errors.New("视频导出选区超出原处理输出窗")
	}
	duration := framesSeconds(resolved.Root.DurationFrames, settings) * float64(resolved.Root.SpeedNum) / float64(resolved.Root.SpeedDen)
	if !finite(duration) || duration <= 0 {
		return renderVideoSampling{}, errors.New("视频原处理输入长度无效")
	}
	return renderVideoSampling{root: resolved.Root, windowStart: start, windowEnd: end, sourceDuration: duration}, nil
}
