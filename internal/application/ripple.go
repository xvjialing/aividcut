package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/mcpserver"
	"aividcut/internal/workflows"
	"context"
	"encoding/json"
	"errors"
)

type RippleInput struct {
	workflows.RippleInput
	SourceEpoch uint64 `json:"sourceEpoch"`
}

func (s *Service) PreviewRippleDelete(ctx context.Context, input RippleInput) (WorkflowPreview, error) {
	return s.previewRippleForMCP(ctx, input, nil)
}
func (s *Service) previewRippleForMCP(ctx context.Context, input RippleInput, scope *mcpProjectScope) (WorkflowPreview, error) {
	return s.previewCompiledWorkflow(ctx, scope, func(project editor.Project) (workflows.Plan, error) {
		s.thumbMu.Lock()
		epoch := s.thumbEpoch
		s.thumbMu.Unlock()
		if epoch != input.SourceEpoch {
			return workflows.Plan{}, errors.New("时间区间所属工程实例已切换")
		}
		return workflows.PreviewRippleDelete(project, input.RippleInput)
	})
}
func (s *Service) rippleTool(scope *mcpProjectScope) mcpserver.Tool {
	return mcpserver.Tool{Name: "aividcut_preview_ripple_delete", Description: "只读预览明确半开帧区间的全轨波纹删除；所有轨道后段统一前移，含未选/隐藏/静音轨。应用仍需编辑许可。", ReadOnly: true, InputSchema: mcpSchema(map[string]any{"projectRevision": map[string]any{"type": "integer", "minimum": 0}, "fromFrame": map[string]any{"type": "integer", "minimum": 0}, "toFrame": map[string]any{"type": "integer", "minimum": 1}}, "projectRevision", "fromFrame", "toFrame"), Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
		input, err := decodeMCP[workflows.RippleInput](data)
		if err != nil {
			return nil, err
		}
		input.ProjectID = scope.projectID
		s.thumbMu.Lock()
		epoch := s.thumbEpoch
		s.thumbMu.Unlock()
		return s.previewRippleForMCP(ctx, RippleInput{RippleInput: input, SourceEpoch: epoch}, scope)
	}}
}
