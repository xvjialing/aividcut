package speechjobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"aividcut/internal/speech"
)

func timingFingerprint(value *speech.TimedAudioResult) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sameTiming(a, b *speech.TimedAudioResult) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	aHash, bHash := timingFingerprint(a), timingFingerprint(b)
	return aHash != "" && aHash == bHash
}

func applyTimingSummary(summary *Summary, timing *speech.TimedAudioResult) {
	if !summary.TimingRequested {
		return
	}
	if timing == nil {
		summary.TimingAvailable, summary.TimingComplete = false, false
		summary.TimingReason = "没有可核查的原文时间回执"
		summary.NormalizedTimingAvailable, summary.NormalizedTimingComplete = false, false
		summary.NormalizedTimingReason = "没有可核查的规范化时间回执"
		return
	}
	summary.TimingAvailable, summary.TimingComplete, summary.TimingReason = speech.AssessTiming(*timing)
	normalized := speech.TimedAudioResult{Alignment: timing.NormalizedAlignment, RawAlignment: timing.RawNormalizedAlignment}
	summary.NormalizedTimingAvailable, summary.NormalizedTimingComplete, summary.NormalizedTimingReason = speech.AssessTiming(normalized)
	summary.NormalizedTimingReason = strings.ReplaceAll(summary.NormalizedTimingReason, "原文", "规范化文本")
	appendWarning := func(message string) {
		for _, previous := range summary.Warnings {
			if previous == message {
				return
			}
		}
		if len(summary.Warnings) < 20 {
			summary.Warnings = append(summary.Warnings, message)
		}
	}
	if !summary.TimingComplete {
		appendWarning(summary.TimingReason + "；保留音频，未补时间、未回退或再次生成")
	}
	if !summary.NormalizedTimingComplete {
		appendWarning(summary.NormalizedTimingReason + "；不以原文或字符比例填补")
	}
}

func validateTimingReceipt(e *entry, receipt audioReceipt) error {
	requested := e.record.Input.Synthesize.WithTiming
	if !requested {
		if receipt.Timing != nil || receipt.TimingSHA256 != "" {
			return errors.New("普通配音回执不能夹带另一模式的时间证据")
		}
		return nil
	}
	value := receipt.Timing
	if value == nil || value.Operation != receipt.Operation || value.VoiceID != receipt.VoiceID || value.MIME != receipt.MIME || value.OutputFormat != receipt.OutputFormat {
		return errors.New("时间回执与原配音操作、模型或音色身份不一致")
	}
	digest := timingFingerprint(value)
	if digest == "" || receipt.TimingSHA256 != digest || hashValue(e.record.TimingSHA256) == "" || e.record.TimingSHA256 != digest {
		return errors.New("原始时间证据指纹不匹配，不使用被改写的对齐")
	}
	return nil
}

func (m *Manager) probeAudio(ctx context.Context, e *entry) (AudioProbe, error) {
	var result AudioProbe
	var err error
	if m.options.ProbeAudio != nil {
		result, err = m.options.ProbeAudio(ctx, m.audioPath(e.record.Summary.ID))
	} else if m.options.Probe != nil {
		result.Asset, err = m.options.Probe(ctx, m.audioPath(e.record.Summary.ID))
	} else {
		return result, errors.New("尚未配置音频探测")
	}
	if err != nil {
		return result, err
	}
	return result, validateProbeEvidence(result)
}

func validateProbeEvidence(result AudioProbe) error {
	if result.SampleRate < 0 || result.SampleRate > 768000 || result.Channels < 0 || result.Channels > 64 || result.SampleCount != nil && (*result.SampleCount <= 0 || result.SampleRate <= 0 || result.Channels <= 0) {
		return errors.New("原始音频采样证据无效")
	}
	if len(result.RawMetadata) > 1<<20 || len(result.RawMetadata) > 0 && !json.Valid(result.RawMetadata) {
		return errors.New("原始探测元数据无效或超过1MiB")
	}
	return nil
}

func (m *Manager) readAudioProbe(e *entry, receipt audioReceipt) (*AudioProbe, error) {
	var result AudioProbe
	if err := readJSON(m.output(e.record.Summary.ID, "audio-probe.json"), &result, 2<<20); err != nil {
		return nil, err
	}
	if result.Asset.Kind != "audio" || result.Asset.Path != m.audioPath(e.record.Summary.ID) || result.Asset.DurationUs <= 0 || hashValue(result.Asset.Fingerprint) != receipt.SHA256 || result.Asset.ID != e.record.Summary.ID+"-audio" {
		return nil, errors.New("探测证据与配音身份不一致")
	}
	if err := validateProbeEvidence(result); err != nil {
		return nil, err
	}
	if expected := e.record.Summary.Audio; expected != nil && (expected.DurationUs != result.Asset.DurationUs || hashValue(expected.Fingerprint) != receipt.SHA256) {
		return nil, errors.New("原始探测证据与已登记音频时长不同")
	}
	// 返回独立副本，原始探测JSON及null样本数不会在调用者处改写回执。
	result.RawMetadata = bytes.Clone(result.RawMetadata)
	return &result, nil
}
