package media

import (
	"aividcut/internal/managedprocess"
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"aividcut/internal/editor"
	"aividcut/internal/projectfonts"
)

type renderPlan struct {
	Args          []string
	Filter        string
	Frames        int64
	AudioSamples  int64
	Duration      float64
	Width, Height int
	HasText       bool
}

// ProjectDurationFrames 包括时间线上的空档和隐藏轨道范围。
func ProjectDurationFrames(project editor.Project) int64 {
	var end int64
	for _, clip := range project.Clips {
		if value := clip.StartFrame + clip.DurationFrames; value > end {
			end = value
		}
	}
	return end
}

// Export 在独立 FFmpeg 进程中渲染工程快照，成功后才提交到用户目标路径。
func (t Toolchain) Export(ctx context.Context, project editor.Project, options ExportOptions, onProgress func(Progress)) error {
	if options.Renderer != "" && options.Renderer != "auto" && options.Renderer != "ffmpeg" {
		return errors.New("原生合成需通过统一应用导出接口调用")
	}
	if t.FFmpegPath == "" {
		return errors.New("未配置 ffmpeg，无法导出")
	}
	if strings.ToLower(filepath.Ext(options.Path)) != ".mp4" {
		return errors.New("首轮视频导出仅支持 .mp4")
	}
	return t.render(ctx, project, options, false, onProgress)
}

// PreviewFrame 与导出共用合成规划，仅用于低频检查，不能视为实时播放引擎。
func (t Toolchain) PreviewFrame(ctx context.Context, project editor.Project, frame int64, path string) error {
	if t.FFmpegPath == "" {
		return errors.New("未配置 ffmpeg，无法生成检查帧")
	}
	if strings.ToLower(filepath.Ext(path)) != ".png" {
		return errors.New("预览帧输出需要使用 .png 文件")
	}
	if frame < 0 || frame >= ProjectDurationFrames(project) {
		return errors.New("预览帧不在工程有效区间")
	}
	return t.render(ctx, project, ExportOptions{Path: path, Preset: "project", FromFrame: frame, ToFrame: frame + 1, Overwrite: true}, true, nil)
}

