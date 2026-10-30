package application

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"sync"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/playback"
	"aividcut/internal/previewproxy"
)

type PlaybackBackend interface {
	Load(context.Context, editor.Project) (playback.Info, error)
	Play(context.Context) error
	Pause(context.Context) error
	Seek(context.Context, int64) (playback.Frame, error)
	Next(context.Context) (playback.Frame, error)
	Close() error
	Done() <-chan struct{}
}
type PlaybackOptions struct {
	Config       playback.Config
	Probe        func(context.Context, playback.Config) (playback.Capabilities, error)
	Factory      func(context.Context, playback.Config) (PlaybackBackend, error)
	CheckProject func(context.Context, playback.Config, editor.Project) error
}

func previewPlaybackFrame(native PlaybackBackend, ctx context.Context, position int64) (playback.Frame, error) {
	if pictures, ok := native.(interface {
		Preview(context.Context, int64) (playback.Frame, error)
	}); ok {
		return pictures.Preview(ctx, position)
	}
	return native.Seek(ctx, position)
}

type PlaybackCapabilities struct {
	playback.Capabilities
	ProjectSupported bool   `json:"projectSupported"`
	ProjectReason    string `json:"projectReason"`
	MaxWidth         int    `json:"maxWidth"`
	MaxHeight        int    `json:"maxHeight"`
}
type PlaybackStartInput struct {
	ProxyProfile    string `json:"proxyProfile,omitempty"`
	SourceEpoch     uint64 `json:"sourceEpoch,omitempty"`
	RenderQuality   string `json:"renderQuality,omitempty"`
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	Frame           int64  `json:"frame"`
}
type PlaybackControlInput struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Frame  *int64 `json:"frame,omitempty"`
}
type PlaybackStatus struct {
	ProxyProfile        string `json:"proxyProfile,omitempty"`
	ProxyAssets         int    `json:"proxyAssets"`
	OriginalVideoAssets int    `json:"originalVideoAssets"`
	ID                  string `json:"id"`
	ProjectID           string `json:"projectId"`
	ProjectRevision     int64  `json:"projectRevision"`
	Status              string `json:"status"`
	Frame               int64  `json:"frame"`
	DurationFrames      int64  `json:"durationFrames"`
	Width               int    `json:"width"`
	Height              int    `json:"height"`
	FPSNum              int    `json:"fpsNum"`
	FPSDen              int    `json:"fpsDen"`
	SampleRate          int    `json:"sampleRate"`
	Channels            int    `json:"channels"`
	Epoch               uint64 `json:"epoch"`
	Reason              string `json:"reason"`
	Connected           bool   `json:"connected"`
	InFlight            int    `json:"inFlight"`
}
type PlaybackStart struct {
	Status    PlaybackStatus `json:"status"`
	StreamURL string         `json:"streamUrl"`
}
type playbackState struct {
	options    PlaybackOptions
	transition chan struct{}
	probeGate  chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	current    *playbackRecord
	// 加载失败的后端也必须真实退出，才能启动替代进程。仅由transition持锁访问。
	retiring <-chan struct{}
}
type playbackEvidence struct {
	engine   *editor.Engine
	revision int64
}

func PlaybackEnvironment() playback.Config {
	return playback.Config{WorkerPath: os.Getenv("AIVIDCUT_MLT_WORKER"), MLTRepository: os.Getenv("AIVIDCUT_MLT_REPOSITORY"), MLTProfilesPath: os.Getenv("AIVIDCUT_MLT_PROFILES"), MLTDataPath: os.Getenv("AIVIDCUT_MLT_DATA"), MaxWidth: 1280, MaxHeight: 720, Timeout: 15 * time.Second}
}

func (s *Service) ConfigurePlayback(options PlaybackOptions) error {
	s.playbackMu.Lock()
	defer s.playbackMu.Unlock()
	if s.playbackClosed || s.playbackState != nil {
		return errors.New("实时媒体服务已配置或关闭")
	}
	if options.Config.MaxWidth == 0 {
		options.Config.MaxWidth = 1280
	}
	if options.Config.MaxHeight == 0 {
		options.Config.MaxHeight = 720
	}
	if options.Probe == nil {
		options.Probe = playback.Inspect
	}
	if options.Factory == nil {
		options.Factory = func(ctx context.Context, config playback.Config) (PlaybackBackend, error) {
			return playback.Start(ctx, config)
		}
	}
	if options.CheckProject == nil {
		options.CheckProject = playback.CheckProject
	}
	ctx, cancel := context.WithCancel(context.Background())
	transition := make(chan struct{}, 1)
	transition <- struct{}{}
	probeGate := make(chan struct{}, 1)
	probeGate <- struct{}{}
	s.playbackState = &playbackState{options: options, transition: transition, probeGate: probeGate, ctx: ctx, cancel: cancel}
	config := options.Config
	s.nativeExportConfig.Store(&config)
	return nil
}
func (s *Service) playbackLocked() (*playbackState, error) {
	if s.playbackClosed || s.playbackState == nil {
		return nil, errors.New("实时媒体服务尚未配置或已关闭")
	}
	return s.playbackState, nil
}

