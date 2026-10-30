package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aividcut/internal/connections"
	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/generationjobs"
	"aividcut/internal/media"
	"aividcut/internal/speech"
	"aividcut/internal/speechjobs"
	"aividcut/internal/statefilelock"
)

type GenerationOptions struct {
	Directory        string
	SecretStore      connections.SecretStore
	StorageName      string
	Factory          generationjobs.Factory
	SpeechFactory    func(generation.SecretSource, speech.MediaSource) (speech.Provider, error)
	SpeechPrepare    func(context.Context, editor.Asset, string) (speechjobs.Preparation, error)
	SpeechProbe      func(context.Context, string) (editor.Asset, error)
	SpeechAudioProbe func(context.Context, string) (speechjobs.AudioProbe, error)
}

type ConnectionProfile struct {
	connections.Profile
	Warning string `json:"warning,omitempty"`
}

type ConnectionMutation struct {
	Warning string `json:"warning,omitempty"`
}

type ConnectionHealth struct {
	PendingCleanup int    `json:"pendingCleanup"`
	Storage        string `json:"storage"`
}

type GenerationAsset struct {
	generation.ImportedAsset
	URL string `json:"url,omitempty"`
}

type GenerationJob struct {
	generationjobs.Job
	Assets []GenerationAsset `json:"assets"`
}

type generationState struct {
	connections      *connections.Manager
	jobs             *generationjobs.Manager
	lock             *statefilelock.Lock
	captures         string
	assets           string
	storage          string
	speech           *speechjobs.Manager
	speechOutputs    string
	speechFactory    func(generation.SecretSource, speech.MediaSource) (speech.Provider, error)
	audioPreparation Capability
	audioImport      Capability
	ctx              context.Context
	cancel           context.CancelFunc
	auxWG            sync.WaitGroup
	replacements     map[string]*generationReplacementRecord
	placements       map[string]*generationPlacementRecord
}

