package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/localagent"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fixtureExecutionAgent struct {
	*fixtureAgent
	calls   atomic.Int32
	execute func(context.Context, localagent.ExecutionInput, func(localagent.Event)) (localagent.ExecutionResult, error)
}

func (r *fixtureExecutionAgent) InspectExecution(ctx context.Context, p localagent.Provider) localagent.Installation {
	return localagent.Installation{Provider: p, Installed: true, Version: "execution-fixture", AuthState: "signed_in", Isolation: localagent.Isolation{Supported: true, Status: "mcp_ready"}}
}
func (r *fixtureExecutionAgent) Execute(ctx context.Context, input localagent.ExecutionInput, notify func(localagent.Event)) (localagent.ExecutionResult, error) {
	r.calls.Add(1)
	return r.execute(ctx, input, notify)
}
func executionInput(s *Service, id string) StartAgentExecutionInput {
	p := s.GetBootstrap().Project
	return StartAgentExecutionInput{ID: id, Provider: localagent.Codex, ProjectID: p.ID, ProjectRevision: p.Revision, Instruction: "将工程名改为自动剪辑", Workflow: "manual-plan", Selection: []string{}, AllowEdits: true, Consent: true}
}
func awaitExecution(t *testing.T, s *Service, id, status string) AgentExecution {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		v, err := s.GetAgentExecution(id)
		if err != nil {
			t.Fatal(err)
		}
		if v.Status == status {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	v, _ := s.GetAgentExecution(id)
	t.Fatalf("执行未到达 %s: %+v", status, v)
	return AgentExecution{}
}
func fixtureExecutionClient(ctx context.Context, input localagent.ExecutionInput) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "aividcut-execution-fixture", Version: "1"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: input.MCP.URL, HTTPClient: &http.Client{Transport: mcpBearerTransport{map[string]string{"Authorization": "Bearer " + input.MCP.Token}}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
}
func fixtureExecutionCall(ctx context.Context, session *mcp.ClientSession, name string, arguments any) error {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return err
	}
	if result.IsError {
		return errors.New("工具拒绝操作")
	}
	return nil
}
func fixtureExecutionRename(ctx context.Context, input localagent.ExecutionInput, name string) error {
	session, err := fixtureExecutionClient(ctx, input)
	if err != nil {
		return err
	}
	defer session.Close()
	return fixtureExecutionCall(ctx, session, "aividcut_apply_edit", map[string]any{"request": editor.EditRequest{BaseRevision: input.ProjectRevision, IdempotencyKey: "execution-" + name, Commands: []editor.Command{{Type: "update_project", Name: name}}}})
}

func TestAgentExecutionRealMCPCommitUndoDurabilityAndNoReplay(t *testing.T) {
	directory := t.TempDir()
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}}
	runner.execute = func(ctx context.Context, input localagent.ExecutionInput, notify func(localagent.Event)) (localagent.ExecutionResult, error) {
		if input.MCP.URL == "" || input.MCP.Token == "" || len(input.MCP.Tools) == 0 {
			return localagent.ExecutionResult{}, errors.New("缺少专用MCP连接")
		}
		if notify != nil {
			notify(localagent.Event{Stage: "tools", Message: "fixture令牌:" + input.MCP.Token})
		}
		if err := fixtureExecutionRename(ctx, input, "自动剪辑"); err != nil {
			return localagent.ExecutionResult{}, err
		}
		return localagent.ExecutionResult{Provider: input.Provider, Summary: "已更新工程名。" + input.MCP.Token, Warnings: []string{"fixture令牌:" + input.MCP.Token}}, nil
	}
	s := newAgentService(t, directory, runner)
	initial := s.GetBootstrap().Project
	status, err := s.GetAgentExecutionStatus(context.Background())
	if err != nil || len(status) != 2 || status[0].Version != "execution-fixture" || runner.inspections.Load() != 0 {
		t.Fatal("执行检测误用了快照规划契约", err)
	}
	input := executionInput(s, "real-mcp-execution")
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	finished := awaitExecution(t, s, input.ID, "succeeded")
	if s.GetBootstrap().Project.Name != "自动剪辑" || finished.EndRevision != initial.Revision+1 || len(finished.Audit) != 1 || !finished.Audit[0].Success {
		t.Fatal("没有实际MCP编辑/审计", finished)
	}
	if !strings.Contains(finished.Summary, "[已隐藏]") || !strings.Contains(strings.Join(finished.Warnings, " "), "[已隐藏]") {
		t.Fatal("执行记录未清理临时令牌")
	}
	if s.GetMCPStatus().Running {
		t.Fatal("执行结束后仍监听工具服务")
	}
	rows, err := s.ListAgentExecutions()
	if err != nil || len(rows) != 1 || len(rows[0].Audit) != 0 {
		t.Fatal("执行列表不是摘要", err)
	}
	if _, err := s.Undo(); err != nil || s.GetBootstrap().Project.Name != initial.Name {
		t.Fatal("MCP编辑不能撤销", err)
	}
	input.Consent = false
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil || runner.calls.Load() != 1 {
		t.Fatal("重复请求重新执行", err)
	}
	s.Close()
	reopened := newAgentService(t, directory, runner)
	previous, err := reopened.GetAgentExecution(input.ID)
	if err != nil || previous.Status != "succeeded" || len(previous.Audit) != 1 || runner.calls.Load() != 1 {
		t.Fatal("重开执行记录未恢复或自动调用", err)
	}
}

