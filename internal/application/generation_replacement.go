package application

import (
	"context"
	"errors"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/generationjobs"
	"aividcut/internal/replacements"
)

type GenerationReplacementInput struct {
	JobID           string `json:"jobId"`
	CandidateID     string `json:"candidateId"`
	ClipID          string `json:"clipId"`
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	SourceInUs      int64  `json:"sourceInUs"`
	AudioMode       string `json:"audioMode"`
	LUTMode         string `json:"lutMode"`
	SpeedMode       string `json:"speedMode"`
}

type GenerationReplacementPreview struct {
	ID              string                `json:"id"`
	ProjectID       string                `json:"projectId"`
	ProjectRevision int64                 `json:"projectRevision"`
	JobID           string                `json:"jobId"`
	CandidateID     string                `json:"candidateId"`
	ClipID          string                `json:"clipId"`
	CreatedAt       time.Time             `json:"createdAt"`
	Applied         bool                  `json:"applied"`
	Summary         string                `json:"summary"`
	Effects         []replacements.Effect `json:"effects"`
	Warnings        []string              `json:"warnings"`
	AffectedClipIDs []string              `json:"affectedClipIds"`
	OriginalClip    editor.Clip           `json:"originalClip"`
	OriginalAsset   editor.Asset          `json:"originalAsset"`
	CandidateAsset  editor.Asset          `json:"candidateAsset"`
	ReplacementClip editor.Clip           `json:"replacementClip"`
	AudioMode       string                `json:"audioMode"`
	LUTMode         string                `json:"lutMode"`
	SpeedMode       string                `json:"speedMode"`
}

type generationReplacementRecord struct {
	scopeID   string
	view      GenerationReplacementPreview
	request   editor.EditRequest
	engine    *editor.Engine
	hash      string
	sources   []editor.Asset
	candidate generation.ImportedAsset
	job       generationjobs.Job
	applying  bool
}

func sameReplacementMedia(a, b editor.Asset) bool {
	return a.Kind == b.Kind && a.Fingerprint == b.Fingerprint && a.DurationUs == b.DurationUs && a.HasAudio == b.HasAudio && a.Width == b.Width && a.Height == b.Height && a.FPSNum == b.FPSNum && a.FPSDen == b.FPSDen && !a.Missing
}

