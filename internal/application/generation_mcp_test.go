package application

import (
	"aividcut/internal/generation"
	"aividcut/internal/generationjobs"
	"aividcut/internal/localagent"
	"context"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func generationPlacementArgs(s *Service, job GenerationJob) map[string]any {
	b := s.GetBootstrap()
	return map[string]any{"jobId": job.ID, "candidateId": job.Assets[0].ID, "projectRevision": b.Project.Revision, "startFrame": 7, "durationFrames": 30, "sourceInUs": 0, "audioMode": "muted"}
}
func assertGenerationProjection(t *testing.T, value any, privatePath string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{privatePath, "http://127.0.0.1", "token=", "Bearer ", "sha256:", "\"commands\"", "\"sourcePhase\""} {
		if forbidden != "" && strings.Contains(string(data), forbidden) {
			t.Fatalf("MCP投影泄漏私有字段：%s", forbidden)
		}
	}
}

func TestGenerationMCPReadOnlyCataloguePagingAndRedaction(t *testing.T) {
	s, store, control, job := placementFixture(t)
	session := mcpApplicationClient(t, s, false)
	before := s.GetBootstrap()
	reads, posts := store.readCount(), control.posts.Load()
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range listed.Tools {
		if strings.Contains(tool.Name, "generation") {
			seen[tool.Name] = true
		}
		if strings.HasPrefix(tool.Name, "aividcut_apply_generation_") {
			t.Fatal("只读连接暴露候选应用")
		}
		if tool.Name == "aividcut_preview_generation_placement" || tool.Name == "aividcut_preview_generation_replacement" || tool.Name == "aividcut_list_generation_candidates" {
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Fatal("只读注解错误")
			}
		}
	}
	for _, name := range []string{"aividcut_preview_generation_placement", "aividcut_preview_generation_replacement", "aividcut_list_generation_candidates"} {
		if !seen[name] {
			t.Fatal("缺少工具", name)
		}
	}
	page := callApplicationMCP(t, session, "aividcut_list_generation_candidates", map[string]any{"limit": 1}, false)
	assertGenerationProjection(t, page, job.Assets[0].Path)
	if page["total"].(float64) != 1 || len(page["candidates"].([]any)) != 1 {
		t.Fatal("候选分页不正确")
	}
	empty := callApplicationMCP(t, session, "aividcut_list_generation_candidates", map[string]any{"offset": 999}, false)
	if len(empty["candidates"].([]any)) != 0 {
		t.Fatal("分页越界没有截断")
	}
	callApplicationMCP(t, session, "aividcut_list_generation_candidates", map[string]any{"limit": 101}, true)
	args := generationPlacementArgs(s, job)
	view := callApplicationMCP(t, session, "aividcut_preview_generation_placement", args, false)
	assertGenerationProjection(t, view, job.Assets[0].Path)
	callApplicationMCP(t, session, "aividcut_apply_generation_placement", map[string]any{"id": view["id"]}, true)
	forged := generationPlacementArgs(s, job)
	forged["path"] = job.Assets[0].Path
	callApplicationMCP(t, session, "aividcut_preview_generation_placement", forged, true)
	if s.GetBootstrap().Project.Revision != before.Project.Revision || store.readCount() != reads || control.posts.Load() != posts {
		t.Fatal("只读调用编辑或访问模型/凭据")
	}
}

func TestGenerationMCPPlacementReceiptUndoAndConnectionOwnership(t *testing.T) {
	s, _, _, job := placementFixture(t)
	session := mcpApplicationClient(t, s, true)
	view := callApplicationMCP(t, session, "aividcut_preview_generation_placement", generationPlacementArgs(s, job), false)
	id := view["id"].(string)
	if _, err := s.ApplyGenerationPlacement(context.Background(), id); err == nil {
		t.Fatal("手动入口应用了MCP预览")
	}
	receipt := callApplicationMCP(t, session, "aividcut_apply_generation_placement", map[string]any{"id": id}, false)
	assertGenerationProjection(t, receipt, job.Assets[0].Path)
	if receipt["newlyApplied"] != true || receipt["alreadyApplied"] != false || receipt["matchesPreview"] != true {
		t.Fatal("第一次回执不准确", receipt)
	}
	again := callApplicationMCP(t, session, "aividcut_apply_generation_placement", map[string]any{"id": id}, false)
	if again["newlyApplied"] != false || again["alreadyApplied"] != true || again["revision"] != receipt["revision"] {
		t.Fatal("重复回执不准确")
	}
	callApplicationMCP(t, session, "aividcut_undo", map[string]any{"baseRevision": receipt["revision"]}, false)
	undone := s.GetBootstrap()
	again = callApplicationMCP(t, session, "aividcut_apply_generation_placement", map[string]any{"id": id}, false)
	if again["newlyApplied"] != false || again["clipExists"] != false || again["matchesPreview"] != false || s.GetBootstrap().Project.Revision != undone.Project.Revision {
		t.Fatal("撤销后重试谎称创建")
	}
	manual, err := s.PreviewGenerationPlacement(context.Background(), placementInput(s, job))
	if err != nil {
		t.Fatal(err)
	}
	callApplicationMCP(t, session, "aividcut_apply_generation_placement", map[string]any{"id": manual.ID}, true)
	if _, err = s.StopMCP(); err != nil {
		t.Fatal(err)
	}
	next := mcpApplicationClient(t, s, true)
	callApplicationMCP(t, next, "aividcut_apply_generation_placement", map[string]any{"id": id}, true)
	if _, err = s.ApplyGenerationPlacement(context.Background(), manual.ID); err != nil {
		t.Fatal("MCP拒绝跨入口不应破坏手动预览", err)
	}
}

