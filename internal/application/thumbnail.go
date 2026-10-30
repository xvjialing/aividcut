package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
)

type ThumbnailRequest struct {
	SourceEpoch uint64 `json:"sourceEpoch"`
	RequestID   string `json:"requestId"`
	ProjectID   string `json:"projectId"`
	AssetID     string `json:"assetId"`
	Fingerprint string `json:"fingerprint"`
	PositionUs  int64  `json:"positionUs"`
}
type AssetThumbnail struct {
	AssetID    string `json:"assetId"`
	PositionUs int64  `json:"positionUs"`
	URL        string `json:"url"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

func thumbnailAsset(p editor.Project, r ThumbnailRequest) (editor.Asset, error) {
	if p.ID != r.ProjectID {
		return editor.Asset{}, errors.New("缩略图所属工程已切换")
	}
	for _, a := range p.Assets {
		if a.ID == r.AssetID {
			if a.Missing || (a.Kind != "image" && a.Kind != "video") || a.Fingerprint != r.Fingerprint {
				return editor.Asset{}, errors.New("缩略图素材不可用或已变化，请重新定位素材")
			}
			if r.PositionUs < 0 || r.PositionUs >= 86400_000_000 || a.Kind == "image" && r.PositionUs != 0 || a.Kind == "video" && r.PositionUs >= a.DurationUs {
				return editor.Asset{}, errors.New("缩略图源位置无效")
			}
			return a, nil
		}
	}
	return editor.Asset{}, errors.New("找不到缩略图所属素材")
}

// 只读派生请求；图片能力URL与当前工程实例绑定，不进入工程快照或撤销历史。
func (s *Service) GetAssetThumbnail(ctx context.Context, r ThumbnailRequest) (AssetThumbnail, error) {
	value, err := s.readSourceFrame(ctx, r, "overview", true, nil, false)
	if err != nil {
		return AssetThumbnail{}, err
	}
	return AssetThumbnail{AssetID: value.Evidence.AssetID, PositionUs: value.Evidence.PositionUs, URL: value.URL, Width: value.Evidence.Width, Height: value.Evidence.Height}, nil
}

func (s *Service) readSourceFrame(ctx context.Context, r ThumbnailRequest, profile string, exposeURL bool, scope *mcpProjectScope, verified bool) (sourceFrameRead, error) {
	if strings.TrimSpace(r.RequestID) == "" || len(r.RequestID) > 128 {
		return sourceFrameRead{}, errors.New("缩略图请求标识无效")
	}
	s.mu.Lock()
	if scope != nil {
		if err := scope.checkLocked(s); err != nil || !scope.allowSourceFrames {
			s.mu.Unlock()
			return sourceFrameRead{}, errors.New("当前连接未允许源画面读取或工程范围已失效")
		}
	}
	engine := s.engine
	asset, err := thumbnailAsset(engine.Snapshot(), r)
	if err != nil {
		s.mu.Unlock()
		return sourceFrameRead{}, err
	}
	s.thumbMu.Lock()
	if r.SourceEpoch != s.thumbEpoch {
		s.thumbMu.Unlock()
		s.mu.Unlock()
		return sourceFrameRead{}, errors.New("缩略图所属工程实例已切换")
	}
	if at, ok := s.thumbCancelled[r.RequestID]; ok && time.Since(at) < 30*time.Second {
		delete(s.thumbCancelled, r.RequestID)
		s.thumbMu.Unlock()
		s.mu.Unlock()
		return sourceFrameRead{}, context.Canceled
	}
	if s.thumbClosed {
		s.thumbMu.Unlock()
		s.mu.Unlock()
		return sourceFrameRead{}, errors.New("缩略图服务已关闭")
	}
	if s.thumbRequests == nil {
		s.thumbRequests = map[string]context.CancelFunc{}
	}
	if _, ok := s.thumbRequests[r.RequestID]; ok {
		s.thumbMu.Unlock()
		s.mu.Unlock()
		return sourceFrameRead{}, errors.New("缩略图请求标识重复")
	}
	if len(s.thumbRequests) >= 128 {
		s.thumbMu.Unlock()
		s.mu.Unlock()
		return sourceFrameRead{}, errors.New("缩略图请求过多，请稍后重试")
	}
	var reader *media.ThumbnailReader
	if profile == "detail" {
		if s.frameReader == nil {
			s.frameReader = media.NewSourceFrameReader(s.tools)
		}
		reader = s.frameReader
	} else {
		if s.thumbReader == nil {
			s.thumbReader = media.NewThumbnailReader(s.tools)
		}
		reader = s.thumbReader
	}
	epoch := s.thumbEpoch
	child, cancel := context.WithCancel(ctx)
	s.thumbRequests[r.RequestID] = cancel
	s.thumbMu.Unlock()
	s.mu.Unlock()
	defer func() { cancel(); s.thumbMu.Lock(); delete(s.thumbRequests, r.RequestID); s.thumbMu.Unlock() }()
	var proof os.FileInfo
	if verified {
		proof, err = s.verifyFrameSource(child, asset, epoch)
		if err != nil {
			return sourceFrameRead{}, err
		}
	}
	value, err := reader.Read(child, asset, r.PositionUs)
	if err != nil {
		return sourceFrameRead{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope != nil {
		if err := scope.checkLocked(s); err != nil || !scope.allowSourceFrames {
			return sourceFrameRead{}, errors.New("源画面所属连接已失效")
		}
	}
	if verified {
		after, statErr := os.Stat(asset.Path)
		if statErr != nil || !media.SameSourceFile(proof, after) {
			return sourceFrameRead{}, errors.New("源文件已变化，未将画面标为原指纹证据")
		}
	}
	current, err := thumbnailAsset(s.engine.Snapshot(), r)
	s.thumbMu.Lock()
	currentEpoch := s.thumbEpoch
	s.thumbMu.Unlock()
	if err != nil || engine != s.engine || epoch != currentEpoch || current.Path != asset.Path || child.Err() != nil {
		return sourceFrameRead{}, errors.New("缩略图读取已取消或源工程已变化")
	}
	sum := sha256.Sum256(value.JPEG)
	imageHash := fmt.Sprintf("%x", sum)
	version := profile + "-jpeg-v1"
	evidence := SourceFrameEvidence{AssetID: asset.ID, AssetName: asset.Name, SourceFingerprint: asset.Fingerprint, PositionUs: value.PositionUs, RequestedPositionUs: r.PositionUs, ImageSHA256: imageHash, Width: value.Width, Height: value.Height, Profile: profile, RecipeVersion: version}
	evidence.ID = frameEvidenceID(evidence)
	result := sourceFrameRead{Evidence: evidence, JPEG: value.JPEG}
	if exposeURL {
		register := s.files.RegisterJPEG
		if profile == "detail" {
			register = s.files.RegisterSourceJPEG
		}
		url, err := register(fmt.Sprintf("source-%s-%d-%x", profile, epoch, sum), value.JPEG)
		if err != nil {
			return sourceFrameRead{}, err
		}
		result.URL = url
	}
	return result, nil
}

func (s *Service) CancelThumbnailRequest(id string) {
	s.thumbMu.Lock()
	defer s.thumbMu.Unlock()
	if cancel := s.thumbRequests[id]; cancel != nil {
		cancel()
		return
	}
	if s.thumbClosed || id == "" || len(id) > 128 {
		return
	}
	if s.thumbCancelled == nil {
		s.thumbCancelled = map[string]time.Time{}
	}
	for key, at := range s.thumbCancelled {
		if time.Since(at) >= 30*time.Second {
			delete(s.thumbCancelled, key)
		}
	}
	if len(s.thumbCancelled) >= 256 {
		var oldestKey string
		var oldest time.Time
		for key, at := range s.thumbCancelled {
			if oldest.IsZero() || at.Before(oldest) {
				oldestKey, oldest = key, at
			}
		}
		delete(s.thumbCancelled, oldestKey)
	}
	s.thumbCancelled[id] = time.Now()
}

func (s *Service) resetThumbnails(closed bool) {
	s.resetSourceOCR(closed)
	s.cancelFrameCapture()
	s.cancelPreviewProxies()
	s.cancelShotIndexes()
	s.thumbMu.Lock()
	defer s.thumbMu.Unlock()
	s.thumbClosed = closed
	s.thumbEpoch++
	for _, cancel := range s.thumbRequests {
		cancel()
	}
	if s.frameReader != nil {
		s.frameReader.Close()
		s.frameReader = nil
	}
	if s.thumbReader != nil {
		s.thumbReader.Close()
		s.thumbReader = nil
	}
}