func (t Toolchain) render(ctx context.Context, project editor.Project, options ExportOptions, still bool, onProgress func(Progress)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resourceArgs, err := EncodingResourceArgs(options.VideoEncoder, options.ResourceMode)
	if err != nil {
		return err
	}
	output, err := filepath.Abs(options.Path)
	if err != nil || options.Path == "" {
		return errors.New("需要有效的本地输出路径")
	}
	for _, asset := range project.Assets {
		if input, _ := filepath.Abs(asset.Path); input == output {
			return errors.New("不能覆盖工程源素材")
		}
	}
	if _, err := os.Stat(output); err == nil && !options.Overwrite {
		return errors.New("输出文件已存在，请选择新文件名或明确允许覆盖")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "aividcut-render-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	// 首帧定位使用与真实渲染相同的源裁剪参数，不猜测素材内部音轨起点。
	cache := map[renderAudioKey]renderAudioTiming{}
	resolveAudio := func(path string, sourceInUs int64, duration float64) (renderAudioTiming, error) {
		key := renderAudioKey{path: path, sourceInUs: sourceInUs, duration: number(duration)}
		if value, found := cache[key]; found {
			return value, nil
		}
		value, err := t.renderAudioTiming(ctx, path, sourceInUs, duration)
		if err == nil {
			cache[key] = value
		}
		return value, err
	}
	plan, err := buildPlan(project, options, work, still, resolveAudio)
	if err != nil {
		return err
	}
	if plan.HasText {
		filters, filterErr := managedprocess.CommandContext(ctx, t.FFmpegPath, "-hide_banner", "-filters").Output()
		if filterErr != nil || !parseFilterNames(filters)["drawtext"] {
			return errors.New("当前 ffmpeg 构建不含 drawtext，不能导出文字/字幕；请配置具备文字渲染能力的工具")
		}
	}
	if err := os.WriteFile(filepath.Join(work, "filter.graph"), []byte(plan.Filter), 0600); err != nil {
		return err
	}
	ext := filepath.Ext(output)
	if still {
		ext = ".png"
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".aividcut-output-*"+ext)
	if err != nil {
		return err
	}
	tempPath := temporary.Name()
	temporary.Close()
	defer os.Remove(tempPath)
	args := append([]string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-filter_complex_threads", "1"}, plan.Args...)
	graphOption, err := t.renderGraphOption(ctx)
	if err != nil {
		return err
	}
	args = append(args, graphOption, "filter.graph", "-map", "[video_out]")
	if still {
		args = append(args, "-frames:v", "1", "-an", "-c:v", "png", "-f", "image2")
	} else {
		encoder := options.VideoEncoder
		if encoder == "" {
			encoder = "libx264"
		}
		if encoder != "libx264" && encoder != "h264_videotoolbox" && encoder != "mpeg4" {
			return errors.New("未支持的导出编码器")
		}
		args = append(args, "-map", "[audio_out]", "-c:v", encoder)
		if encoder == "libx264" {
			args = append(args, "-preset", "veryfast", "-crf", "20")
		} else if encoder == "h264_videotoolbox" {
			args = append(args, "-b:v", "8M")
		} else {
			args = append(args, "-q:v", "3")
		}
		args = append(args, resourceArgs...)
		// 视频按帧结束；音频图已按全局采样时钟裁到准确样本数，不能再用小数-t截末样本。
		args = append(args, "-pix_fmt", "yuv420p", "-frames:v", strconv.FormatInt(plan.Frames, 10), "-c:a", "aac", "-b:a", "192k", "-ar", strconv.Itoa(project.Settings.SampleRate), "-ac", "2", "-movflags", "+faststart")
	}
	args = append(args, "-progress", "pipe:1", "-nostats", tempPath)
	if onProgress != nil {
		onProgress(Progress{Stage: "encoding"})
	}
	command := managedprocess.CommandContext(ctx, t.FFmpegPath, args...)
	command.Dir = work
	var stderr tailBuffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动媒体渲染失败: %w", err)
	}
	scanner := bufio.NewScanner(stdout)
	var currentFrame int64
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		if key == "frame" {
			currentFrame, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		}
		if key == "out_time_us" && onProgress != nil {
			us, _ := strconv.ParseFloat(value, 64)
			fraction := math.Max(0, math.Min(0.999, us/(plan.Duration*1e6)))
			onProgress(Progress{Stage: "encoding", Fraction: fraction, Frame: currentFrame})
		}
	}
	if err := scanner.Err(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("读取渲染进度失败: %w", err)
	}
	if err := command.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("媒体渲染失败: %s", strings.TrimSpace(stderr.String()))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if onProgress != nil {
		onProgress(Progress{Stage: "committing", Fraction: 0.999, Frame: plan.Frames})
	}
	if err := commitOutput(ctx, tempPath, output, options.Overwrite, defaultCommitOperations()); err != nil {
		return fmt.Errorf("提交输出失败: %w", err)
	}
	if onProgress != nil {
		onProgress(Progress{Stage: "completed", Fraction: 1, Frame: plan.Frames})
	}
	return nil
}

// FFmpeg 9移除了旧script选项；使用文件参数形式，避免大型时间线超出命令行长度。
func (t Toolchain) renderGraphOption(ctx context.Context) (string, error) {
	data, err := managedprocess.CommandContext(ctx, t.FFmpegPath, "-version").Output()
	if err != nil {
		return "", fmt.Errorf("读取渲染工具版本失败: %w", err)
	}
	return renderGraphOptionForVersion(string(data)), nil
}
func renderGraphOptionForVersion(version string) string {
	first := strings.TrimPrefix(firstLine([]byte(version)), "ffmpeg version ")
	major, _ := strconv.Atoi(strings.SplitN(first, ".", 2)[0])
	if major >= 9 {
		return "-/filter_complex"
	}
	return "-filter_complex_script"
}

