package localagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"aividcut/internal/editor"
)

const executionFixtureToken = "execution-fixture-token-private-0123456789"

func fixtureExecutionCLI(mode string) int {
	if strings.HasPrefix(mode, "execute-claude-") {
		return fixtureClaudeExecutionCLI(mode)
	}
	for _, arg := range os.Args[1:] {
		if strings.Contains(arg, executionFixtureToken) {
			return 10
		}
	}
	data, _ := io.ReadAll(io.LimitReader(os.Stdin, 256<<10))
	if bytes.Contains(data, []byte(executionFixtureToken)) || os.Getenv(executionTokenVariable) != executionFixtureToken {
		return 11
	}
	if mode == "execute-flood" {
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 128<<10))
		return 0
	}
	fmt.Println(`{"type":"thread.started","thread_id":"fixture"}`)
	fmt.Println(`{"type":"turn.started"}`)
	itemType, server, tool := "mcp_tool_call", executionServerName, "aividcut_get_project"
	if mode == "execute-builtin" {
		itemType = "command_execution"
	}
	if mode == "execute-foreign" {
		server = "external"
	}
	if mode == "execute-unknown-tool" {
		tool = "aividcut_unauthorized"
	}
	item, _ := json.Marshal(map[string]any{"type": "item.started", "item": map[string]any{"type": itemType, "server": server, "tool": tool, "arguments": map[string]string{"secret": executionFixtureToken}}})
	fmt.Println(string(item))
	fmt.Println(`{"type":"item.completed","item":{"type":"reasoning","text":"fixture-private-chain-of-thought"}}`)
	summary := "已读取当前工程；本测试没有伪造编辑计划。"
	if mode == "execute-echo-token" {
		summary += " " + executionFixtureToken
	}
	message, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": summary}})
	fmt.Println(string(message))
	for index, arg := range os.Args[1:] {
		if arg == "--output-last-message" {
			written := summary
			if mode == "execute-mismatch" {
				written = "different"
			}
			if err := os.WriteFile(os.Args[index+2], []byte(written), 0600); err != nil {
				return 12
			}
		}
	}
	if mode != "execute-incomplete" {
		fmt.Println(`{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`)
	}
	return 0
}

func executionFixturePlanner(t *testing.T, mode string) *Planner {
	planner := fixturePlanner(t, mode)
	planner.executionInspector = func(_ context.Context, provider Provider, config ExecutionMCP) Installation {
		return Installation{Provider: provider, Installed: true, Path: planner.config.Executables[provider], Version: "fixture", AuthState: "signed_in", Isolation: Isolation{Supported: true, Status: "execution_ready"}}
	}
	return planner
}
func executionFixtureInput() ExecutionInput {
	return ExecutionInput{Provider: Codex, ProjectID: "project-fixture", ProjectRevision: 2, Instruction: "读取当前工程，不改动", SkillInstructions: "先读取revision再按受控工具操作，不执行脚本。", Selection: []string{"clip-1"}, MCP: ExecutionMCP{URL: "http://127.0.0.1:12345/mcp", Token: executionFixtureToken, Tools: []string{"aividcut_get_project"}}}
}

func TestExecutableExecutionSummaryAndSecretBoundary(t *testing.T) {
	for _, mode := range []string{"execute-success", "execute-echo-token"} {
		t.Run(mode, func(t *testing.T) {
			planner := executionFixturePlanner(t, mode)
			var events []Event
			result, err := planner.Execute(context.Background(), executionFixtureInput(), func(event Event) { events = append(events, event) })
			if err != nil || result.Provider != Codex || result.Summary == "" || len(result.Warnings) < 2 {
				t.Fatalf("执行总结无效：%+v %v", result, err)
			}
			public, _ := json.Marshal(struct {
				Result ExecutionResult
				Events []Event
				Input  ExecutionInput
			}{result, events, executionFixtureInput()})
			if bytes.Contains(public, []byte(executionFixtureToken)) || bytes.Contains(public, []byte("chain-of-thought")) || bytes.Contains(public, []byte("CoreValidated")) {
				t.Fatal("执行总结/公开DTO泄漏凭据、思考或伪装副本验证")
			}
			if entries, _ := os.ReadDir(planner.config.WorkDir); len(entries) != 0 {
				t.Fatal("执行未清理私有总结目录")
			}
		})
	}
}

