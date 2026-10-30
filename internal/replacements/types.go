// Package replacements 以已有本地素材编译可核查、可撤销的镜头替换，不调用生成或语音服务。
package replacements

import "aividcut/internal/editor"

const (
	KeepOriginal  = "keep_original"
	UseCandidate  = "use_candidate"
	Mute          = "mute"
	KeepLUT       = "keep"
	ClearLUT      = "clear"
	PreserveSpeed = "preserve"
	NormalSpeed   = "normal"
)

type Input struct {
	ClipID       string            `json:"clipId"`
	AssetID      string            `json:"assetId"`
	SourceInUs   int64             `json:"sourceInUs"`
	AudioMode    string            `json:"audioMode"`
	LUTMode      string            `json:"lutMode"`
	SpeedMode    string            `json:"speedMode"`
	Transform    *editor.Transform `json:"transform,omitempty"`
	AudioTrackID string            `json:"audioTrackId,omitempty"`
}

type Effect struct {
	Kind        string `json:"kind"`
	ClipID      string `json:"clipId,omitempty"`
	TrackID     string `json:"trackId,omitempty"`
	AssetID     string `json:"assetId,omitempty"`
	Description string `json:"description"`
}

type Evidence struct {
	AssetID           string `json:"assetId"`
	SourceFingerprint string `json:"sourceFingerprint"`
	ClipID            string `json:"clipId,omitempty"`
	SourceInUs        int64  `json:"sourceInUs"`
	DurationFrames    int64  `json:"durationFrames"`
	SpeedNum          int    `json:"speedNum"`
	SpeedDen          int    `json:"speedDen"`
}

type Plan struct {
	ID              string             `json:"id"`
	ProjectID       string             `json:"projectId"`
	Request         editor.EditRequest `json:"request"`
	ClipID          string             `json:"clipId"`
	AssetID         string             `json:"assetId"`
	AudioMode       string             `json:"audioMode"`
	Summary         string             `json:"summary"`
	Effects         []Effect           `json:"effects"`
	Evidence        []Evidence         `json:"evidence"`
	Warnings        []string           `json:"warnings"`
	AffectedClipIDs []string           `json:"affectedClipIds"`
}
