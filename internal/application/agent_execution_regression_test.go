package application

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"aividcut/internal/generation"
	"aividcut/internal/localagent"
)

func TestCancelledExecutionRevokesOwnPendingAfterTransportScopeReplacement(t *testing.T) {
	control := &appProviderControl{}
	service := configuredGenerationService(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)
	connection := appSaveConnection(t, service, "", "fixture-only-key")
	prepared := make(chan struct{})
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}}
	runner.execute = func(ctx context.Context, input localagent.ExecutionInput, _ func(localagent.Event)) (localagent.ExecutionResult, error) {
		session, err := fixtureExecutionClient(ctx, input)
		if err != nil {
			return localagent.ExecutionResult{}, err
		}
		defer session.Close()
		if err := fixtureExecutionCall(ctx, session, "aividcut_request_generation", map[string]any{
			"id": "cancel-old-scope-generation", "connectionId": connection.ID,
			"request": map[string]any{"providerId": generation.OpenAI, "modelId": connection.ModelID, "mode": generation.ImageGenerate, "prompt": "取消之后不能提交", "size": "1024x1024", "count": 1},
		}); err != nil {
			return localagent.ExecutionResult{}, err
		}
		close(prepared)
		<-ctx.Done()
		return localagent.ExecutionResult{}, ctx.Err()
	}
	if err := service.ConfigureAgents(AgentOptions{Directory: filepath.Join(t.TempDir(), "agents"), Runner: runner}); err != nil {
		t.Fatal(err)
	}
	input := executionInput(service, "cancel-replaced-scope-execution")
	if _, err := service.StartAgentExecution(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	select {
	case <-prepared:
	case <-time.After(5 * time.Second):
		t.Fatal("未准备待确认请求")
	}

	// 固定正常清理的中间状态：传输已停止并保留请求，执行终态尚未发布。
	// 不依赖毫秒级竞争，让新scope先于旧执行的取消取得当前监听位置。
	if _, err := service.stopMCP(input.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartMCP(MCPStartInput{ProjectID: input.ProjectID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelAgentExecution(input.ID); err != nil {
		t.Fatal(err)
	}
	awaitExecution(t, service, input.ID, "cancelled")
	request, err := service.GetMCPRequest("cancel-old-scope-generation")
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != "rejected" || request.CanApprove {
		t.Errorf("取消旧执行后仍保留人工提交许可: status=%s canApprove=%v", request.Status, request.CanApprove)
	}
	if created, err := service.ApproveMCPRequest(context.Background(), request.ID, MCPActionConfirmation{}); err == nil {
		t.Error("取消旧执行后人工审批仍创建了生成任务")
		appAwaitJob(t, service, created.JobID, "succeeded")
	}
	if control.posts.Load() != 0 {
		t.Error("取消旧执行后仍调用了生成服务")
	}
	if status := service.GetMCPStatus(); !status.Running || status.ExecutionID != "" || !status.ScopeValid {
		t.Error("取消旧执行错误停止了新scope", status)
	}
}