func TestExecutableExecutionRejectsUnauthorizedAndIncompleteOutput(t *testing.T) {
	for _, mode := range []string{"execute-builtin", "execute-foreign", "execute-unknown-tool", "execute-mismatch", "execute-incomplete", "execute-flood"} {
		t.Run(mode, func(t *testing.T) {
			planner := executionFixturePlanner(t, mode)
			_, err := planner.Execute(context.Background(), executionFixtureInput(), nil)
			if err == nil {
				t.Fatal("未授权/不完整输出被当作完成")
			}
			if strings.Contains(err.Error(), executionFixtureToken) {
				t.Fatal("错误泄漏执行凭据")
			}
		})
	}
}

func TestExecutionConfigIsLoopbackWhitelistedAndEnvOnly(t *testing.T) {
	input := executionFixtureInput()
	for _, endpoint := range []string{"https://external.example/mcp", "http://localhost:123/mcp", "http://127.0.0.1:0/mcp", "http://127.0.0.1:123/mcp?token=secret", "http://user:pass@127.0.0.1:123/mcp"} {
		config := input.MCP
		config.URL = endpoint
		if _, err := checkedExecutionMCP(config); err == nil {
			t.Fatalf("接受了非第一方连接：%s", endpoint)
		}
	}
	spec := buildExecutionCommand("/cli", "/private/job", "/private/job/summary.txt", "/private/profile", input.MCP, []byte("prompt"))
	if strings.Contains(strings.Join(spec.args, " "), input.MCP.Token) || !strings.Contains(strings.Join(spec.args, " "), executionTokenVariable) {
		t.Fatal("token进入argv或未使用env引用")
	}
	if !strings.Contains(strings.Join(spec.environment, "\n"), executionTokenVariable+"="+input.MCP.Token) {
		t.Fatal("第一方token未绑定独立子进程env")
	}
	environment := strings.Join(executionEnvironment([]string{"NO_PROXY=example.com", executionTokenVariable + "=old", "NODE_OPTIONS=not-added-by-real-base"}, "new"), "\n")
	if !strings.Contains(environment, "NO_PROXY=127.0.0.1,localhost,example.com") || strings.Contains(environment, executionTokenVariable+"=old") {
		t.Fatal("旧会话凭据未清除或loopback未绕开代理")
	}
}

func TestExecutionCancellationAndUnverifiedModeNeverDowngrade(t *testing.T) {
	planner := executionFixturePlanner(t, "sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := planner.Execute(ctx, executionFixtureInput(), nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("执行取消未保留期限原因：%v", err)
	}
	planner = executionFixturePlanner(t, "sleep")
	planner.executionInspector = func(context.Context, Provider, ExecutionMCP) Installation {
		return Installation{Installed: true, Isolation: Isolation{Status: "policy_unverified", Reason: "策略未核查"}}
	}
	started := time.Now()
	if _, err := planner.Execute(context.Background(), executionFixtureInput(), nil); !errors.Is(err, ErrUnsupported) || time.Since(started) > time.Second {
		t.Fatal("执行模式不能在隔离未知时降级启动")
	}
}

func TestPlanRejectsVisualReplacementCarrierFields(t *testing.T) {
	project := editor.NewProject("测试")
	carrier := editor.DefaultClip("carrier", "track-audio-1", "audio")
	for _, command := range []editor.Command{{Type: "update_project", Name: "bad", AudioMode: "preserve"}, {Type: "update_project", Name: "bad", AudioCarrier: &carrier}, {Type: "replace_visual"}} {
		request := editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "bad-carrier", Commands: []editor.Command{command}}
		data, _ := json.Marshal(request)
		if command.AudioCarrier != nil || command.AudioMode != "" {
			if _, err := ParseEditRequest(data, 64<<10); !errors.Is(err, ErrUnsupported) {
				t.Fatal("Parse允许夹带高层替换字段")
			}
		}
		if err := ValidatePlanForInput(PlanInput{Project: project}, request); !errors.Is(err, ErrUnsupported) {
			t.Fatal("快照模式允许高层替换命令/字段")
		}
	}
}
