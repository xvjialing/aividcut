package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"aividcut/internal/editor"
	"aividcut/internal/jobs"
	"aividcut/internal/localagent"
	"aividcut/internal/workflows"
)

// 执行和只读规划具有不同工具契约；旧 Runner 不会被静默赋予 MCP 执行能力。
type AgentExecutionRunner interface {
	InspectExecution(context.Context, localagent.Provider) localagent.Installation
	Execute(context.Context, localagent.ExecutionInput, func(localagent.Event)) (localagent.ExecutionResult, error)
}

type StartAgentExecutionInput struct {
	ID                string              `json:"id"`
	Provider          localagent.Provider `json:"provider"`
	ProjectID         string              `json:"projectId"`
	ProjectRevision   int64               `json:"projectRevision"`
	Instruction       string              `json:"instruction"`
	Workflow          string              `json:"workflow"`
	Selection         []string            `json:"selection"`
	AllowEdits        bool                `json:"allowEdits"`
	AllowSourceFrames bool                `json:"allowSourceFrames,omitempty"`
	Consent           bool                `json:"consent"`
}

type AgentExecution struct {
	ID                string                `json:"id"`
	Provider          localagent.Provider   `json:"provider"`
	ProjectID         string                `json:"projectId"`
	ProjectRevision   int64                 `json:"projectRevision"`
	EndRevision       int64                 `json:"endRevision"`
	AllowEdits        bool                  `json:"allowEdits"`
	AllowSourceFrames bool                  `json:"allowSourceFrames,omitempty"`
	Selection         []string              `json:"selection"`
	Workflow          string                `json:"workflow"`
	Status            string                `json:"status"`
	Stage             string                `json:"stage"`
	Message           string                `json:"message"`
	Summary           string                `json:"summary,omitempty"`
	FrameEvidence     []SourceFrameEvidence `json:"frameEvidence,omitempty"`
	CreatedAt         time.Time             `json:"createdAt"`
	UpdatedAt         time.Time             `json:"updatedAt"`
	Audit             []MCPAudit            `json:"audit,omitempty"`
	Warnings          []string              `json:"warnings"`
}

type agentExecutionRecord struct {
	Run       AgentExecution `json:"run"`
	Signature string         `json:"signature"`
	Hash      string         `json:"projectHash"`
	engine    *editor.Engine
	queueID   string
	cancel    context.CancelFunc
}

func executionTerminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled" || status == "interrupted"
}

func cloneAgentExecution(value AgentExecution, detail bool) AgentExecution {
	value.Selection = append([]string{}, value.Selection...)
	value.Warnings = append([]string{}, value.Warnings...)
	if detail {
		value.Audit = append([]MCPAudit{}, value.Audit...)
		value.FrameEvidence = append([]SourceFrameEvidence{}, value.FrameEvidence...)
	} else {
		value.Audit = nil
		value.FrameEvidence = nil
	}
	return value
}

func (state *agentState) executionPath() string {
	return filepath.Join(filepath.Dir(state.path), "executions.json")
}

func (state *agentState) loadExecutions() error {
	info, err := os.Lstat(state.executionPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return errors.New("CLI 执行记录不是有效普通文件或超过16MiB")
	}
	f, err := os.Open(state.executionPath())
	if err != nil {
		return err
	}
	defer f.Close()
	var saved struct {
		Version int                    `json:"version"`
		Runs    []agentExecutionRecord `json:"runs"`
	}
	d := json.NewDecoder(io.LimitReader(f, (16<<20)+1))
	d.DisallowUnknownFields()
	if d.Decode(&saved) != nil || saved.Version != 1 || len(saved.Runs) > 1000 {
		return errors.New("CLI 执行记录损坏或版本无效，不会重放已有请求")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("CLI 执行记录包含额外内容")
	}
	for i := range saved.Runs {
		r := &saved.Runs[i]
		v := &r.Run
		if !agentIDPattern.MatchString(v.ID) || state.executions[v.ID] != nil || !knownAgent(v.Provider) || v.ProjectID == "" || v.ProjectRevision < 0 || v.EndRevision < v.ProjectRevision || len(r.Signature) != 64 || len(r.Hash) != 64 || v.CreatedAt.IsZero() || len(v.Audit) > 80 || len(v.Selection) > 512 || len(v.Summary) > 8192 || len(v.Warnings) > 20 || len(v.FrameEvidence) > 20 {
			return errors.New("CLI 执行记录的身份、数量或长度无效")
		}
		seenFrames := map[string]bool{}
		for _, frame := range v.FrameEvidence {
			if !v.AllowSourceFrames || !validFrameEvidence(frame) || seenFrames[frame.ID] {
				return errors.New("CLI画面证据的身份、来源或范围无效")
			}
			seenFrames[frame.ID] = true
		}
		if v.Status == "queued" || v.Status == "running" || v.Status == "cancelling" {
			v.Status, v.Stage = "interrupted", "interrupted"
			v.Message = "上次执行已中断，可能已提交部分编辑；请核对工程与 CLI 使用记录，不会自动重新运行"
		} else if !executionTerminal(v.Status) {
			return errors.New("CLI 执行记录状态无效")
		}
		state.executions[v.ID] = r
	}
	return nil
}