// ConfigureGeneration 显式配置应用数据目录；构造只读取非秘密资料，不访问系统钥匙串。
func (s *Service) ConfigureGeneration(options GenerationOptions) error {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	if s.generationClosed {
		return errors.New("应用生成服务已关闭")
	}
	if s.generation != nil {
		return errors.New("应用生成服务已经配置")
	}
	if strings.TrimSpace(options.Directory) == "" {
		return errors.New("请指定生成服务的持久数据目录")
	}
	directory, err := filepath.Abs(options.Directory)
	if err != nil {
		return errors.New("生成服务目录无效")
	}
	lock, err := statefilelock.Acquire(filepath.Join(directory, ".lock"))
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = lock.Close()
		}
	}()
	store, storage := options.SecretStore, strings.TrimSpace(options.StorageName)
	if store == nil {
		native, err := connections.NewNativeStore()
		if err != nil {
			return err
		}
		store = native
		if storage == "" {
			storage = native.Backend()
		}
	}
	if storage == "" {
		storage = "已注入的安全凭据存储"
	}
	manager, err := connections.New(filepath.Join(directory, "connections.json"), store)
	if err != nil {
		return err
	}
	queue, err := generationjobs.New(generationjobs.Options{Directory: filepath.Join(directory, "generation"), Factory: options.Factory, Resolve: func(ctx context.Context, id string) (generationjobs.Binding, error) {
		lease, err := manager.Acquire(ctx, id)
		if err != nil {
			return generationjobs.Binding{}, err
		}
		return generationjobs.Binding{ProviderID: lease.Profile.ProviderID, ModelID: lease.Profile.ModelID, Secrets: lease, Release: func() { _ = lease.Close() }}, nil
	}})
	if err != nil {
		return err
	}
	assets, err := filepath.EvalSymlinks(filepath.Join(directory, "generation", "assets"))
	if err != nil {
		queue.Close()
		<-queue.Done()
		return errors.New("无法解析生成素材目录")
	}
	state := &generationState{connections: manager, jobs: queue, lock: lock, assets: assets, captures: filepath.Join(directory, "frame-captures"), storage: storage, speechOutputs: filepath.Join(directory, "speech", "results")}
	prepare := options.SpeechPrepare
	state.audioPreparation = Capability{Available: options.SpeechPrepare != nil || s.mediaReport.FFmpeg.Available && s.mediaReport.FFprobe.Available, Reason: "音轨准备需要可用的 FFmpeg 与 ffprobe"}
	if state.audioPreparation.Available {
		state.audioPreparation.Reason = ""
	}
	if prepare == nil {
		prepare = func(ctx context.Context, asset editor.Asset, dir string) (speechjobs.Preparation, error) {
			value, err := s.tools.PrepareSpeechAudio(ctx, asset, dir, media.SpeechAudioOptions{})
			if err != nil {
				return speechjobs.Preparation{}, err
			}
			prepared := speechjobs.Preparation{SourceAssetID: value.SourceAssetID, SourceFingerprint: value.SourceFingerprint, SourceDurationUs: value.SourceDurationUs, SampleRate: value.SampleRate, Channels: value.Channels, AudioOffsetUs: value.AudioOffsetUs, CoverageEndUs: value.CoverageEndUs, TimestampToleranceUs: value.TimestampToleranceUs, Warnings: value.Warnings, Parts: []speechjobs.Part{}}
			for _, part := range value.Parts {
				prepared.Parts = append(prepared.Parts, speechjobs.Part{ID: part.ID, Path: part.Path, Fingerprint: part.Fingerprint, OffsetUs: part.OffsetUs, DurationUs: part.DurationUs, SampleCount: part.SampleCount, AudioDurationUs: part.AudioDurationUs})
			}
			return prepared, nil
		}
	}
	probe := options.SpeechProbe
	state.audioImport = Capability{Available: probe != nil || options.SpeechAudioProbe != nil || s.mediaReport.FFprobe.Available, Reason: "配音需要 ffprobe 核对真实音频时长"}
	if state.audioImport.Available {
		state.audioImport.Reason = ""
	}
	if probe == nil {
		probe = s.tools.Probe
	}
	probeAudio := options.SpeechAudioProbe
	if probeAudio == nil && options.SpeechProbe == nil && s.tools.FFmpegPath != "" && s.tools.FFprobePath != "" {
		probeAudio = func(ctx context.Context, path string) (speechjobs.AudioProbe, error) {
			asset, err := s.tools.Probe(ctx, path)
			if err != nil {
				return speechjobs.AudioProbe{}, err
			}
			measured, err := s.tools.MeasureAudioSamples(ctx, asset)
			if err != nil {
				return speechjobs.AudioProbe{}, err
			}
			count := measured.SampleCount
			asset.AudioSampleRate = measured.SampleRate
			asset.AudioSampleCount = &count
			asset.AudioStartUs = measured.SourceStartUs
			raw, _ := json.Marshal(measured)
			return speechjobs.AudioProbe{Asset: asset, SampleRate: measured.SampleRate, Channels: measured.Channels, SampleCount: &count, RawMetadata: raw}, nil
		}
	}
	factory := options.SpeechFactory
	if factory == nil {
		factory = func(secrets generation.SecretSource, files speech.MediaSource) (speech.Provider, error) {
			return speech.NewElevenLabs(speech.Config{Secrets: secrets, Files: files})
		}
	}
	state.speechFactory = factory
	speechQueue, err := speechjobs.New(speechjobs.Options{Directory: filepath.Join(directory, "speech"), Factory: factory, Prepare: prepare, Probe: probe, ProbeAudio: probeAudio, Resolve: func(ctx context.Context, id string) (speechjobs.Binding, error) {
		lease, err := manager.Acquire(ctx, id)
		if err != nil {
			return speechjobs.Binding{}, err
		}
		return speechjobs.Binding{ProviderID: lease.Profile.ProviderID, ModelID: lease.Profile.ModelID, Secrets: lease, Release: func() { _ = lease.Close() }}, nil
	}})
	if err != nil {
		queue.Close()
		<-queue.Done()
		return err
	}
	resolvedSpeech, err := filepath.EvalSymlinks(state.speechOutputs)
	if err != nil {
		queue.Close()
		speechQueue.Close()
		<-queue.Done()
		<-speechQueue.Done()
		return errors.New("无法解析语音结果目录")
	}
	state.speechOutputs = resolvedSpeech
	state.speech = speechQueue
	state.ctx, state.cancel = context.WithCancel(context.Background())
	s.generation = state
	committed = true
	return nil
}

func (s *Service) closeGeneration() {
	s.generationMu.Lock()
	s.generationClosed = true
	state := s.generation
	s.generation = nil
	s.generationMu.Unlock()
	if state == nil {
		return
	}
	state.cancel()
	var closed sync.WaitGroup
	closed.Add(2)
	go func() { defer closed.Done(); state.jobs.Close() }()
	go func() { defer closed.Done(); state.speech.Close() }()
	done := make(chan struct{})
	go func() {
		closed.Wait()
		<-state.jobs.Done()
		<-state.speech.Done()
		state.auxWG.Wait()
		_ = state.lock.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		// 极慢的提供商/音色查询结束前，后台仍保留根锁。
	}
}

