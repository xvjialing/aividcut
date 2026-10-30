package application

import (
	"aividcut/internal/media"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"aividcut/internal/editor"
)

type VisualNoteInput struct {
	Kind        string   `json:"kind"`
	Text        string   `json:"text"`
	Tags        []string `json:"tags,omitempty"`
	EvidenceIDs []string `json:"evidenceIds"`
}
type PrepareVisualAnalysisInput struct {
	ID      string            `json:"id"`
	AssetID string            `json:"assetId"`
	Notes   []VisualNoteInput `json:"notes"`
}
type AcceptVisualAnalysisInput struct {
	ID              string `json:"id"`
	SourceEpoch     uint64 `json:"sourceEpoch"`
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
}

func (s *Service) prepareVisualAnalysis(ctx context.Context, scope *mcpProjectScope, input PrepareVisualAnalysisInput) (MCPAction, error) {
	if !agentIDPattern.MatchString(input.ID) || len(input.Notes) < 1 || len(input.Notes) > 50 {
		return MCPAction{}, errors.New("分析候选需要有效标识及1–50条记录")
	}
	producer := "external-mcp"
	if scope.executionID != "" {
		s.agentMu.Lock()
		state, err := s.agentLocked()
		if err != nil {
			s.agentMu.Unlock()
			return MCPAction{}, err
		}
		record := state.executions[scope.executionID]
		if record == nil {
			s.agentMu.Unlock()
			return MCPAction{}, errors.New("分析来源执行不存在")
		}
		producer = string(record.Run.Provider)
		s.agentMu.Unlock()
	}
	s.mu.Lock()
	err := scope.checkLocked(s)
	p := s.engine.Snapshot()
	s.mu.Unlock()
	if err != nil || !scope.allowSourceFrames {
		return MCPAction{}, errors.New("分析会话未允许源画面或已失效")
	}
	index := assetIndex(p, input.AssetID)
	if index < 0 || p.Assets[index].Missing {
		return MCPAction{}, errors.New("分析源素材不存在或缺失")
	}
	asset := p.Assets[index]
	value := editor.VisualAnalysis{ID: input.ID, AssetID: asset.ID, SourceKind: asset.Kind, SourceFingerprint: asset.Fingerprint, SourceDurationUs: asset.DurationUs, Producer: producer, ExecutionID: scope.executionID, MethodVersion: "visual-notes-v1", Coverage: "sampled_frames", Frames: []SourceFrameEvidence{}, Notes: []editor.VisualNote{}}
	used := map[string]bool{}
	scope.frameMu.Lock()
	for i, note := range input.Notes {
		entry := editor.VisualNote{ID: fmt.Sprintf("%s-note-%d", input.ID, i+1), Kind: note.Kind, Text: note.Text, Tags: append([]string{}, note.Tags...), EvidenceIDs: append([]string{}, note.EvidenceIDs...)}
		for _, id := range entry.EvidenceIDs {
			frame, ok := scope.frameEvidence[id]
			if !ok || frame.AssetID != asset.ID || frame.SourceFingerprint != asset.Fingerprint {
				scope.frameMu.Unlock()
				return MCPAction{}, errors.New("分析引用了未在本会话准备或不属于该素材的画面证据")
			}
			if !used[id] {
				value.Frames = append(value.Frames, frame)
				used[id] = true
			}
		}
		value.Notes = append(value.Notes, entry)
	}
	scope.frameMu.Unlock()
	sort.Slice(value.Frames, func(i, j int) bool {
		if value.Frames[i].PositionUs == value.Frames[j].PositionUs {
			return value.Frames[i].ID < value.Frames[j].ID
		}
		return value.Frames[i].PositionUs < value.Frames[j].PositionUs
	})
	if err = editor.ValidateVisualAnalysis(value); err != nil {
		return MCPAction{}, err
	}
	if err = ctx.Err(); err != nil {
		return MCPAction{}, err
	}
	return s.prepareMCPAction(ctx, scope, MCPAction{ID: input.ID, Kind: "analysis", Analysis: &value})
}