func (state *agentState) saveExecutions() error {
	saved := struct {
		Version int                    `json:"version"`
		Runs    []agentExecutionRecord `json:"runs"`
	}{Version: 1, Runs: []agentExecutionRecord{}}
	for _, r := range state.executions {
		saved.Runs = append(saved.Runs, *r)
	}
	sort.Slice(saved.Runs, func(i, j int) bool { return saved.Runs[i].Run.ID < saved.Runs[j].Run.ID })
	data, err := json.Marshal(saved)
	if err != nil || len(data) > 16<<20 {
		return errors.New("CLI 执行记录超过保存上限，请归档历史后继续")
	}
	f, err := os.CreateTemp(filepath.Dir(state.path), ".agent-executions-*")
	if err != nil {
		return errors.New("无法保存 CLI 执行记录")
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), state.executionPath()); err != nil {
		return errors.New("无法提交 CLI 执行记录")
	}
	return nil
}

func (state *agentState) finishExecutionLocked(job jobs.Job) {
	for _, record := range state.executions {
		if record.queueID != job.ID || executionTerminal(record.Run.Status) {
			continue
		}
		if job.Status == "failed" || job.Status == "cancelled" {
			record.engine = nil
			record.Run.Status, record.Run.Stage = job.Status, job.Status
			record.Run.Message = "CLI 执行已结束；已提交的编辑保留，请核对工程和调用记录"
			record.Run.UpdatedAt = time.Now().UTC()
			_ = state.saveExecutions()
		}
		return
	}
}

