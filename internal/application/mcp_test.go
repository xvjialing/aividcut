package application

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpBearerTransport struct{ headers map[string]string }

func (t mcpBearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	for key, value := range t.headers {
		copy.Header.Set(key, value)
	}
	return http.DefaultTransport.RoundTrip(copy)
}
func mcpApplicationClient(t *testing.T, s *Service, allowEdits bool) *mcp.ClientSession {
	t.Helper()
	project := s.GetBootstrap().Project
	status, err := s.StartMCP(MCPStartInput{ProjectID: project.ID, AllowEdits: allowEdits})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || !status.ScopeValid || status.AllowEdits != allowEdits {
		t.Fatal("MCP授权状态不一致")
	}
	configuration, err := s.GetMCPConnection()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "aividcut-app-fixture", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: configuration.URL, HTTPClient: &http.Client{Transport: mcpBearerTransport{configuration.HTTPHeaders}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
func callApplicationMCP(t *testing.T, session *mcp.ClientSession, name string, args any, expectError bool) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		if expectError {
			return nil
		}
		t.Fatal(err)
	}
	if expectError {
		if !result.IsError {
			t.Fatalf("%s应拒绝而成功: %+v", name, result)
		}
		return nil
	}
	if result.IsError {
		t.Fatalf("%s失败: %+v", name, result)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err = json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	return output
}
func newMCPApplication(t *testing.T) *Service {
	t.Helper()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestMCPApplicationReadOnlyProjectionAndScopeInvalidation(t *testing.T) {
	s := newMCPApplication(t)
	project := s.GetBootstrap().Project
	asset := editor.Asset{ID: "private-image", Name: "已登记图", Kind: "image", Path: "/private/fixture/image.png", Width: 10, Height: 10, Fingerprint: "sha256:" + strings.Repeat("a", 64)}
	s.mu.Lock()
	_, err := s.engine.Apply(editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "fixture-import", Commands: []editor.Command{{Type: "add_asset", Asset: &asset}}})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	session := mcpApplicationClient(t, s, false)
	result := callApplicationMCP(t, session, "aividcut_get_project", map[string]any{}, false)
	if result["id"] != project.ID {
		t.Fatal("不是授权工程")
	}
	assets := callApplicationMCP(t, session, "aividcut_list_assets", map[string]any{"limit": 20}, false)
	data, _ := json.Marshal(assets)
	if strings.Contains(string(data), "/private/") || strings.Contains(string(data), "token=") || strings.Contains(string(data), "sha256:") {
		t.Fatal("只读摘要泄漏路径或访问凭据")
	}
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "aividcut_apply_edit" {
			t.Fatal("只读连接暴露编辑工具")
		}
	}
	if _, err := s.NewProject("其他工程"); err != nil {
		t.Fatal(err)
	}
	callApplicationMCP(t, session, "aividcut_get_project", map[string]any{}, true)
	if s.GetMCPStatus().ScopeValid {
		t.Fatal("工程切换后仍声称可用")
	}
	if _, err := s.GetMCPConnection(); err == nil {
		t.Fatal("切工程后仍导出旧访问配置")
	}
}

func TestMCPApplicationUnifiedEditRevisionUndoAndTranscriptGuard(t *testing.T) {
	s := newMCPApplication(t)
	session := mcpApplicationClient(t, s, true)
	p := s.GetBootstrap().Project
	request := editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "mcp-rename", Label: "Agent工程名", Commands: []editor.Command{{Type: "update_project", Name: "Agent编排"}}}
	result := callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": request}, false)
	revision := int64(result["revision"].(float64))
	if !s.GetBootstrap().Dirty || s.GetBootstrap().Project.Name != "Agent编排" {
		t.Fatal("没有作用于同一真实编辑核心")
	}
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": request}, false)
	if s.GetBootstrap().Project.Revision != revision {
		t.Fatal("幂等重试重复编辑")
	}
	stale := request
	stale.IdempotencyKey = "stale-new-id"
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": stale}, true)
	forged := editor.EditRequest{BaseRevision: revision, IdempotencyKey: "no-forged-asr", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &editor.Transcript{ID: "forged"}}}}
	callApplicationMCP(t, session, "aividcut_apply_edit", map[string]any{"request": forged}, true)
	callApplicationMCP(t, session, "aividcut_undo", map[string]any{"baseRevision": revision}, false)
	if s.GetBootstrap().Project.Name != p.Name {
		t.Fatal("MCP编辑不能统一撤销")
	}
	if len(s.GetMCPStatus().Audit) < 4 {
		t.Fatal("没有受控调用记录")
	}
}

func TestMCPPreparedRequestsNeverSubmitOrConfirmTheirOwnCost(t *testing.T) {
	s := newMCPApplication(t)
	session := mcpApplicationClient(t, s, false)
	p := s.GetBootstrap().Project
	callApplicationMCP(t, session, "aividcut_request_generation", map[string]any{"id": "prepared-image", "connectionId": "not-configured", "request": map[string]any{"providerId": "openai", "modelId": "gpt-image-2.5-flare", "mode": "image.generate", "prompt": "仅准备请求", "size": "1024x1024", "count": 1}}, false)
	rows := s.ListMCPRequests()
	if len(rows) != 1 || rows[0].Status != "pending" || rows[0].ProjectID != p.ID || rows[0].JobID != "" {
		t.Fatal("待核对请求提前执行")
	}
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if strings.Contains(tool.Name, "approve") || strings.Contains(tool.Name, "confirm") || tool.Name == "StartSpeechJob" {
			t.Fatal("外部工具可以自行确认收费")
		}
	}
	callApplicationMCP(t, session, "aividcut_request_speech", map[string]any{"input": map[string]any{"id": "prepared-clone", "connectionId": "not-configured", "mode": "clone", "consent": true, "clone": map[string]any{"name": "模型声明", "assetIds": []string{"sample"}, "consent": map[string]any{"confirmed": true, "subject": "模型声称本人", "authority": "self"}}}}, false)
	request, err := s.GetMCPRequest("prepared-clone")
	if err != nil {
		t.Fatal(err)
	}
	if request.Speech.Consent || request.Speech.Clone.Consent.Confirmed || request.Speech.Clone.Consent.Subject != "" {
		t.Fatal("信任模型的消费/身份声明")
	}
	if _, err := s.ApproveMCPRequest(context.Background(), request.ID, MCPActionConfirmation{}); err == nil {
		t.Fatal("未取得人的克隆声明仍提交")
	}
	if _, err := s.StopMCP(); err != nil {
		t.Fatal(err)
	}
	for _, row := range s.ListMCPRequests() {
		if row.Status != "rejected" {
			t.Fatal("停止后未确认请求仍可执行")
		}
	}
}

func TestMCPClosedApplicationCannotReopenListener(t *testing.T) {
	s := newMCPApplication(t)
	projectID := s.GetBootstrap().Project.ID
	_ = mcpApplicationClient(t, s, false)
	s.Close()
	if _, err := s.StartMCP(MCPStartInput{ProjectID: projectID}); err == nil {
		t.Fatal("应用关闭后又启动工具端口")
	}
}