func TestAgentExecutionCancellationPreservesCommittedEditAndRejectsPending(t *testing.T) {
	committed := make(chan struct{})
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}}
	runner.execute = func(ctx context.Context, input localagent.ExecutionInput, _ func(localagent.Event)) (localagent.ExecutionResult, error) {
		if err := fixtureExecutionRename(ctx, input, "取消前已完成"); err != nil {
			return localagent.ExecutionResult{}, err
		}
		session, err := fixtureExecutionClient(ctx, input)
		if err != nil {
			return localagent.ExecutionResult{}, err
		}
		defer session.Close()
		if err := fixtureExecutionCall(ctx, session, "aividcut_request_export", map[string]any{"id": "cancel-pending-export", "options": MCPExportInput{Preset: "project"}}); err != nil {
			return localagent.ExecutionResult{}, err
		}
		close(committed)
		<-ctx.Done()
		return localagent.ExecutionResult{}, ctx.Err()
	}
	s := newAgentService(t, t.TempDir(), runner)
	input := executionInput(s, "cancel-execution")
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	select {
	case <-committed:
	case <-time.After(5 * time.Second):
		t.Fatal("未完成MCP提交")
	}
	if _, err := s.CancelAgentExecution(input.ID); err != nil {
		t.Fatal(err)
	}
	finished := awaitExecution(t, s, input.ID, "cancelled")
	if s.GetBootstrap().Project.Name != "取消前已完成" || len(finished.Audit) != 2 {
		t.Fatal("取消抹掉已完成编辑或审计")
	}
	if row, err := s.GetMCPRequest("cancel-pending-export"); err != nil || row.Status != "rejected" {
		t.Fatal("取消后未确认请求仍可执行", err)
	}
	if s.GetMCPStatus().Running {
		t.Fatal("取消后MCP仍运行")
	}
	if _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletedExecutionPendingHumanRequestSurvivesTransportAndNextSession(t *testing.T) {
	control := &appProviderControl{}
	s := configuredGenerationService(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)
	connection := appSaveConnection(t, s, "", "fixture-only-key")
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}}
	runner.execute = func(ctx context.Context, input localagent.ExecutionInput, _ func(localagent.Event)) (localagent.ExecutionResult, error) {
		session, err := fixtureExecutionClient(ctx, input)
		if err != nil {
			return localagent.ExecutionResult{}, err
		}
		defer session.Close()
		err = fixtureExecutionCall(ctx, session, "aividcut_request_generation", map[string]any{"id": "execution-human-image", "connectionId": connection.ID, "request": map[string]any{"providerId": generation.OpenAI, "modelId": connection.ModelID, "mode": generation.ImageGenerate, "prompt": "仅人确认后生成", "size": "1024x1024", "count": 1}})
		return localagent.ExecutionResult{Provider: input.Provider, Summary: "已准备生成请求，请人工核对"}, err
	}
	if err := s.ConfigureAgents(AgentOptions{Directory: filepath.Join(t.TempDir(), "agents"), Runner: runner}); err != nil {
		t.Fatal(err)
	}
	input := executionInput(s, "human-confirm-execution")
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	awaitExecution(t, s, input.ID, "succeeded")
	prepared, err := s.GetMCPRequest("execution-human-image")
	if err != nil || prepared.Status != "pending" || prepared.ExecutionID != input.ID || control.posts.Load() != 0 {
		t.Fatal("执行结束误确认或丢弃人类请求", err)
	}
	if _, err := s.StartMCP(MCPStartInput{ProjectID: input.ProjectID}); err != nil {
		t.Fatal(err)
	}
	created, err := s.ApproveMCPRequest(context.Background(), prepared.ID, MCPActionConfirmation{})
	if err != nil || created.Status != "started" {
		t.Fatal("关闭执行传输后无法人工确认", err)
	}
	appAwaitJob(t, s, created.JobID, "succeeded")
	if control.posts.Load() != 1 {
		t.Fatal("人工确认未恰好提交一次")
	}
	if _, err := s.ApproveMCPRequest(context.Background(), prepared.ID, MCPActionConfirmation{}); err != nil || control.posts.Load() != 1 {
		t.Fatal("重复人工确认重新收费", err)
	}
}

