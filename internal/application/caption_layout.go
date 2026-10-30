package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/mcpserver"
)

type CaptionLayoutInput struct {
	ProjectID          string               `json:"projectId"`
	ProjectRevision    int64                `json:"projectRevision"`
	TranscriptID       string               `json:"transcriptId"`
	TranscriptRevision int64                `json:"transcriptRevision"`
	TrackID            string               `json:"trackId,omitempty"`
	CreateNewTrack     bool                 `json:"createNewTrack,omitempty"`
	Layout             editor.CaptionLayout `json:"layout"`
}
type CaptionLayoutPreview struct {
	ID                 string               `json:"id"`
	ProjectID          string               `json:"projectId"`
	ProjectRevision    int64                `json:"projectRevision"`
	TranscriptID       string               `json:"transcriptId"`
	TranscriptRevision int64                `json:"transcriptRevision"`
	Layout             editor.CaptionLayout `json:"layout"`
	Summary            string               `json:"summary"`
	BeforeCount        int                  `json:"beforeCount"`
	AfterCount         int                  `json:"afterCount"`
	ProtectedCount     int                  `json:"protectedCount"`
	Warnings           []string             `json:"warnings"`
	Captions           []editor.Clip        `json:"captions"`
	Truncated          bool                 `json:"truncated"`
	CreatedAt          time.Time            `json:"createdAt"`
	Applied            bool                 `json:"applied"`
}
type captionLayoutRecord struct {
	view     CaptionLayoutPreview
	request  editor.EditRequest
	engine   *editor.Engine
	hash     string
	source   editor.Asset
	applying bool
}
type captionLayoutState struct {
	previews map[string]*captionLayoutRecord
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func (s *Service) captionLayoutsLocked() (*captionLayoutState, error) {
	if s.captionsClosed {
		return nil, errors.New("字幕排版服务已关闭")
	}
	if s.captionState == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s.captionState = &captionLayoutState{previews: map[string]*captionLayoutRecord{}, ctx: ctx, cancel: cancel}
	}
	return s.captionState, nil
}
func (s *Service) closeCaptionLayouts() {
	s.captionMu.Lock()
	s.captionsClosed = true
	state := s.captionState
	s.captionState = nil
	s.captionMu.Unlock()
	if state == nil {
		return
	}
	state.cancel()
	done := make(chan struct{})
	go func() { state.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}
func (s *Service) PreviewCaptionLayout(ctx context.Context, input CaptionLayoutInput) (CaptionLayoutPreview, error) {
	return s.previewCaptionLayout(ctx, input, nil)
}
func (s *Service) previewCaptionLayout(ctx context.Context, input CaptionLayoutInput, scope *mcpProjectScope) (CaptionLayoutPreview, error) {
	if err := ctx.Err(); err != nil {
		return CaptionLayoutPreview{}, err
	}
	if input.Layout.Mode != "word" && input.Layout.Mode != "readable" {
		return CaptionLayoutPreview{}, errors.New("请选择逐词显示或阅读分句")
	}
	if input.CreateNewTrack && input.TrackID != "" {
		return CaptionLayoutPreview{}, errors.New("新建字幕轨时不能同时指定已有轨道")
	}
	s.captionMu.Lock()
	state, err := s.captionLayoutsLocked()
	if err == nil {
		state.wg.Add(1)
	}
	s.captionMu.Unlock()
	if err != nil {
		return CaptionLayoutPreview{}, err
	}
	defer state.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	s.mu.Lock()
	p, engine := s.engine.Snapshot(), s.engine
	if scope != nil {
		err = scope.checkLocked(s)
	}
	s.mu.Unlock()
	if err != nil {
		return CaptionLayoutPreview{}, err
	}
	if p.ID != input.ProjectID || p.Revision != input.ProjectRevision {
		return CaptionLayoutPreview{}, errors.New("工程版本已变化，请重新核对字幕排版")
	}
	var transcript *editor.Transcript
	for i := range p.Transcripts {
		if p.Transcripts[i].ID == input.TranscriptID {
			transcript = &p.Transcripts[i]
			break
		}
	}
	if transcript == nil || transcript.Stale || transcript.Revision != input.TranscriptRevision {
		return CaptionLayoutPreview{}, errors.New("源字幕不存在、已陈旧或接受版本发生变化")
	}
	index := assetIndex(p, transcript.AssetID)
	if index < 0 || p.Assets[index].Missing || p.Assets[index].Fingerprint != transcript.SourceFingerprint {
		return CaptionLayoutPreview{}, errors.New("字幕源素材缺失或与接受词来源不一致")
	}
	source := p.Assets[index]
	if err := verifySourceFingerprint(ctx, source.Path, source.Fingerprint); err != nil {
		return CaptionLayoutPreview{}, err
	}
	trackID := input.TrackID
	if trackID == "" && !input.CreateNewTrack {
		trackID = transcript.CaptionTrackID
	}
	commands := []editor.Command{}
	if trackID == "" {
		trackID = editor.NewID("caption-track")
		track := editor.Track{ID: trackID, Name: "源字幕", Kind: "caption"}
		commands = append(commands, editor.Command{Type: "add_track", Track: &track})
	}
	layout := input.Layout
	commands = append(commands, editor.Command{Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: trackID, CaptionLayout: &layout})
	id := editor.NewID("caption-layout")
	request := editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: id, Label: "调整字幕分句与排版", Commands: commands}
	check, err := editor.NewEngine(p)
	if err != nil {
		return CaptionLayoutPreview{}, err
	}
	after, err := check.Apply(request)
	if err != nil {
		return CaptionLayoutPreview{}, err
	}
	view := CaptionLayoutPreview{ID: id, ProjectID: p.ID, ProjectRevision: p.Revision, TranscriptID: transcript.ID, TranscriptRevision: transcript.Revision, Layout: layout, Warnings: []string{}, Captions: []editor.Clip{}, CreatedAt: time.Now().UTC()}
	for _, t := range after.Transcripts {
		if t.ID == transcript.ID && t.CaptionLayout != nil {
			view.Layout = *t.CaptionLayout
			break
		}
	}
	for _, clip := range p.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.TranscriptID == transcript.ID {
			view.BeforeCount++
		}
	}
	for _, clip := range after.Clips {
		if clip.CaptionSource != nil && clip.CaptionSource.TranscriptID == transcript.ID {
			view.AfterCount++
			// 核心会识别旧工程缺失标记的手工布局；影响说明以实际保留结果为准。
			if clip.CaptionSource.TextOverride || clip.CaptionSource.StyleOverride {
				view.ProtectedCount++
			}
			if len(view.Captions) < 200 {
				view.Captions = append(view.Captions, clip)
			}
		}
	}
	view.Truncated = view.AfterCount > len(view.Captions)
	view.Summary = fmt.Sprintf("这份源字幕由%d条调整为%d条，所有引用该素材的片段按真实词时间更新。", view.BeforeCount, view.AfterCount)
	if view.ProtectedCount > 0 {
		view.Warnings = append(view.Warnings, fmt.Sprintf("%d条人工修改字幕保留原分句、文字和布局，不自动合并。", view.ProtectedCount))
	}
	if view.AfterCount == 0 {
		view.Warnings = append(view.Warnings, "当前时间线没有可见的对应源词；排版规则会保留，后续上轨时生效。")
	}
	s.captionMu.Lock()
	defer s.captionMu.Unlock()
	if s.captionsClosed || s.captionState != state {
		return CaptionLayoutPreview{}, errors.New("字幕排版服务已关闭")
	}
	s.mu.Lock()
	current := s.engine.Snapshot()
	if scope != nil {
		err = scope.checkLocked(s)
	}
	valid := err == nil && s.engine == engine && current.Revision == p.Revision && projectHash(current) == projectHash(p)
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return CaptionLayoutPreview{}, err
	}
	if !valid {
		return CaptionLayoutPreview{}, errors.New("预览期间工程发生变化，请重新生成字幕排版预览")
	}
	for key, record := range state.previews {
		if record.engine != engine && !record.applying {
			delete(state.previews, key)
		}
	}
	state.previews[id] = &captionLayoutRecord{view: view, request: request, engine: engine, hash: projectHash(p), source: source}
	if len(state.previews) > 80 {
		var oldestID string
		var oldest time.Time
		for key, record := range state.previews {
			if key == id || record.applying {
				continue
			}
			if oldestID == "" || record.view.CreatedAt.Before(oldest) {
				oldestID, oldest = key, record.view.CreatedAt
			}
		}
		if oldestID != "" {
			delete(state.previews, oldestID)
		}
	}
	return view, nil
}

