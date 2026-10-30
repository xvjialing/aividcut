package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/generationjobs"
	"aividcut/internal/workflows"
	"context"
	"encoding/json"
	"errors"
	"time"
)

type GenerationPlacementInput struct {
	JobID           string `json:"jobId"`
	CandidateID     string `json:"candidateId"`
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	SourceEpoch     uint64 `json:"sourceEpoch"`
	StartFrame      int64  `json:"startFrame"`
	DurationFrames  int64  `json:"durationFrames"`
	AutoDuration    bool   `json:"autoDuration"`
	SourceInUs      int64  `json:"sourceInUs"`
	TrackID         string `json:"trackId,omitempty"`
	AudioMode       string `json:"audioMode"`
}
type GenerationPlacementPreview struct {
	ID              string       `json:"id"`
	ProjectID       string       `json:"projectId"`
	ProjectRevision int64        `json:"projectRevision"`
	SourceEpoch     uint64       `json:"sourceEpoch"`
	CreatedAt       time.Time    `json:"createdAt"`
	Asset           editor.Asset `json:"asset"`
	Clip            editor.Clip  `json:"clip"`
	Track           editor.Track `json:"track"`
	ImportRequired  bool         `json:"importRequired"`
	NewTrack        bool         `json:"newTrack"`
	CaptionCount    int          `json:"captionCount"`
	AudioMode       string       `json:"audioMode"`
	Summary         string       `json:"summary"`
	Warnings        []string     `json:"warnings"`
	Applied         bool         `json:"applied"`
}
type generationPlacementRecord struct {
	scopeID   string
	view      GenerationPlacementPreview
	request   editor.EditRequest
	hash      string
	source    editor.Asset
	candidate generation.ImportedAsset
	job       generationjobs.Job
	applying  bool
}

func clonePlacement(value GenerationPlacementPreview) GenerationPlacementPreview {
	data, _ := json.Marshal(value)
	var copy GenerationPlacementPreview
	_ = json.Unmarshal(data, &copy)
	return copy
}
func (s *Service) placementEpoch() uint64 {
	s.thumbMu.Lock()
	defer s.thumbMu.Unlock()
	return s.thumbEpoch
}

