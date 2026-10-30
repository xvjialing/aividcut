package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/generationjobs"
	"aividcut/internal/mcpserver"
	"aividcut/internal/media"
	"aividcut/internal/speech"
	"aividcut/internal/speechjobs"
)

type MCPExportInput struct {
	ResourceMode string `json:"resourceMode,omitempty"`
	Renderer     string `json:"renderer,omitempty"`
	Preset       string `json:"preset,omitempty"`
	FromFrame    int64  `json:"fromFrame,omitempty"`
	ToFrame      int64  `json:"toFrame,omitempty"`
	VideoEncoder string `json:"videoEncoder,omitempty"`
}
type MCPAction struct {
	ID              string                 `json:"id"`
	ProjectID       string                 `json:"projectId"`
	ProjectRevision int64                  `json:"projectRevision"`
	ExecutionID     string                 `json:"executionId,omitempty"`
	AnalysisPresent bool                   `json:"analysisPresent,omitempty"`
	CanApprove      bool                   `json:"canApprove"`
	ApprovalReason  string                 `json:"approvalReason,omitempty"`
	Kind            string                 `json:"kind"`
	Status          string                 `json:"status"`
	Message         string                 `json:"message"`
	ConnectionID    string                 `json:"connectionId,omitempty"`
	Analysis        *editor.VisualAnalysis `json:"analysis,omitempty"`
	Generation      *generation.Request    `json:"generation,omitempty"`
	Speech          *speechjobs.Input      `json:"speech,omitempty"`
	Export          *MCPExportInput        `json:"export,omitempty"`
	JobID           string                 `json:"jobId,omitempty"`
	CreatedAt       time.Time              `json:"createdAt"`
	UpdatedAt       time.Time              `json:"updatedAt"`
}
type MCPActionConfirmation struct {
	CloneConsent *speech.CloneConsent `json:"cloneConsent,omitempty"`
	ExportPath   string               `json:"exportPath,omitempty"`
	Overwrite    bool                 `json:"overwrite"`
}
type mcpActionRecord struct {
	Action    MCPAction
	Signature string
	scope     *mcpProjectScope
}

func cloneMCPAction(value MCPAction) MCPAction {
	data, _ := json.Marshal(value)
	var result MCPAction
	_ = json.Unmarshal(data, &result)
	return result
}

// 调用方持有mcpMu。每条请求以自己的工程实例/版本判断，不借用当前传输的状态。
func (s *Service) mcpActionViewLocked(record *mcpActionRecord) MCPAction {
	value := cloneMCPAction(record.Action)
	value.CanApprove = false
	if value.Analysis != nil {
		s.mu.Lock()
		id, _, present, asset, exists := s.engine.VisualAnalysisContext(value.Analysis.ID, value.Analysis.AssetID)
		s.mu.Unlock()
		value.AnalysisPresent = id == value.ProjectID && present
		if value.Status == "pending" && (!exists || asset.Missing || asset.Fingerprint != value.Analysis.SourceFingerprint || asset.DurationUs != value.Analysis.SourceDurationUs || asset.Kind != value.Analysis.SourceKind) {
			value.ApprovalReason = "分析源素材已变化或缺失，请重新准备"
			return value
		}
	}
	if value.Status != "pending" {
		value.ApprovalReason = "请求已处理"
		return value
	}
	if s.mcpClosed || !record.scope.requestsActive.Load() {
		value.ApprovalReason = "请求授权已停止"
		return value
	}
	s.mu.Lock()
	projectID, revision := s.engine.Identity()
	valid := s.engine == record.scope.engine && projectID == value.ProjectID && revision == value.ProjectRevision && record.scope.sequenceGeneration == s.sequenceGeneration
	s.mu.Unlock()
	if !valid {
		value.ApprovalReason = "原工程或版本已变化，请重新准备请求"
		return value
	}
	value.CanApprove = true
	value.ApprovalReason = ""
	return value
}

