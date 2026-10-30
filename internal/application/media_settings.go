package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/mediatools"
	"aividcut/internal/playback"
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

type MediaToolsView struct {
	Saved           mediatools.Config      `json:"saved"`
	Active          mediatools.Config      `json:"active"`
	Origins         map[string]string      `json:"origins"`
	RestartRequired bool                   `json:"restartRequired"`
	Warning         string                 `json:"warning,omitempty"`
	Capabilities    media.CapabilityReport `json:"capabilities"`
}
type MediaToolsInspection struct {
	ID           string                 `json:"id"`
	Settings     mediatools.Config      `json:"settings"`
	CanSave      bool                   `json:"canSave"`
	Errors       map[string]string      `json:"errors"`
	Capabilities media.CapabilityReport `json:"capabilities"`
	Native       *playback.Capabilities `json:"native"`
	NativeReason string                 `json:"nativeReason"`
	CheckedAt    time.Time              `json:"checkedAt"`
}
type toolProof struct {
	path string
	info os.FileInfo
}
type toolInspection struct {
	runtimeStamp string
	view         MediaToolsInspection
	proofs       []toolProof
	stamp        string
	expires      time.Time
}
type mediaSettingsState struct {
	runtimeData  string
	executable   string
	mu           sync.Mutex
	check        sync.Mutex
	store        *mediatools.Store
	saved, start mediatools.Config
	active       mediatools.Config
	origins      map[string]string
	warning      string
	inspections  map[string]toolInspection
	ctx          context.Context
	cancel       context.CancelFunc
	closed       bool
}

