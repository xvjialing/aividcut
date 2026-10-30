package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
)

type WaveformRequest struct {
	RequestID   string `json:"requestId"`
	ProjectID   string `json:"projectId"`
	AssetID     string `json:"assetId"`
	Fingerprint string `json:"fingerprint"`
	Index       int64  `json:"index"`
}

func waveformAsset(p editor.Project, r WaveformRequest) (editor.Asset, error) {
	if p.ID != r.ProjectID {
		return editor.Asset{}, errors.New("波形所属工程已切换")
	}
	for _, a := range p.Assets {
		if a.ID == r.AssetID {
			if a.Missing || !a.HasAudio || (a.Kind != "audio" && a.Kind != "video") || a.Fingerprint != r.Fingerprint {
				return editor.Asset{}, errors.New("波形素材不可用或已变化，请重新定位素材")
			}
			return a, nil
		}
	}
	return editor.Asset{}, errors.New("找不到波形所属素材")
}

func (s *Service) GetWaveformTile(ctx context.Context, r WaveformRequest) (media.WaveformTile, error) {
	if strings.TrimSpace(r.RequestID) == "" || len(r.RequestID) > 128 || r.Index < 0 || r.Index >= 21600 {
		return media.WaveformTile{}, errors.New("波形请求标识或源窗口无效")
	}
	s.mu.Lock()
	engine := s.engine
	asset, err := waveformAsset(engine.Snapshot(), r)
	if err != nil {
		s.mu.Unlock()
		return media.WaveformTile{}, err
	}
	s.waveMu.Lock()
	if at, exists := s.waveCancelled[r.RequestID]; exists && time.Since(at) < 30*time.Second {
		delete(s.waveCancelled, r.RequestID)
		s.waveMu.Unlock()
		s.mu.Unlock()
		return media.WaveformTile{}, context.Canceled
	}
	if s.waveClosed {
		s.waveMu.Unlock()
		s.mu.Unlock()
		return media.WaveformTile{}, errors.New("波形服务已关闭")
	}
	if s.waveRequests == nil {
		s.waveRequests = map[string]context.CancelFunc{}
	}
	if _, exists := s.waveRequests[r.RequestID]; exists {
		s.waveMu.Unlock()
		s.mu.Unlock()
		return media.WaveformTile{}, errors.New("波形请求标识重复")
	}
	if len(s.waveRequests) >= 128 {
		s.waveMu.Unlock()
		s.mu.Unlock()
		return media.WaveformTile{}, errors.New("波形请求过多，请稍后重试")
	}
	if s.waveReader == nil {
		s.waveReader = media.NewWaveformReader(s.tools)
	}
	reader := s.waveReader
	child, cancel := context.WithCancel(ctx)
	s.waveRequests[r.RequestID] = cancel
	s.waveMu.Unlock()
	s.mu.Unlock()
	defer func() { cancel(); s.waveMu.Lock(); delete(s.waveRequests, r.RequestID); s.waveMu.Unlock() }()
	tile, err := reader.Read(child, asset, r.Index)
	if err != nil {
		return media.WaveformTile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := waveformAsset(s.engine.Snapshot(), r)
	if err != nil || engine != s.engine || current.Path != asset.Path || child.Err() != nil {
		return media.WaveformTile{}, errors.New("波形读取已取消或源工程已变化")
	}
	return tile, nil
}

func (s *Service) CancelWaveformRequest(id string) {
	s.waveMu.Lock()
	defer s.waveMu.Unlock()
	if cancel := s.waveRequests[id]; cancel != nil {
		cancel()
	} else if !s.waveClosed && id != "" && len(id) <= 128 {
		if s.waveCancelled == nil {
			s.waveCancelled = map[string]time.Time{}
		}
		for key, at := range s.waveCancelled {
			if time.Since(at) >= 30*time.Second {
				delete(s.waveCancelled, key)
			}
		}
		if len(s.waveCancelled) >= 256 {
			var oldestKey string
			var oldest time.Time
			for key, at := range s.waveCancelled {
				if oldest.IsZero() || at.Before(oldest) {
					oldestKey, oldest = key, at
				}
			}
			delete(s.waveCancelled, oldestKey)
		}
		s.waveCancelled[id] = time.Now()
	}
}
func (s *Service) resetWaveforms(closed bool) {
	s.waveMu.Lock()
	defer s.waveMu.Unlock()
	s.waveClosed = closed
	for _, cancel := range s.waveRequests {
		cancel()
	}
	if s.waveReader != nil {
		s.waveReader.Close()
		s.waveReader = nil
	}
	// 等待中的调用负责删除自身标识，避免同名新请求被旧 defer 删除。
}