func (s *Service) GetPlaybackCapabilities(ctx context.Context) (PlaybackCapabilities, error) {
	s.playbackMu.Lock()
	state, err := s.playbackLocked()
	if err == nil {
		state.wg.Add(1)
	}
	s.playbackMu.Unlock()
	if err != nil {
		return PlaybackCapabilities{Capabilities: playback.Capabilities{Protocol: playback.Protocol, Reason: err.Error(), Display: "webview_binary_canvas_audio"}}, nil
	}
	defer state.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	s.mu.Lock()
	p, engine := s.engine.Snapshot(), s.engine
	s.mu.Unlock()
	// 连续编辑可能产生过期的能力查询；至多一个短检查进程，排队旧版本不再解码建图。
	select {
	case <-state.probeGate:
		defer func() { state.probeGate <- struct{}{} }()
	case <-ctx.Done():
		return PlaybackCapabilities{}, ctx.Err()
	}
	s.mu.Lock()
	_, revision := s.engine.Identity()
	current := s.engine == engine && revision == p.Revision
	s.mu.Unlock()
	if !current {
		return PlaybackCapabilities{}, errors.New("预览能力查询的工程已变化，请读取当前版本")
	}
	caps, err := state.options.Probe(ctx, state.options.Config)
	if ctx.Err() != nil {
		return PlaybackCapabilities{}, ctx.Err()
	}
	value := PlaybackCapabilities{Capabilities: caps, MaxWidth: state.options.Config.MaxWidth, MaxHeight: state.options.Config.MaxHeight}
	value.Protocol = playback.Protocol
	value.Display = "webview_binary_canvas_audio"
	if err != nil {
		value.Available = false
		value.Reason = "媒体工作进程检查未通过，请核对安装与插件；没有降级成假播放"
		return value, nil
	}
	if err := playback.ValidateProject(p, caps); err != nil {
		value.ProjectReason = err.Error()
	} else if err := state.options.CheckProject(ctx, state.options.Config, p); err != nil {
		value.ProjectReason = err.Error()
	} else {
		value.ProjectSupported = true
	}
	return value, nil
}