// 仅客户端的人为接纳入口。普通MCP编辑不能构造或确认分析来源。
func (s *Service) AcceptMCPVisualAnalysis(ctx context.Context, input AcceptVisualAnalysisInput) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	s.mcpMu.Lock()
	record := s.mcpRequests[input.ID]
	if record == nil || record.Action.Kind != "analysis" || record.Action.Analysis == nil {
		s.mcpMu.Unlock()
		return Bootstrap{}, errors.New("待核对画面分析不存在")
	}
	scope := record.scope
	action := cloneMCPAction(record.Action)
	if record.Action.Status != "pending" && record.Action.Status != "started" {
		s.mcpMu.Unlock()
		return Bootstrap{}, errors.New("分析请求已处理或正在提交")
	}
	s.mu.Lock()
	p := s.engine.Snapshot()
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	err := scope.checkRequestLocked(s)
	if err == nil && (p.ID != input.ProjectID || p.ID != action.ProjectID || epoch != input.SourceEpoch) {
		err = errors.New("分析所属工程实例已切换")
	}
	if err == nil && record.Action.Status == "started" {
		value := s.bootstrapLocked()
		s.mu.Unlock()
		s.mcpMu.Unlock()
		return value, nil
	}
	if err == nil && (p.Revision != input.ProjectRevision || p.Revision != action.ProjectRevision) {
		err = errors.New("分析核对后工程版本已变化")
	}
	index := assetIndex(p, action.Analysis.AssetID)
	if err == nil && (index < 0 || p.Assets[index].Missing || p.Assets[index].Fingerprint != action.Analysis.SourceFingerprint || p.Assets[index].DurationUs != action.Analysis.SourceDurationUs) {
		err = errors.New("分析源素材已变化或缺失")
	}
	var asset editor.Asset
	if index >= 0 {
		asset = p.Assets[index]
	}
	s.mu.Unlock()
	if err != nil {
		s.mcpMu.Unlock()
		return Bootstrap{}, err
	}
	record.Action.Status, record.Action.Message = "submitting", "正在接纳画面分析，未调用模型或修改时间线"
	s.mcpMu.Unlock()
	proof, err := s.verifyFrameSource(ctx, asset, epoch)
	var result Bootstrap
	if err == nil {
		s.mu.Lock()
		current := s.engine.Snapshot()
		s.thumbMu.Lock()
		currentEpoch := s.thumbEpoch
		s.thumbMu.Unlock()
		err = scope.checkRequestLocked(s)
		if err == nil && (current.ID != input.ProjectID || current.Revision != input.ProjectRevision || currentEpoch != input.SourceEpoch) {
			err = errors.New("核对源文件期间工程已变化")
		}
		currentIndex := assetIndex(current, asset.ID)
		if err == nil && (currentIndex < 0 || current.Assets[currentIndex].Path != asset.Path) {
			err = errors.New("核对源文件期间素材已变化")
		}
		if err == nil {
			err = checkFrameProof(asset.Path, proof)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			_, err = s.engine.Apply(editor.EditRequest{BaseRevision: input.ProjectRevision, IdempotencyKey: "accept-visual-" + input.ID, Label: "接纳画面分析", Commands: []editor.Command{{Type: "accept_visual_analysis", VisualAnalysis: action.Analysis}}})
		}
		if err == nil {
			result = s.bootstrapLocked()
		}
		s.mu.Unlock()
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	record.Action.UpdatedAt = time.Now().UTC()
	if err != nil {
		record.Action.Status, record.Action.Message = "pending", "未接纳："+err.Error()+"；修复后可明确重新核对"
		return Bootstrap{}, err
	}
	record.Action.Status, record.Action.Message = "started", "已接纳画面分析记录，可撤销；未调用模型或修改时间线"
	return result, nil
}

func checkFrameProof(path string, proof os.FileInfo) error {
	current, err := os.Stat(path)
	if err != nil || !media.SameSourceFile(current, proof) {
		return errors.New("源文件已变化，未接纳分析")
	}
	return nil
}

type RemoveVisualAnalysisInput struct {
	SourceEpoch     uint64 `json:"sourceEpoch"`
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	ID              string `json:"id"`
	OperationID     string `json:"operationId"`
}

func (s *Service) RemoveVisualAnalysis(ctx context.Context, input RemoveVisualAnalysisInput) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if !agentIDPattern.MatchString(input.OperationID) {
		return Bootstrap{}, errors.New("移除记录需要有效操作标识")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, revision := s.engine.Identity()
	s.thumbMu.Lock()
	epoch := s.thumbEpoch
	s.thumbMu.Unlock()
	if id != input.ProjectID || revision != input.ProjectRevision || epoch != input.SourceEpoch {
		return Bootstrap{}, errors.New("分析移除的工程或版本已变化")
	}
	if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: input.ProjectRevision, IdempotencyKey: input.OperationID, Label: "移除画面分析记录", Commands: []editor.Command{{Type: "delete_visual_analysis", VisualAnalysisID: input.ID}}}); err != nil {
		return Bootstrap{}, err
	}
	return s.bootstrapLocked(), nil
}