func TestAgentExecutionRejectsQueuedProjectChangeAndExistingConnection(t *testing.T) {
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}, execute: func(context.Context, localagent.ExecutionInput, func(localagent.Event)) (localagent.ExecutionResult, error) {
		return localagent.ExecutionResult{}, errors.New("不应调用")
	}}
	s := newAgentService(t, t.TempDir(), runner)
	input := executionInput(s, "queued-old-execution")
	if _, err := s.StartMCP(MCPStartInput{ProjectID: input.ProjectID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartAgentExecution(context.Background(), input); err == nil {
		t.Fatal("静默借用了已有MCP连接")
	}
	if _, err := s.StopMCP(); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	_, err := s.agents.queue.Start("fixture.block", "fixture", func(context.Context, func(float64, string)) (string, error) {
		close(started)
		<-release
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil {
		close(release)
		t.Fatal(err)
	}
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: input.ProjectRevision, IdempotencyKey: "manual-queued-change", Commands: []editor.Command{{Type: "update_project", Name: "排队时手动更改"}}}); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	awaitExecution(t, s, input.ID, "failed")
	if runner.calls.Load() != 0 || s.GetBootstrap().Project.Name != "排队时手动更改" {
		t.Fatal("排队旧版本仍运行或覆盖新编辑")
	}
}

func TestInterruptedAgentExecutionNeverReplaysAndStableIDCannotBeRebound(t *testing.T) {
	directory := t.TempDir()
	s := newAgentService(t, directory, &fixtureAgent{})
	input := executionInput(s, "interrupted-execution")
	now := time.Now().UTC()
	s.agentMu.Lock()
	s.agents.executions[input.ID] = &agentExecutionRecord{Run: AgentExecution{ID: input.ID, Provider: input.Provider, ProjectID: input.ProjectID, ProjectRevision: input.ProjectRevision, EndRevision: input.ProjectRevision, Status: "running", CreatedAt: now, UpdatedAt: now, Warnings: []string{}}, Signature: executionSignature(input), Hash: strings.Repeat("a", 64)}
	if err := s.agents.saveExecutions(); err != nil {
		s.agentMu.Unlock()
		t.Fatal(err)
	}
	s.agentMu.Unlock()
	s.Close()
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}}
	reopened := newAgentService(t, directory, runner)
	previous, err := reopened.GetAgentExecution(input.ID)
	if err != nil || previous.Status != "interrupted" || runner.calls.Load() != 0 {
		t.Fatal("中断执行被重放", err)
	}
	if _, err := reopened.StartAgentExecution(context.Background(), input); err != nil || runner.calls.Load() != 0 {
		t.Fatal("重试中断ID重新调用", err)
	}
	input.Instruction = "另一段内容"
	if _, err := reopened.StartAgentExecution(context.Background(), input); err == nil {
		t.Fatal("旧执行ID被不同请求复用")
	}
	data, err := os.ReadFile(filepath.Join(directory, "executions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if json.Unmarshal(data, &saved) != nil || strings.Contains(string(data), "Authorization") || strings.Contains(string(data), "http://127.0.0.1") {
		t.Fatal("持久记录含临时连接配置")
	}
}

