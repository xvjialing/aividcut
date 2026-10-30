package editor

// 工程中时间线位置按工程帧计时，素材入点按微秒计时。
type Settings struct {
	Width      int `json:"width"`
	Height     int `json:"height"`
	FPSNum     int `json:"fpsNum"`
	FPSDen     int `json:"fpsDen"`
	SampleRate int `json:"sampleRate"`
}

type FontInfo struct {
	Copyright  string `json:"copyright,omitempty"`
	License    string `json:"license,omitempty"`
	LicenseURL string `json:"licenseUrl,omitempty"`
	Family     string `json:"family"`
	Style      string `json:"style"`
	FullName   string `json:"fullName"`
	Weight     int    `json:"weight"`
	Italic     bool   `json:"italic"`
}

type Asset struct {
	Font             *FontInfo `json:"font,omitempty"`
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Kind             string    `json:"kind"`
	Path             string    `json:"path"`
	URL              string    `json:"url,omitempty"`
	DurationUs       int64     `json:"durationUs"`
	Width            int       `json:"width"`
	Height           int       `json:"height"`
	FPSNum           int       `json:"fpsNum"`
	FPSDen           int       `json:"fpsDen"`
	HasAudio         bool      `json:"hasAudio"`
	Missing          bool      `json:"missing"`
	ThumbnailURL     string    `json:"thumbnailUrl,omitempty"`
	Fingerprint      string    `json:"fingerprint,omitempty"`
	AudioSampleRate  int       `json:"audioSampleRate,omitempty"`
	AudioSampleCount *int64    `json:"audioSampleCount,omitempty"`
	AudioStartUs     int64     `json:"audioStartUs,omitempty"`
}

type Track struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Locked bool   `json:"locked"`
	Muted  bool   `json:"muted"`
	Hidden bool   `json:"hidden"`
}

type Transform struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Scale    float64 `json:"scale"`
	Rotation float64 `json:"rotation"`
}

type TextStyle struct {
	FontAssetID string `json:"fontAssetId,omitempty"`
	FontSize    int    `json:"fontSize"`
	Color       string `json:"color"`
	Background  string `json:"background"`
	Align       string `json:"align"`
}

type LUT struct {
	AssetID   string  `json:"assetId"`
	Intensity float64 `json:"intensity"`
}

// SourceWord 使用源素材微秒区间；稳定标识使纠错不会丢失时间线上的人工覆盖。
type SourceWord struct {
	ID        string `json:"id"`
	StartUs   int64  `json:"startUs"`
	EndUs     int64  `json:"endUs"`
	Text      string `json:"text"`
	SpeakerID string `json:"speakerId,omitempty"`
}

// CaptionLayout 只规定自动阅读分句，不生成或估算词时间。
type CaptionLayout struct {
	Mode             string `json:"mode"`
	MaxCharsPerLine  int    `json:"maxCharsPerLine,omitempty"`
	MaxLines         int    `json:"maxLines,omitempty"`
	MaxDurationUs    int64  `json:"maxDurationUs,omitempty"`
	PauseThresholdUs int64  `json:"pauseThresholdUs,omitempty"`
}

// 原始词只在接纳时写入，AcceptedWords 为当前接受版本；生成配音使用明确tts来源。
type Transcript struct {
	ID                string             `json:"id"`
	AssetID           string             `json:"assetId"`
	SourceFingerprint string             `json:"sourceFingerprint"`
	SourceDurationUs  int64              `json:"sourceDurationUs"`
	Language          string             `json:"language,omitempty"`
	Origin            string             `json:"origin,omitempty"`
	TimingSource      string             `json:"timingSource,omitempty"`
	CaptionLayout     *CaptionLayout     `json:"captionLayout,omitempty"`
	RawWords          []SourceWord       `json:"rawWords"`
	AcceptedWords     []SourceWord       `json:"acceptedWords"`
	Revision          int64              `json:"revision"`
	CaptionTrackID    string             `json:"captionTrackId,omitempty"`
	Stale             bool               `json:"stale,omitempty"`
	CaptionExclusions []CaptionExclusion `json:"captionExclusions,omitempty"`
}

type CaptionExclusion struct {
	SourceClipID string `json:"sourceClipId"`
	WordID       string `json:"wordId"`
}

type CaptionBinding struct {
	TranscriptID  string   `json:"transcriptId"`
	SourceClipID  string   `json:"sourceClipId"`
	WordIDs       []string `json:"wordIds"`
	TextOverride  bool     `json:"textOverride,omitempty"`
	StyleOverride bool     `json:"styleOverride,omitempty"`
	Partial       bool     `json:"partial,omitempty"`
	Stale         bool     `json:"stale,omitempty"`
}

