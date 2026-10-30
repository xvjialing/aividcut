package media

const (
	SpeechSampleRate                 = 16000
	SpeechChannels                   = 1
	DefaultSpeechChunkUs       int64 = 5 * 60 * 1_000_000
	DefaultSpeechMaxDurationUs int64 = 6 * 60 * 60 * 1_000_000
	SpeechMaxDurationUs        int64 = 24 * 60 * 60 * 1_000_000
	SpeechMaxParts                   = 1024
	SpeechPartMaxBytes         int64 = 64 << 20
)

type SpeechAudioOptions struct {
	ChunkDurationUs int64                     `json:"chunkDurationUs,omitempty"`
	MaxDurationUs   int64                     `json:"maxDurationUs,omitempty"`
	Progress        func(SpeechAudioProgress) `json:"-"`
}

type SpeechAudioProgress struct {
	Stage        string `json:"stage"`
	PartIndex    int    `json:"partIndex"`
	SampleCount  int64  `json:"sampleCount"`
	TotalSamples int64  `json:"totalSamples"` // 解码结束前未知，保持0，不把视频总时长冒充实际音频采样总量。
}

type SpeechAudioPreparation struct {
	SourceAssetID        string            `json:"sourceAssetId"`
	SourceFingerprint    string            `json:"sourceFingerprint"`
	SourceDurationUs     int64             `json:"sourceDurationUs"`
	SampleRate           int               `json:"sampleRate"`
	Channels             int               `json:"channels"`
	AudioOffsetUs        int64             `json:"audioOffsetUs"`
	CoverageEndUs        int64             `json:"coverageEndUs"`
	Warnings             []string          `json:"warnings"`
	TimestampToleranceUs int64             `json:"timestampToleranceUs"`
	Parts                []SpeechAudioPart `json:"parts"`
}

type SpeechAudioPart struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Fingerprint string `json:"fingerprint"`
	// 逻辑区间连续覆盖实际音轨范围；末块PCM采样量化差不足63微秒，不补齐视频无音轨区域。
	OffsetUs        int64 `json:"offsetUs"`
	DurationUs      int64 `json:"durationUs"`
	SampleCount     int64 `json:"sampleCount"`
	AudioDurationUs int64 `json:"audioDurationUs"`
}
