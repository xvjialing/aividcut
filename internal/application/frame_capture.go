package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/playback"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var captureIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

type FrameCaptureInput struct {
	ID              string `json:"id"`
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	SourceEpoch     uint64 `json:"sourceEpoch"`
	Kind            string `json:"kind"`
	Frame           int64  `json:"frame"`
	AssetID         string `json:"assetId,omitempty"`
	Fingerprint     string `json:"fingerprint,omitempty"`
	PositionUs      int64  `json:"positionUs,omitempty"`
}
type FrameCaptureOrigin struct {
	Version     string               `json:"version"`
	Input       FrameCaptureInput    `json:"input"`
	Signature   string               `json:"signature"`
	ProjectHash string               `json:"projectHash"`
	Sources     []FrameCaptureSource `json:"sources"`
	Asset       editor.Asset         `json:"asset"`
	Renderer    string               `json:"renderer"`
	Approximate bool                 `json:"approximate"`
	Applied     bool                 `json:"applied"`
}
type FrameCaptureSource struct {
	AssetID     string `json:"assetId"`
	Fingerprint string `json:"fingerprint"`
}
type FrameCaptureResult struct {
	Bootstrap Bootstrap          `json:"bootstrap"`
	AssetID   string             `json:"assetId"`
	Imported  bool               `json:"imported"`
	Origin    FrameCaptureOrigin `json:"origin"`
	Duplicate bool               `json:"duplicate"`
}