func buildPlan(project editor.Project, options ExportOptions, work string, still bool, resolveAudio func(string, int64, float64) (renderAudioTiming, error)) (renderPlan, error) {
	fonts, fontErr := projectfonts.Resolve(project)
	if fontErr != nil {
		return renderPlan{}, fontErr
	}
	var plan renderPlan
	settings := project.Settings
	if settings.Width < 2 || settings.Height < 2 || settings.Width > 8192 || settings.Height > 8192 || settings.FPSNum <= 0 || settings.FPSDen <= 0 || float64(settings.FPSNum)/float64(settings.FPSDen) > 240 || settings.SampleRate < 8000 || settings.SampleRate > 192000 {
		return plan, errors.New("工程画幅、帧率或采样率无效")
	}
	end := ProjectDurationFrames(project)
	from, to := options.FromFrame, options.ToFrame
	if to == 0 {
		to = end
	}
	if from < 0 || from >= to || to > end {
		return plan, errors.New("导出区间不在时间线范围内")
	}
	plan.Frames = to - from
	plan.Duration = framesSeconds(plan.Frames, settings)
	if !finite(plan.Duration) || plan.Duration > 24*3600 {
		return plan, errors.New("首轮单次渲染时长不能超过 24 小时")
	}
	plan.Width, plan.Height = outputSize(settings.Width, settings.Height, options.Preset)
	if plan.Width == 0 {
		return plan, errors.New("未支持的输出预设")
	}
	rate := fmt.Sprintf("%d/%d", settings.FPSNum, settings.FPSDen)
	filters := []string{fmt.Sprintf("color=c=black:s=%dx%d:r=%s:d=%s,format=rgba[base]", settings.Width, settings.Height, rate, number(plan.Duration))}
	assets := map[string]editor.Asset{}
	for _, asset := range project.Assets {
		assets[asset.ID] = asset
	}
	tracks := map[string]editor.Track{}
	order := map[string]int{}
	for i, track := range project.Tracks {
		tracks[track.ID], order[track.ID] = track, i
	}
	clips := append([]editor.Clip(nil), project.Clips...)
	// 顶层轨道最后合成；同轨道内按原始时间顺序处理。
	sort.SliceStable(clips, func(i, j int) bool {
		if order[clips[i].TrackID] != order[clips[j].TrackID] {
			return order[clips[i].TrackID] > order[clips[j].TrackID]
		}
		return clips[i].StartFrame < clips[j].StartFrame
	})
	// 有淡化的声音统一进入精确输出窗链路，不能再走旧的帧近似声音分支。
	for i := range clips {
		if clips[i].AudioFade != nil {
			var err error
			clips[i], err = editor.CaptureSourcePhase(clips[i], assets[clips[i].AssetID], settings)
			if err != nil {
				return plan, err
			}
		}
	}
	inputIndex, visualIndex, audioIndex := 0, 0, 0
	totalSamples, err := renderSampleSpan(from, to, settings)
	if err != nil {
		return plan, err
	}
	plan.AudioSamples = totalSamples
	base := "base"
	audioLabels := []string{}
	if !still {
		exact, err := buildExactAudio(project, clips, assets, tracks, from, to, inputIndex, resolveAudio)
		if err != nil {
			return plan, err
		}
		plan.Args = append(plan.Args, exact.args...)
		filters = append(filters, exact.filters...)
		audioLabels = append(audioLabels, exact.labels...)
		inputIndex, audioIndex = exact.nextInput, len(exact.labels)
	}
	for _, original := range clips {
		clip := original
		track, found := tracks[clip.TrackID]
		if !found || clip.StartFrame < 0 || clip.DurationFrames <= 0 || clip.SpeedNum <= 0 || clip.SpeedDen <= 0 {
			return plan, errors.New("时间线包含无效轨道、区间或速度")
		}
		if speed := float64(clip.SpeedNum) / float64(clip.SpeedDen); speed < 0.125 || speed > 8 {
			return plan, errors.New("首轮渲染速度范围为 0.125–8 倍")
		}
		// 精确音频已按真实声音区间规划，不能再次按显示帧裁剪或重复混音。
		if clip.Kind == "audio" && clip.AudioRange != nil {
			continue
		}
		if clip.StartFrame >= to || clip.StartFrame+clip.DurationFrames <= from {
			continue
		}
		if still && clip.Kind == "audio" {
			continue
		}
		left := max(clip.StartFrame, from)
		right := min(clip.StartFrame+clip.DurationFrames, to)
		clip.SourceInUs += int64(math.Round(framesSeconds(left-clip.StartFrame, settings) * float64(clip.SpeedNum) / float64(clip.SpeedDen) * 1e6))
		clip.StartFrame, clip.DurationFrames = left-from, right-left
		start, duration := framesSeconds(clip.StartFrame, settings), framesSeconds(clip.DurationFrames, settings)
		if clip.Kind == "text" || clip.Kind == "caption" {
			if track.Hidden {
				continue
			}
			fontOptions := options
			expectedFont := ""
			if clip.TextStyle.FontAssetID != "" {
				if options.FontPath != "" {
					return plan, errors.New("工程字体不能被单次FontPath覆盖")
				}
				font := fonts[clip.TextStyle.FontAssetID]
				fontOptions.FontPath = font.Path
				expectedFont = font.SHA256
			}
			textFilter, err := textOverlay(clip, fontOptions, work, visualIndex, start, duration, expectedFont)
			if err != nil {
				return plan, err
			}
			next := fmt.Sprintf("visual_%d", visualIndex)
			filters = append(filters, fmt.Sprintf("[%s]%s[%s]", base, textFilter, next))
			base, plan.HasText = next, true
			visualIndex++
			continue
		}
		asset, found := assets[clip.AssetID]
		if !found || asset.Missing || clip.SourceInUs < 0 {
			return plan, errors.New("片段引用的素材不可用")
		}
		if clip.Kind != "image" && clip.Kind != "video" && clip.Kind != "audio" {
			return plan, fmt.Errorf("首轮尚不支持 %s 类型片段", clip.Kind)
		}
		if clip.Kind == "video" && asset.Kind != "video" {
			return plan, errors.New("视频片段必须引用视频素材")
		}
		if clip.Kind == "audio" && (!asset.HasAudio || clip.LUT != nil) {
			return plan, errors.New("音频片段需要音频流，且不能应用视觉 LUT")
		}
		local, err := filepath.Abs(asset.Path)
		if err != nil {
			return plan, err
		}
		if stat, err := os.Stat(local); err != nil || !stat.Mode().IsRegular() {
			return plan, fmt.Errorf("素材不可读取: %s", asset.Name)
		}
		sourceDuration := duration * float64(clip.SpeedNum) / float64(clip.SpeedDen)
		if asset.Kind != "image" && asset.DurationUs > 0 && float64(clip.SourceInUs)+sourceDuration*1e6 > float64(asset.DurationUs)+1000 {
			return plan, errors.New("片段超出源素材范围")
		}
		input, audioInput := inputIndex, -1
		var sampling renderVideoSampling
		if clip.Kind == "video" {
			sampling, err = resolveRenderVideoSampling(original, asset, settings, left, right)
			if err != nil {
				return plan, err
			}
			plan.Args = append(plan.Args, "-ss", number(float64(sampling.root.SourceInUs)/1e6), "-t", number(sampling.sourceDuration), "-i", local)
			inputIndex++
			// 旧nil音频仍用自己的选区输入；不能拿video原recipe输入冒充其派生源区间。
			if !still && clip.SourcePhase == nil && clip.AudioRange == nil && asset.HasAudio && !clip.Muted && !track.Muted && clip.Volume > 0 {
				audioInput = inputIndex
				inputIndex++
				plan.Args = append(plan.Args, "-ss", number(float64(clip.SourceInUs)/1e6), "-t", number(sourceDuration), "-i", local)
			}
		} else if clip.Kind == "image" {
			if asset.Kind != "image" {
				return plan, errors.New("图片片段必须引用图片素材")
			}
			plan.Args = append(plan.Args, "-loop", "1", "-framerate", rate, "-i", local)
			inputIndex++
		} else {
			plan.Args = append(plan.Args, "-ss", number(float64(clip.SourceInUs)/1e6), "-t", number(sourceDuration), "-i", local)
			audioInput = inputIndex
			inputIndex++
		}
		if clip.Kind != "audio" && !track.Hidden {
			if clip.Transform.Scale <= 0 || !finite(clip.Transform.X, clip.Transform.Y, clip.Transform.Scale, clip.Transform.Rotation) {
				return plan, errors.New("片段变换参数无效")
			}
			w := max(2, int(math.Round(float64(settings.Width)*clip.Transform.Scale/2))*2)
			h := max(2, int(math.Round(float64(settings.Height)*clip.Transform.Scale/2))*2)
			if w > 16384 || h > 16384 {
				return plan, errors.New("片段缩放超出当前渲染上限")
			}
			label := fmt.Sprintf("clip_%d", visualIndex)
			var chain string
			if clip.Kind == "video" {
				chain = fmt.Sprintf("[%d:v]trim=duration=%s,setpts=(PTS-STARTPTS)*%d/%d,fps=%s,trim=start_frame=%d:end_frame=%d,setpts=PTS-STARTPTS,scale=%d:%d:force_original_aspect_ratio=decrease,setsar=1,format=rgba", input, number(sampling.sourceDuration), sampling.root.SpeedDen, sampling.root.SpeedNum, rate, sampling.windowStart, sampling.windowEnd, w, h)
			} else {
				chain = fmt.Sprintf("[%d:v]trim=duration=%s,setpts=(PTS-STARTPTS)*%d/%d,fps=%s,scale=%d:%d:force_original_aspect_ratio=decrease,setsar=1,format=rgba", input, number(sourceDuration), clip.SpeedDen, clip.SpeedNum, rate, w, h)
			}
			if clip.Transform.Rotation != 0 {
				chain += fmt.Sprintf(",rotate=%s*PI/180:ow=rotw(%s*PI/180):oh=roth(%s*PI/180):c=none", number(clip.Transform.Rotation), number(clip.Transform.Rotation), number(clip.Transform.Rotation))
			}
			filters = append(filters, chain+"["+label+"]")
			if clip.LUT != nil {
				var fragment string
				label, fragment, err = addLUT(label, clip, assets, work, visualIndex)
				if err != nil {
					return plan, err
				}
				if fragment != "" {
					filters = append(filters, fragment)
				}
			}
			shifted := fmt.Sprintf("shifted_%d", visualIndex)
			// fps后的TB显式落在工程帧，不经小数秒再截断到整数PTS。
			filters = append(filters, fmt.Sprintf("[%s]settb=expr=%d/%d,setpts=PTS-STARTPTS+%d[%s]", label, settings.FPSDen, settings.FPSNum, clip.StartFrame, shifted))
			next := fmt.Sprintf("visual_%d", visualIndex)
			filters = append(filters, fmt.Sprintf("[%s][%s]overlay=x=(W-w)/2+%s:y=(H-h)/2+%s:eof_action=pass:shortest=0:enable='gte(n,%d)*lt(n,%d)'[%s]", base, shifted, number(clip.Transform.X), number(clip.Transform.Y), clip.StartFrame, clip.StartFrame+clip.DurationFrames, next))
			base = next
			visualIndex++
		}
		if !finite(clip.Volume) || clip.Volume < 0 || clip.Volume > 16 {
			return plan, errors.New("音量超出可渲染范围")
		}
		if !still && clip.SourcePhase == nil && clip.AudioRange == nil && asset.HasAudio && clip.Kind != "image" && !clip.Muted && !track.Muted && clip.Volume > 0 {
			timing, err := resolveAudio(local, clip.SourceInUs, sourceDuration)
			if err != nil {
				return plan, err
			}
			if !timing.hasSamples {
				continue
			}
			label := fmt.Sprintf("audio_%d", audioIndex)
			chain := fmt.Sprintf("[%d:a]atrim=start=0:end=%s,asetpts=N/SR/TB", audioInput, number(sourceDuration))
			chain += tempoChain(float64(clip.SpeedNum) / float64(clip.SpeedDen))
			lead := timing.leadSeconds * float64(clip.SpeedDen) / float64(clip.SpeedNum)
			delaySamples := int64(math.Round((start + lead) * float64(settings.SampleRate)))
			chain += fmt.Sprintf(",volume=%s,aresample=%d,adelay=%dS:all=1,asetpts=N/SR/TB,apad=whole_len=%d,atrim=end_sample=%d,asetpts=N/SR/TB[%s]", number(clip.Volume), settings.SampleRate, delaySamples, totalSamples, totalSamples, label)
			filters = append(filters, chain)
			audioLabels = append(audioLabels, "["+label+"]")
			audioIndex++
		}
	}
	filters = append(filters, fmt.Sprintf("[%s]trim=duration=%s,setpts=PTS-STARTPTS,scale=%d:%d,setsar=1,format=yuv420p[video_out]", base, number(plan.Duration), plan.Width, plan.Height))
	if still {
		plan.Filter = strings.Join(filters, ";\n")
		return plan, nil
	}
	if len(audioLabels) == 0 {
		filters = append(filters, fmt.Sprintf("anullsrc=r=%d:cl=stereo,atrim=end_sample=%d[audio_out]", settings.SampleRate, totalSamples))
	} else {
		filters = append(filters, fmt.Sprintf("%samix=inputs=%d:duration=longest:normalize=0,atrim=end_sample=%d,asetpts=N/SR/TB[audio_out]", strings.Join(audioLabels, ""), len(audioLabels), totalSamples))
	}
	plan.Filter = strings.Join(filters, ";\n")
	return plan, nil
}

