// Package playback 将受控工程快照编译到常驻MLT进程，图像/音频以有界二进制拉取。
package playback

import (
	"time"
)

const Protocol = "AIV1"

type Config struct {
	RenderQuality string
	// VisualSources仅由已核查缓存注入，音频与原工程仍用原片。
	VisualSources map[string]VisualSource
	// OfflineRender仅供独立导出会话，允许原画幅二进制帧；不能转发到WebView。
	OfflineRender   bool
	WorkerPath      string
	MLTRepository   string
	MLTProfilesPath string
	MLTDataPath     string
	MaxWidth        int
	MaxHeight       int
	Timeout         time.Duration
	// WindowClipLimit/WindowFrames供开发验收收紧窗口；0采用生产默认上界。
	WindowClipLimit int
	WindowFrames    int64
}

type VisualSource struct {
	Path              string
	Fingerprint       string
	SourceFingerprint string
	OriginUs          int64
}

type Feature struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

type Capabilities struct {
	AudioFades    Feature  `json:"audioFades"`
	Available     bool     `json:"available"`
	Reason        string   `json:"reason,omitempty"`
	Protocol      string   `json:"protocol"`
	MLTVersion    string   `json:"mltVersion"`
	Producers     []string `json:"producers"`
	Filters       []string `json:"filters"`
	Transitions   []string `json:"transitions"`
	Video         Feature  `json:"video"`
	Image         Feature  `json:"image"`
	Audio         Feature  `json:"audio"`
	PictureSeek   Feature  `json:"pictureSeek"`
	WindowedGraph Feature  `json:"windowedGraph"`
	SourceProxies Feature  `json:"sourceProxies"`
	OfflineRender Feature  `json:"offlineRender"`
	FontFiles     Feature  `json:"fontFiles"`
	Text          Feature  `json:"text"`
	Transform     Feature  `json:"transform"`
	Speed         Feature  `json:"speed"`
	LUT           Feature  `json:"lut"`
	Display       string   `json:"display"`
}

type Info struct {
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	DurationFrames  int64  `json:"durationFrames"`
	Position        int64  `json:"position"`
	Playing         bool   `json:"playing"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	FPSNum          int    `json:"fpsNum"`
	FPSDen          int    `json:"fpsDen"`
	SampleRate      int    `json:"sampleRate"`
	Channels        int    `json:"channels"`
}

// PCM为小端、交错float32；Samples是每声道样本帧数，绝不放进JSON/base64。
// Session复用RGBA/PCM缓冲，字节只保证有效至下一次Load/Next/Seek/Preview/Close。
// PictureOnly表示仅取画面，Samples与PCM为空；后续Next仍产生完整真实声音。
type Frame struct {
	Position         int64  `json:"position"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	FPSNum           int    `json:"fpsNum"`
	FPSDen           int    `json:"fpsDen"`
	SampleRate       int    `json:"sampleRate"`
	Channels         int    `json:"channels"`
	Samples          int    `json:"samples"`
	AudioStartSample int64  `json:"audioStartSample"`
	End              bool   `json:"end"`
	PictureOnly      bool   `json:"pictureOnly,omitempty"`
	RGBA             []byte `json:"-"`
	PCM              []byte `json:"-"`
}
