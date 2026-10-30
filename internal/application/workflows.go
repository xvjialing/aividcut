package application

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/mcpserver"
	"aividcut/internal/statefilelock"
	"aividcut/internal/workflows"
)

type WorkflowPreview struct {
	workflows.Plan
	ProjectRevision int64     `json:"projectRevision"`
	CreatedAt       time.Time `json:"createdAt"`
	Applied         bool      `json:"applied"`
}
type workflowPreviewRecord struct {
	view     WorkflowPreview
	engine   *editor.Engine
	hash     string
	sources  []editor.Asset
	applying bool
}
type workflowState struct {
	store    *workflows.PresetStore
	lock     *statefilelock.Lock
	previews map[string]*workflowPreviewRecord
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func (s *Service) ConfigureWorkflows(directory string) error {
	s.workflowMu.Lock()
	defer s.workflowMu.Unlock()
	if s.workflowsClosed || s.workflowState != nil {
		return errors.New("工作流服务已配置或正在关闭")
	}
	if directory == "" {
		return errors.New("工作流需要独立资料目录")
	}
	lock, err := statefilelock.Acquire(filepath.Join(directory, ".lock"))
	if err != nil {
		return err
	}
	store, err := workflows.NewPresetStore(filepath.Join(directory, "presets.json"))
	if err != nil {
		lock.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.workflowState = &workflowState{store: store, lock: lock, previews: map[string]*workflowPreviewRecord{}, ctx: ctx, cancel: cancel}
	return nil
}
func (s *Service) workflowLocked() (*workflowState, error) {
	if s.workflowsClosed || s.workflowState == nil {
		return nil, errors.New("工作流服务尚未配置或已关闭")
	}
	return s.workflowState, nil
}
func (s *Service) GetWorkflowCatalog() []workflows.Definition {
	s.mu.Lock()
	project := s.engine.Snapshot()
	s.mu.Unlock()
	return workflows.Catalog(project)
}
func (s *Service) GetWorkflowPresets() ([]workflows.Preset, error) {
	s.workflowMu.Lock()
	defer s.workflowMu.Unlock()
	state, err := s.workflowLocked()
	if err != nil {
		return nil, err
	}
	return state.store.List(), nil
}
func (s *Service) SaveWorkflowPreset(ctx context.Context, input workflows.PresetInput) (workflows.Preset, error) {
	s.workflowMu.Lock()
	defer s.workflowMu.Unlock()
	state, err := s.workflowLocked()
	if err != nil {
		return workflows.Preset{}, err
	}
	return state.store.Save(ctx, input)
}
func (s *Service) DeleteWorkflowPreset(ctx context.Context, id string, version int64) error {
	s.workflowMu.Lock()
	defer s.workflowMu.Unlock()
	state, err := s.workflowLocked()
	if err != nil {
		return err
	}
	return state.store.Delete(ctx, id, version)
}

func (s *Service) PreviewWorkflow(ctx context.Context, input workflows.Input) (WorkflowPreview, error) {
	return s.previewWorkflowForMCP(ctx, input, nil)
}
func (s *Service) previewWorkflowForMCP(ctx context.Context, input workflows.Input, scope *mcpProjectScope) (WorkflowPreview, error) {
	return s.previewCompiledWorkflow(ctx, scope, func(project editor.Project) (workflows.Plan, error) { return workflows.Preview(project, input) })
}
func (s *Service) previewCompiledWorkflow(ctx context.Context, scope *mcpProjectScope, build func(editor.Project) (workflows.Plan, error)) (WorkflowPreview, error) {
	if err := ctx.Err(); err != nil {
		return WorkflowPreview{}, err
	}
	s.workflowMu.Lock()
	state, err := s.workflowLocked()
	if err == nil {
		state.wg.Add(1)
	}
	s.workflowMu.Unlock()
	if err != nil {
		return WorkflowPreview{}, err
	}
	defer state.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	s.mu.Lock()
	project, engine := s.engine.Snapshot(), s.engine
	if scope != nil {
		err = scope.checkLocked(s)
	}
	s.mu.Unlock()
	if err != nil {
		return WorkflowPreview{}, err
	}
	plan, err := build(project)
	if err != nil {
		return WorkflowPreview{}, err
	}
	sources := []editor.Asset{}
	seen := map[string]bool{}
	for _, evidence := range plan.Evidence {
		if evidence.AssetID == "" || evidence.SourceFingerprint == "" || seen[evidence.AssetID] {
			continue
		}
		index := assetIndex(project, evidence.AssetID)
		if index < 0 || project.Assets[index].Fingerprint != evidence.SourceFingerprint {
			return WorkflowPreview{}, errors.New("工作流证据与当前素材来源不一致")
		}
		source := project.Assets[index]
		if err := verifySourceFingerprint(ctx, source.Path, source.Fingerprint); err != nil {
			return WorkflowPreview{}, err
		}
		sources = append(sources, source)
		seen[source.ID] = true
	}
	if err := ctx.Err(); err != nil {
		return WorkflowPreview{}, err
	}
	s.workflowMu.Lock()
	defer s.workflowMu.Unlock()
	if s.workflowsClosed || s.workflowState != state {
		return WorkflowPreview{}, errors.New("工作流服务已关闭")
	}
	s.mu.Lock()
	current := s.engine.Snapshot()
	if scope != nil {
		err = scope.checkLocked(s)
	}
	valid := err == nil && s.engine == engine && current.ID == project.ID && current.Revision == project.Revision && projectHash(current) == projectHash(project)
	s.mu.Unlock()
	if !valid {
		return WorkflowPreview{}, errors.New("准备工作流期间工程发生变化，请重新预览")
	}
	view := WorkflowPreview{Plan: plan, ProjectRevision: project.Revision, CreatedAt: time.Now().UTC()}
	state.previews[view.ID] = &workflowPreviewRecord{view: view, engine: engine, hash: projectHash(project), sources: sources}
	if len(state.previews) > 80 {
		var oldestID string
		var oldest time.Time
		for id, r := range state.previews {
			if id == view.ID || r.applying {
				continue
			}
			if oldestID == "" || r.view.CreatedAt.Before(oldest) {
				oldestID, oldest = id, r.view.CreatedAt
			}
		}
		delete(state.previews, oldestID)
	}
	return cloneWorkflowPreview(view), nil
}
func cloneWorkflowPreview(value WorkflowPreview) WorkflowPreview {
	data, _ := json.Marshal(value)
	var result WorkflowPreview
	_ = json.Unmarshal(data, &result)
	return result
}

func (s *Service) ApplyWorkflowPreview(ctx context.Context, id string) (Bootstrap, error) {
	return s.applyWorkflowForMCP(ctx, id, nil)
}
func (s *Service) applyWorkflowForMCP(ctx context.Context, id string, scope *mcpProjectScope) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	s.workflowMu.Lock()
	state, err := s.workflowLocked()
	if err != nil {
		s.workflowMu.Unlock()
		return Bootstrap{}, err
	}
	record := state.previews[id]
	if record == nil || record.applying {
		s.workflowMu.Unlock()
		return Bootstrap{}, errors.New("工作流预览不存在或正在应用")
	}
	if scope != nil && !scope.allowEdits {
		s.workflowMu.Unlock()
		return Bootstrap{}, errors.New("此工具连接未获编辑权限")
	}
	record.applying = true
	state.wg.Add(1)
	s.workflowMu.Unlock()
	defer state.wg.Done()
	defer func() { s.workflowMu.Lock(); record.applying = false; s.workflowMu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	for _, source := range record.sources {
		if err := verifySourceFingerprint(ctx, source.Path, source.Fingerprint); err != nil {
			return Bootstrap{}, err
		}
	}
	s.workflowMu.Lock()
	defer s.workflowMu.Unlock()
	if s.workflowsClosed || s.workflowState != state {
		return Bootstrap{}, errors.New("工作流服务已关闭")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	project := s.engine.Snapshot()
	if scope != nil {
		if err := scope.checkLocked(s); err != nil {
			return Bootstrap{}, err
		}
	}
	if s.engine != record.engine || project.ID != record.view.ProjectID {
		return Bootstrap{}, errors.New("请返回预览对应的原工程")
	}
	if record.view.Applied {
		return s.bootstrapLocked(), nil
	}
	if project.Revision != record.view.ProjectRevision || projectHash(project) != record.hash {
		return Bootstrap{}, errors.New("工程已经变化，请重新生成工作流预览")
	}
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if len(record.view.Request.Commands) > 0 {
		if _, err := s.engine.Apply(record.view.Request); err != nil {
			return Bootstrap{}, err
		}
	}
	record.view.Applied = true
	return s.bootstrapLocked(), nil
}
func (s *Service) closeWorkflows() {
	s.workflowMu.Lock()
	s.workflowsClosed = true
	state := s.workflowState
	s.workflowState = nil
	s.workflowMu.Unlock()
	if state == nil {
		return
	}
	state.cancel()
	done := make(chan struct{})
	go func() { state.wg.Wait(); state.lock.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

func (s *Service) workflowMCPTools(scope *mcpProjectScope) []mcpserver.Tool {
	return []mcpserver.Tool{
		{Name: "aividcut_list_workflows", Description: "读取可复用工作流、真实前置条件和参数。", ReadOnly: true, IdempotentHint: true, InputSchema: mcpSchema(map[string]any{}), Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
			if _, err := decodeMCP[struct{}](data); err != nil {
				return nil, err
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if err := scope.checkLocked(s); err != nil {
				return nil, err
			}
			return workflows.Catalog(s.engine.Snapshot()), nil
		}},
		{Name: "aividcut_preview_workflow", Description: "根据当前已接受转写/已导入素材生成免费编辑预览，不执行或假造媒体生成。", ReadOnly: true, InputSchema: mcpSchema(map[string]any{"input": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"workflowId"}, "properties": map[string]any{"workflowId": mcpString(), "scope": map[string]any{"type": "string", "enum": []string{"selection", "project"}}, "selection": map[string]any{"type": "array", "maxItems": 1000, "uniqueItems": true, "items": mcpString()}, "parameters": map[string]any{"type": "object"}}}}, "input"), Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
			input, err := decodeMCP[struct {
				Input workflows.Input `json:"input"`
			}](data)
			if err != nil {
				return nil, err
			}
			return s.previewWorkflowForMCP(ctx, input.Input, scope)
		}},
	}
}
