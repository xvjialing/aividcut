package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/generationjobs"
	"context"
	"errors"
	"strings"
	"time"
)

type GenerationReuseInput struct {
	JobID       string `json:"jobId"`
	CandidateID string `json:"candidateId,omitempty"`
	ProjectID   string `json:"projectId"`
	SourceEpoch uint64 `json:"sourceEpoch"`
}
type GenerationReusePlan struct {
	Input                 GenerationReuseInput `json:"input"`
	ProjectRevision       int64                `json:"projectRevision"`
	ConnectionID          string               `json:"connectionId"`
	Request               generation.Request   `json:"request"`
	ReferenceFingerprints map[string]string    `json:"referenceFingerprints"`
	Warnings              []string             `json:"warnings"`
	Candidate             *editor.Asset        `json:"candidate,omitempty"`
	NeedsImport           bool                 `json:"needsImport"`
}

func (s *Service) PrepareGenerationReuse(ctx context.Context, input GenerationReuseInput) (GenerationReusePlan, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var empty GenerationReusePlan
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	state, err := s.generationLocked()
	if err != nil {
		return empty, err
	}
	s.mu.Lock()
	project, engine := s.engine.Snapshot(), s.engine
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	s.mu.Unlock()
	if input.ProjectID != project.ID || input.SourceEpoch != epoch {
		return empty, errors.New("工程实例已切换，请重新准备生成参数")
	}
	job, err := state.jobs.Get(input.JobID)
	if err != nil {
		return empty, err
	}
	if job.ProjectID != project.ID || job.Status != generationjobs.Succeeded {
		return empty, errors.New("只能复用当前工程中已成功保存的任务")
	}
	recipe, err := state.jobs.CompletedRecipe(job.ID)
	if err != nil {
		return empty, err
	}
	plan := GenerationReusePlan{Input: input, ProjectRevision: project.Revision, ConnectionID: job.ConnectionID, Request: job.Request, ReferenceFingerprints: map[string]string{}, Warnings: []string{}}
	plan.Request.ID = ""
	plan.Request.ParentRecipeID = recipe.ID
	// 适配器记录了实际默认值时优先沿用；旧配方未记录的字段保留原请求的默认语义。
	if recipe.Size != "" {
		plan.Request.Size = recipe.Size
	}
	if recipe.AspectRatio != "" {
		plan.Request.AspectRatio = recipe.AspectRatio
	}
	if recipe.Quality != "" {
		plan.Request.Quality = recipe.Quality
	}
	if recipe.OutputFormat != "" {
		plan.Request.OutputFormat = recipe.OutputFormat
	}
	if recipe.Background != "" {
		plan.Request.Background = recipe.Background
	}
	if recipe.DurationSeconds != 0 {
		plan.Request.DurationSeconds = recipe.DurationSeconds
	}
	if recipe.GenerateAudio != nil {
		plan.Request.GenerateAudio = recipe.GenerateAudio
	}
	if input.CandidateID != "" {
		var output *generation.ImportedAsset
		for i := range job.Assets {
			if job.Assets[i].ID == input.CandidateID {
				output = &job.Assets[i]
				break
			}
		}
		if output == nil || output.Kind != "image" || output.MIME != "image/png" && output.MIME != "image/jpeg" {
			return empty, errors.New("继续修改需要当前任务已保存的PNG/JPEG图片")
		}
		asset, e := s.probeGeneratedAsset(ctx, state, job, *output)
		if e != nil {
			return empty, e
		}
		plan.Candidate = &asset
		plan.NeedsImport = true
		for _, current := range project.Assets {
			if current.ID == asset.ID {
				if current.Fingerprint != asset.Fingerprint || current.Missing {
					return empty, errors.New("已有候选素材与保存结果不一致，请先核对素材")
				}
				plan.NeedsImport = false
			}
		}
		plan.Request.Mode = generation.ImageEdit
		plan.Request.Count = 1
		plan.Request.References = []generation.Reference{{AssetID: asset.ID, Role: "reference_image"}}
		plan.ReferenceFingerprints[asset.ID] = asset.Fingerprint
	} else {
		proofs := map[string]string{}
		for _, ref := range recipe.References {
			proofs[ref.AssetID] = "sha256:" + strings.TrimPrefix(ref.SHA256, "sha256:")
		}
		for _, ref := range plan.Request.References {
			expected := proofs[ref.AssetID]
			if expected == "" {
				expected = "unverified"
			}
			plan.ReferenceFingerprints[ref.AssetID] = expected
			at := assetIndex(project, ref.AssetID)
			if at < 0 || project.Assets[at].Missing || project.Assets[at].Kind != "image" || expected == "unverified" || project.Assets[at].Fingerprint != expected {
				plan.Warnings = append(plan.Warnings, "原参考图片已缺失、改变或缺少配方证明，请在创作页移除或重新选择。")
				continue
			}
			if _, e := s.verifyFrameSource(ctx, project.Assets[at], input.SourceEpoch); e != nil {
				return empty, e
			}
		}
	}
	validator, e := generationjobs.BuiltinProvider(plan.Request.ProviderID, plan.Request.ModelID, nil, nil)
	if e != nil {
		return empty, e
	}
	check := plan.Request
	check.ID = "reuse-validation"
	if e = validator.Validate(check); e != nil {
		return empty, e
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.thumbMu.Lock()
	currentEpoch := s.thumbEpoch
	s.thumbMu.Unlock()
	_, revision := s.engine.Identity()
	if s.engine != engine || revision != project.Revision || currentEpoch != epoch {
		return empty, errors.New("准备期间工程已变化，请重新核对")
	}
	return plan, nil
}