func (s *Service) generationLocked() (*generationState, error) {
	if s.generation == nil || s.generationClosed {
		return nil, errors.New("生成服务尚未配置或已关闭")
	}
	return s.generation, nil
}

func (s *Service) GetConnections() ([]ConnectionProfile, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return nil, err
	}
	result := []ConnectionProfile{}
	for _, profile := range state.connections.List() {
		result = append(result, ConnectionProfile{Profile: profile})
	}
	return result, nil
}

func pendingConnectionJob(job generationjobs.Job) bool {
	switch job.Status {
	case generationjobs.Queued, generationjobs.Submitting, generationjobs.Running, generationjobs.Waiting, generationjobs.Importing:
		return true
	case generationjobs.Unknown:
		return !job.Acknowledged
	}
	return false
}

func hasPendingConnection(state *generationState, id string) bool {
	for _, job := range state.jobs.List() {
		if job.ConnectionID == id && pendingConnectionJob(job) {
			return true
		}
	}
	if state.speech != nil {
		for _, job := range state.speech.List() {
			if job.ConnectionID != id {
				continue
			}
			switch job.Status {
			case speechjobs.Queued, speechjobs.Preparing, speechjobs.Submitting, speechjobs.Running, speechjobs.Saving, speechjobs.Cancelling:
				return true
			case speechjobs.Unknown:
				if !job.Acknowledged {
					return true
				}
			}
		}
	}
	return false
}

func (s *Service) SaveConnection(ctx context.Context, input connections.SaveInput) (ConnectionProfile, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return ConnectionProfile{}, err
	}
	if input.ID != "" {
		for _, old := range state.connections.List() {
			if old.ID != input.ID {
				continue
			}
			if input.ProviderID == "" {
				input.ProviderID = old.ProviderID
			}
			if hasPendingConnection(state, input.ID) && strings.TrimSpace(input.ModelID) != old.ModelID {
				return ConnectionProfile{}, errors.New("连接仍有关联的未完成或未核对任务，不能改变模型；可保留原模型更新密钥")
			}
		}
	}
	if strings.TrimSpace(input.ModelID) == "" {
		return ConnectionProfile{}, errors.New("请选择或配置明确的模型标识")
	}
	if input.ProviderID == speech.ElevenLabsID {
		model := strings.TrimSpace(input.ModelID)
		if model != speech.ScribeV2 && model != speech.MultilingualV2 {
			return ConnectionProfile{}, errors.New("语音模型尚未在适配器中声明")
		}
	} else if _, err := generationjobs.BuiltinProvider(input.ProviderID, strings.TrimSpace(input.ModelID), nil, nil); err != nil {
		return ConnectionProfile{}, err
	}
	profile, err := state.connections.Save(ctx, input)
	if err != nil && !connections.IsCommitted(err) {
		return ConnectionProfile{}, err
	}
	result := ConnectionProfile{Profile: profile}
	if err != nil {
		result.Warning = err.Error()
	}
	return result, nil
}

func (s *Service) DeleteConnection(ctx context.Context, id string) (ConnectionMutation, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return ConnectionMutation{}, err
	}
	if hasPendingConnection(state, id) {
		return ConnectionMutation{}, errors.New("连接仍有关联的未完成或未核对任务，请先处理原任务")
	}
	err = state.connections.Delete(ctx, id)
	if err != nil && !connections.IsCommitted(err) {
		return ConnectionMutation{}, err
	}
	result := ConnectionMutation{}
	if err != nil {
		result.Warning = err.Error()
	}
	return result, nil
}

func (s *Service) GetConnectionHealth() (ConnectionHealth, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return ConnectionHealth{}, err
	}
	return ConnectionHealth{PendingCleanup: state.connections.PendingCleanupCount(), Storage: state.storage}, nil
}

func (s *Service) CleanupCredentials(ctx context.Context) (ConnectionHealth, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return ConnectionHealth{}, err
	}
	err = state.connections.Cleanup(ctx)
	return ConnectionHealth{PendingCleanup: state.connections.PendingCleanupCount(), Storage: state.storage}, err
}

func (s *Service) GetGenerationCatalog() ([]generation.Capability, error) {
	return generationjobs.Catalogue()
}

func (s *Service) SubmitGeneration(ctx context.Context, input generationjobs.SubmitInput) (GenerationJob, error) {
	return s.submitGenerationForMCP(ctx, input, nil, 0)
}

