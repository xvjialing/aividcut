package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
)

const importAudioMaxDurationUs int64 = 5 * 60 * 1_000_000
const importAudioBatchDurationUs int64 = 10 * 60 * 1_000_000
const importAudioMaxBytes int64 = 128 << 20
const importAudioMeasurementTimeout = 15 * time.Second

type importAudioBudget struct {
	ctx       context.Context
	remaining int64
}

func newImportAudioBudget(ctx context.Context) (*importAudioBudget, context.CancelFunc) {
	bounded, cancel := context.WithTimeout(ctx, importAudioMeasurementTimeout)
	return &importAudioBudget{ctx: bounded, remaining: importAudioBatchDurationUs}, cancel
}

func importedAudioEvidence(ctx context.Context, tools media.Toolchain, asset editor.Asset, budget *importAudioBudget) (editor.Asset, string, error) {
	if err := ctx.Err(); err != nil {
		return asset, "", err
	}
	if !asset.HasAudio || asset.AudioSampleCount != nil {
		return asset, "", nil
	}
	unknown := func(reason string) (editor.Asset, string, error) {
		return asset, fmt.Sprintf("%s：%s；未实测样本数，添加音频时保留声明的微秒末端，不标为采样精度。", asset.Name, reason), nil
	}
	if tools.FFmpegPath == "" || tools.FFprobePath == "" {
		return unknown("当前工具仅能读取声明时长，实际计数需要 FFmpeg 与 ffprobe")
	}
	stat, err := os.Stat(asset.Path)
	if err != nil {
		return asset, "", err
	}
	if asset.DurationUs <= 0 || asset.DurationUs > importAudioMaxDurationUs || stat.Size() > importAudioMaxBytes {
		return unknown("超过本轮自动计数上限（每文件5分钟、128MiB）")
	}
	if budget.remaining < asset.DurationUs {
		return unknown("超过本次批量自动计数的10分钟累计上限")
	}
	if budget.ctx.Err() != nil {
		if ctx.Err() != nil {
			return asset, "", ctx.Err()
		}
		return unknown("本次批量自动计数的15秒预算已用尽")
	}
	budget.remaining -= asset.DurationUs
	measured, err := tools.MeasureAudioSamples(budget.ctx, asset)
	if err != nil {
		if ctx.Err() != nil {
			return asset, "", ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) && budget.ctx.Err() != nil {
			return unknown("本次批量自动计数的15秒预算已用尽")
		}
		return asset, "", fmt.Errorf("%s 的真实音轨无法安全计数：%w", asset.Name, err)
	}
	// Probe的SHA与解码计数必须属于同一文件内容，而不只是在各自读取期间稳定。
	if err := verifySourceFingerprint(budget.ctx, asset.Path, asset.Fingerprint); err != nil {
		if ctx.Err() != nil {
			return asset, "", ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) && budget.ctx.Err() != nil {
			return unknown("本次批量自动计数的15秒预算已用尽")
		}
		return asset, "", fmt.Errorf("%s 的计数与内容指纹无法核对，请重新导入：%w", asset.Name, err)
	}
	count := measured.SampleCount
	asset.AudioSampleRate, asset.AudioSampleCount, asset.AudioStartUs = measured.SampleRate, &count, measured.SourceStartUs
	return asset, "", nil
}

func audioMeasurementCapability(tools media.Toolchain) Capability {
	if tools.FFmpegPath == "" || tools.FFprobePath == "" {
		return Capability{Reason: "实际样本计数需要 FFmpeg 与 ffprobe；当前仅保留探测到的声明时长，未知样本数不标为实测。"}
	}
	return Capability{Available: true, Reason: "普通导入自动计数限每文件5分钟/128MiB、单批累计10分钟/15秒；超限返回导入提示，未知样本数不标为实测。"}
}