func (s *Service) PreviewGenerationReplacement(ctx context.Context, input GenerationReplacementInput) (GenerationReplacementPreview, error) {
	return s.previewGenerationReplacement(ctx, input, nil)
}
func (s *Service) previewGenerationReplacement(ctx context.Context, input GenerationReplacementInput, scope *mcpProjectScope) (GenerationReplacementPreview, error) {
	if err := ctx.Err(); err != nil {
		return GenerationReplacementPreview{}, err
	}
	s.generationMu.Lock()
	state, err := s.generationLocked()
	if err != nil {
		s.generationMu.Unlock()
		return GenerationReplacementPreview{}, err
	}
	job, err := state.jobs.Get(input.JobID)
	if err != nil {
		s.generationMu.Unlock()
		return GenerationReplacementPreview{}, err
	}
	state.auxWG.Add(1)
	s.generationMu.Unlock()
	defer state.auxWG.Done()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	if job.Status != generationjobs.Succeeded || job.ProjectID != input.ProjectID {
		return GenerationReplacementPreview{}, errors.New("替换需要当前原工程已保存成功的生成候选")
	}
	var candidate *generation.ImportedAsset
	for _, item := range job.Assets {
		if item.ID == input.CandidateID {
			copy := item
			candidate = &copy
			break
		}
	}
	if candidate == nil {
		return GenerationReplacementPreview{}, errors.New("候选不属于此生成任务")
	}
	s.mu.Lock()
	initial, engine := s.engine.Snapshot(), s.engine
	if scope != nil {
		err = scope.checkLocked(s)
	}
	s.mu.Unlock()
	if err != nil {
		return GenerationReplacementPreview{}, err
	}
	if initial.ID != input.ProjectID || initial.Revision != input.ProjectRevision {
		return GenerationReplacementPreview{}, errors.New("工程版本已经变化，请重新核对目标镜头")
	}
	var original editor.Clip
	found := false
	for _, clip := range initial.Clips {
		if clip.ID == input.ClipID {
			original, found = clip, true
			break
		}
	}
	if !found || original.Kind != "video" && original.Kind != "image" {
		return GenerationReplacementPreview{}, errors.New("请选择当前工程的视频或图片镜头")
	}
	originalIndex := assetIndex(initial, original.AssetID)
	if originalIndex < 0 {
		return GenerationReplacementPreview{}, errors.New("原镜头素材不存在")
	}
	verified, err := s.probeGeneratedAsset(ctx, state, job, *candidate)
	if err != nil {
		return GenerationReplacementPreview{}, err
	}
	draft := editor.CloneProject(initial)
	commands := []editor.Command{}
	if index := assetIndex(draft, verified.ID); index >= 0 {
		if !sameReplacementMedia(draft.Assets[index], verified) {
			return GenerationReplacementPreview{}, errors.New("工程存在同标识但不同内容或媒体参数的素材，未覆盖")
		}
		verified = draft.Assets[index]
	} else {
		draft.Assets = append(draft.Assets, verified)
		copy := verified
		commands = append(commands, editor.Command{Type: "add_asset", Asset: &copy})
	}
	plan, err := replacements.Preview(draft, replacements.Input{ClipID: input.ClipID, AssetID: verified.ID, SourceInUs: input.SourceInUs, AudioMode: input.AudioMode, LUTMode: input.LUTMode, SpeedMode: input.SpeedMode})
	if err != nil {
		return GenerationReplacementPreview{}, err
	}
	sources := []editor.Asset{}
	seen := map[string]bool{}
	for _, evidence := range plan.Evidence {
		if seen[evidence.AssetID] {
			continue
		}
		index := assetIndex(draft, evidence.AssetID)
		if index < 0 || draft.Assets[index].Fingerprint != evidence.SourceFingerprint {
			return GenerationReplacementPreview{}, errors.New("替换证据与实际素材不一致")
		}
		source := draft.Assets[index]
		if err := verifySourceFingerprint(ctx, source.Path, source.Fingerprint); err != nil {
			return GenerationReplacementPreview{}, err
		}
		seen[source.ID] = true
		sources = append(sources, source)
	}
	commands = append(commands, plan.Request.Commands...)
	request := plan.Request
	request.Commands = commands
	check, err := editor.NewEngine(initial)
	if err != nil {
		return GenerationReplacementPreview{}, err
	}
	result, err := check.Apply(request)
	if err != nil {
		return GenerationReplacementPreview{}, err
	}
	var replacement editor.Clip
	for _, clip := range result.Clips {
		if clip.ID == original.ID {
			replacement = clip
			break
		}
	}
	view := GenerationReplacementPreview{ID: plan.ID, ProjectID: initial.ID, ProjectRevision: initial.Revision, JobID: job.ID, CandidateID: verified.ID, ClipID: original.ID, CreatedAt: time.Now().UTC(), Summary: plan.Summary, Effects: plan.Effects, Warnings: plan.Warnings, AffectedClipIDs: plan.AffectedClipIDs, OriginalClip: original, OriginalAsset: initial.Assets[originalIndex], CandidateAsset: verified, ReplacementClip: replacement, AudioMode: input.AudioMode, LUTMode: input.LUTMode, SpeedMode: input.SpeedMode}
	committed := false
	defer func() {
		if !committed {
			s.files.Unregister("replacement-original-" + view.ID)
			s.files.Unregister("replacement-candidate-" + view.ID)
		}
	}()
	if view.AudioMode == "" {
		view.AudioMode = "keep_original"
	}
	if view.LUTMode == "" {
		view.LUTMode = "keep"
	}
	if view.SpeedMode == "" {
		view.SpeedMode = "preserve"
	}
	if view.OriginalAsset.Path != "" {
		view.OriginalAsset.URL, _ = s.files.Register("replacement-original-"+view.ID, view.OriginalAsset.Path)
	}
	view.CandidateAsset.URL, _ = s.files.Register("replacement-candidate-"+view.ID, view.CandidateAsset.Path)
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	if s.generationClosed || s.generation != state {
		return GenerationReplacementPreview{}, errors.New("生成服务已关闭")
	}
	s.mu.Lock()
	current := s.engine.Snapshot()
	scopeValid := scope == nil || scope.checkLocked(s) == nil
	valid := scopeValid && ctx.Err() == nil && s.engine == engine && current.ID == initial.ID && current.Revision == initial.Revision && projectHash(current) == projectHash(initial)
	s.mu.Unlock()
	if !valid {
		return GenerationReplacementPreview{}, errors.New("准备替换期间工程已变化，请重新预览")
	}
	if state.replacements == nil {
		state.replacements = map[string]*generationReplacementRecord{}
	}
	state.replacements[view.ID] = &generationReplacementRecord{scopeID: generationScopeID(scope), view: view, request: request, engine: engine, hash: projectHash(initial), sources: sources, candidate: *candidate, job: job}
	if len(state.replacements) > 80 {
		var oldestID string
		var oldest time.Time
		for id, record := range state.replacements {
			if id == view.ID || record.applying {
				continue
			}
			if oldestID == "" || record.view.CreatedAt.Before(oldest) {
				oldestID, oldest = id, record.view.CreatedAt
			}
		}
		if oldestID != "" {
			delete(state.replacements, oldestID)
			s.files.Unregister("replacement-original-" + oldestID)
			s.files.Unregister("replacement-candidate-" + oldestID)
		}
	}
	committed = true
	return view, nil
}