func (s *Service) submitGenerationForMCP(ctx context.Context, input generationjobs.SubmitInput, scope *mcpProjectScope, revision int64) (GenerationJob, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return GenerationJob{}, err
	}
	s.mu.Lock()
	project := s.engine.Snapshot()
	if input.Context != nil {
		c := input.Context
		s.thumbMu.Lock()
		epoch := s.thumbEpoch
		s.thumbMu.Unlock()
		if c.ProjectID != project.ID || c.Revision != project.Revision || c.SourceEpoch != nil && *c.SourceEpoch != epoch {
			s.mu.Unlock()
			return GenerationJob{}, errors.New("工程或参考素材已变化，请重新核对生成请求")
		}
	}
	if scope != nil {
		if err := scope.checkRequestLocked(s); err != nil {
			s.mu.Unlock()
			return GenerationJob{}, err
		}
		if project.Revision != revision {
			s.mu.Unlock()
			return GenerationJob{}, errors.New("工程已变化，请重新核对本次生成请求")
		}
	}
	s.mu.Unlock()
	if input.Request.ParentRecipeID != "" {
		parent, e := state.jobs.Get(input.Request.ParentRecipeID)
		if e != nil || parent.ProjectID != project.ID {
			return GenerationJob{}, errors.New("父配方不属于当前工程的已保存任务")
		}
		if _, e = state.jobs.CompletedRecipe(parent.ID); e != nil {
			return GenerationJob{}, e
		}
	}
	job, err := state.jobs.Submit(ctx, project, input)
	if err != nil {
		return GenerationJob{}, err
	}
	return s.generationViewLocked(state, job), nil
}

func (s *Service) ListGenerationJobs() ([]GenerationJob, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return nil, err
	}
	return s.generationListViewLocked(state), nil
}

type GenerationSyncInput struct {
	Fingerprint string `json:"fingerprint"`
}
type GenerationSync struct {
	Fingerprint string           `json:"fingerprint"`
	Changed     bool             `json:"changed"`
	Jobs        *[]GenerationJob `json:"jobs,omitempty"`
}

func (s *Service) SyncGenerationJobs(ctx context.Context, input GenerationSyncInput) (GenerationSync, error) {
	if input.Fingerprint != "" {
		decoded, err := hex.DecodeString(input.Fingerprint)
		if err != nil || len(decoded) != sha256.Size || len(input.Fingerprint) != sha256.Size*2 {
			return GenerationSync{}, errors.New("生成任务摘要无效，请重新读取完整列表")
		}
	}
	if err := ctx.Err(); err != nil {
		return GenerationSync{}, err
	}
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return GenerationSync{}, err
	}
	views := s.generationListViewLocked(state)
	data, err := json.Marshal(views)
	if err != nil {
		return GenerationSync{}, errors.New("无法编码生成任务视图")
	}
	if err = ctx.Err(); err != nil {
		return GenerationSync{}, err
	}
	sum := sha256.Sum256(data)
	fingerprint := hex.EncodeToString(sum[:])
	result := GenerationSync{Fingerprint: fingerprint, Changed: input.Fingerprint != fingerprint}
	if result.Changed {
		result.Jobs = &views
	}
	return result, nil
}

func (s *Service) generationListViewLocked(state *generationState) []GenerationJob {
	s.mu.Lock()
	project := s.engine.Snapshot()
	s.mu.Unlock()
	result := []GenerationJob{}
	for _, job := range state.jobs.List() {
		result = append(result, s.generationViewForProject(state, job, project))
	}
	return result
}

// 归档只管理本地任务记录，不移动媒体或修改当前工程。
func (s *Service) ListArchivedGenerationJobs(ctx context.Context, input generationjobs.ArchivePageInput) (generationjobs.ArchivePage, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return generationjobs.ArchivePage{}, err
	}
	return state.jobs.ListArchived(ctx, input)
}
func (s *Service) ArchiveGenerationJob(ctx context.Context, id string) (generationjobs.ArchivedRecord, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return generationjobs.ArchivedRecord{}, err
	}
	return state.jobs.Archive(ctx, id)
}
func (s *Service) RestoreArchivedGenerationJob(ctx context.Context, id string) (GenerationJob, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return GenerationJob{}, err
	}
	job, err := state.jobs.RestoreArchived(ctx, id)
	if err != nil {
		return GenerationJob{}, err
	}
	return s.generationViewLocked(state, job), nil
}