func NewWithMediaSettings(cacheDir, directory string) (*Service, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return NewWithMediaSettingsAtExecutable(cacheDir, directory, executable)
}
func NewWithMediaSettingsAtExecutable(cacheDir, directory, executable string) (*Service, error) {
	store, err := mediatools.Open(directory)
	if err != nil {
		return nil, err
	}
	saved, warning := store.Load()
	check, cancelCheck := context.WithTimeout(context.Background(), 20*time.Second)
	choice := selectMediaRuntime(check, saved, executable, os.Getenv)
	cancelCheck()
	wanted, origins, tools := choice.paths, choice.origins, choice.tools
	warning = strings.TrimSpace(warning + " " + choice.warning)
	s, err := New(tools, cacheDir)
	if err != nil {
		store.Close()
		return nil, err
	}
	s.mediaStartupWarning = warning
	active := wanted
	active.FFmpeg, active.FFprobe = tools.FFmpegPath, tools.FFprobePath
	ctx, cancel := context.WithCancel(context.Background())
	s.mediaSettings = &mediaSettingsState{runtimeData: choice.data, executable: executable, store: store, saved: saved, start: saved, active: active, origins: origins, warning: warning, inspections: map[string]toolInspection{}, ctx: ctx, cancel: cancel}
	return s, nil
}
func (s *Service) ConfiguredPlayback() playback.Config {
	config := PlaybackEnvironment()
	if state := s.mediaSettings; state != nil {
		config.WorkerPath = state.active.Worker
		config.MLTRepository = state.active.Repository
		config.MLTProfilesPath = state.active.Profiles
		config.MLTDataPath = state.runtimeData
	}
	return config
}
func (s *Service) GetMediaTools() (MediaToolsView, error) {
	state := s.mediaSettings
	if state == nil {
		return MediaToolsView{}, errors.New("媒体工具设置未连接，请重启客户端")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return MediaToolsView{}, errors.New("媒体工具设置已关闭")
	}
	origins := map[string]string{}
	for k, v := range state.origins {
		origins[k] = v
	}
	s.mu.Lock()
	report := s.mediaReport
	s.mu.Unlock()
	return MediaToolsView{Saved: state.saved, Active: state.active, Origins: origins, RestartRequired: !mediatools.Equal(state.saved, state.start), Warning: state.warning, Capabilities: report}, nil
}
func proofFor(path string, directory bool) (toolProof, error) {
	info, err := os.Stat(path)
	if err != nil {
		return toolProof{}, errors.New("路径不存在或无法读取")
	}
	if directory {
		if !info.IsDir() {
			return toolProof{}, errors.New("请选择目录")
		}
	} else if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return toolProof{}, errors.New("请选择本机可执行普通文件")
	}
	return toolProof{path: path, info: info}, nil
}
func sameProof(proof toolProof) bool {
	now, err := os.Stat(proof.path)
	return err == nil && os.SameFile(proof.info, now) && now.Size() == proof.info.Size() && now.ModTime().Equal(proof.info.ModTime()) && now.Mode() == proof.info.Mode()
}
func (s *Service) InspectMediaTools(ctx context.Context, input mediatools.Config) (MediaToolsInspection, error) {
	state := s.mediaSettings
	if state == nil {
		return MediaToolsInspection{}, errors.New("媒体工具设置未连接")
	}
	if err := mediatools.Validate(input); err != nil {
		return MediaToolsInspection{}, err
	}
	if !state.check.TryLock() {
		return MediaToolsInspection{}, errors.New("已有媒体工具检测正在进行")
	}
	defer state.check.Unlock()
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return MediaToolsInspection{}, errors.New("媒体工具设置已关闭")
	}
	state.mu.Unlock()
	query, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	stamp, err := state.store.Stamp()
	if err != nil {
		return MediaToolsInspection{}, err
	}
	out := MediaToolsInspection{Settings: input, Errors: map[string]string{}, CheckedAt: time.Now().UTC()}
	choice := selectMediaRuntime(query, input, state.executable, func(string) string { return "" })
	effective := choice.paths
	if choice.warning != "" {
		out.Errors["runtime"] = choice.warning
	}
	var proofs []toolProof
	for _, entry := range []struct {
		key, path string
		directory bool
	}{{"ffmpeg", effective.FFmpeg, false}, {"ffprobe", effective.FFprobe, false}, {"worker", effective.Worker, false}, {"repository", effective.Repository, true}, {"profiles", effective.Profiles, true}, {"data", choice.data, true}} {
		if entry.path == "" {
			continue
		}
		proof, err := proofFor(entry.path, entry.directory)
		if err != nil {
			out.Errors[entry.key] = err.Error()
		} else {
			proofs = append(proofs, proof)
		}
	}
	tools := choice.tools
	// 候选忽略环境覆盖，空手动路径或未随包的自动模式沿PATH；损坏随包不回落。
	for _, entry := range []struct{ key, wanted, path string }{{"ffmpeg", effective.FFmpeg, tools.FFmpegPath}, {"ffprobe", effective.FFprobe, tools.FFprobePath}} {
		if entry.wanted == "" && entry.path != "" {
			proof, err := proofFor(entry.path, false)
			if err == nil {
				proofs = append(proofs, proof)
			}
		}
	}
	if len(out.Errors) == 0 {
		out.Capabilities = tools.CheckCapabilities(query)
		if effective.FFmpeg != "" && !out.Capabilities.FFmpeg.Available {
			out.Errors["ffmpeg"] = out.Capabilities.FFmpeg.Reason
		}
		if effective.FFprobe != "" && !out.Capabilities.FFprobe.Available {
			out.Errors["ffprobe"] = out.Capabilities.FFprobe.Reason
		}
		if effective.Worker != "" {
			caps, err := playback.Inspect(query, playback.Config{WorkerPath: effective.Worker, MLTRepository: effective.Repository, MLTProfilesPath: effective.Profiles, MLTDataPath: choice.data, MaxWidth: 1280, MaxHeight: 720, Timeout: 15 * time.Second})
			if err != nil {
				out.Errors["worker"] = err.Error()
				out.NativeReason = err.Error()
			} else {
				out.Native = &caps
				if !caps.Available {
					out.Errors["worker"] = caps.Reason
				}
			}
		} else {
			out.NativeReason = "未配置原生worker；保存空值不会自动安装或扫描其它应用。"
		}
	}
	if err := query.Err(); err != nil {
		return out, errors.New("媒体工具检测已取消或超时，请检查工具后重试")
	}
	for _, proof := range proofs {
		if !sameProof(proof) {
			out.Errors["files"] = "检测期间工具或目录已变化，请重新检测"
		}
	}
	out.CanSave = len(out.Errors) == 0
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return out, errors.New("媒体工具设置已关闭")
	}
	if out.CanSave {
		for id, value := range state.inspections {
			if time.Now().After(value.expires) {
				delete(state.inspections, id)
			}
		}
		if len(state.inspections) >= 16 {
			state.inspections = map[string]toolInspection{}
		}
		out.ID = editor.NewID("media-tools")
		state.inspections[out.ID] = toolInspection{runtimeStamp: choice.stamp, view: out, proofs: proofs, stamp: stamp, expires: time.Now().Add(10 * time.Minute)}
	}
	return out, nil
}
func (s *Service) SaveMediaTools(id string) (MediaToolsView, error) {
	state := s.mediaSettings
	if state == nil {
		return MediaToolsView{}, errors.New("媒体工具设置未连接")
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return MediaToolsView{}, errors.New("媒体工具设置已关闭")
	}
	value, ok := state.inspections[id]
	if !ok || time.Now().After(value.expires) {
		state.mu.Unlock()
		return MediaToolsView{}, errors.New("检测已失效，请重新检测")
	}
	if value.view.Settings.UseBundled {
		check, cancel := context.WithTimeout(state.ctx, 20*time.Second)
		choice := selectMediaRuntime(check, value.view.Settings, state.executable, func(string) string { return "" })
		cancel()
		if choice.warning != "" || choice.stamp != value.runtimeStamp {
			state.mu.Unlock()
			return MediaToolsView{}, errors.New("随包媒体内容或清单已变化，请重新检测后保存")
		}
	}
	for _, proof := range value.proofs {
		if !sameProof(proof) {
			state.mu.Unlock()
			return MediaToolsView{}, errors.New("工具或目录已变化，请重新检测后保存")
		}
	}
	stamp, err := state.store.Stamp()
	if err != nil || stamp != value.stamp {
		state.mu.Unlock()
		return MediaToolsView{}, errors.New("媒体设置已被修改，请重新读取并检测")
	}
	if err := state.store.Save(value.view.Settings); err != nil {
		state.mu.Unlock()
		return MediaToolsView{}, err
	}
	state.saved = value.view.Settings
	state.warning = ""
	delete(state.inspections, id)
	state.mu.Unlock()
	return s.GetMediaTools()
}
func (s *Service) closeMediaSettings() {
	state := s.mediaSettings
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	state.closed = true
	state.cancel()
	state.inspections = nil
	state.store.Close()
}
