// Package speechjobs 协调持久语音任务，不修改工程，不自动重发可能计费的提交。
package speechjobs

import (
	"context"
	"encoding/json"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/speech"
)

const (
	Transcribe = "transcribe"
	Synthesize = "synthesize"
	Clone      = "clone"
	Queued     = "queued"
	Preparing  = "preparing"
	Submitting = "submitting"
	Running    = "running"
	Saving     = "saving"
	Succeeded  = "succeeded"
	Failed     = "failed"
	Unknown    = "unknown"
	Cancelling = "cancelling"
	Cancelled  = "cancelled"
)

type Input struct {
	ID           string                    `json:"id"`
	ConnectionID string                    `json:"connectionId"`
	Mode         string                    `json:"mode"`
	Consent      bool                      `json:"consent"`
	Transcribe   *speech.TranscribeRequest `json:"transcribe,omitempty"`
	Synthesize   *speech.SynthesizeRequest `json:"synthesize,omitempty"`
	Clone        *speech.CloneRequest      `json:"clone,omitempty"`
}

type Binding struct {
	ProviderID string
	ModelID    string
	Secrets    generation.SecretSource
	Release    func()
}

type Part struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	Fingerprint     string `json:"fingerprint"`
	OffsetUs        int64  `json:"offsetUs"`
	DurationUs      int64  `json:"durationUs"`
	SampleCount     int64  `json:"sampleCount,omitempty"`
	AudioDurationUs int64  `json:"audioDurationUs"`
}

type Preparation struct {
	SourceAssetID        string   `json:"sourceAssetId"`
	SourceFingerprint    string   `json:"sourceFingerprint"`
	SourceDurationUs     int64    `json:"sourceDurationUs"`
	AudioOffsetUs        int64    `json:"audioOffsetUs"`
	CoverageEndUs        int64    `json:"coverageEndUs"`
	Warnings             []string `json:"warnings,omitempty"`
	TimestampToleranceUs int64    `json:"timestampToleranceUs,omitempty"`
	SampleRate           int      `json:"sampleRate,omitempty"`
	Channels             int      `json:"channels,omitempty"`
	Parts                []Part   `json:"parts"`
}

type Options struct {
	Directory  string
	Resolve    func(context.Context, string) (Binding, error)
	Factory    func(generation.SecretSource, speech.MediaSource) (speech.Provider, error)
	Prepare    func(context.Context, editor.Asset, string) (Preparation, error)
	Probe      func(context.Context, string) (editor.Asset, error)
	ProbeAudio func(context.Context, string) (AudioProbe, error)
}

// AudioProbe 只接受真实探测证据；SampleCount缺失时不能由时长推算。
type AudioProbe struct {
	Asset       editor.Asset    `json:"asset"`
	SampleRate  int             `json:"sampleRate,omitempty"`
	Channels    int             `json:"channels,omitempty"`
	SampleCount *int64          `json:"sampleCount"`
	RawMetadata json.RawMessage `json:"rawMetadata,omitempty"`
}

type PartState struct {
	ID              string `json:"id"`
	RequestID       string `json:"requestId"`
	Fingerprint     string `json:"fingerprint"`
	OffsetUs        int64  `json:"offsetUs"`
	DurationUs      int64  `json:"durationUs"`
	AudioDurationUs int64  `json:"audioDurationUs"`
	Status          string `json:"status"`
	RemoteID        string `json:"remoteId,omitempty"`
	Message         string `json:"message"`
}

type Summary struct {
	ID                        string        `json:"id"`
	ConnectionID              string        `json:"connectionId"`
	ProjectID                 string        `json:"projectId"`
	ProjectRevision           int64         `json:"projectRevision"`
	ProviderID                string        `json:"providerId"`
	ModelID                   string        `json:"modelId"`
	Mode                      string        `json:"mode"`
	Status                    string        `json:"status"`
	Message                   string        `json:"message"`
	SourceAssetID             string        `json:"sourceAssetId,omitempty"`
	SourceFingerprint         string        `json:"sourceFingerprint,omitempty"`
	SourceDurationUs          int64         `json:"sourceDurationUs,omitempty"`
	CompletedParts            int           `json:"completedParts"`
	TotalParts                int           `json:"totalParts"`
	CanResumeLocal            bool          `json:"canResumeLocal"`
	Acknowledged              bool          `json:"acknowledged,omitempty"`
	CreatedAt                 time.Time     `json:"createdAt"`
	UpdatedAt                 time.Time     `json:"updatedAt"`
	Audio                     *editor.Asset `json:"audio,omitempty"`
	Voice                     *speech.Voice `json:"voice,omitempty"`
	Warnings                  []string      `json:"warnings"`
	TimingRequested           bool          `json:"timingRequested"`
	TimingAvailable           bool          `json:"timingAvailable"`
	TimingComplete            bool          `json:"timingComplete"`
	TimingReason              string        `json:"timingReason,omitempty"`
	NormalizedTimingAvailable bool          `json:"normalizedTimingAvailable"`
	NormalizedTimingComplete  bool          `json:"normalizedTimingComplete"`
	NormalizedTimingReason    string        `json:"normalizedTimingReason,omitempty"`
}

type Job struct {
	Summary
	Input         Input                    `json:"input"`
	Parts         []PartState              `json:"parts"`
	Transcription *speech.Transcription    `json:"transcription,omitempty"`
	PartResults   []speech.Transcription   `json:"partResults,omitempty"`
	Timing        *speech.TimedAudioResult `json:"timing,omitempty"`
	AudioProbe    *AudioProbe              `json:"audioProbe,omitempty"`
}

type record struct {
	Version      int            `json:"version"`
	Summary      Summary        `json:"summary"`
	Input        Input          `json:"input"`
	Signature    string         `json:"signature"`
	Sources      []editor.Asset `json:"sources"`
	Preparation  *Preparation   `json:"preparation,omitempty"`
	Parts        []PartState    `json:"parts"`
	Intent       bool           `json:"intent"`
	TimingSHA256 string         `json:"timingSha256,omitempty"`
}

type partReceipt struct {
	JobID       string               `json:"jobId"`
	PartID      string               `json:"partId"`
	RequestID   string               `json:"requestId"`
	Fingerprint string               `json:"fingerprint"`
	Result      speech.Transcription `json:"result"`
}

type audioReceipt struct {
	JobID        string                   `json:"jobId"`
	Operation    speech.Operation         `json:"operation"`
	VoiceID      string                   `json:"voiceId"`
	MIME         string                   `json:"mime"`
	OutputFormat string                   `json:"outputFormat"`
	Bytes        int64                    `json:"bytes"`
	SHA256       string                   `json:"sha256"`
	Timing       *speech.TimedAudioResult `json:"timing,omitempty"`
	TimingSHA256 string                   `json:"timingSha256,omitempty"`
}

type cloneReceipt struct {
	JobID  string             `json:"jobId"`
	Result speech.CloneResult `json:"result"`
}