func (s *Service) StopGenerationWaiting(id string) (GenerationJob, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return GenerationJob{}, err
	}
	job, err := state.jobs.StopWaiting(id)
	if err != nil {
		return GenerationJob{}, err
	}
	return s.generationViewLocked(state, job), nil
}

func (s *Service) ResumeGeneration(ctx context.Context, id string) (GenerationJob, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return GenerationJob{}, err
	}
	job, err := state.jobs.Resume(ctx, id)
	if err != nil {
		return GenerationJob{}, err
	}
	return s.generationViewLocked(state, job), nil
}

func (s *Service) AcknowledgeGenerationUnknown(id string) (GenerationJob, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return GenerationJob{}, err
	}
	job, err := state.jobs.AcknowledgeUnknown(id)
	if err != nil {
		return GenerationJob{}, err
	}
	return s.generationViewLocked(state, job), nil
}

func generatedPath(state *generationState, job generationjobs.Job, asset generation.ImportedAsset) (string, os.FileInfo, error) {
	if asset.RecipeID != job.ID || asset.ID == "" || asset.Path == "" || (asset.Kind != "image" && asset.Kind != "video") {
		return "", nil, errors.New("生成结果身份无效")
	}
	if asset.Kind == "video" && asset.MIME != "video/mp4" || asset.Kind == "image" && asset.MIME != "image/png" && asset.MIME != "image/jpeg" && asset.MIME != "image/webp" {
		return "", nil, errors.New("生成结果的媒体格式未受支持")
	}
	base := filepath.Join(state.assets, job.ID)
	path, err := filepath.Abs(asset.Path)
	if err != nil {
		return "", nil, errors.New("生成结果路径无效")
	}
	within := func(path string) bool {
		rel, err := filepath.Rel(base, path)
		return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if !within(path) {
		return "", nil, errors.New("生成结果不在原任务目录内")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() != asset.Bytes || info.Size() > 512<<20 {
		return "", nil, errors.New("生成结果文件缺失、类型或大小不一致")
	}
	if asset.Kind == "image" && info.Size() > 50<<20 {
		return "", nil, errors.New("生成图片超过允许大小")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !within(resolved) {
		return "", nil, errors.New("生成结果路径越出原任务目录")
	}
	return path, info, nil
}

func (s *Service) generationViewLocked(state *generationState, job generationjobs.Job) GenerationJob {
	s.mu.Lock()
	project := s.engine.Snapshot()
	s.mu.Unlock()
	return s.generationViewForProject(state, job, project)
}

func (s *Service) generationViewForProject(state *generationState, job generationjobs.Job, project editor.Project) GenerationJob {
	view := GenerationJob{Job: job, Assets: []GenerationAsset{}}
	view.Imported, view.ImportedAssetIDs = false, []string{}
	for _, asset := range job.Assets {
		item := GenerationAsset{ImportedAsset: asset}
		if path, _, err := generatedPath(state, job, asset); err == nil {
			item.URL, _ = s.files.Register("generated-"+asset.ID, path)
		} else {
			// 文件被移走或改为越界链接后，同时撤销之前已发出的会话 URL。
			s.files.Unregister("generated-" + asset.ID)
		}
		view.Assets = append(view.Assets, item)
		if project.ID != job.ProjectID {
			continue
		}
		index := assetIndex(project, asset.ID)
		if index < 0 {
			continue
		}
		actual := project.Assets[index]
		if actual.Kind != asset.Kind || actual.Fingerprint != "sha256:"+asset.SHA256 || actual.Missing {
			continue
		}
		if info, err := os.Stat(actual.Path); err == nil && info.Mode().IsRegular() && info.Size() == asset.Bytes {
			view.ImportedAssetIDs = append(view.ImportedAssetIDs, asset.ID)
		}
	}
	sort.Strings(view.ImportedAssetIDs)
	view.Imported = len(job.Assets) > 0 && len(view.ImportedAssetIDs) == len(job.Assets)
	return view
}

func (s *Service) ImportGenerationResult(ctx context.Context, id string) (Bootstrap, error) {
	return s.importGenerationAssets(ctx, id, nil, true)
}

func (s *Service) ImportGenerationAssets(ctx context.Context, id string, assetIDs []string) (Bootstrap, error) {
	return s.importGenerationAssets(ctx, id, assetIDs, false)
}

func (s *Service) importGenerationAssets(ctx context.Context, id string, assetIDs []string, all bool) (Bootstrap, error) {
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return Bootstrap{}, err
	}
	job, err := state.jobs.Get(id)
	if err != nil {
		return Bootstrap{}, err
	}
	if job.Status != generationjobs.Succeeded || len(job.Assets) == 0 {
		return Bootstrap{}, errors.New("只有已成功保存在本机的生成结果可以加入工程")
	}
	selected := map[string]bool{}
	for _, id := range assetIDs {
		if id == "" || selected[id] {
			return Bootstrap{}, errors.New("请选择不重复的生成素材标识")
		}
		selected[id] = true
	}
	if all {
		for _, asset := range job.Assets {
			selected[asset.ID] = true
		}
	}
	if len(selected) == 0 {
		return Bootstrap{}, errors.New("请至少选择一个生成候选")
	}
	s.mu.Lock()
	engine, initial := s.engine, s.engine.Snapshot()
	s.mu.Unlock()
	if initial.ID != job.ProjectID {
		return Bootstrap{}, errors.New("当前工程不是生成请求的原目标工程，请先打开原工程")
	}
	verified := []editor.Asset{}
	for _, candidate := range job.Assets {
		if !selected[candidate.ID] {
			continue
		}
		delete(selected, candidate.ID)
		asset, err := s.probeGeneratedAsset(ctx, state, job, candidate)
		if err != nil {
			return Bootstrap{}, err
		}
		verified = append(verified, asset)
	}
	if len(selected) != 0 {
		return Bootstrap{}, errors.New("选择的生成素材不属于此任务")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.engine.Snapshot()
	if s.engine != engine || current.ID != job.ProjectID {
		return Bootstrap{}, errors.New("读取生成结果期间工程已切换，请在原工程重新导入")
	}
	commands := []editor.Command{}
	ids := []string{}
	for _, asset := range verified {
		ids = append(ids, asset.ID)
		if index := assetIndex(current, asset.ID); index >= 0 {
			if current.Assets[index].Fingerprint != asset.Fingerprint || current.Assets[index].Kind != asset.Kind {
				return Bootstrap{}, errors.New("工程中已存在同标识的不同素材，未覆盖工程")
			}
			continue
		}
		commands = append(commands, editor.Command{Type: "add_asset", Asset: &asset})
	}
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if len(commands) > 0 {
		if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: current.Revision, IdempotencyKey: editor.NewID("generated-import"), Label: "导入生成素材", Commands: commands}); err != nil {
			return Bootstrap{}, err
		}
	}
	// 工程是实际导入状态的唯一来源，任务标记只是持久提示；Undo 后展示会重新计算。
	_, _ = state.jobs.MarkImported(id, ids)
	return s.bootstrapLocked(), nil
}

func (s *Service) probeGeneratedAsset(ctx context.Context, state *generationState, job generationjobs.Job, candidate generation.ImportedAsset) (editor.Asset, error) {
	if err := ctx.Err(); err != nil {
		return editor.Asset{}, err
	}
	path, before, err := generatedPath(state, job, candidate)
	if err != nil {
		return editor.Asset{}, err
	}
	hash, err := hex.DecodeString(candidate.SHA256)
	if err != nil || len(hash) != 32 {
		return editor.Asset{}, errors.New("生成结果缺少有效 SHA-256 指纹")
	}
	file, err := os.Open(path)
	if err != nil {
		return editor.Asset{}, errors.New("无法读取生成结果")
	}
	header := make([]byte, 512)
	n, _ := file.Read(header)
	_ = file.Close()
	if http.DetectContentType(header[:n]) != candidate.MIME {
		return editor.Asset{}, errors.New("生成结果真实格式与声明不一致")
	}
	asset, err := s.tools.Probe(ctx, path)
	if err != nil {
		return editor.Asset{}, fmt.Errorf("生成素材探测失败：%w", err)
	}
	if asset.Kind != candidate.Kind || asset.Fingerprint != "sha256:"+candidate.SHA256 {
		return editor.Asset{}, errors.New("生成素材的类型或 SHA-256 校验不符，未修改工程")
	}
	if asset.Width < 1 || asset.Height < 1 || asset.Kind == "image" && int64(asset.Width)*int64(asset.Height) > 16_777_216 {
		return editor.Asset{}, errors.New("生成素材缺少有效画面尺寸或图片尺寸超过安全限制")
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return editor.Asset{}, errors.New("读取生成结果期间文件发生变化")
	}
	asset.ID, asset.Name = candidate.ID, candidate.Name
	asset.URL, asset.ThumbnailURL, asset.Missing = "", "", false
	return asset, nil
}