func TestGenerationMCPReplacementUsesCoreAndTruthfulUndoReceipt(t *testing.T) {
	s, job, original := replacementFixture(t)
	session := mcpApplicationClient(t, s, true)
	args := map[string]any{"jobId": job.ID, "candidateId": job.Assets[0].ID, "clipId": original.ID, "projectRevision": s.GetBootstrap().Project.Revision, "audioMode": "keep_original", "lutMode": "clear", "speedMode": "normal"}
	view := callApplicationMCP(t, session, "aividcut_preview_generation_replacement", args, false)
	assertGenerationProjection(t, view, job.Assets[0].Path)
	id := view["id"].(string)
	if _, err := s.ApplyGenerationReplacement(context.Background(), id); err == nil {
		t.Fatal("手动入口应用了Agent替换")
	}
	receipt := callApplicationMCP(t, session, "aividcut_apply_generation_replacement", map[string]any{"id": id}, false)
	if receipt["newlyApplied"] != true || receipt["matchesPreview"] != true {
		t.Fatal("替换回执不准确", receipt)
	}
	if s.GetBootstrap().Project.Clips[0].AssetID != job.Assets[0].ID {
		t.Fatal("未进入共同替换核心")
	}
	callApplicationMCP(t, session, "aividcut_undo", map[string]any{"baseRevision": receipt["revision"]}, false)
	again := callApplicationMCP(t, session, "aividcut_apply_generation_replacement", map[string]any{"id": id}, false)
	if again["newlyApplied"] != false || again["alreadyApplied"] != true || again["clipExists"] != true || again["matchesPreview"] != false || s.GetBootstrap().Project.Clips[0].AssetID != original.AssetID {
		t.Fatal("撤销后的替换被重放或回执不准确")
	}
}