func (s *Service) PreviewGenerationPlacement(ctx context.Context, input GenerationPlacementInput) (GenerationPlacementPreview, error) {
	return s.previewGenerationPlacement(ctx, input, nil)
}
func (s *Service) previewGenerationPlacement(ctx context.Context, input GenerationPlacementInput, scope *mcpProjectScope) (GenerationPlacementPreview, error) {
	var empty GenerationPlacementPreview
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if input.StartFrame < 0 || input.StartFrame > 1e9 || input.SourceInUs < 0 || input.SourceInUs > 1e12 || input.DurationFrames < 0 || input.DurationFrames > 1e9 || input.AutoDuration && input.DurationFrames != 0 || input.AudioMode != "muted" && input.AudioMode != "embedded" {
		return empty, errors.New("请核对添加位置、源入点、长度与声音策略")
	}
	s.generationMu.Lock()
	state, err := s.generationLocked()
	if err != nil {
		s.generationMu.Unlock()
		return empty, err
	}
	job, err := state.jobs.Get(input.JobID)
	if err != nil {
		s.generationMu.Unlock()
		return empty, err
	}
	state.auxWG.Add(1)
	s.generationMu.Unlock()
	defer state.auxWG.Done()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	if job.Status != generationjobs.Succeeded || job.ProjectID != input.ProjectID {
		return empty, errors.New("请选择当前工程已成功保存的生成候选")
	}
	var candidate *generation.ImportedAsset
	for i := range job.Assets {
		if job.Assets[i].ID == input.CandidateID {
			candidate = &job.Assets[i]
			break
		}
	}
	if candidate == nil {
		return empty, errors.New("候选不属于当前任务")
	}
	s.mu.Lock()
	initial, engine := s.engine.Snapshot(), s.engine
	epoch := s.placementEpoch()
	if scope != nil {
		err = scope.checkLocked(s)
	}
	s.mu.Unlock()
	if err != nil {
		return empty, err
	}
	if initial.ID != input.ProjectID || initial.Revision != input.ProjectRevision || epoch != input.SourceEpoch {
		return empty, errors.New("工程或实例已变化，请重新选择添加范围")
	}
	verified, err := s.probeGeneratedAsset(ctx, state, job, *candidate)
	if err != nil {
		return empty, err
	}
	draft := editor.CloneProject(initial)
	commands := []editor.Command{}
	needed := assetIndex(draft, verified.ID) < 0
	if index := assetIndex(draft, verified.ID); index >= 0 {
		if !sameReplacementMedia(draft.Assets[index], verified) {
			return empty, errors.New("同标识素材内容或媒体参数不同，未覆盖工程")
		}
		verified = draft.Assets[index]
	} else {
		draft.Assets = append(draft.Assets, verified)
		copy := verified
		commands = append(commands, editor.Command{Type: "add_asset", Asset: &copy})
	}
	if input.AudioMode == "embedded" && (verified.Kind != "video" || !verified.HasAudio) {
		return empty, errors.New("保留声音需要视频实际包含音轨")
	}
	if input.AudioMode == "embedded" && input.TrackID != "" {
		for _, track := range draft.Tracks {
			if track.ID == input.TrackID && track.Muted {
				return empty, errors.New("目标轨已静音，不能声称保留可听的候选声音")
			}
		}
	}
	duration := input.DurationFrames
	if input.AutoDuration {
		if verified.Kind != "video" || input.SourceInUs >= verified.DurationUs {
			return empty, errors.New("完整可用长度仅适用于有效视频源窗")
		}
		duration, err = editor.USToFrames(verified.DurationUs-input.SourceInUs, initial.Settings)
		if err != nil {
			return empty, err
		}
	}
	if duration < 1 || duration > 1e9 {
		return empty, errors.New("请设置至少一帧的可用长度，视频末尾不足一帧保留在源素材中")
	}
	plan, err := workflows.Preview(draft, workflows.Input{WorkflowID: "broll", Parameters: map[string]any{"assetId": verified.ID, "startFrame": input.StartFrame, "durationFrames": duration, "sourceInUs": input.SourceInUs, "trackId": input.TrackID, "muted": input.AudioMode == "muted", "scale": 1, "offsetX": 0, "offsetY": 0}})
	if err != nil {
		return empty, err
	}
	commands = append(commands, plan.Request.Commands...)
	request := plan.Request
	request.Commands = commands
	request.Label = "添加生成候选到时间线"
	sandbox, err := editor.NewEngine(initial)
	if err != nil {
		return empty, err
	}
	after, err := sandbox.Apply(request)
	if err != nil {
		return empty, err
	}
	view := GenerationPlacementPreview{ID: plan.ID, ProjectID: initial.ID, ProjectRevision: initial.Revision, SourceEpoch: epoch, CreatedAt: time.Now().UTC(), Asset: verified, ImportRequired: needed, NewTrack: input.TrackID == "", AudioMode: input.AudioMode, Summary: plan.Summary, Warnings: append([]string{}, plan.Warnings...)}
	for _, command := range plan.Request.Commands {
		if command.Type == "add_clip" && command.Clip != nil {
			view.Clip = *command.Clip
		}
	}
	for _, track := range after.Tracks {
		if track.ID == view.Clip.TrackID {
			view.Track = track
		}
	}
	view.NewTrack = true
	for _, track := range initial.Tracks {
		if track.ID == view.Clip.TrackID {
			view.NewTrack = false
			break
		}
	}
	beforeCaptions := 0
	for _, c := range initial.Clips {
		if c.Kind == "caption" {
			beforeCaptions++
		}
	}
	for _, c := range after.Clips {
		if c.Kind == "caption" {
			view.CaptionCount++
		}
	}
	view.CaptionCount -= beforeCaptions
	if input.AudioMode == "embedded" {
		resolved, e := editor.ResolveAudioRange(view.Clip, verified, initial.Settings)
		if e != nil {
			return empty, e
		}
		if resolved.Empty {
			return empty, errors.New("所选画面源窗没有真实音频覆盖，请调整入点或不用声音")
		}
		if verified.AudioSampleCount == nil {
			view.Warnings = append(view.Warnings, "源音轨尚无实测总样本数，声音按声明的源微秒窗口取用，未标为样本无损。")
		}
	}
	if input.AutoDuration {
		view.Warnings = append(view.Warnings, "按实际源可用长度取完整工程帧；不足一帧的尾部仍保留在素材文件中。")
	}
	proof, err := s.verifyFrameSource(ctx, verified, epoch)
	if err != nil {
		return empty, err
	}
	view.Asset.URL, err = s.files.Register("placement-candidate-"+view.ID, verified.Path)
	if err != nil {
		return empty, err
	}
	committed := false
	defer func() {
		if !committed {
			s.files.Unregister("placement-candidate-" + view.ID)
		}
	}()
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.engine.Snapshot()
	if scope != nil {
		if err = scope.checkLocked(s); err != nil {
			return empty, err
		}
	}
	if s.generationClosed || s.generation != state || s.engine != engine || s.placementEpoch() != epoch || current.Revision != initial.Revision || projectHash(current) != projectHash(initial) {
		return empty, errors.New("准备添加期间工程已变化，请重新预览")
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if err = checkFrameProof(verified.Path, proof); err != nil {
		return empty, err
	}
	if state.placements == nil {
		state.placements = map[string]*generationPlacementRecord{}
	}
	for id, record := range state.placements {
		if !record.applying && (record.view.SourceEpoch != epoch || record.view.ProjectID != initial.ID || time.Since(record.view.CreatedAt) > 30*time.Minute) {
			delete(state.placements, id)
			s.files.Unregister("placement-candidate-" + id)
		}
	}
	if len(state.placements) >= 80 {
		var oldest string
		var created time.Time
		for id, r := range state.placements {
			if !r.applying && (oldest == "" || r.view.CreatedAt.Before(created)) {
				oldest, created = id, r.view.CreatedAt
			}
		}
		if oldest == "" {
			return empty, errors.New("添加预览繁忙，请稍后重试")
		}
		delete(state.placements, oldest)
		s.files.Unregister("placement-candidate-" + oldest)
	}
	state.placements[view.ID] = &generationPlacementRecord{scopeID: generationScopeID(scope), view: view, request: request, hash: projectHash(initial), source: verified, candidate: *candidate, job: job}
	committed = true
	return clonePlacement(view), nil
}

func (s *Service) ApplyGenerationPlacement(ctx context.Context, id string) (Bootstrap, error) {
	outcome, err := s.applyGenerationPlacement(ctx, id, nil)
	return outcome.boot, err
}
func (s *Service) applyGenerationPlacement(ctx context.Context, id string, scope *mcpProjectScope) (generationEditOutcome, error) {
	if err := ctx.Err(); err != nil {
		return generationEditOutcome{}, err
	}
	s.generationMu.Lock()
	state, err := s.generationLocked()
	if err != nil {
		s.generationMu.Unlock()
		return generationEditOutcome{}, err
	}
	record := state.placements[id]
	if record == nil || record.applying || time.Since(record.view.CreatedAt) > 30*time.Minute {
		s.generationMu.Unlock()
		return generationEditOutcome{}, errors.New("添加预览不存在、已过期或正在应用")
	}
	if record.scopeID != generationScopeID(scope) {
		s.generationMu.Unlock()
		return generationEditOutcome{}, generationScopeMismatch()
	}
	s.mu.Lock()
	err = checkGenerationScopeLocked(s, scope, true)
	s.mu.Unlock()
	if err != nil {
		s.generationMu.Unlock()
		return generationEditOutcome{}, err
	}
	if record.view.Applied {
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.generationMu.Unlock()
		if err = checkGenerationScopeLocked(s, scope, true); err != nil {
			return generationEditOutcome{}, err
		}
		if s.engine.Snapshot().ID != record.view.ProjectID || s.placementEpoch() != record.view.SourceEpoch {
			return generationEditOutcome{}, errors.New("原工程实例已关闭，请重新预览")
		}
		return generationEditOutcome{boot: s.bootstrapLocked(), duplicate: true, expected: record.view.Clip, candidateID: record.candidate.ID}, nil
	}
	record.applying = true
	state.auxWG.Add(1)
	s.generationMu.Unlock()
	defer state.auxWG.Done()
	defer func() { s.generationMu.Lock(); record.applying = false; s.generationMu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	if _, err = s.probeGeneratedAsset(ctx, state, record.job, record.candidate); err != nil {
		return generationEditOutcome{}, err
	}
	proof, err := s.verifyFrameSource(ctx, record.source, record.view.SourceEpoch)
	if err != nil {
		return generationEditOutcome{}, err
	}
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.engine.Snapshot()
	if err = checkGenerationScopeLocked(s, scope, true); err != nil {
		return generationEditOutcome{}, err
	}
	if s.generationClosed || s.generation != state || current.ID != record.view.ProjectID || current.Revision != record.view.ProjectRevision || s.placementEpoch() != record.view.SourceEpoch || projectHash(current) != record.hash {
		return generationEditOutcome{}, errors.New("工程已变化，请重新预览候选添加")
	}
	if err = ctx.Err(); err != nil {
		return generationEditOutcome{}, err
	}
	if err = checkFrameProof(record.source.Path, proof); err != nil {
		return generationEditOutcome{}, err
	}
	if _, err = s.engine.Apply(record.request); err != nil {
		return generationEditOutcome{}, err
	}
	record.view.Applied = true
	_, _ = state.jobs.MarkImported(record.job.ID, []string{record.candidate.ID})
	return generationEditOutcome{boot: s.bootstrapLocked(), expected: record.view.Clip, candidateID: record.candidate.ID}, nil
}