func (s *Service) prepareMCPAction(ctx context.Context, scope *mcpProjectScope, action MCPAction) (MCPAction, error) {
	if err := ctx.Err(); err != nil {
		return MCPAction{}, err
	}
	if !agentIDPattern.MatchString(action.ID) {
		return MCPAction{}, &mcpserver.ToolError{Code: "request_id", Message: "请求需要新的稳定标识"}
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if err := ctx.Err(); err != nil {
		return MCPAction{}, err
	}
	if s.mcpClosed || s.mcpScope != scope {
		return MCPAction{}, &mcpserver.ToolError{Code: "scope", Message: "工具连接已更换或关闭"}
	}
	s.mu.Lock()
	err := ctx.Err()
	if err == nil {
		err = scope.checkLocked(s)
	}
	project := s.engine.Snapshot()
	if err == nil && action.Kind == "analysis" {
		if action.Analysis == nil {
			err = errors.New("画面分析候选缺少内容")
		} else if index := assetIndex(project, action.Analysis.AssetID); index < 0 || project.Assets[index].Missing || project.Assets[index].Fingerprint != action.Analysis.SourceFingerprint || project.Assets[index].DurationUs != action.Analysis.SourceDurationUs {
			err = errors.New("画面分析来源已变化")
		}
	}
	s.mu.Unlock()
	if err != nil {
		return MCPAction{}, err
	}
	action.ProjectID, action.ProjectRevision, action.ExecutionID = scope.projectID, project.Revision, scope.executionID
	if action.Kind == "export" {
		if action.Export == nil {
			return MCPAction{}, &mcpserver.ToolError{Code: "export_options", Message: "导出请求缺少参数"}
		}
		input := *action.Export
		if _, err := media.EncodingResourceArgs(input.VideoEncoder, input.ResourceMode); err != nil {
			return MCPAction{}, &mcpserver.ToolError{Code: "export_options", Message: err.Error()}
		}
		input.Renderer, err = resolveExportRenderer(input.Renderer, s.nativeExportConfig.Load())
		if err != nil {
			return MCPAction{}, &mcpserver.ToolError{Code: "export_options", Message: err.Error()}
		}
		action.Export = &input
	}
	data, _ := json.Marshal(action)
	digest := sha256.Sum256(data)
	signature := hex.EncodeToString(digest[:])
	if s.mcpRequests == nil {
		s.mcpRequests = map[string]*mcpActionRecord{}
	}
	if old := s.mcpRequests[action.ID]; old != nil {
		if old.Signature != signature {
			return MCPAction{}, &mcpserver.ToolError{Code: "request_id", Message: "该请求标识已用于不同内容或工程版本"}
		}
		return s.mcpActionViewLocked(old), nil
	}
	currentCount := 0
	for _, record := range s.mcpRequests {
		if record.scope == scope {
			currentCount++
		}
	}
	// 已处理旧记录可以回收；待人核对的请求不因新会话被悄悄丢弃。
	if len(s.mcpRequests) >= 200 {
		var oldestID string
		var oldest time.Time
		for id, record := range s.mcpRequests {
			if record.Action.Status == "pending" || record.Action.Status == "submitting" || record.scope == scope {
				continue
			}
			if oldestID == "" || record.Action.UpdatedAt.Before(oldest) {
				oldestID, oldest = id, record.Action.UpdatedAt
			}
		}
		if oldestID != "" {
			delete(s.mcpRequests, oldestID)
		}
	}
	if currentCount >= 40 || len(s.mcpRequests) >= 200 {
		return MCPAction{}, &mcpserver.ToolError{Code: "request_limit", Message: "待核对请求已达到当前会话上限，请先在客户端处理"}
	}
	now := time.Now().UTC()
	action.CreatedAt, action.UpdatedAt, action.Status, action.Message = now, now, "pending", "等待用户在客户端核对；尚未调用收费服务或写出文件"
	record := &mcpActionRecord{Action: action, Signature: signature, scope: scope}
	s.mcpRequests[action.ID] = record
	return s.mcpActionViewLocked(record), nil
}
func (s *Service) ListMCPRequests() []MCPAction {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	result := []MCPAction{}
	for _, record := range s.mcpRequests {
		result = append(result, s.mcpActionViewLocked(record))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}
func (s *Service) GetMCPRequest(id string) (MCPAction, error) {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	record := s.mcpRequests[id]
	if record == nil {
		return MCPAction{}, errors.New("待核对请求不存在")
	}
	return s.mcpActionViewLocked(record), nil
}
func (s *Service) RejectMCPRequest(id string) (MCPAction, error) {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	record := s.mcpRequests[id]
	if record == nil {
		return MCPAction{}, errors.New("待核对请求不存在")
	}
	if record.Action.Status == "submitting" || record.Action.Status == "started" {
		return MCPAction{}, errors.New("请求已经提交，请在对应任务中停止等待")
	}
	record.Action.Status, record.Action.Message, record.Action.UpdatedAt = "rejected", "用户未接受此请求，没有自动执行", time.Now().UTC()
	return cloneMCPAction(record.Action), nil
}

// 此方法仅由客户端确认入口调用，不注册为MCP工具。
func (s *Service) ApproveMCPRequest(ctx context.Context, id string, confirmation MCPActionConfirmation) (MCPAction, error) {
	if err := ctx.Err(); err != nil {
		return MCPAction{}, err
	}
	s.mcpMu.Lock()
	record := s.mcpRequests[id]
	if s.mcpClosed {
		s.mcpMu.Unlock()
		return MCPAction{}, errors.New("应用工具服务已关闭")
	}
	if err := ctx.Err(); err != nil {
		s.mcpMu.Unlock()
		return MCPAction{}, err
	}
	if record == nil {
		s.mcpMu.Unlock()
		return MCPAction{}, errors.New("待核对请求不存在")
	}
	if record.Action.Status == "started" {
		value := cloneMCPAction(record.Action)
		s.mcpMu.Unlock()
		return value, nil
	}
	if record.Action.Status != "pending" || !record.scope.requestsActive.Load() || record.scope.executionID == "" && s.mcpScope != record.scope {
		s.mcpMu.Unlock()
		return MCPAction{}, errors.New("此请求已处理或连接已变化，请重新准备")
	}
	if record.Action.Kind == "analysis" {
		s.mcpMu.Unlock()
		return MCPAction{}, errors.New("画面分析需通过专用接纳入口，不是收费任务")
	}
	scope := record.scope
	action := cloneMCPAction(record.Action)
	s.mu.Lock()
	err := ctx.Err()
	if err == nil {
		err = scope.checkRequestLocked(s)
	}
	revision := s.engine.Snapshot().Revision
	s.mu.Unlock()
	if err != nil || revision != action.ProjectRevision {
		s.mcpMu.Unlock()
		return MCPAction{}, errors.New("原工程或版本已变化，请重新核对请求后再提交")
	}
	if action.Kind == "speech" && action.Speech != nil && action.Speech.Mode == speechjobs.Clone {
		if action.Speech.Clone == nil || confirmation.CloneConsent == nil || !confirmation.CloneConsent.Confirmed {
			s.mcpMu.Unlock()
			return MCPAction{}, errors.New("自定义音色需要用户单独声明主体和授权，模型声明不代替用户确认")
		}
		action.Speech.Clone.Consent = *confirmation.CloneConsent
	}
	if action.Kind == "export" && confirmation.ExportPath == "" {
		s.mcpMu.Unlock()
		return MCPAction{}, errors.New("请选择导出文件位置")
	}
	record.Action.Status, record.Action.Message, record.Action.UpdatedAt = "submitting", "用户已确认，正在创建对应任务", time.Now().UTC()
	s.mcpMu.Unlock()
	jobID := ""
	switch action.Kind {
	case "generation":
		if action.Generation == nil {
			err = errors.New("生成请求缺少内容")
			break
		}
		var job GenerationJob
		job, err = s.submitGenerationForMCP(ctx, generationjobs.SubmitInput{ConnectionID: action.ConnectionID, Request: *action.Generation, Consent: true}, scope, action.ProjectRevision)
		jobID = job.ID
	case "speech":
		if action.Speech == nil {
			err = errors.New("语音请求缺少内容")
			break
		}
		input := *action.Speech
		input.Consent = true
		var job SpeechSummary
		job, err = s.startSpeechForMCP(ctx, input, scope, action.ProjectRevision)
		jobID = job.ID
	case "export":
		if action.Export == nil {
			err = errors.New("导出请求缺少范围")
			break
		}
		created, createErr := s.exportVideoForMCP(media.ExportOptions{ResourceMode: action.Export.ResourceMode, Renderer: action.Export.Renderer, Path: confirmation.ExportPath, Preset: action.Export.Preset, FromFrame: action.Export.FromFrame, ToFrame: action.Export.ToFrame, VideoEncoder: action.Export.VideoEncoder, Overwrite: confirmation.Overwrite}, scope, action.ProjectRevision)
		err, jobID = createErr, created.ID
	default:
		err = errors.New("未支持的待核对操作")
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if err != nil {
		record.Action.Status, record.Action.Message = "failed", err.Error()
	} else {
		record.Action.Status, record.Action.Message, record.Action.JobID = "started", "已创建任务，请在对应任务面板跟进；没有自动添加或覆盖工程", jobID
	}
	record.Action.UpdatedAt = time.Now().UTC()
	return cloneMCPAction(record.Action), err
}

func (s *Service) mcpRequestTools(scope *mcpProjectScope) []mcpserver.Tool {
	return []mcpserver.Tool{
		{Name: "aividcut_get_connections", Description: "读取连接名称、供应商、准确模型与配置状态，不读取API Key。", ReadOnly: true, IdempotentHint: true, InputSchema: mcpSchema(map[string]any{}), Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
			if _, err := decodeMCP[struct{}](data); err != nil {
				return nil, err
			}
			s.mu.Lock()
			err := scope.checkLocked(s)
			s.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return s.GetConnections()
		}},
		{Name: "aividcut_request_generation", Description: "准备图片/视频生成请求供用户确认；不会调用收费服务。客户端确认与MCP参数隔离。", InputSchema: mcpSchema(map[string]any{"id": mcpString(), "connectionId": mcpString(), "request": map[string]any{"type": "object"}}, "id", "connectionId", "request"), Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
			input, err := decodeMCP[struct {
				ID           string             `json:"id"`
				ConnectionID string             `json:"connectionId"`
				Request      generation.Request `json:"request"`
			}](data)
			if err != nil {
				return nil, err
			}
			if input.Request.ID != "" && input.Request.ID != input.ID {
				return nil, &mcpserver.ToolError{Code: "id", Message: "内外请求ID不一致"}
			}
			input.Request.ID = input.ID
			if input.Request.Count == 0 {
				input.Request.Count = 1
			}
			provider, err := generationjobs.BuiltinProvider(input.Request.ProviderID, input.Request.ModelID, nil, nil)
			if err != nil {
				return nil, err
			}
			// 将可选声音默认值显式放入待核对请求，避免确认时隐藏服务端默认行为。
			for _, capability := range provider.Capabilities() {
				if capability.ModelID == input.Request.ModelID && capability.SupportsAudioGeneration && input.Request.GenerateAudio == nil {
					value := capability.DefaultGenerateAudio
					input.Request.GenerateAudio = &value
				}
			}
			if err = provider.Validate(input.Request); err != nil {
				return nil, err
			}
			return s.prepareMCPAction(ctx, scope, MCPAction{ID: input.ID, Kind: "generation", ConnectionID: input.ConnectionID, Generation: &input.Request})
		}},
		{Name: "aividcut_request_speech", Description: "准备语音请求供客户端用户核对上传/费用；模型不能通过consent自行扣费或确认音色身份。", InputSchema: mcpSchema(map[string]any{"input": map[string]any{"type": "object"}}, "input"), Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
			input, err := decodeMCP[struct {
				Input speechjobs.Input `json:"input"`
			}](data)
			if err != nil {
				return nil, err
			}
			input.Input.Consent = false
			count := 0
			if input.Input.Transcribe != nil {
				count++
			}
			if input.Input.Synthesize != nil {
				count++
			}
			if input.Input.Clone != nil {
				count++
			}
			if count != 1 || input.Input.Mode == speechjobs.Transcribe && input.Input.Transcribe == nil || input.Input.Mode == speechjobs.Synthesize && input.Input.Synthesize == nil || input.Input.Mode == speechjobs.Clone && input.Input.Clone == nil || input.Input.Mode != speechjobs.Transcribe && input.Input.Mode != speechjobs.Synthesize && input.Input.Mode != speechjobs.Clone {
				return nil, &mcpserver.ToolError{Code: "speech_mode", Message: "语音模式必须且只能提供相应的一项请求"}
			}
			if input.Input.Clone != nil {
				input.Input.Clone.Consent = speech.CloneConsent{}
			}
			return s.prepareMCPAction(ctx, scope, MCPAction{ID: input.Input.ID, Kind: "speech", ConnectionID: input.Input.ConnectionID, Speech: &input.Input})
		}},
		{Name: "aividcut_request_export", Description: "按半开工程帧区间准备导出，固定本次实际合成方式；不接受文件路径，用户确认时选择输出位置。", InputSchema: mcpSchema(map[string]any{"id": mcpString(), "options": mcpSchema(map[string]any{
			"renderer":     map[string]any{"type": "string", "enum": []string{"auto", "native", "ffmpeg"}},
			"preset":       map[string]any{"type": "string", "enum": []string{"project", "720p", "1080p", "4k"}},
			"resourceMode": map[string]any{"type": "string", "enum": []string{"standard", "lower_memory"}},
			"videoEncoder": map[string]any{"type": "string", "enum": []string{"libx264", "mpeg4", "h264_videotoolbox"}},
			"fromFrame":    map[string]any{"type": "integer", "minimum": 0},
			"toFrame":      map[string]any{"type": "integer", "minimum": 0},
		})}, "id", "options"), Handler: func(ctx context.Context, data json.RawMessage) (any, error) {
			input, err := decodeMCP[struct {
				ID      string         `json:"id"`
				Options MCPExportInput `json:"options"`
			}](data)
			if err != nil {
				return nil, err
			}
			return s.prepareMCPAction(ctx, scope, MCPAction{ID: input.ID, Kind: "export", Export: &input.Options})
		}},
	}
}
