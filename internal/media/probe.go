package media

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"aividcut/internal/editor"
)

type probeResult struct {
	Streams []struct {
		CodecType    string `json:"codec_type"`
		TimeBase     string `json:"time_base"`
		StartTime    string `json:"start_time"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		RFrameRate   string `json:"r_frame_rate"`
		Duration     string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration  string `json:"duration"`
		StartTime string `json:"start_time"`
	} `json:"format"`
}

// Probe 读取真实本地媒体，图片不依赖 ffprobe；音视频需要所配置的工具。
func (t Toolchain) Probe(ctx context.Context, path string) (editor.Asset, error) {
	var asset editor.Asset
	local, err := filepath.Abs(path)
	if err != nil {
		return asset, fmt.Errorf("素材路径无效: %w", err)
	}
	stat, err := os.Stat(local)
	if err != nil || !stat.Mode().IsRegular() {
		return asset, errors.New("素材不是可读取的本地普通文件")
	}
	if stat.Size() == 0 {
		return asset, errors.New("不能导入空文件")
	}
	asset.Path, asset.Name = local, filepath.Base(local)
	asset.ID = newID()
	if f, openErr := os.Open(local); openErr == nil {
		config, format, imageErr := image.DecodeConfig(f)
		f.Close()
		if imageErr == nil && (format == "png" || format == "jpeg") {
			asset.Kind = "image"
			asset.Width, asset.Height = config.Width, config.Height
		}
	}
	if asset.Kind == "" {
		if t.FFprobePath == "" {
			return asset, errors.New("未配置 ffprobe，暂时只能导入 PNG/JPEG 图片")
		}
		out, commandErr := runOutput(ctx, t.FFprobePath, "-v", "error", "-show_streams", "-show_format", "-of", "json", "-i", local)
		if commandErr != nil {
			if ctx.Err() != nil {
				return asset, ctx.Err()
			}
			return asset, fmt.Errorf("媒体探测失败: %w", commandErr)
		}
		if len(out) > 4<<20 {
			return asset, errors.New("媒体元数据超过探测上限")
		}
		var metadata probeResult
		if err := json.Unmarshal(out, &metadata); err != nil {
			return asset, fmt.Errorf("无法解析媒体信息: %w", err)
		}
		asset.DurationUs = secondsToUs(metadata.Format.Duration)
		for _, stream := range metadata.Streams {
			if duration := secondsToUs(stream.Duration); duration > asset.DurationUs {
				asset.DurationUs = duration
			}
			if stream.CodecType == "video" && asset.Kind != "video" {
				asset.Kind = "video"
				asset.Width, asset.Height = stream.Width, stream.Height
				asset.FPSNum, asset.FPSDen = parseRate(stream.AvgFrameRate)
				if asset.FPSNum == 0 {
					asset.FPSNum, asset.FPSDen = parseRate(stream.RFrameRate)
				}
			}
			if stream.CodecType == "audio" {
				asset.HasAudio = true
				if asset.Kind == "" {
					asset.Kind = "audio"
				}
			}
		}
		if asset.Kind == "" || asset.DurationUs <= 0 {
			return asset, errors.New("素材缺少可编辑的音视频流或有效时长")
		}
	}
	asset.Fingerprint, err = fingerprint(ctx, local)
	if err != nil {
		return asset, err
	}
	return asset, nil
}

func newID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("系统随机数源不可用")
	}
	return hex.EncodeToString(value[:])
}

func parseRate(value string) (int, int) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return 0, 0
	}
	num, e1 := strconv.Atoi(parts[0])
	den, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || num <= 0 || den <= 0 {
		return 0, 0
	}
	return num, den
}

func secondsToUs(value string) int64 {
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > float64(math.MaxInt64)/1e6 {
		return 0
	}
	return int64(math.Round(seconds * 1e6))
}

func fingerprint(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// LocalFileFingerprint 为本次派生操作核对普通本机文件，不改变已有工程元数据。
func LocalFileFingerprint(ctx context.Context, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("来源不是可读取的普通本地文件")
	}
	return fingerprint(ctx, path)
}