func (s *Service) CaptureFrameAsset(parent context.Context, input FrameCaptureInput) (FrameCaptureResult, error) {
	var empty FrameCaptureResult
	if !captureIDPattern.MatchString(input.ID) || (input.Kind != "source" && input.Kind != "timeline") {
		return empty, errors.New("取图需要有效操作标识和明确来源")
	}
	if input.Frame < 0 || input.PositionUs < 0 || input.Kind == "timeline" && (input.AssetID != "" || input.Fingerprint != "" || input.PositionUs != 0) || input.Kind == "source" && input.Frame != 0 {
		return empty, errors.New("取图参数不能混用源位置和时间线帧")
	}
	s.generationMu.Lock()
	state, err := s.generationLocked()
	if err == nil {
		state.auxWG.Add(1)
	}
	s.generationMu.Unlock()
	if err != nil {
		return empty, err
	}
	defer state.auxWG.Done()
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	s.captureMu.Lock()
	if s.captureActive != nil {
		s.captureMu.Unlock()
		return empty, errors.New("已有画面正在保存，请等待或取消后重试")
	}
	s.captureActive = cancel
	s.captureActiveID = input.ID
	s.captureActiveProject = input.ProjectID
	s.captureActiveEpoch = input.SourceEpoch
	s.captureMu.Unlock()
	defer func() { s.captureMu.Lock(); s.captureActive = nil; s.captureMu.Unlock() }()
	s.mu.Lock()
	project, engine := s.engine.Snapshot(), s.engine
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	s.mu.Unlock()
	if project.ID != input.ProjectID || epoch != input.SourceEpoch {
		return empty, errors.New("取图所属工程实例已切换")
	}
	raw, _ := json.Marshal(input)
	signature := fmt.Sprintf("%x", sha256.Sum256(raw))
	dir := filepath.Join(state.captures, input.ID)
	metaPath := filepath.Join(dir, "origin.json")
	for _, folder := range []string{state.captures, dir} {
		if info, e := os.Lstat(folder); e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return empty, errors.New("取图目录不是私有普通目录")
		} else if e != nil && !os.IsNotExist(e) {
			return empty, e
		}
	}
	if info, e := os.Lstat(metaPath); e == nil {
		if !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return empty, errors.New("取图记录损坏，请新建操作")
		}
		data, e := os.ReadFile(metaPath)
		if e != nil {
			return empty, e
		}
		var old FrameCaptureOrigin
		if json.Unmarshal(data, &old) != nil || old.Signature != signature || old.Input != input || old.Asset.ID != "captured-"+input.ID || old.Asset.Kind != "image" || old.Version != "frame-capture-v1" {
			return empty, errors.New("取图操作标识已用于其它参数，请新建操作")
		}
		expectedName := "frame.png"
		if input.Kind == "source" {
			expectedName = "frame.jpg"
		}
		if filepath.Clean(old.Asset.Path) != filepath.Join(dir, expectedName) {
			return empty, errors.New("取图文件位置与记录不符")
		}
		info, e := os.Lstat(old.Asset.Path)
		if e != nil || !info.Mode().IsRegular() {
			return empty, errors.New("取图文件已缺失或类型变化")
		}
		if e = verifySourceFingerprint(ctx, old.Asset.Path, old.Asset.Fingerprint); e != nil {
			return empty, e
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.thumbMu.Lock()
		currentEpoch := s.thumbEpoch
		s.thumbMu.Unlock()
		if s.engine != engine || currentEpoch != input.SourceEpoch {
			return empty, errors.New("工程实例已变化")
		}
		if err = ctx.Err(); err != nil {
			return empty, err
		}
		current := s.engine.Snapshot()
		at := assetIndex(current, old.Asset.ID)
		if old.Applied || at >= 0 {
			if at >= 0 && current.Assets[at].Fingerprint != old.Asset.Fingerprint {
				return empty, errors.New("取图素材标识已被其它内容占用")
			}
			return FrameCaptureResult{Bootstrap: s.bootstrapLocked(), AssetID: old.Asset.ID, Imported: at >= 0, Origin: old, Duplicate: true}, nil
		}
		return empty, errors.New("旧取图尚未完成，请使用新操作标识")
	} else if !os.IsNotExist(e) {
		return empty, e
	}
	if project.Revision != input.ProjectRevision {
		return empty, errors.New("工程版本已变化，请重新核对画面")
	}
	sources := []editor.Asset{}
	if input.Kind == "source" {
		at := assetIndex(project, input.AssetID)
		if at < 0 {
			return empty, errors.New("源素材不存在")
		}
		a := project.Assets[at]
		if a.Kind != "video" && a.Kind != "image" || a.Missing || a.Fingerprint == "" || a.Fingerprint != input.Fingerprint {
			return empty, errors.New("源素材不可用或指纹已变化")
		}
		sources = append(sources, a)
	} else {
		if input.Frame >= editor.DurationFrames(project) {
			return empty, errors.New("时间线取图必须在有效工程帧内")
		}
		ids := map[string]bool{}
		for _, c := range project.Clips {
			if c.AssetID != "" {
				ids[c.AssetID] = true
			}
			if c.LUT != nil {
				ids[c.LUT.AssetID] = true
			}
		}
		for _, a := range project.Assets {
			if ids[a.ID] {
				if a.Missing {
					return empty, errors.New("工程中有缺失源，不能冒充完整合成画面")
				}
				sources = append(sources, a)
			}
		}
	}
	proofs := map[string]os.FileInfo{}
	origin := FrameCaptureOrigin{Version: "frame-capture-v1", Input: input, Signature: signature, ProjectHash: projectHash(project), Sources: []FrameCaptureSource{}}
	for i := range sources {
		a := sources[i]
		if a.Fingerprint == "" && a.Kind == "lut" {
			value, e := media.LocalFileFingerprint(ctx, a.Path)
			if e != nil {
				return empty, e
			}
			a.Fingerprint = value
			sources[i] = a
		}
		if a.Fingerprint == "" {
			return empty, errors.New("取图源缺少内容指纹，请重新导入核对")
		}
		proof, e := s.verifyFrameSource(ctx, a, input.SourceEpoch)
		if e != nil {
			return empty, e
		}
		proofs[a.ID] = proof
		origin.Sources = append(origin.Sources, FrameCaptureSource{AssetID: a.ID, Fingerprint: a.Fingerprint})
	}
	if err = os.MkdirAll(state.captures, 0700); err != nil {
		return empty, err
	}
	rootInfo, err := os.Lstat(state.captures)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return empty, errors.New("取图资料目录不可用")
	}
	staging, err := os.MkdirTemp(state.captures, ".capture-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(staging)
	extension := ".png"
	if input.Kind == "source" {
		extension = ".jpg"
	}
	path := filepath.Join(staging, "frame"+extension)
	if input.Kind == "source" {
		sample, e := s.tools.ExtractSourceFrame(ctx, sources[0], input.PositionUs)
		if e != nil {
			return empty, e
		}
		if err = os.WriteFile(path, sample.JPEG, 0600); err != nil {
			return empty, err
		}
		origin.Renderer = "source-detail-jpeg"
		origin.Approximate = true
	} else if config := s.nativeExportConfig.Load(); config != nil && config.WorkerPath != "" {
		cfg := *config
		cfg.OfflineRender = true
		cfg.RenderQuality = "full"
		cfg.VisualSources = nil
		session, e := playback.Start(ctx, cfg)
		if e != nil {
			return empty, e
		}
		defer session.Close()
		if _, e = session.Load(ctx, project); e != nil {
			return empty, e
		}
		frame, e := session.Preview(ctx, input.Frame)
		if e != nil {
			return empty, e
		}
		if frame.Position != input.Frame || frame.Width != project.Settings.Width || frame.Height != project.Settings.Height || len(frame.RGBA) != frame.Width*frame.Height*4 {
			return empty, errors.New("合成帧与原画幅或位置不一致")
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return empty, e
		}
		e = png.Encode(f, &image.RGBA{Pix: frame.RGBA, Stride: frame.Width * 4, Rect: image.Rect(0, 0, frame.Width, frame.Height)})
		closeErr := f.Close()
		if e != nil {
			return empty, e
		}
		if closeErr != nil {
			return empty, closeErr
		}
		origin.Renderer = "native-full-original"
	} else {
		if err = s.tools.PreviewFrame(ctx, project, input.Frame, path); err != nil {
			return empty, err
		}
		origin.Renderer = "ffmpeg-full-original"
	}
	asset, err := s.tools.Probe(ctx, path)
	if err != nil {
		return empty, err
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() >= 50_000_000 || asset.Width*asset.Height > 32_000_000 {
		return empty, errors.New("取图超过参考图片字节/像素预算")
	}
	asset.ID = "captured-" + input.ID
	asset.Name = fmt.Sprintf("合成参考 · 帧%d（含字幕/LUT）", input.Frame)
	if input.Kind == "source" {
		asset.Name = fmt.Sprintf("源参考 · %s · %.3f秒", sources[0].Name, float64(input.PositionUs)/1e6)
	}
	asset.Path = filepath.Join(dir, "frame"+extension)
	origin.Asset = asset
	for _, a := range sources {
		if err = checkFrameProof(a.Path, proofs[a.ID]); err != nil {
			return empty, err
		}
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	encoded, _ := json.MarshalIndent(origin, "", "  ")
	if err = os.WriteFile(filepath.Join(staging, "origin.json"), encoded, 0600); err != nil {
		return empty, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.thumbMu.Lock()
	currentEpoch := s.thumbEpoch
	s.thumbMu.Unlock()
	_, revision := s.engine.Identity()
	if s.engine != engine || revision != input.ProjectRevision || currentEpoch != input.SourceEpoch {
		return empty, errors.New("保存画面期间工程已变化，未导入取图")
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if _, err = os.Lstat(dir); !os.IsNotExist(err) {
		return empty, errors.New("取图操作目录已存在，未覆盖")
	}
	if err = os.Rename(staging, dir); err != nil {
		return empty, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()
	if _, err = s.engine.Apply(editor.EditRequest{BaseRevision: input.ProjectRevision, IdempotencyKey: "frame-capture/" + input.ID, Label: "保存参考画面", Commands: []editor.Command{{Type: "add_asset", Asset: &asset}}}); err != nil {
		return empty, err
	}
	committed = true
	origin.Applied = true
	encoded, _ = json.MarshalIndent(origin, "", "  ")
	warnings := []string{}
	if err = os.WriteFile(metaPath, encoded, 0600); err != nil {
		warnings = append(warnings, "图片已加入工程，但来源记录完成标记写入失败；请保存工程后核对")
	}
	boot := s.bootstrapLocked()
	boot.Warnings = append(boot.Warnings, warnings...)
	return FrameCaptureResult{Bootstrap: boot, AssetID: asset.ID, Imported: true, Origin: origin}, nil
}
func (s *Service) cancelFrameCapture() {
	s.captureMu.Lock()
	cancel := s.captureActive
	s.captureMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

type FrameCaptureCancel struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	SourceEpoch uint64 `json:"sourceEpoch"`
}

func (s *Service) CancelFrameCapture(input FrameCaptureCancel) error {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	if s.captureActive == nil {
		return nil
	}
	if s.captureActiveID != input.ID || s.captureActiveProject != input.ProjectID || s.captureActiveEpoch != input.SourceEpoch {
		return errors.New("取图操作已更换，未取消新操作")
	}
	s.captureActive()
	return nil
}
