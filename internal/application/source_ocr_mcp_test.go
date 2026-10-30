package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/localagent"
	"aividcut/internal/ocr"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ocrMCPClient(t *testing.T, s *Service, allow bool) *mcp.ClientSession {
	t.Helper()
	if _, err := s.StartMCP(MCPStartInput{ProjectID: s.GetBootstrap().Project.ID, AllowSourceFrames: allow}); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.GetMCPConnection()
	if err != nil {
		t.Fatal(err)
	}
	client, err := fixtureExecutionClient(context.Background(), localagent.ExecutionInput{MCP: localagent.ExecutionMCP{URL: cfg.URL, Token: strings.TrimPrefix(cfg.HTTPHeaders["Authorization"], "Bearer ")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}
func TestMCPSourceOCROptInNoCredentialsAndHumanAnalysisUndo(t *testing.T) {
	s, input := setupOCRFixture(t)
	var calls atomic.Int32
	s.ocrRecognizer = ocrFixtureFunc(func(context.Context, []byte) (ocr.Result, error) { calls.Add(1); return ocrFixtureResult(), nil })
	client := ocrMCPClient(t, s, false)
	list, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "aividcut_read_source_ocr" {
			t.Fatal("未允许源画面就提供OCR")
		}
	}
	callApplicationMCP(t, client, "aividcut_read_source_ocr", map[string]any{"assetId": input.AssetID, "positionUs": 0}, true)
	if calls.Load() != 0 {
		t.Fatal("未允许却调用识别器")
	}
	s.StopMCP()
	client = ocrMCPClient(t, s, true)
	before := s.GetBootstrap()
	result := callApplicationMCP(t, client, "aividcut_read_source_ocr", map[string]any{"assetId": input.AssetID, "positionUs": 0}, false)
	data, _ := json.Marshal(result)
	cfg, _ := s.GetMCPConnection()
	for _, secret := range []string{before.Project.Assets[0].Path, strings.TrimPrefix(cfg.HTTPHeaders["Authorization"], "Bearer "), `"url":`, `"path":`} {
		if strings.Contains(string(data), secret) {
			t.Fatal("OCR响应泄露本机路径或预览/会话凭据")
		}
	}
	if calls.Load() != 1 || projectHash(s.GetBootstrap().Project) != projectHash(before.Project) || s.GetBootstrap().Dirty {
		t.Fatal("只读OCR改工程或重复调用")
	}
	s.ocrMu.Lock()
	pending := len(s.ocrState.records)
	s.ocrMu.Unlock()
	if pending != 0 {
		t.Fatal("MCP读取消耗人工候选名额")
	}
	frame := result["frame"].(map[string]any)
	frameID := frame["id"].(string)
	if frameID == "" || result["result"].(map[string]any)["engine"] != "apple-vision" {
		t.Fatal("缺少真实引擎或帧证据")
	}
	args := map[string]any{"id": "ocr-interpretation", "assetId": input.AssetID, "notes": []any{map[string]any{"kind": "observation", "text": "基于未人工校对OCR的解释：原始识别", "evidenceIds": []string{frameID}}}}
	callApplicationMCP(t, client, "aividcut_prepare_visual_analysis", args, false)
	action := s.ListMCPRequests()[0]
	if action.Analysis.Producer != "external-mcp" || s.GetBootstrap().Dirty {
		t.Fatal("模型解释伪装原始OCR或自动接纳")
	}
	b, err := s.AcceptMCPVisualAnalysis(context.Background(), AcceptVisualAnalysisInput{ID: action.ID, SourceEpoch: before.SourceEpoch, ProjectID: before.Project.ID, ProjectRevision: before.Project.Revision})
	if err != nil || len(b.Project.VisualAnalyses) != 1 {
		t.Fatal(err)
	}
	if _, err = s.Undo(); err != nil || len(s.GetBootstrap().Project.VisualAnalyses) != 0 {
		t.Fatal("分析不能撤销", err)
	}
}
func TestMCPSourceOCRBudgetAndSourceChangesRejectBeforeDisclosure(t *testing.T) {
	s, input := setupOCRFixture(t)
	_ = ocrMCPClient(t, s, true)
	scope := s.mcpScope
	scope.frameMu.Lock()
	scope.frameEvidence = map[string]SourceFrameEvidence{}
	for i := 0; i < 128; i++ {
		scope.frameEvidence[fmt.Sprint(i)] = SourceFrameEvidence{AssetID: "other", PositionUs: int64(i)}
	}
	scope.frameMu.Unlock()
	var calls atomic.Int32
	s.ocrRecognizer = ocrFixtureFunc(func(context.Context, []byte) (ocr.Result, error) { calls.Add(1); return ocrFixtureResult(), nil })
	if _, err := s.readMCPSourceOCR(context.Background(), scope, input.AssetID, 0); err == nil || calls.Load() != 0 {
		t.Fatal("预算满仍识别")
	}
	scope.frameMu.Lock()
	scope.frameEvidence = map[string]SourceFrameEvidence{}
	scope.frameMu.Unlock()
	if _, err := s.readMCPSourceOCR(context.Background(), scope, "not-in-project", 0); err == nil || calls.Load() != 0 {
		t.Fatal("越界素材仍识别")
	}
	s.ocrRecognizer = ocrFixtureFunc(func(context.Context, []byte) (ocr.Result, error) {
		_, err := s.ApplyEdit(editor.EditRequest{BaseRevision: input.ProjectRevision, IdempotencyKey: "ocr-external-edit", Commands: []editor.Command{{Type: "update_project", Name: "识别期间外部修改"}}})
		return ocrFixtureResult(), err
	})
	if _, err := s.readMCPSourceOCR(context.Background(), scope, input.AssetID, 0); err == nil {
		t.Fatal("过期识别泄露给调用者")
	}
	if len(scope.frameEvidence) != 0 {
		t.Fatal("过期结果仍登记证据")
	}
}
func TestMCPSourceOCRStopCancelsRunningRecognition(t *testing.T) {
	s, input := setupOCRFixture(t)
	client := ocrMCPClient(t, s, true)
	started, stopped := make(chan struct{}), make(chan struct{})
	s.ocrRecognizer = ocrFixtureFunc(func(ctx context.Context, _ []byte) (ocr.Result, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ocr.Result{}, ctx.Err()
	})
	done := make(chan bool, 1)
	go func() {
		result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "aividcut_read_source_ocr", Arguments: map[string]any{"assetId": input.AssetID, "positionUs": 0}})
		done <- err == nil && (result == nil || !result.IsError)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("识别没有启动")
	}
	s.StopMCP()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("停止连接未取消识别")
	}
	select {
	case bad := <-done:
		if bad {
			t.Fatal("停止后仍返回识别内容")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("工具请求未结束")
	}
	if s.GetBootstrap().Dirty {
		t.Fatal("取消只读识别改变工程")
	}
}