func TestGenerationMCPScopeReopenAndBoundedPrompt(t *testing.T) {
	s, _, _, job := placementFixture(t)
	long := appSubmit("long-prompt", job.ConnectionID, 1)
	long.Request.Prompt = strings.Repeat("镜头", 400)
	if _, err := s.SubmitGeneration(context.Background(), long); err != nil {
		t.Fatal(err)
	}
	appAwaitJob(t, s, long.Request.ID, generationjobs.Succeeded)
	session := mcpApplicationClient(t, s, false)
	page := callApplicationMCP(t, session, "aividcut_list_generation_candidates", map[string]any{}, false)
	found := false
	for _, raw := range page["candidates"].([]any) {
		item := raw.(map[string]any)
		if item["jobId"] == long.Request.ID {
			found = true
			if item["promptTruncated"] != true || len([]rune(item["prompt"].(string))) != 500 {
				t.Fatal("描述没有正确有界")
			}
		}
	}
	if !found {
		t.Fatal("候选遗漏")
	}
	path := filepath.Join(t.TempDir(), "same-id.aividcut")
	if _, err := s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	callApplicationMCP(t, session, "aividcut_list_generation_candidates", map[string]any{}, true)
	if _, err := s.StopMCP(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NewProject("其它工程"); err != nil {
		t.Fatal(err)
	}
	next := mcpApplicationClient(t, s, false)
	other := callApplicationMCP(t, next, "aividcut_list_generation_candidates", map[string]any{}, false)
	if other["total"].(float64) != 0 {
		t.Fatal("返回了其它工程候选")
	}
}

// 外部工具夹具仅提供固定元数据和可控等待，验证在途授权；不冒充真实视频质量验收。
func TestGenerationMCPStopDuringCandidateProbeCannotPublishPreview(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX进程等待夹具，Windows另验")
	}
	s, _, control, job := placementFixture(t)
	// 用既有MP4容器构造第二个私有候选，真正媒体字节和SHA仍走导入器。
	raw, err := os.ReadFile("../generationjobs/testdata/seedance-result.mp4")
	if err != nil {
		t.Fatal(err)
	}
	control.submit = func(_ context.Context, r generation.Request) (generation.Task, error) {
		task := appTask(r, generation.Succeeded, "")
		task.Outputs = []generation.Output{{Kind: "video", MIME: "video/mp4", Data: raw}}
		return task, nil
	}
	input := appSubmit("probe-gate", job.ConnectionID, 1)
	if _, err = s.SubmitGeneration(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	video := appAwaitJob(t, s, input.Request.ID, generationjobs.Succeeded)
	dir := t.TempDir()
	marker, gate := filepath.Join(dir, "ready"), filepath.Join(dir, "continue")
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nprintf ready > " + quote(marker) + "\nwhile [ ! -f " + quote(gate) + " ]; do sleep 0.01; done\nprintf '%s' '{\"streams\":[{\"codec_type\":\"video\",\"width\":32,\"height\":18,\"duration\":\"1\",\"avg_frame_rate\":\"30/1\"}],\"format\":{\"duration\":\"1\"}}'\n"
	executable := filepath.Join(dir, "ffprobe-fixture")
	if err = os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s.tools.FFprobePath = executable
	if _, err = s.StartMCP(MCPStartInput{ProjectID: s.GetBootstrap().Project.ID, AllowEdits: true}); err != nil {
		t.Fatal(err)
	}
	scope := s.mcpScope
	b := s.GetBootstrap()
	args := placementInput(s, video)
	args.DurationFrames = 1
	done := make(chan error, 1)
	go func() { _, e := s.previewGenerationPlacement(context.Background(), args, scope); done <- e }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = os.WriteFile(gate, []byte("go"), 0600)
			t.Fatal("工具等待夹具未到达")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = s.StopMCP(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(gate, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("停止后仍发布预览")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("停止后准备未结束")
	}
	if !reflect.DeepEqual(b.Project, s.GetBootstrap().Project) {
		t.Fatal("停止修改了工程")
	}
	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	for _, record := range s.generation.placements {
		if record.scopeID == scope.ID {
			t.Fatal("停止的预览仍可检索")
		}
	}
}

func TestAgentExecutionReceivesGenerationCandidateToolsAndUsesSharedCore(t *testing.T) {
	runner := &fixtureExecutionAgent{fixtureAgent: &fixtureAgent{}}
	s, _, _, job := placementFixture(t)
	if err := s.ConfigureAgents(AgentOptions{Directory: filepath.Join(t.TempDir(), "agents"), Runner: runner}); err != nil {
		t.Fatal(err)
	}
	runner.execute = func(ctx context.Context, input localagent.ExecutionInput, _ func(localagent.Event)) (localagent.ExecutionResult, error) {
		for _, name := range []string{"aividcut_list_generation_candidates", "aividcut_preview_generation_placement", "aividcut_apply_generation_placement", "aividcut_preview_generation_replacement", "aividcut_apply_generation_replacement"} {
			present := false
			for _, advertised := range input.MCP.Tools {
				if advertised == name {
					present = true
				}
			}
			if !present {
				return localagent.ExecutionResult{}, errors.New("候选工具未进入实际白名单")
			}
		}
		session, err := fixtureExecutionClient(ctx, input)
		if err != nil {
			return localagent.ExecutionResult{}, err
		}
		defer session.Close()
		response, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "aividcut_preview_generation_placement", Arguments: generationPlacementArgs(s, job)})
		if err != nil {
			return localagent.ExecutionResult{}, err
		}
		data, _ := json.Marshal(response.StructuredContent)
		var view map[string]any
		_ = json.Unmarshal(data, &view)
		if response.IsError {
			return localagent.ExecutionResult{}, errors.New("候选预览失败")
		}
		if err = fixtureExecutionCall(ctx, session, "aividcut_apply_generation_placement", map[string]any{"id": view["id"]}); err != nil {
			return localagent.ExecutionResult{}, err
		}
		return localagent.ExecutionResult{Provider: input.Provider, Summary: "已添加本地已保存候选"}, nil
	}
	input := executionInput(s, "generation-tools-execution")
	input.Instruction = "把已保存候选放入时间线，不调用模型"
	if _, err := s.StartAgentExecution(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	run := awaitExecution(t, s, input.ID, "succeeded")
	if len(run.Audit) < 2 || len(s.GetBootstrap().Project.Clips) != 1 {
		t.Fatal("执行未走共享核心/审计")
	}
}
