// 自建逐帧编号视频，只写新目录；用于实际编码/GOP/PTS与剪切定位验收。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/projectstore"
)

func main() {
	dir := flag.String("out", "", "必须尚不存在的输出目录")
	seconds := flag.Int("seconds", 90, "源长度1到300秒")
	clips := flag.Int("clips", 64, "实际分割1到10000段")
	variable := flag.Bool("vfr", false, "保留不均匀帧间隔")
	audio := flag.Bool("audio", false, "包含48kHz正弦音轨，用于实际PCM连续性验证")
	width := flag.Int("width", 64, "源和工程宽度，64到1920偶数")
	height := flag.Int("height", 32, "源和工程高度，32到1080偶数")
	flag.Parse()
	if err := create(*dir, *seconds, *clips, *variable, *width, *height, *audio); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func create(directory string, seconds, clips int, variable bool, width, height int, audio bool) error {
	if directory == "" || seconds < 1 || seconds > 300 || clips < 1 || clips > 10000 || width < 64 || width > 1920 || height < 32 || height > 1080 || width%2 != 0 || height%2 != 0 {
		return errors.New("需要新目录、1到300秒及1到10000段")
	}
	tools := media.Discover()
	if tools.FFmpegPath == "" || tools.FFprobePath == "" {
		return errors.New("需要显式可用的FFmpeg/ffprobe")
	}
	path, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return fmt.Errorf("输出目录必须尚不存在：%w", err)
	}
	video := filepath.Join(path, "numbered.mp4")
	// 8列×2行亮暗块编码源帧号，压缩后仍可按阈值独立读出；不依赖字体。
	graph := "geq=lum='if(mod(floor(N/pow(2,floor(X/8)+8*floor(Y/16))),2),220,40)':cb=128:cr=128"
	if variable {
		graph += ",select='not(eq(mod(n,5),2))'"
	}
	if width != 64 || height != 32 {
		graph += fmt.Sprintf(",scale=%d:%d:flags=neighbor", width, height)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", "lavfi", "-i", fmt.Sprintf("nullsrc=s=64x32:r=60:d=%d", seconds)}
	if audio {
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:sample_rate=48000:duration=%d", seconds), "-map", "0:v:0", "-map", "1:a:0", "-c:a", "aac", "-b:a", "128k")
	}
	args = append(args, "-vf", graph, "-fps_mode", "vfr", "-c:v", "libx264", "-preset", "fast", "-crf", "18", "-pix_fmt", "yuv420p", "-g", "120", "-bf", "2", "-x264-params", "b-adapt=0:scenecut=0", "-threads", "1", video)
	if output, err := exec.CommandContext(ctx, tools.FFmpegPath, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("编号视频生成失败：%w %s", err, output)
	}
	asset, err := tools.Probe(ctx, video)
	if err != nil {
		return err
	}
	file, err := os.Open(video)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	asset.Fingerprint = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	p := editor.NewProject("自建编号视频定位负载")
	p.Settings.Width, p.Settings.Height, p.Settings.FPSNum, p.Settings.FPSDen = width, height, 30000, 1001
	p.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("numbered", "track-video-1", "video")
	clip.AssetID, clip.SourceInUs = asset.ID, 12345
	clip.DurationFrames = (asset.DurationUs - clip.SourceInUs) * int64(p.Settings.FPSNum) / (1_000_000 * int64(p.Settings.FPSDen))
	if clip.DurationFrames < int64(clips) {
		return errors.New("源视频可用工程帧少于分割数")
	}
	p.Clips = []editor.Clip{clip}
	if err := projectstore.Save(filepath.Join(path, "whole.aividcut"), p); err != nil {
		return err
	}
	e, err := editor.NewEngine(p)
	if err != nil {
		return err
	}
	commands := make([]editor.Command, 0, clips-1)
	for i := clips - 1; i > 0; i-- {
		commands = append(commands, editor.Command{Type: "split_clip", ClipID: clip.ID, AtFrame: clip.DurationFrames * int64(i) / int64(clips)})
	}
	if len(commands) > 0 {
		if _, err = e.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "fixture-splits", Commands: commands}); err != nil {
			return err
		}
	}
	if err := projectstore.Save(filepath.Join(path, "split.aividcut"), e.Snapshot()); err != nil {
		return err
	}
	fmt.Printf("已生成%s，时长%d微秒、%d个派生片段、VFR=%t、有音轨=%t\n", video, asset.DurationUs, len(e.Snapshot().Clips), variable, asset.HasAudio)
	return nil
}