func (s *Service) GetAgentExecutionStatus(ctx context.Context) ([]localagent.Installation, error) {
	s.agentMu.Lock()
	state, err := s.agentLocked()
	var runner AgentExecutionRunner
	if err == nil {
		runner, _ = state.runner.(AgentExecutionRunner)
		state.metadataWG.Add(1)
	}
	s.agentMu.Unlock()
	if err != nil {
		return nil, err
	}
	defer state.metadataWG.Done()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	stop := context.AfterFunc(state.ctx, cancel)
	defer stop()
	result := []localagent.Installation{}
	for _, provider := range []localagent.Provider{localagent.Codex, localagent.Claude} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if runner == nil {
			result = append(result, localagent.Installation{Provider: provider, AuthState: "unknown", Isolation: localagent.Isolation{Status: "unsupported", Reason: "当前适配器没有 MCP 执行契约"}})
		} else {
			result = append(result, runner.InspectExecution(ctx, provider))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func executionSignature(input StartAgentExecutionInput) string {
	input.Consent = false
	data, _ := json.Marshal(input)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Service) StartAgentExecution(ctx context.Context, input StartAgentExecutionInput) (AgentExecution, error) {
	if err := ctx.Err(); err != nil {
		return AgentExecution{}, err
	}
	input.Instruction = strings.TrimSpace(input.Instruction)
	input.Selection = append([]string{}, input.Selection...)
	sort.Strings(input.Selection)
	if !agentIDPattern.MatchString(input.ID) || !knownAgent(input.Provider) || input.ProjectID == "" || input.ProjectRevision < 0 || input.Instruction == "" || len(input.Instruction) > 32<<10 || len(input.Selection) > 512 {
		return AgentExecution{}, errors.New("执行需要稳定请求标识、当前工程版本及有效的剪辑要求")
	}
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	state, err := s.agentLocked()
	if err != nil {
		return AgentExecution{}, err
	}
	signature := executionSignature(input)
	if old := state.executions[input.ID]; old != nil {
		if old.Signature != signature {
			return AgentExecution{}, errors.New("该请求标识已用于不同 CLI 执行内容")
		}
		return cloneAgentExecution(old.Run, true), nil
	}
	if !input.Consent {
		return AgentExecution{}, errors.New("请先确认本次 CLI 额度和工程编辑权限")
	}
	if len(state.executions) >= 1000 {
		return AgentExecution{}, errors.New("CLI 执行历史达到当前上限，需归档后继续")
	}
	runner, ok := state.runner.(AgentExecutionRunner)
	if !ok {
		return AgentExecution{}, errors.New("当前适配器尚未提供 MCP 执行契约")
	}
	skill := ""
	if input.Workflow != "" && input.Workflow != "manual-plan" {
		skill, err = workflows.BuiltinSkill(input.Workflow)
		if err != nil {
			return AgentExecution{}, errors.New("未知的本地执行工作流")
		}
	}
	if input.AllowSourceFrames {
		skill += "\n" + workflows.SourceFrameSkill()
	}
	s.mcpMu.Lock()
	connected := s.mcpServer != nil && s.mcpServer.Info().Running
	closed := s.mcpClosed
	s.mcpMu.Unlock()
	if connected || closed {
		return AgentExecution{}, errors.New("已有 MCP 连接或工具服务已关闭；请先停止旧连接再执行")
	}
	s.mu.Lock()
	project, engine := s.engine.Snapshot(), s.engine
	s.mu.Unlock()
	if project.ID != input.ProjectID || project.Revision != input.ProjectRevision {
		return AgentExecution{}, errors.New("工程版本已变化，请重新核对后执行")
	}
	clips := map[string]bool{}
	for _, clip := range project.Clips {
		clips[clip.ID] = true
	}
	for i, id := range input.Selection {
		if !clips[id] || i > 0 && input.Selection[i-1] == id {
			return AgentExecution{}, errors.New("选区片段不存在或重复")
		}
	}
	if err := ctx.Err(); err != nil {
		return AgentExecution{}, err
	}
	now := time.Now().UTC()
	record := &agentExecutionRecord{Run: AgentExecution{ID: input.ID, Provider: input.Provider, ProjectID: project.ID, ProjectRevision: project.Revision, EndRevision: project.Revision, AllowEdits: input.AllowEdits, AllowSourceFrames: input.AllowSourceFrames, Selection: input.Selection, Workflow: input.Workflow, Status: "queued", Stage: "queued", Message: "执行已登记，等待本地 CLI；尚未调用推理", CreatedAt: now, UpdatedAt: now, Warnings: []string{"选区用于任务上下文，编辑许可覆盖整个当前工程。", "每批工具编辑可以撤销；取消会保留已完成编辑。"}}, Signature: signature, Hash: projectHash(project), engine: engine}
	state.executions[input.ID] = record
	if err := state.saveExecutions(); err != nil {
		delete(state.executions, input.ID)
		return AgentExecution{}, err
	}
	job, err := state.queue.Start("agent.execute", "等待本地 CLI 使用工程工具", func(runCtx context.Context, _ func(float64, string)) (string, error) {
		return "", s.runAgentExecution(runCtx, state, record, runner, input, skill)
	})
	if err != nil {
		record.Run.Status, record.Run.Stage, record.Run.Message = "failed", "failed", "未能进入队列，没有调用 CLI"
		_ = state.saveExecutions()
		return AgentExecution{}, err
	}
	record.queueID = job.ID
	return cloneAgentExecution(record.Run, true), nil
}

func (s *Service) runAgentExecution(parent context.Context, state *agentState, record *agentExecutionRecord, runner AgentExecutionRunner, input StartAgentExecutionInput, skill string) (runErr error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	s.agentMu.Lock()
	if record.Run.Status == "cancelling" || ctx.Err() != nil {
		s.agentMu.Unlock()
		return context.Canceled
	}
	record.cancel = cancel
	record.Run.Status, record.Run.Stage, record.Run.Message = "running", "starting", "正在检查本次工程的 CLI 工具执行契约"
	record.Run.UpdatedAt = time.Now().UTC()
	if err := state.saveExecutions(); err != nil {
		record.Run.Status, record.Run.Stage, record.Run.Message = "failed", "failed", "无法保存执行意图，没有调用 CLI"
		s.agentMu.Unlock()
		return err
	}
	s.agentMu.Unlock()
	var summary string
	var warnings []string
	var scope *mcpProjectScope
	var token string
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = errors.New("本地执行适配器异常；请核对已提交的编辑")
		}
		if scope != nil {
			// 只有正常完成保留人工请求；停止工具服务不等于批准这些请求。
			_, stopErr := s.stopMCP(record.Run.ID, runErr == nil && ctx.Err() == nil)
			if runErr == nil && stopErr != nil {
				runErr = stopErr
			}
		}
		s.mu.Lock()
		_, endRevision := record.engine.Identity()
		s.mu.Unlock()
		s.agentMu.Lock()
		defer s.agentMu.Unlock()
		record.cancel = nil
		record.engine = nil
		record.Run.EndRevision, record.Run.UpdatedAt = endRevision, time.Now().UTC()
		record.Run.Summary = safeExecutionText(summary, token, 8192)
		for _, warning := range warnings {
			if len(record.Run.Warnings) == 20 {
				break
			}
			record.Run.Warnings = append(record.Run.Warnings, safeExecutionText(warning, token, 1024))
		}
		if ctx.Err() != nil || errors.Is(runErr, context.Canceled) || record.Run.Status == "cancelling" {
			record.Run.Status, record.Run.Stage, record.Run.Message = "cancelled", "cancelled", "本地执行已停止；已提交编辑保留，可按批撤销"
			if runErr == nil {
				runErr = context.Canceled
			}
		} else if runErr != nil {
			record.Run.Status, record.Run.Stage, record.Run.Message = "failed", "failed", "CLI 执行未完成；请核对已提交编辑与调用记录，不会自动重新调用"
			var known *localagent.Error
			if errors.As(runErr, &known) {
				record.Run.Message = safeExecutionText(known.Message, token, 1024) + "；已提交编辑保留"
			}
		} else {
			record.Run.Status, record.Run.Stage, record.Run.Message = "succeeded", "completed", "CLI 执行已结束，请核对工程与工具记录；模型请求仍需人工确认"
		}
		if err := state.saveExecutions(); err != nil {
			record.Run.Message += "；运行记录暂未保存，重开后不会自动重运行"
		}
	}()
	s.mu.Lock()
	p := s.engine.Snapshot()
	valid := s.engine == record.engine && p.ID == input.ProjectID && p.Revision == input.ProjectRevision && projectHash(p) == record.Hash
	sequenceGeneration := s.sequenceGeneration
	s.mu.Unlock()
	if !valid {
		return errors.New("排队期间工程已变化，未调用 CLI")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	scope = &mcpProjectScope{sequenceGeneration: sequenceGeneration, ID: editor.NewID("mcp"), projectID: input.ProjectID, engine: record.engine, initialRevision: input.ProjectRevision, allowEdits: input.AllowEdits, allowSourceFrames: input.AllowSourceFrames, startedAt: time.Now().UTC(), executionID: input.ID, stopRunner: cancel}
	scope.onAudit = func(entry MCPAudit) {
		s.agentMu.Lock()
		defer s.agentMu.Unlock()
		record.Run.Audit = append(record.Run.Audit, entry)
		if len(record.Run.Audit) > 80 {
			record.Run.Audit = append([]MCPAudit{}, record.Run.Audit[len(record.Run.Audit)-80:]...)
		}
		record.Run.EndRevision, record.Run.UpdatedAt = max(record.Run.EndRevision, entry.Revision), time.Now().UTC()
		_ = state.saveExecutions()
	}
	s.mcpMu.Lock()
	_, err := s.startMCPLocked(MCPStartInput{ProjectID: input.ProjectID, AllowEdits: input.AllowEdits}, scope)
	started := err == nil
	server := s.mcpServer
	var endpoint localagent.ExecutionMCP
	if err == nil {
		connection, configErr := s.mcpServer.ConnectionConfig()
		err = configErr
		if err == nil {
			token = strings.TrimPrefix(connection.HTTPHeaders["Authorization"], "Bearer ")
			endpoint.URL, endpoint.Token = connection.URL, token
			for _, tool := range s.projectMCPTools(scope) {
				endpoint.Tools = append(endpoint.Tools, tool.Name)
			}
		}
	}
	s.mcpMu.Unlock()
	if started {
		// 非协作工具可能晚于Stop的等待预算结束；资料锁须保留到最后审计回调完成。
		s.agentMu.Lock()
		state.metadataWG.Add(1)
		s.agentMu.Unlock()
		go func() { defer state.metadataWG.Done(); <-server.Done() }()
	}
	if err != nil {
		if !started {
			scope = nil
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := runner.Execute(ctx, localagent.ExecutionInput{Provider: input.Provider, ProjectID: input.ProjectID, ProjectRevision: input.ProjectRevision, Instruction: input.Instruction, WorkflowID: input.Workflow, SkillInstructions: skill, Selection: append([]string{}, input.Selection...), MCP: endpoint}, func(event localagent.Event) {
		s.agentMu.Lock()
		defer s.agentMu.Unlock()
		if record.Run.Status != "cancelling" {
			record.Run.Stage = safeExecutionText(event.Stage, token, 64)
			record.Run.Message = safeExecutionText(event.Message, token, 512)
			record.Run.UpdatedAt = time.Now().UTC()
		}
	})
	if result.Provider != input.Provider && err == nil {
		err = errors.New("执行结果的 CLI 归属不一致")
	}
	if err == nil {
		s.mu.Lock()
		err = scope.checkLocked(s)
		s.mu.Unlock()
	}
	summary, warnings = result.Summary, result.Warnings
	return err
}

func safeExecutionText(value, token string, limit int) string {
	if token != "" {
		value = strings.ReplaceAll(value, token, "[已隐藏]")
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func (s *Service) ListAgentExecutions() ([]AgentExecution, error) {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	state, err := s.agentLocked()
	if err != nil {
		return nil, err
	}
	result := []AgentExecution{}
	for _, record := range state.executions {
		result = append(result, cloneAgentExecution(record.Run, false))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (s *Service) GetAgentExecution(id string) (AgentExecution, error) {
	s.agentMu.Lock()
	defer s.agentMu.Unlock()
	state, err := s.agentLocked()
	if err != nil {
		return AgentExecution{}, err
	}
	if record := state.executions[id]; record != nil {
		return cloneAgentExecution(record.Run, true), nil
	}
	return AgentExecution{}, errors.New("找不到 CLI 执行记录")
}

func (s *Service) CancelAgentExecution(id string) (AgentExecution, error) {
	s.agentMu.Lock()
	state, err := s.agentLocked()
	if err != nil {
		s.agentMu.Unlock()
		return AgentExecution{}, err
	}
	record := state.executions[id]
	if record == nil {
		s.agentMu.Unlock()
		return AgentExecution{}, errors.New("找不到 CLI 执行记录")
	}
	if executionTerminal(record.Run.Status) {
		value := cloneAgentExecution(record.Run, true)
		s.agentMu.Unlock()
		return value, nil
	}
	if _, err := state.queue.Cancel(record.queueID); err != nil {
		s.agentMu.Unlock()
		return AgentExecution{}, err
	}
	if record.cancel != nil {
		record.cancel()
	}
	record.Run.Status, record.Run.Stage, record.Run.Message = "cancelling", "cancelling", "正在停止本地执行；已完成编辑保留"
	record.Run.UpdatedAt = time.Now().UTC()
	_ = state.saveExecutions()
	s.agentMu.Unlock()
	_, _ = s.stopMCP(id, false)
	return s.GetAgentExecution(id)
}
