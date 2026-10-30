package playback

import (
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"aividcut/internal/editor"
	"aividcut/internal/media"
)

const nativeVisualService = "aividcut.visual"

// qtext已经按原工程坐标画好文字；这里只作原尺寸RGBA合成，不能再次应用Transform。
func compilePreparedVisualTransition(settings editor.Settings, capabilities Capabilities, trackIndex int, durationFrames int64) (string, error) {
	if !contains(capabilities.Transitions, nativeVisualService) {
		return "", errors.New("worker缺少原尺寸RGBA合成服务")
	}
	return `<transition in="0" out="` + strconv.FormatInt(durationFrames-1, 10) + `">` +
		prop("mlt_service", nativeVisualService) + prop("a_track", "0") + prop("b_track", strconv.Itoa(trackIndex)) +
		prop("source_kind", "prepared") + prop("source_width", strconv.Itoa(settings.Width)) + prop("source_height", strconv.Itoa(settings.Height)) +
		prop("canvas_width", strconv.Itoa(settings.Width)) + prop("canvas_height", strconv.Itoa(settings.Height)) +
		prop("scale_width", strconv.Itoa(settings.Width)) + prop("scale_height", strconv.Itoa(settings.Height)) +
		prop("rotation", "0") + prop("offset_x", "0") + prop("offset_y", "0") + prop("lut_file", "") + prop("lut_intensity", "0") + `</transition>`, nil
}

// compileVisualTransition与FFmpeg导出共用先fit/scale、再中心旋转、LUT、最后平移的顺序。
func compileVisualTransition(settings editor.Settings, clip editor.Clip, asset editor.Asset, assets map[string]editor.Asset, capabilities Capabilities, trackIndex int, durationFrames int64) (string, error) {
	if clip.Kind != "video" && clip.Kind != "image" {
		return "", errors.New("原生视觉transition只接受视频或图片")
	}
	if !contains(capabilities.Transitions, nativeVisualService) {
		return "", errors.New("worker缺少受控原生视觉transition")
	}
	values := []float64{clip.Transform.Scale, clip.Transform.X, clip.Transform.Y, clip.Transform.Rotation}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", errors.New("原生视觉变换需要有限数值")
		}
	}
	scale := clip.Transform.Scale
	if scale <= 0 || math.Abs(clip.Transform.Rotation) > 360000 {
		return "", errors.New("原生视觉缩放或旋转超出当前范围")
	}
	width, height := math.Round(float64(settings.Width)*scale/2)*2, math.Round(float64(settings.Height)*scale/2)*2
	if width > 16384 || height > 16384 || math.IsInf(width, 0) || math.IsInf(height, 0) {
		return "", errors.New("原生中间缩放尺寸超过16384，未分配超大画布")
	}
	w, h := max(2, int(width)), max(2, int(height))
	if int64(w)*int64(h) > 40*1024*1024 {
		return "", errors.New("原生中间缩放画布超过40Mi像素，请减小缩放；未分配该画布")
	}
	if asset.Width < 1 || asset.Height < 1 || int64(asset.Width)*int64(asset.Height) > 64*1024*1024 {
		return "", errors.New("原生素材实际尺寸需要有效且不超过64Mi像素")
	}
	number := func(value float64) string { return strconv.FormatFloat(value, 'f', 9, 64) }
	lutFile := ""
	intensity := 0.0
	if clip.LUT != nil {
		intensity = clip.LUT.Intensity
		if math.IsNaN(intensity) || math.IsInf(intensity, 0) || intensity < 0 || intensity > 1 {
			return "", errors.New("LUT强度必须在0–1之间")
		}
		if intensity > 0 {
			if !capabilities.LUT.Supported {
				return "", errors.New("worker未提供原生LUT能力")
			}
			lut, ok := assets[clip.LUT.AssetID]
			if !ok || lut.Kind != "lut" || lut.Missing || !filepath.IsAbs(lut.Path) || strings.ContainsAny(lut.Path, "\x00\r\n") {
				return "", errors.New("LUT需要当前工程已登记的本地文件")
			}
			if _, err := media.ValidateCube(lut.Path); err != nil {
				return "", err
			}
			lutFile = lut.Path
		}
	}
	return `<transition in="0" out="` + strconv.FormatInt(durationFrames-1, 10) + `">` +
		prop("mlt_service", nativeVisualService) + prop("a_track", "0") + prop("b_track", strconv.Itoa(trackIndex)) +
		prop("source_width", strconv.Itoa(asset.Width)) + prop("source_height", strconv.Itoa(asset.Height)) +
		prop("source_kind", clip.Kind) +
		prop("canvas_width", strconv.Itoa(settings.Width)) + prop("canvas_height", strconv.Itoa(settings.Height)) +
		prop("scale_width", strconv.Itoa(w)) + prop("scale_height", strconv.Itoa(h)) +
		prop("rotation", number(clip.Transform.Rotation)) + prop("offset_x", number(clip.Transform.X)) + prop("offset_y", number(clip.Transform.Y)) +
		prop("lut_file", lutFile) + prop("lut_intensity", number(intensity)) + `</transition>`, nil
}