func (s *Service) StartPlayback(ctx context.Context, input PlaybackStartInput) (PlaybackStart, error) {
	if err := ctx.Err(); err != nil {
		return PlaybackStart{}, err
	}
	if input.ProxyProfile != "" {
		if _, err := previewproxy.Edge(input.ProxyProfile); err != nil {
			return PlaybackStart{}, err
		}
	}
	if _, err := playback.QualityScale(input.RenderQuality); err != nil {
		return PlaybackStart{}, err
	}
	s.playbackMu.Lock()
	state, err := s.playbackLocked()
	if err == nil {
		state.wg.Add(1)
	}
	s.playbackMu.Unlock()
	if err != nil {
		return PlaybackStart{}, err
	}
	defer state.wg.Done()
	select {
	case <-state.transition:
		defer func() { state.transition <- struct{}{} }()
	case <-ctx.Done():
		return PlaybackStart{}, ctx.Err()
	case <-state.ctx.Done():
		return PlaybackStart{}, errors.New("实时媒体服务已关闭")
	}
	if err := ctx.Err(); err != nil {
		return PlaybackStart{}, err
	}
	if err := state.ctx.Err(); err != nil {
		return PlaybackStart{}, errors.New("实时媒体服务已关闭")
	}
	if state.retiring != nil {
		select {
		case <-state.retiring:
			state.retiring = nil
		default:
			return PlaybackStart{}, errors.New("此前加载失败的媒体进程仍在释放，未启动重复实例")
		}
	}
	s.mu.Lock()
	p, engine := s.engine.Snapshot(), s.engine
	s.thumbMu.Lock()
	sourceEpoch := s.thumbEpoch
	s.thumbMu.Unlock()
	if input.ProxyProfile != "" && input.SourceEpoch != sourceEpoch {
		s.mu.Unlock()
		return PlaybackStart{}, errors.New("代理预览所属工程实例已切换")
	}
	s.mu.Unlock()
	if p.ID != input.ProjectID || p.Revision != input.ProjectRevision || input.Frame < 0 {
		return PlaybackStart{}, errors.New("工程或版本已变化，请重新读取后启动预览")
	}
	s.playbackMu.Lock()
	old := state.current
	s.playbackMu.Unlock()
	if old != nil {
		if err := old.close(); err != nil {
			return PlaybackStart{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return PlaybackStart{}, err
	}
	s.playbackMu.Lock()
	if state.current == old {
		state.current = nil
	}
	s.playbackMu.Unlock()
	runCtx, runCancel := context.WithCancel(state.ctx)
	setupCtx, setupCancel := context.WithTimeout(ctx, 45*time.Second)
	defer setupCancel()
	stop := context.AfterFunc(setupCtx, runCancel)
	committed := false
	defer func() {
		stop()
		if !committed {
			runCancel()
		}
	}()
	config := state.options.Config
	config.RenderQuality = input.RenderQuality
	originalVideos := 0
	if input.ProxyProfile != "" {
		config.VisualSources, originalVideos = s.playbackVisualSources(p, input.ProxyProfile, sourceEpoch)
	}
	native, err := state.options.Factory(runCtx, config)
	if err != nil {
		return PlaybackStart{}, err
	}
	state.retiring = native.Done()
	defer func() {
		if !committed {
			runCancel()
			_ = native.Close()
			select {
			case <-native.Done():
				state.retiring = nil
			default:
			}
		}
	}()
	info, err := native.Load(setupCtx, p)
	if err != nil {
		return PlaybackStart{}, err
	}
	if info.ProjectID != p.ID || info.ProjectRevision != p.Revision || info.DurationFrames != editor.DurationFrames(p) || info.DurationFrames < 1 || info.FPSNum != p.Settings.FPSNum || info.FPSDen != p.Settings.FPSDen || info.SampleRate != 48000 || info.Channels != 2 || info.Width < 1 || info.Height < 1 || info.Width > state.options.Config.MaxWidth || info.Height > state.options.Config.MaxHeight {
		return PlaybackStart{}, errors.New("原生媒体返回的工程、输出或采样率不一致")
	}
	position := min(input.Frame, info.DurationFrames-1)
	frame, err := previewPlaybackFrame(native, setupCtx, position)
	if err != nil {
		return PlaybackStart{}, err
	}
	if err := validPlaybackFrame(frame, info); err != nil {
		return PlaybackStart{}, err
	}
	if frame.Position != position {
		return PlaybackStart{}, errors.New("原生媒体未返回请求定位的画面")
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return PlaybackStart{}, err
	}
	record, err := s.newPlaybackRecord(runCtx, runCancel, native, engine, info, frame, base64.RawURLEncoding.EncodeToString(secret[:]))
	if err != nil {
		return PlaybackStart{}, err
	}
	record.update(func(status *PlaybackStatus) {
		status.ProxyProfile = input.ProxyProfile
		status.ProxyAssets = len(config.VisualSources)
		status.OriginalVideoAssets = originalVideos
	})
	stop()
	if setupCtx.Err() != nil || runCtx.Err() != nil {
		_ = record.closeUnstarted()
		return PlaybackStart{}, errors.New("预览加载已取消或超时")
	}
	s.playbackMu.Lock()
	s.mu.Lock()
	valid := !s.playbackClosed && s.playbackState == state && s.engine == engine
	_, revision := s.engine.Identity()
	valid = valid && revision == p.Revision
	s.mu.Unlock()
	if valid {
		state.current = record
		state.wg.Add(1)
		s.playbackEvidence.Store(&playbackEvidence{engine: engine, revision: p.Revision})
	}
	s.playbackMu.Unlock()
	if !valid {
		_ = record.closeUnstarted()
		return PlaybackStart{}, errors.New("加载预览期间工程发生变化，请重新启动")
	}
	committed = true
	state.retiring = nil
	go func() { defer state.wg.Done(); record.run() }()
	return PlaybackStart{Status: record.snapshot(), StreamURL: record.streamURL()}, nil
}

func (s *Service) GetPlaybackStatus() PlaybackStatus {
	s.playbackMu.Lock()
	defer s.playbackMu.Unlock()
	if s.playbackState == nil || s.playbackState.current == nil {
		return PlaybackStatus{Status: "idle"}
	}
	return s.playbackState.current.snapshot()
}
func (s *Service) ControlPlayback(ctx context.Context, input PlaybackControlInput) (PlaybackStatus, error) {
	s.playbackMu.Lock()
	state, err := s.playbackLocked()
	var record *playbackRecord
	if err == nil {
		record = state.current
	}
	s.playbackMu.Unlock()
	if err != nil {
		return PlaybackStatus{}, err
	}
	if record == nil || record.snapshot().ID != input.ID {
		return PlaybackStatus{}, errors.New("此预览会话已关闭或更换")
	}
	return record.control(ctx, input.Action, input.Frame)
}
func (s *Service) StopPlayback(id string) (PlaybackStatus, error) {
	s.playbackMu.Lock()
	state := s.playbackState
	var record *playbackRecord
	if state != nil {
		record = state.current
	}
	s.playbackMu.Unlock()
	if record == nil {
		return PlaybackStatus{Status: "idle"}, nil
	}
	if record.snapshot().ID != id {
		return PlaybackStatus{}, errors.New("预览会话已更换，未停止新的预览")
	}
	err := record.close()
	return record.snapshot(), err
}
func (s *Service) closePlayback() {
	s.nativeExportConfig.Store(nil)
	s.playbackMu.Lock()
	s.playbackClosed = true
	state := s.playbackState
	s.playbackState = nil
	var record *playbackRecord
	if state != nil {
		record = state.current
	}
	s.playbackMu.Unlock()
	s.playbackEvidence.Store(nil)
	if state == nil {
		return
	}
	state.cancel()
	if record != nil {
		_ = record.close()
	}
	done := make(chan struct{})
	go func() { state.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}