func TestAgentExecutionPanicIsFailedAndReleasesTransport(t *testing.T) {
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}, execute: func(context.Context, localagent.ExecutionInput, func(localagent.Event)) (localagent.ExecutionResult, error) {
		panic("fixture panic")
	}}
	s := newAgentService(t, t.TempDir(), runner)
	input := executionInput(s, "panic-execution")
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	finished := awaitExecution(t, s, input.ID, "failed")
	if s.GetMCPStatus().Running || strings.Contains(fmt.Sprint(finished), "fixture panic") {
		t.Fatal("异常误报成功或泄漏原始错误")
	}
}

func TestAgentReadOnlyExecutionCannotDiscoverOrSubmitEdits(t *testing.T) {
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}}
	runner.execute = func(ctx context.Context, input localagent.ExecutionInput, _ func(localagent.Event)) (localagent.ExecutionResult, error) {
		session, err := fixtureExecutionClient(ctx, input)
		if err != nil {
			return localagent.ExecutionResult{}, err
		}
		defer session.Close()
		tools, err := session.ListTools(ctx, nil)
		if err != nil {
			return localagent.ExecutionResult{}, err
		}
		for _, tool := range tools.Tools {
			if tool.Name == "aividcut_apply_edit" || tool.Name == "aividcut_undo" || tool.Name == "aividcut_apply_workflow" {
				return localagent.ExecutionResult{}, errors.New("只读暴露编辑工具")
			}
		}
		if err := fixtureExecutionCall(ctx, session, "aividcut_get_project", map[string]any{}); err != nil {
			return localagent.ExecutionResult{}, err
		}
		if err := fixtureExecutionCall(ctx, session, "aividcut_apply_edit", map[string]any{"request": editor.EditRequest{BaseRevision: input.ProjectRevision, IdempotencyKey: "readonly-forgery", Commands: []editor.Command{{Type: "update_project", Name: "错误修改"}}}}); err == nil {
			return localagent.ExecutionResult{}, errors.New("只读接受写入")
		}
		return localagent.ExecutionResult{Provider: input.Provider, Summary: "只读检查完成"}, nil
	}
	s := newAgentService(t, t.TempDir(), runner)
	before := s.GetBootstrap().Project
	input := executionInput(s, "readonly-execution")
	input.AllowEdits = false
	withoutConsent := input
	withoutConsent.Consent = false
	if _, err := s.StartAgentExecution(context.Background(), withoutConsent); err == nil || runner.calls.Load() != 0 {
		t.Fatal("未确认就调用CLI")
	}
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	awaitExecution(t, s, input.ID, "succeeded")
	if projectHash(s.GetBootstrap().Project) != projectHash(before) {
		t.Fatal("只读执行修改了工程")
	}
}

func TestAgentExecutionCannotWriteIntoSwitchedProject(t *testing.T) {
	started, proceed := make(chan struct{}), make(chan struct{})
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}}
	runner.execute = func(ctx context.Context, input localagent.ExecutionInput, _ func(localagent.Event)) (localagent.ExecutionResult, error) {
		close(started)
		<-proceed
		err := fixtureExecutionRename(ctx, input, "不应修改新工程")
		if err == nil {
			return localagent.ExecutionResult{}, errors.New("旧scope写入成功")
		}
		return localagent.ExecutionResult{Provider: input.Provider, Summary: "模型声称完成"}, nil
	}
	s := newAgentService(t, t.TempDir(), runner)
	input := executionInput(s, "switched-project-execution")
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("没有进入执行")
	}
	if _, err := s.NewProject("新工程"); err != nil {
		close(proceed)
		t.Fatal(err)
	}
	close(proceed)
	finished := awaitExecution(t, s, input.ID, "failed")
	if s.GetBootstrap().Project.Name != "新工程" || finished.EndRevision < input.ProjectRevision {
		t.Fatal("旧连接修改新工程或审计修订无效")
	}
}