func (s *Service) ApplyGenerationReplacement(ctx context.Context, id string) (Bootstrap, error) {
	outcome, err := s.applyGenerationReplacement(ctx, id, nil)
	return outcome.boot, err
}
func (s *Service) applyGenerationReplacement(ctx context.Context, id string, scope *mcpProjectScope) (generationEditOutcome, error) {
	if err := ctx.Err(); err != nil {
		return generationEditOutcome{}, err
	}
	s.generationMu.Lock()
	state, err := s.generationLocked()
	if err != nil {
		s.generationMu.Unlock()
		return generationEditOutcome{}, err
	}
	record := state.replacements[id]
	if record == nil || record.applying {
		s.generationMu.Unlock()
		return generationEditOutcome{}, errors.New("替换预览不存在或正在应用")
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
		if err = checkGenerationScopeLocked(s, scope, true); err != nil {
			s.mu.Unlock()
			s.generationMu.Unlock()
			return generationEditOutcome{}, err
		}
		if s.engine != record.engine {
			s.mu.Unlock()
			s.generationMu.Unlock()
			return generationEditOutcome{}, errors.New("原工程已关闭，请重新预览")
		}
		view := s.bootstrapLocked()
		s.mu.Unlock()
		s.generationMu.Unlock()
		return generationEditOutcome{boot: view, duplicate: true, expected: record.view.ReplacementClip, candidateID: record.candidate.ID}, nil
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
	if _, err := s.probeGeneratedAsset(ctx, state, record.job, record.candidate); err != nil {
		return generationEditOutcome{}, err
	}
	for _, source := range record.sources {
		if err := verifySourceFingerprint(ctx, source.Path, source.Fingerprint); err != nil {
			return generationEditOutcome{}, err
		}
	}
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	if s.generationClosed || s.generation != state {
		return generationEditOutcome{}, errors.New("生成服务已关闭")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.engine.Snapshot()
	if err = checkGenerationScopeLocked(s, scope, true); err != nil {
		return generationEditOutcome{}, err
	}
	if s.engine != record.engine || current.ID != record.view.ProjectID || current.Revision != record.view.ProjectRevision || projectHash(current) != record.hash {
		return generationEditOutcome{}, errors.New("工程已变化，请重新预览镜头替换")
	}
	if err := ctx.Err(); err != nil {
		return generationEditOutcome{}, err
	}
	if _, err := s.engine.Apply(record.request); err != nil {
		return generationEditOutcome{}, err
	}
	record.view.Applied = true
	_, _ = state.jobs.MarkImported(record.job.ID, []string{record.candidate.ID})
	return generationEditOutcome{boot: s.bootstrapLocked(), expected: record.view.ReplacementClip, candidateID: record.candidate.ID}, nil
}
