package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/mcpserver"
	"aividcut/internal/workflows"
	"context"
	"encoding/json"
	"errors"
)

type TranscriptWordSelection = workflows.TranscriptWordSelection
type TranscriptCutInput struct {
	workflows.TranscriptCutInput
	SourceEpoch uint64 `json:"sourceEpoch"`
}

func (s *Service) LocateTranscriptWords(ctx context.Context, input TranscriptCutInput) (workflows.TranscriptWordSelection, error) {
	if err := ctx.Err(); err != nil {
		return workflows.TranscriptWordSelection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	if epoch != input.SourceEpoch {
		return workflows.TranscriptWordSelection{}, errors.New("选词所属工程实例已切换")
	}
	return workflows.LocateTranscriptWords(s.engine.Snapshot(), input.TranscriptCutInput)
}
func (s *Service) PreviewTranscriptCut(ctx context.Context, input TranscriptCutInput) (WorkflowPreview, error) {
	return s.previewTranscriptCutForMCP(ctx, input, nil)
}
func (s *Service) previewTranscriptCutForMCP(ctx context.Context, input TranscriptCutInput, scope *mcpProjectScope) (WorkflowPreview, error) {
	return s.previewCompiledWorkflow(ctx, scope, func(project editor.Project) (workflows.Plan, error) {
		s.thumbMu.Lock()
		epoch := s.thumbEpoch
		s.thumbMu.Unlock()
		if epoch != input.SourceEpoch {
			return workflows.Plan{}, errors.New("选词所属工程实例已切换")
		}
		return workflows.PreviewTranscriptCut(project, input.TranscriptCutInput)
	})
}
func (s *Service) transcriptCutTool(scope *mcpProjectScope) mcpserver.Tool {
	return mcpserver.Tool{Name: "aividcut_preview_transcript_cut", Description: "按接受词和明确的时间线实例预览删减；不改工程、不判断口癖语义。按帧覆盖并提示量化，应用需原编辑许可及返回的预览ID。", ReadOnly: true, IdempotentHint: true, InputSchema: mcpSchema(map[string]any{
		"projectRevision": map[string]any{"type": "integer", "minimum": 0}, "transcriptId": mcpString(), "transcriptRevision": map[string]any{"type": "integer", "minimum": 0}, "clipId": mcpString(), "wordIds": map[string]any{"type": "array", "minItems": 1, "maxItems": 1000, "uniqueItems": true, "items": mcpString()}, "closeGap": map[string]any{"type": "boolean"}, "rippleAll": map[string]any{"type": "boolean"},
	}, "projectRevision", "transcriptId", "transcriptRevision", "clipId", "wordIds", "closeGap"), Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
		input, err := decodeMCP[workflows.TranscriptCutInput](data)
		if err != nil {
			return nil, err
		}
		input.ProjectID = scope.projectID
		s.thumbMu.Lock()
		epoch := s.thumbEpoch
		s.thumbMu.Unlock()
		view, err := s.previewTranscriptCutForMCP(ctx, TranscriptCutInput{TranscriptCutInput: input, SourceEpoch: epoch}, scope)
		if err != nil {
			return nil, &mcpserver.ToolError{Code: "transcript_cut", Message: err.Error()}
		}
		return view, nil
	}}
}
