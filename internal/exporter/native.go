// Package exporter将原生合成帧流与编码/原子提交组合，交互预览不参与导出时钟。
package exporter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/playback"
)

func sampleAt(frame int64, s editor.Settings) int64 {
	return (frame*int64(s.SampleRate)*int64(s.FPSDen) + int64(s.FPSNum) - 1) / int64(s.FPSNum)
}

func Native(ctx context.Context, tools media.Toolchain, config playback.Config, project editor.Project, options media.ExportOptions, progress func(media.Progress)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := media.EncodingResourceArgs(options.VideoEncoder, options.ResourceMode); err != nil {
		return err
	}
	if tools.FFmpegPath == "" {
		return errors.New("原生导出仍需FFmpeg编码器")
	}
	if options.FontPath != "" {
		return errors.New("原生合成沿用预览字体，尚不支持单次FontPath覆盖；请明确选择FFmpeg合成")
	}
	if options.Path == "" || strings.ToLower(filepath.Ext(options.Path)) != ".mp4" {
		return errors.New("视频导出需要本地.mp4路径")
	}
	output, err := filepath.Abs(options.Path)
	if err != nil {
		return err
	}
	existing, statErr := os.Lstat(output)
	if statErr == nil {
		if !existing.Mode().IsRegular() {
			return errors.New("输出目标不能是目录、符号链接或特殊文件")
		}
		if !options.Overwrite {
			return errors.New("输出文件已存在，请选择新文件名或明确允许覆盖")
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	for _, a := range project.Assets {
		source, _ := filepath.Abs(a.Path)
		info, _ := os.Stat(a.Path)
		if source == output || existing != nil && info != nil && os.SameFile(existing, info) {
			return errors.New("不能覆盖工程源素材")
		}
	}
	from, to := options.FromFrame, options.ToFrame
	duration := media.ProjectDurationFrames(project)
	if to == 0 {
		to = duration
	}
	if from < 0 || from >= to || to > duration {
		return errors.New("导出区间不在时间线范围内")
	}
	width, height := media.OutputSize(project.Settings.Width, project.Settings.Height, options.Preset)
	if width < 2 || height < 2 || width > 8192 || height > 8192 {
		return errors.New("未支持的输出预设或画幅")
	}
	config.VisualSources = nil
	config.OfflineRender = true
	config.RenderQuality = "full"
	session, err := playback.Start(ctx, config)
	if err != nil {
		return err
	}
	defer session.Close()
	info, err := session.Load(ctx, project)
	if err != nil {
		return err
	}
	s := project.Settings
	if info.Width != s.Width || info.Height != s.Height || info.FPSNum != s.FPSNum || info.FPSDen != s.FPSDen || info.Channels != 2 {
		return errors.New("原生导出画幅或音画时钟与工程不一致")
	}
	if _, err = session.Preview(ctx, from); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	work, err := os.MkdirTemp(filepath.Dir(output), ".aividcut-native-render-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	videoPath, audioPath, tempOutput := filepath.Join(work, "video.mp4"), filepath.Join(work, "audio.m4a"), filepath.Join(work, "output.mp4")
	video, err := tools.StartRawVideo(ctx, s, width, height, to-from, options.VideoEncoder, videoPath, options.ResourceMode)
	if err != nil {
		return err
	}
	defer video.Abort()
	audio, err := tools.StartRawAudio(ctx, info.SampleRate, s.SampleRate, sampleAt(to, s)-sampleAt(from, s), audioPath)
	if err != nil {
		return err
	}
	defer audio.Abort()
	if err = session.Play(ctx); err != nil {
		return err
	}
	report := func(fraction float64, stage string, frame int64) {
		if progress != nil {
			progress(media.Progress{Fraction: fraction, Stage: stage, Frame: frame})
		}
	}
	report(0, "原生合成与编码", 0)
	lastReport := time.Now()
	clock := s
	clock.SampleRate = info.SampleRate
	for at := from; at < to; at++ {
		frame, err := session.Next(ctx)
		if err != nil {
			return err
		}
		start, end := sampleAt(at, clock), sampleAt(at+1, clock)
		if frame.Position != at || frame.Width != s.Width || frame.Height != s.Height || frame.PictureOnly || frame.AudioStartSample != start || int64(frame.Samples) != end-start || len(frame.PCM) != frame.Samples*8 || len(frame.RGBA) != s.Width*s.Height*4 || frame.End && at < to-1 {
			return fmt.Errorf("原生导出在第%d帧返回了不连续的画面或采样窗", at)
		}
		if err = video.Write(frame.RGBA); err != nil {
			return err
		}
		if err = audio.Write(frame.PCM); err != nil {
			return err
		}
		if time.Since(lastReport) >= 200*time.Millisecond || at == to-1 {
			report(.9*float64(at-from+1)/float64(to-from), "原生合成与编码", at-from+1)
			lastReport = time.Now()
		}
	}
	if err = video.Finish(); err != nil {
		return err
	}
	if err = audio.Finish(); err != nil {
		return err
	}
	report(.95, "封装音画", to-from)
	if err = tools.MuxEncoded(ctx, videoPath, audioPath, tempOutput, s.SampleRate); err != nil {
		return err
	}
	report(.999, "提交输出", to-from)
	if err = media.CommitOutput(ctx, tempOutput, output, options.Overwrite); err != nil {
		return err
	}
	report(1, "导出完成", to-from)
	return nil
}