func TestMCPSourceOCRErrorDoesNotExposeLocalPath(t *testing.T) {
	s, input := setupOCRFixture(t)
	client := ocrMCPClient(t, s, true)
	path := s.GetBootstrap().Project.Assets[0].Path
	s.ocrRecognizer = ocrFixtureFunc(func(context.Context, []byte) (ocr.Result, error) {
		return ocr.Result{}, &os.PathError{Op: "read", Path: path, Err: errors.New("不可读取")}
	})
	response, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "aividcut_read_source_ocr", Arguments: map[string]any{"assetId": input.AssetID, "positionUs": 0}})
	if err != nil || !response.IsError {
		t.Fatal("错误未返回", err)
	}
	data, _ := json.Marshal(response)
	if strings.Contains(string(data), path) || !strings.Contains(string(data), "客户端核对素材") {
		t.Fatal("错误泄露路径或没有恢复建议")
	}
}
func TestAgentOCRToolWhitelistSkillAndEvidencePersistWithoutTextOrReplay(t *testing.T) {
	directory := t.TempDir()
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}}
	s := newAgentService(t, directory, runner)
	asset := sourceFrameFixture(t, s)
	const privateOCR = "不应持久化的原始OCR文字"
	s.ocrRecognizer = ocrFixtureFunc(func(context.Context, []byte) (ocr.Result, error) {
		v := ocrFixtureResult()
		v.Lines[0].Text = privateOCR
		return v, nil
	})
	runner.execute = func(ctx context.Context, input localagent.ExecutionInput, _ func(localagent.Event)) (localagent.ExecutionResult, error) {
		if !strings.Contains(strings.Join(input.MCP.Tools, ","), "aividcut_read_source_ocr") || !strings.Contains(input.SkillInstructions, "aividcut_read_source_ocr") {
			return localagent.ExecutionResult{}, errors.New("工具或Skill未注入")
		}
		client, err := fixtureExecutionClient(ctx, input)
		if err != nil {
			return localagent.ExecutionResult{}, err
		}
		defer client.Close()
		result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "aividcut_read_source_ocr", Arguments: map[string]any{"assetId": asset.ID, "positionUs": 0}})
		if err != nil {
			return localagent.ExecutionResult{}, err
		}
		if result.IsError || len(result.Content) != 1 {
			return localagent.ExecutionResult{}, errors.New("OCR返回错误或夹带图像")
		}
		return localagent.ExecutionResult{Provider: input.Provider, Summary: "已读取本机OCR，尚未核对，无编辑（协议夹具，不调用模型）。"}, nil
	}
	input := executionInput(s, "read-local-ocr")
	input.AllowEdits = false
	input.AllowSourceFrames = true
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	run := awaitExecution(t, s, input.ID, "succeeded")
	if len(run.FrameEvidence) != 1 || !validFrameEvidence(run.FrameEvidence[0]) || s.GetBootstrap().Dirty {
		t.Fatal("执行未保存证据或修改工程")
	}
	data, err := os.ReadFile(filepath.Join(directory, "executions.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{asset.Path, privateOCR, "Bearer ", "http://", "image/jpeg"} {
		if strings.Contains(string(data), bad) {
			t.Fatal("持久记录包含原始内容或凭据")
		}
	}
	s.Close()
	restored := newAgentService(t, directory, runner)
	run, err = restored.GetAgentExecution(input.ID)
	if err != nil || len(run.FrameEvidence) != 1 || runner.calls.Load() != 1 {
		t.Fatal("证据未持久或重开重跑", err)
	}
}
