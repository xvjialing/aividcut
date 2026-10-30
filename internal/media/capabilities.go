package media

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Feature struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

type CapabilityReport struct {
	FFmpeg         Feature            `json:"ffmpeg"`
	FFprobe        Feature            `json:"ffprobe"`
	DrawText       Feature            `json:"drawText"`
	LUT3D          Feature            `json:"lut3d"`
	PreviewFrame   Feature            `json:"previewFrame"`
	ExportVideo    Feature            `json:"exportVideo"`
	NativePlayback Feature            `json:"nativePlayback"`
	Encoders       map[string]Feature `json:"encoders"`
	Filters        map[string]bool    `json:"filters"`
	FFmpegVersion  string             `json:"ffmpegVersion"`
	FFprobeVersion string             `json:"ffprobeVersion"`
}

type toolOutput func(context.Context, string, ...string) ([]byte, error)

// CheckCapabilities 真实查询工具声明的能力；不能把编码器列表当作硬件实测。
// 建议在工具设置改变时调用并缓存，不要在每次编辑或播放帧中重复启动工具。
func (t Toolchain) CheckCapabilities(ctx context.Context) CapabilityReport {
	return t.checkCapabilities(ctx, runOutput)
}

func (t Toolchain) checkCapabilities(parent context.Context, run toolOutput) CapabilityReport {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	report := CapabilityReport{
		FFmpeg:         Feature{Reason: "未配置 ffmpeg"},
		FFprobe:        Feature{Reason: "未配置 ffprobe；音视频元数据探测不可用，PNG/JPEG 导入仍可用"},
		NativePlayback: Feature{Reason: "离线 FFmpeg 和低频检查帧不构成原生多轨实时播放"},
		Filters:        make(map[string]bool),
		Encoders:       make(map[string]Feature),
	}
	if t.FFprobePath != "" {
		data, err := run(ctx, t.FFprobePath, "-version")
		version := firstLine(data)
		if err == nil && strings.HasPrefix(version, "ffprobe version ") {
			report.FFprobe, report.FFprobeVersion = Feature{Available: true, Reason: "已识别 ffprobe"}, version
		} else {
			report.FFprobe.Reason = probeFailure("ffprobe", err)
		}
	}
	if t.FFmpegPath != "" {
		data, err := run(ctx, t.FFmpegPath, "-version")
		version := firstLine(data)
		if err == nil && strings.HasPrefix(version, "ffmpeg version ") {
			report.FFmpeg, report.FFmpegVersion = Feature{Available: true, Reason: "已识别 ffmpeg"}, version
		} else {
			report.FFmpeg.Reason = probeFailure("ffmpeg", err)
		}
	}
	if report.FFmpeg.Available {
		data, err := run(ctx, t.FFmpegPath, "-hide_banner", "-filters")
		if err == nil {
			report.Filters = parseFilterNames(data)
		} else {
			report.FFmpeg.Reason = "ffmpeg 可启动，但无法读取滤镜能力：" + err.Error()
		}
		data, err = run(ctx, t.FFmpegPath, "-hide_banner", "-encoders")
		if err == nil {
			available := parseEncoderNames(data)
			for _, name := range []string{"libx264", "h264_videotoolbox", "mpeg4", "aac", "png"} {
				feature := Feature{Available: available[name], Reason: "当前构建未声明该编码器"}
				if feature.Available {
					feature.Reason = "工具已声明编码器；实际输入与运行环境仍需编码验证"
				}
				report.Encoders[name] = feature
			}
		}
	}
	for _, name := range []string{"libx264", "h264_videotoolbox", "mpeg4", "aac", "png"} {
		if _, found := report.Encoders[name]; !found {
			report.Encoders[name] = Feature{Reason: "未能读取编码器能力"}
		}
	}
	report.DrawText = requireFilters(report.Filters, "文字/字幕渲染", "drawtext")
	report.LUT3D = requireFilters(report.Filters, "3D LUT 与强度混合", "lut3d", "split", "blend")
	visual := []string{"color", "format", "trim", "setpts", "fps", "scale", "setsar", "overlay"}
	report.PreviewFrame = requireFilters(report.Filters, "低频合成检查帧", visual...)
	if !report.Encoders["png"].Available {
		report.PreviewFrame = Feature{Reason: "缺少 PNG 编码器，低频检查帧不可用"}
	}
	all := append(append([]string(nil), visual...), "anullsrc", "atrim", "asetpts", "aresample", "adelay", "apad", "amix", "volume", "atempo")
	report.ExportVideo = requireFilters(report.Filters, "基础 MP4 导出", all...)
	if !report.Encoders["libx264"].Available || !report.Encoders["aac"].Available {
		report.ExportVideo = Feature{Reason: "默认 MP4 导出需要 libx264 和 AAC；其他编码器须显式选择并实际验证"}
	}
	return report
}

func requireFilters(filters map[string]bool, operation string, required ...string) Feature {
	var missing []string
	for _, name := range required {
		if !filters[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return Feature{Reason: operation + "缺少滤镜：" + strings.Join(missing, ", ")}
	}
	return Feature{Available: true, Reason: operation + "的基础滤镜已声明，具体工程仍需渲染验证"}
}

func parseFilterNames(data []byte) map[string]bool {
	names := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		// FFmpeg 9将滤镜能力标记从旧TSC三列改为TS两列；不能把整张真实列表丢成空。
		if len(parts) >= 3 && (len(parts[0]) == 2 || len(parts[0]) == 3) && strings.Contains(parts[2], "->") {
			names[parts[1]] = true
		}
	}
	return names
}

func parseEncoderNames(data []byte) map[string]bool {
	names := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 3 && len(parts[0]) == 6 && strings.ContainsAny(parts[0][:1], "VAS") && parts[1] != "=" {
			names[parts[1]] = true
		}
	}
	return names
}

func firstLine(data []byte) string {
	return strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
}

func probeFailure(tool string, err error) string {
	if err == nil {
		return tool + "输出无法识别为目标工具"
	}
	return fmt.Sprintf("%s 能力探测失败：%v", tool, err)
}