func (s *Service) ApplyCaptionLayout(ctx context.Context, id string) (Bootstrap, error) {
	return s.applyCaptionLayout(ctx, id, nil)
}
func (s *Service) applyCaptionLayout(ctx context.Context, id string, scope *mcpProjectScope) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	s.captionMu.Lock()
	state, err := s.captionLayoutsLocked()
	if err != nil {
		s.captionMu.Unlock()
		return Bootstrap{}, err
	}
	record := state.previews[id]
	if record == nil || record.applying {
		s.captionMu.Unlock()
		return Bootstrap{}, errors.New("字幕排版预览不存在或正在应用")
	}
	if scope != nil && !scope.allowEdits {
		s.captionMu.Unlock()
		return Bootstrap{}, errors.New("当前连接未获得字幕编辑权限")
	}
	if record.view.Applied {
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.captionMu.Unlock()
		if scope != nil {
			if err := scope.checkLocked(s); err != nil {
				return Bootstrap{}, err
			}
		}
		if s.engine != record.engine {
			return Bootstrap{}, errors.New("原工程已关闭，请重新预览")
		}
		return s.bootstrapLocked(), nil
	}
	record.applying = true
	state.wg.Add(1)
	s.captionMu.Unlock()
	defer state.wg.Done()
	defer func() { s.captionMu.Lock(); record.applying = false; s.captionMu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	if err := verifySourceFingerprint(ctx, record.source.Path, record.source.Fingerprint); err != nil {
		return Bootstrap{}, err
	}
	s.captionMu.Lock()
	defer s.captionMu.Unlock()
	if s.captionsClosed || s.captionState != state {
		return Bootstrap{}, errors.New("字幕排版服务已关闭")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.engine.Snapshot()
	if scope != nil {
		if err := scope.checkLocked(s); err != nil {
			return Bootstrap{}, err
		}
	}
	if s.engine != record.engine || p.ID != record.view.ProjectID || p.Revision != record.view.ProjectRevision || projectHash(p) != record.hash {
		return Bootstrap{}, errors.New("工程或源字幕已变化，请重新预览排版")
	}
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if _, err := s.engine.Apply(record.request); err != nil {
		return Bootstrap{}, err
	}
	record.view.Applied = true
	return s.bootstrapLocked(), nil
}

func (s *Service) captionLayoutMCPTools(scope *mcpProjectScope) []mcpserver.Tool {
	integer := func(minimum, maximum int64) map[string]any {
		return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum}
	}
	layoutSchema := map[string]any{"type": "object", "properties": map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"word", "readable"}}, "maxCharsPerLine": integer(0, 120), "maxLines": integer(0, 4), "maxDurationUs": integer(0, 60_000_000), "pauseThresholdUs": integer(0, 10_000_000)}, "required": []string{"mode"}, "additionalProperties": false}
	props := map[string]any{"projectId": mcpString(), "projectRevision": integer(0, 1<<62), "transcriptId": mcpString(), "transcriptRevision": integer(1, 1<<62), "trackId": mcpString(), "createNewTrack": map[string]any{"type": "boolean"}, "layout": layoutSchema}
	tools := []mcpserver.Tool{{Name: "aividcut_preview_caption_layout", Description: "根据真实接受词预览逐词或阅读分句；保护手改字幕，只缓存影响，不修改工程。", ReadOnly: true, InputSchema: mcpSchema(props, "projectId", "projectRevision", "transcriptId", "transcriptRevision", "layout"), Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
		input, err := decodeMCP[CaptionLayoutInput](data)
		if err != nil {
			return nil, err
		}
		return s.previewCaptionLayout(ctx, input, scope)
	}}}
	if scope.allowEdits {
		tools = append(tools, mcpserver.Tool{Name: "aividcut_apply_caption_layout", Description: "应用版本仍一致的字幕排版预览，保留人工覆盖并整组撤销。", IdempotentHint: true, InputSchema: mcpSchema(map[string]any{"id": mcpString()}, "id"), Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
			input, err := decodeMCP[struct {
				ID string `json:"id"`
			}](data)
			if err != nil {
				return nil, err
			}
			value, err := s.applyCaptionLayout(ctx, input.ID, scope)
			if err != nil {
				return nil, err
			}
			return map[string]any{"projectId": value.Project.ID, "revision": value.Project.Revision, "applied": true}, nil
		}})
	}
	return tools
}
