package playback

import (
	"context"
	"errors"
)

// Diagnostics仅供本地开发命令读取，不进入App、媒体帧、WebSocket或MCP接口。
// 数值为本worker进程累计；estimatedBytes是工作面估算，不是实测RSS。
type Diagnostics struct {
	Graph       *GraphDiagnostics       `json:"graph,omitempty"`
	Visual      VisualDiagnostics       `json:"visual"`
	AudioMix    *AudioMixDiagnostics    `json:"audioMix,omitempty"`
	SourceAudio *SourceAudioDiagnostics `json:"sourceAudio,omitempty"`
	SourceVideo *SourceVideoDiagnostics `json:"sourceVideo,omitempty"`
}

type GraphDiagnostics struct {
	Loads                 uint64            `json:"loads"`
	Replacements          uint64            `json:"replacements"`
	PeakXMLBytes          uint64            `json:"peakXmlBytes"`
	LiveGraphs            uint64            `json:"liveGraphs"`
	LiveProducers         uint64            `json:"liveProducers"`
	LiveServices          uint64            `json:"liveServices"`
	LiveServicesByKind    map[string]uint64 `json:"liveServicesByKind,omitempty"`
	PeakReleaseReferences uint64            `json:"peakReleaseReferences"`
}

type SourceVideoDiagnostics struct {
	Created        uint64 `json:"created"`
	Opens          uint64 `json:"opens"`
	DecodedFrames  uint64 `json:"decodedFrames"`
	Seeks          uint64 `json:"seeks"`
	Anchors        uint64 `json:"anchors"`
	Fallbacks      uint64 `json:"fallbacks"`
	ActiveDecoders uint64 `json:"activeDecoders"`
	CacheHits      uint64 `json:"cacheHits"`
	CacheMisses    uint64 `json:"cacheMisses"`
	ResidentStates uint64 `json:"residentStates"`
}

type SourceAudioDiagnostics struct {
	CacheHits             uint64 `json:"cacheHits"`
	CacheMisses           uint64 `json:"cacheMisses"`
	Opens                 uint64 `json:"opens"`
	DecoderResets         uint64 `json:"decoderResets"`
	GraphBuilds           uint64 `json:"graphBuilds"`
	DecodedFrames         uint64 `json:"decodedFrames"`
	GatedFrames           uint64 `json:"gatedFrames"`
	ResidentStates        uint64 `json:"residentStates"`
	ActiveDecoders        uint64 `json:"activeDecoders"`
	QueuedSampleFrames    uint64 `json:"queuedSampleFrames"`
	PeakQueueSampleFrames uint64 `json:"peakQueueSampleFrames"`
}

type AudioMixDiagnostics struct {
	Created            uint64 `json:"created"`
	Calls              uint64 `json:"calls"`
	SkippedBlankFrames uint64 `json:"skippedBlankFrames"`
	MixedSamples       uint64 `json:"mixedSamples"`
	PeakFrameBytes     uint64 `json:"peakFrameBytes"`
}

type VisualDiagnostics struct {
	Builds         uint64 `json:"builds"`
	Hits           uint64 `json:"hits"`
	Calls          uint64 `json:"calls"`
	FastPathCalls  uint64 `json:"fastPathCalls"`
	FilterNs       uint64 `json:"filterNs"`
	CompositeNs    uint64 `json:"compositeNs"`
	ResidentGraphs uint64 `json:"residentGraphs"`
	EstimatedBytes uint64 `json:"estimatedBytes"`
}

func (s *Session) Diagnostics(ctx context.Context) (Diagnostics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply, err := s.callLocked(ctx, map[string]any{"op": "diagnostics"})
	if err != nil {
		return Diagnostics{}, err
	}
	if reply.Type != "diagnostics" || reply.Diagnostics == nil {
		return Diagnostics{}, errors.New("原生进程未提供诊断快照，不补造性能数据")
	}
	return *reply.Diagnostics, nil
}