type Clip struct {
	AudioFade      *AudioFade      `json:"audioFade,omitempty"`
	ID             string          `json:"id"`
	TrackID        string          `json:"trackId"`
	AssetID        string          `json:"assetId,omitempty"`
	Kind           string          `json:"kind"`
	Name           string          `json:"name"`
	StartFrame     int64           `json:"startFrame"`
	DurationFrames int64           `json:"durationFrames"`
	SourceInUs     int64           `json:"sourceInUs"`
	AudioRange     *AudioRange     `json:"audioRange,omitempty"`
	SourcePhase    *SourcePhase    `json:"sourcePhase,omitempty"`
	SpeedNum       int             `json:"speedNum"`
	SpeedDen       int             `json:"speedDen"`
	Volume         float64         `json:"volume"`
	Muted          bool            `json:"muted"`
	Transform      Transform       `json:"transform"`
	Text           string          `json:"text,omitempty"`
	TextStyle      TextStyle       `json:"textStyle"`
	LUT            *LUT            `json:"lut,omitempty"`
	LinkGroup      string          `json:"linkGroup,omitempty"`
	CaptionSource  *CaptionBinding `json:"captionSource,omitempty"`
}

type Project struct {
	// 历史条目记录相反方向的序列上下文；活动工程与持久文件不携带此状态。
	historySequenceID        string
	ActiveSequenceProjection bool             `json:"activeSequenceProjection,omitempty"`
	SequenceID               string           `json:"sequenceId,omitempty"`
	SequenceName             string           `json:"sequenceName,omitempty"`
	SequenceOrder            []string         `json:"sequenceOrder,omitempty"`
	OtherSequences           []Sequence       `json:"otherSequences,omitempty"`
	SchemaVersion            int              `json:"schemaVersion"`
	ID                       string           `json:"id"`
	Name                     string           `json:"name"`
	Revision                 int64            `json:"revision"`
	Settings                 Settings         `json:"settings"`
	Assets                   []Asset          `json:"assets"`
	Tracks                   []Track          `json:"tracks"`
	Clips                    []Clip           `json:"clips"`
	Transcripts              []Transcript     `json:"transcripts,omitempty"`
	VisualAnalyses           []VisualAnalysis `json:"visualAnalyses,omitempty"`
}

type Command struct {
	SequenceID       string          `json:"sequenceId,omitempty"`
	SourceSequenceID string          `json:"sourceSequenceId,omitempty"`
	FromFrame        int64           `json:"fromFrame,omitempty"`
	ToFrame          int64           `json:"toFrame,omitempty"`
	VisualAnalysis   *VisualAnalysis `json:"visualAnalysis,omitempty"`
	VisualAnalysisID string          `json:"visualAnalysisId,omitempty"`
	// 只由当前构造命令填充，不接受外部快照或字幕父源声明。
	rippleNamespace    string              `json:"-"`
	inheritance        *captionInheritance `json:"-"`
	clipboard          *TimelineClipboard  `json:"-"`
	pasteNamespace     string              `json:"-"`
	ClipboardID        string              `json:"clipboardId,omitempty"`
	ClipIDs            []string            `json:"clipIds,omitempty"`
	FrameDelta         int64               `json:"frameDelta,omitempty"`
	Edge               string              `json:"edge,omitempty"`
	TrackDelta         int                 `json:"trackDelta,omitempty"`
	Type               string              `json:"type"`
	Clip               *Clip               `json:"clip,omitempty"`
	AudioCarrier       *Clip               `json:"audioCarrier,omitempty"`
	AudioMode          string              `json:"audioMode,omitempty"`
	ClipID             string              `json:"clipId,omitempty"`
	Track              *Track              `json:"track,omitempty"`
	TrackID            string              `json:"trackId,omitempty"`
	BeforeTrackID      string              `json:"beforeTrackId,omitempty"`
	Asset              *Asset              `json:"asset,omitempty"`
	AtFrame            int64               `json:"atFrame,omitempty"`
	Name               string              `json:"name,omitempty"`
	Settings           *Settings           `json:"settings,omitempty"`
	IncludeLinked      *bool               `json:"includeLinked,omitempty"`
	Transcript         *Transcript         `json:"transcript,omitempty"`
	TranscriptID       string              `json:"transcriptId,omitempty"`
	TranscriptRevision int64               `json:"transcriptRevision,omitempty"`
	Words              []SourceWord        `json:"words,omitempty"`
	CaptionLayout      *CaptionLayout      `json:"captionLayout,omitempty"`
}

type EditRequest struct {
	BaseRevision   int64     `json:"baseRevision"`
	IdempotencyKey string    `json:"idempotencyKey"`
	Label          string    `json:"label"`
	Commands       []Command `json:"commands"`
}