func addLUT(label string, clip editor.Clip, assets map[string]editor.Asset, work string, index int) (string, string, error) {
	lut := clip.LUT
	if !finite(lut.Intensity) || lut.Intensity < 0 || lut.Intensity > 1 {
		return "", "", errors.New("LUT 强度必须在 0–1 之间")
	}
	if lut.Intensity == 0 {
		return label, "", nil
	}
	asset, found := assets[lut.AssetID]
	if !found || asset.Kind != "lut" {
		return "", "", errors.New("LUT 资源不可用")
	}
	if _, err := ValidateCube(asset.Path); err != nil {
		return "", "", err
	}
	filename := fmt.Sprintf("lut_%d.cube", index)
	if err := copyFile(asset.Path, filepath.Join(work, filename)); err != nil {
		return "", "", err
	}
	next := fmt.Sprintf("graded_%d", index)
	fragment := fmt.Sprintf("[%s]lut3d=file=%s:interp=tetrahedral[%s]", label, filename, next)
	if lut.Intensity < 1 {
		fragment = fmt.Sprintf("[%s]split[orig_%d][lut_%d];[lut_%d]lut3d=file=%s:interp=tetrahedral[full_%d];[orig_%d][full_%d]blend=all_mode=normal:all_opacity=%s[%s]", label, index, index, index, filename, index, index, index, number(1-lut.Intensity), next)
	}
	return next, fragment, nil
}

