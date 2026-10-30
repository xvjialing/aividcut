package application

import (
	"context"
	"testing"

	"aividcut/internal/connections"
	"aividcut/internal/editor"
	"aividcut/internal/generation"
)

func TestMCPHumanConfirmationStartsOnePersistentGenerationTask(t *testing.T) {
	control := &appProviderControl{}
	s := configuredGenerationService(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)
	connection := appSaveConnection(t, s, "", "fixture-only-key")
	session := mcpApplicationClient(t, s, false)
	callApplicationMCP(t, session, "aividcut_request_generation", map[string]any{"id": "mcp-human-image", "connectionId": connection.ID, "request": map[string]any{"providerId": generation.OpenAI, "modelId": connection.ModelID, "mode": generation.ImageGenerate, "prompt": "明确确认后才生成", "size": "1024x1024", "count": 1}}, false)
	if control.posts.Load() != 0 {
		t.Fatal("仅准备请求已调用生成")
	}
	started, err := s.ApproveMCPRequest(context.Background(), "mcp-human-image", MCPActionConfirmation{})
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "started" || started.JobID != "mcp-human-image" {
		t.Fatal("确认没有绑定持久任务")
	}
	again, err := s.ApproveMCPRequest(context.Background(), "mcp-human-image", MCPActionConfirmation{})
	if err != nil || again.JobID != started.JobID {
		t.Fatal("重复确认创建新任务", err)
	}
	appAwaitJob(t, s, started.JobID, "succeeded")
	if control.posts.Load() != 1 {
		t.Fatal("重复确认重新生成", control.posts.Load())
	}
}

func TestMCPPreparedRequestRevisionAndUserRejectionDoNotSubmit(t *testing.T) {
	control := &appProviderControl{}
	s := configuredGenerationService(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)
	connection := appSaveConnection(t, s, "", "fixture-only-key")
	session := mcpApplicationClient(t, s, false)
	prepare := func(id string) {
		callApplicationMCP(t, session, "aividcut_request_generation", map[string]any{"id": id, "connectionId": connection.ID, "request": map[string]any{"providerId": generation.OpenAI, "modelId": connection.ModelID, "mode": generation.ImageGenerate, "prompt": "先核对请求", "size": "1024x1024", "count": 1}}, false)
	}
	prepare("old-revision-request")
	project := s.GetBootstrap().Project
	if _, err := s.ApplyEdit(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "human-edit-before-fee", Commands: []editor.Command{{Type: "update_project", Name: "人工更新"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveMCPRequest(context.Background(), "old-revision-request", MCPActionConfirmation{}); err == nil {
		t.Fatal("版本变化仍执行旧收费请求")
	}
	prepare("user-reject")
	if _, err := s.RejectMCPRequest("user-reject"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveMCPRequest(context.Background(), "user-reject", MCPActionConfirmation{}); err == nil {
		t.Fatal("拒绝后仍执行")
	}
	if control.posts.Load() != 0 {
		t.Fatal("未确认请求调用提供商")
	}
}

// Agent 只能准备请求；显式展示默认声音和用户选定的无声，均不触发收费服务。
func TestMCPSeedanceAudioDefaultsAreVisibleBeforeApproval(t *testing.T) {
	control := &appProviderControl{}
	s := configuredGenerationService(t, t.TempDir(), &appSecretStore{values: map[string][]byte{}}, control)
	connection, err := s.SaveConnection(context.Background(), connections.SaveInput{ProviderID: generation.Seedance, ModelID: generation.Seedance25Model, Name: "Seedance夹具"})
	if err != nil {
		t.Fatal(err)
	}
	session := mcpApplicationClient(t, s, false)
	for _, silent := range []bool{false, true} {
		id := "seedance-default-audio"
		if silent {
			id = "seedance-silent"
		}
		request := map[string]any{"providerId": generation.Seedance, "modelId": generation.Seedance25Model, "mode": generation.VideoText, "prompt": "镜头", "size": "720p", "durationSeconds": 5}
		if silent {
			request["generateAudio"] = false
		}
		callApplicationMCP(t, session, "aividcut_request_generation", map[string]any{"id": id, "connectionId": connection.ID, "request": request}, false)
	}
	actions := s.ListMCPRequests()
	if len(actions) != 2 {
		t.Fatal("没有准备请求")
	}
	for _, action := range actions {
		if action.Generation == nil || action.Generation.GenerateAudio == nil || *action.Generation.GenerateAudio != (action.ID == "seedance-default-audio") {
			t.Fatal("待确认请求声音参数不明确")
		}
	}
	if control.posts.Load() != 0 {
		t.Fatal("Agent准备绕过人确认")
	}
}
