// Package speech 提供源素材转写、文本配音和音色管理协议，不修改工程或时间线。
package speech

import (
	"context"
	"io"
	"net/http"
	"time"

	"aividcut/internal/generation"
)

const (
	ElevenLabsID           = "elevenlabs"
	ScribeV2               = "scribe_v2"
	MultilingualV2         = "eleven_multilingual_v2"
	Succeeded              = "succeeded"
	Failed                 = "failed"
	Unknown                = "unknown"
	MaxInputBytes    int64 = 64 << 20
	MaxResponseBytes int64 = 64 << 20
)

type Feature struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason"`
}
type Capabilities struct {
	ProviderID             string   `json:"providerId"`
	ASRModels              []string `json:"asrModels"`
	TTSModels              []string `json:"ttsModels"`
	TimestampGranularities []string `json:"timestampGranularities"`
	OutputFormats          []string `json:"outputFormats"`
	Transcription          Feature  `json:"transcription"`
	SpeechSynthesis        Feature  `json:"speechSynthesis"`
	SpeechTiming           Feature  `json:"speechTiming"`
	VoiceListing           Feature  `json:"voiceListing"`
	InstantCloning         Feature  `json:"instantCloning"`
	Diarization            Feature  `json:"diarization"`
	CharacterTimestamps    Feature  `json:"characterTimestamps"`
	RemoteCancellation     Feature  `json:"remoteCancellation"`
	Streaming              Feature  `json:"streaming"`
	VoiceVerification      Feature  `json:"voiceVerification"`
	MaxInputBytes          int64    `json:"maxInputBytes"`
	MaxTextCharacters      int      `json:"maxTextCharacters"`
	MaxCloneFiles          int      `json:"maxCloneFiles"`
}

type Operation struct {
	ID                  string `json:"id"`
	ProviderID          string `json:"providerId"`
	ModelID             string `json:"modelId,omitempty"`
	RemoteID            string `json:"remoteId,omitempty"`
	Status              string `json:"status"`
	SubmissionUncertain bool   `json:"submissionUncertain"`
}

type TranscribeRequest struct {
	ID             string `json:"id,omitempty"`
	AssetID        string `json:"assetId"`
	ModelID        string `json:"modelId,omitempty"`
	Language       string `json:"language,omitempty"`
	Granularity    string `json:"granularity,omitempty"`
	Diarize        bool   `json:"diarize"`
	TagAudioEvents bool   `json:"tagAudioEvents"`
}

// 缺失时间必须保留 nil，不能把它填成零或按文本长度伪造。
type TimedText struct {
	Text    string `json:"text"`
	StartUs *int64 `json:"startUs"`
	EndUs   *int64 `json:"endUs"`
}
type Word struct {
	TimedText
	TimingComplete bool        `json:"timingComplete"`
	Type           string      `json:"type"` // word、spacing、audio_event，原样区分。
	SpeakerID      string      `json:"speakerId,omitempty"`
	LogProbability *float64    `json:"logProbability,omitempty"`
	Characters     []TimedText `json:"characters,omitempty"`
}
type Transcription struct {
	Operation
	AssetID                 string   `json:"assetId"`
	SourceSHA256            string   `json:"sourceSha256"`
	SourceDurationUs        int64    `json:"sourceDurationUs,omitempty"`
	ReportedAudioDurationUs *int64   `json:"reportedAudioDurationUs,omitempty"`
	Language                string   `json:"language"`
	LanguageProbability     *float64 `json:"languageProbability,omitempty"`
	Text                    string   `json:"text"`
	Words                   []Word   `json:"words"`
	Granularity             string   `json:"granularity"`
	TimingComplete          bool     `json:"timingComplete"`
	Warnings                []string `json:"warnings"`
}

type SynthesizeRequest struct {
	ID           string `json:"id,omitempty"`
	ModelID      string `json:"modelId,omitempty"`
	VoiceID      string `json:"voiceId"`
	Text         string `json:"text"`
	OutputFormat string `json:"outputFormat,omitempty"`
	WithTiming   bool   `json:"withTiming,omitempty"`
}
type AudioResult struct {
	Operation
	VoiceID      string `json:"voiceId"`
	OutputFormat string `json:"outputFormat"`
	MIME         string `json:"mime"`
	Data         []byte `json:"-"`
}

type Voice struct {
	ID                       string            `json:"id"`
	Name                     string            `json:"name"`
	Description              string            `json:"description,omitempty"`
	Category                 string            `json:"category,omitempty"`
	Labels                   map[string]string `json:"labels,omitempty"`
	VerificationState        string            `json:"verificationState"`
	RequiresVerification     *bool             `json:"requiresVerification,omitempty"`
	ProviderReportedVerified *bool             `json:"providerReportedVerified,omitempty"`
	PreviewURL               string            `json:"-"`
}
type VoiceListRequest struct {
	Search        string `json:"search,omitempty"`
	PageSize      int    `json:"pageSize,omitempty"`
	NextPageToken string `json:"nextPageToken,omitempty"`
}
type VoicePage struct {
	Voices        []Voice `json:"voices"`
	HasMore       bool    `json:"hasMore"`
	NextPageToken string  `json:"nextPageToken,omitempty"`
}
type CloneConsent struct {
	Confirmed bool   `json:"confirmed"`
	Subject   string `json:"subject"`
	Authority string `json:"authority"` // self 或 authorized；只记录用户声明，不代表验证完成。
}
type CloneRequest struct {
	ID          string       `json:"id,omitempty"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	AssetIDs    []string     `json:"assetIds"`
	Consent     CloneConsent `json:"consent"`
}
type CloneResult struct {
	Operation
	Voice Voice `json:"voice"`
}

type ASRProvider interface {
	Capabilities() Capabilities
	Transcribe(context.Context, TranscribeRequest) (Transcription, error)
}
type TTSProvider interface {
	Capabilities() Capabilities
	Synthesize(context.Context, SynthesizeRequest) (AudioResult, error)
}
type VoiceProvider interface {
	Capabilities() Capabilities
	ListVoices(context.Context, VoiceListRequest) (VoicePage, error)
	CloneVoice(context.Context, CloneRequest) (CloneResult, error)
}
type Provider interface {
	ASRProvider
	TTSProvider
	VoiceProvider
}

// MediaSource 只由应用注入已登记素材读取器；请求 DTO 不接受路径或 URL。
type MediaSource interface {
	Open(context.Context, string) (*InputFile, error)
}
type InputFile struct {
	AssetID    string
	MIME       string
	Kind       string
	SizeBytes  int64
	DurationUs int64
	SHA256     string
	Reader     io.ReadCloser
}
type Config struct {
	BaseURL            string
	HTTPClient         *http.Client            `json:"-"`
	Secrets            generation.SecretSource `json:"-"`
	Files              MediaSource             `json:"-"`
	WorkDir            string                  `json:"-"`
	Timeout            time.Duration           `json:"-"`
	AllowLocalTestHTTP bool                    `json:"-"`
}

type Error struct {
	Code                string `json:"code"`
	Message             string `json:"message"`
	HTTPStatus          int    `json:"httpStatus,omitempty"`
	SubmissionUncertain bool   `json:"submissionUncertain"`
	Cause               error  `json:"-"`
}

func (e *Error) Error() string           { return e.Message }
func (e *Error) Unwrap() error           { return e.Cause }
func failure(code, message string) error { return &Error{Code: code, Message: message} }