var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$`)

func textOverlay(clip editor.Clip, options ExportOptions, work string, index int, start, duration float64, expectedFont ...string) (string, error) {
	if clip.Transform.Rotation != 0 || clip.LUT != nil {
		return "", errors.New("首轮文字渲染暂不支持旋转或 LUT，请先移除这些处理")
	}
	if len(clip.Text) > 1<<20 || strings.ContainsRune(clip.Text, 0) {
		return "", errors.New("文字内容过大或包含无效字符")
	}
	font := options.FontPath
	if font == "" {
		font = DefaultFont()
	}
	if font == "" {
		return "", errors.New("未找到文字渲染字体，请配置 AIVIDCUT_FONT")
	}
	fontKey := sha256.Sum256([]byte(font))
	fontFilename := fmt.Sprintf("font_%x%s", fontKey[:12], filepath.Ext(font))
	if stat, err := os.Stat(font); err != nil || !stat.Mode().IsRegular() || stat.Size() > 128<<20 {
		return "", errors.New("文字字体必须为可读取的普通文件，且不超过 128 MiB")
	}
	if _, err := os.Stat(filepath.Join(work, fontFilename)); os.IsNotExist(err) {
		if err := copyFile(font, filepath.Join(work, fontFilename)); err != nil {
			return "", fmt.Errorf("读取文字字体失败: %w", err)
		}
	}
	if len(expectedFont) > 0 && expectedFont[0] != "" {
		file, err := projectfonts.Read(filepath.Join(work, fontFilename))
		if err != nil || file.SHA256 != expectedFont[0] {
			return "", errors.New("工程字体复制时已变化，未继续渲染")
		}
	}
	filename := fmt.Sprintf("text_%d.txt", index)
	if err := os.WriteFile(filepath.Join(work, filename), []byte(clip.Text), 0600); err != nil {
		return "", err
	}
	fontSize := clip.TextStyle.FontSize
	if fontSize == 0 {
		fontSize = 48
	}
	scale := clip.Transform.Scale
	if scale == 0 {
		scale = 1
	}
	if fontSize < 8 || fontSize > 1000 || scale <= 0 || !finite(scale, clip.Transform.X, clip.Transform.Y) {
		return "", errors.New("文字尺寸或位置无效")
	}
	color := clip.TextStyle.Color
	if color == "" {
		color = "#ffffff"
	}
	if !colorPattern.MatchString(color) {
		return "", errors.New("文字颜色需要 #RRGGBB 或 #RRGGBBAA")
	}
	x := "(w-text_w)/2"
	if clip.TextStyle.Align == "left" {
		x = "32"
	} else if clip.TextStyle.Align == "right" {
		x = "w-text_w-32"
	} else if clip.TextStyle.Align != "" && clip.TextStyle.Align != "center" {
		return "", errors.New("未支持的文字对齐方式")
	}
	y := "(h-text_h)/2"
	if clip.Kind == "caption" {
		y = "h-text_h-48"
	}
	filter := fmt.Sprintf("drawtext=fontfile=%s:textfile=%s:expansion=none:fontsize=%s:fontcolor=%s:x=%s+%s:y=%s+%s:enable='gte(t,%s)*lt(t,%s)'", fontFilename, filename, number(float64(fontSize)*scale), ffColor(color), x, number(clip.Transform.X), y, number(clip.Transform.Y), number(start), number(start+duration))
	if background := clip.TextStyle.Background; background != "" && background != "transparent" {
		if !colorPattern.MatchString(background) {
			return "", errors.New("文字背景需要透明或十六进制颜色")
		}
		filter += ":box=1:boxborderw=8:boxcolor=" + ffColor(background)
	}
	return filter, nil
}

func ffColor(value string) string {
	if len(value) == 9 {
		alpha, _ := strconv.ParseUint(value[7:], 16, 8)
		return "0x" + value[1:7] + "@" + number(float64(alpha)/255)
	}
	return "0x" + value[1:]
}

func outputSize(width, height int, preset string) (int, int) {
	longEdge := 0
	switch preset {
	case "", "project":
		return (width + 1) / 2 * 2, (height + 1) / 2 * 2
	case "720p":
		longEdge = 1280
	case "1080p":
		longEdge = 1920
	case "4k":
		longEdge = 3840
	default:
		return 0, 0
	}
	factor := float64(longEdge) / float64(max(width, height))
	return max(2, int(math.Round(float64(width)*factor/2))*2), max(2, int(math.Round(float64(height)*factor/2))*2)
}

func framesSeconds(frames int64, settings editor.Settings) float64 {
	return float64(frames) * float64(settings.FPSDen) / float64(settings.FPSNum)
}

func number(value float64) string {
	return strconv.FormatFloat(value, 'f', 9, 64)
}

func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

func tempoChain(speed float64) string {
	var filters []string
	for speed > 2 {
		filters = append(filters, "atempo=2")
		speed /= 2
	}
	for speed < 0.5 {
		filters = append(filters, "atempo=0.5")
		speed *= 2
	}
	filters = append(filters, "atempo="+number(speed))
	return "," + strings.Join(filters, ",")
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}

// tailBuffer 仅保留工具错误末尾，避免长输出无限增长。
type tailBuffer struct{ data []byte }

func (b *tailBuffer) Write(p []byte) (int, error) {
	const limit = 32 << 10
	b.data = append(b.data, p...)
	if len(b.data) > limit {
		b.data = append([]byte(nil), b.data[len(b.data)-limit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string { return string(b.data) }
