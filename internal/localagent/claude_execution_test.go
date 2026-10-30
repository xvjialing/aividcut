package localagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureClaudeExecutionCLI(mode string) int {
	if mode == "execute-claude-mcp" || mode == "execute-claude-mcp-sleep" {
		return fixtureClaudeMCPCLI(mode)
	}
	for _, arg := range os.Args[1:] {
		if strings.Contains(arg, executionFixtureToken) {
			return 40
		}
	}
	prompt, _ := io.ReadAll(io.LimitReader(os.Stdin, 256<<10))
	if bytes.Contains(prompt, []byte(executionFixtureToken)) || os.Getenv(executionTokenVariable) != executionFixtureToken || os.Getenv("ENABLE_TOOL_SEARCH") != "false" {
		return 41
	}
	if mode == "execute-claude-sleep" {
		time.Sleep(60 * time.Second)
		return 0
	}
	if mode == "execute-claude-flood" {
		fmt.Print(strings.Repeat("x", 128<<10))
		return 0
	}
	cwd, _ := os.Getwd()
	session := "claude-fixture-session"
	emit := func(v map[string]any) {
		if _, ok := v["session_id"]; !ok {
			v["session_id"] = session
		}
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
	}
	init := map[string]any{"type": "system", "subtype": "init", "cwd": cwd, "claude_code_version": "fixture", "permissionMode": "dontAsk", "tools": []string{"mcp__aividcut__aividcut_get_project"}, "mcp_servers": []any{map[string]string{"name": "aividcut", "status": "connected"}}, "skills": []string{}, "plugins": []string{}}
	if mode == "execute-claude-init-tool" {
		init["tools"] = []string{"Bash"}
	}
	if mode == "execute-claude-init-plugin" {
		init["plugins"] = []any{map[string]string{"name": "other", "path": "/unused"}}
	}
	if mode == "execute-claude-init-permission" {
		init["permissionMode"] = "bypassPermissions"
	}
	if mode == "execute-claude-init-cwd" {
		init["cwd"] = filepath.Dir(cwd)
	}
	if mode != "execute-claude-no-init" {
		emit(init)
	}
	if mode == "execute-claude-hook" {
		emit(map[string]any{"type": "system", "subtype": "hook_started"})
	}
	tool := "mcp__aividcut__aividcut_get_project"
	if mode == "execute-claude-builtin" {
		tool = "Bash"
	}
	if mode == "execute-claude-foreign" {
		tool = "mcp__other__aividcut_get_project"
	}
	if mode == "execute-claude-unknown-tool" {
		tool = "mcp__aividcut__aividcut_not_granted"
	}
	call := map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "thinking", "thinking": "fixture-private-chain-of-thought"}, map[string]any{"type": "tool_use", "id": "call-1", "name": tool, "input": map[string]string{"private": executionFixtureToken}}}}}
	if mode == "execute-claude-subagent" {
		call["parent_tool_use_id"] = "parent-1"
	}
	emit(call)
	if mode == "execute-claude-duplicate-call" {
		emit(call)
	}
	if mode == "execute-claude-session" {
		session = "other-session"
	}
	if mode != "execute-claude-pending" {
		id := "call-1"
		if mode == "execute-claude-unknown-result" {
			id = "unseen"
		}
		emit(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "fixture-private-result"}}}})
	}
	if mode == "execute-claude-tool-summary" || mode == "execute-claude-bad-tool-summary" {
		id := "call-1"
		if mode == "execute-claude-bad-tool-summary" {
			id = "unseen"
		}
		emit(map[string]any{"type": "tool_use_summary", "preceding_tool_use_ids": []string{id}, "summary": "fixture-private-summary"})
	}
	summary := "Claude协议夹具完成，仅代表本机受控测试。"
	if mode == "execute-claude-echo-token" {
		summary += " " + executionFixtureToken
	}
	result := map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": summary, "permission_denials": []any{}}
	if mode == "execute-claude-denied" {
		result["permission_denials"] = []any{map[string]string{"tool_name": tool, "secret": executionFixtureToken}}
	}
	if mode == "execute-claude-provider-error" {
		result["subtype"] = "error_during_execution"
		result["is_error"] = true
	}
	if mode == "execute-claude-deferred" {
		result["deferred_tool_use"] = map[string]any{"tool": tool}
	}
	if mode != "execute-claude-incomplete" {
		emit(result)
	}
	if mode == "execute-claude-post-terminal" {
		emit(result)
	}
	return 0
}

func TestClaudeExecutableTransportAndTokenBoundary(t *testing.T) {
	for _, mode := range []string{"execute-claude-success", "execute-claude-echo-token", "execute-claude-tool-summary"} {
		t.Run(mode, func(t *testing.T) {
			p := executionFixturePlanner(t, mode)
			in := executionFixtureInput()
			in.Provider = Claude
			var events []Event
			result, err := p.Execute(context.Background(), in, func(e Event) { events = append(events, e) })
			if err != nil || result.Provider != Claude || result.Summary == "" {
				t.Fatal(result, err)
			}
			data, _ := json.Marshal(struct {
				Result ExecutionResult
				Events []Event
				Input  ExecutionInput
			}{result, events, in})
			for _, secret := range []string{executionFixtureToken, "chain-of-thought", "private-result"} {
				if bytes.Contains(data, []byte(secret)) {
					t.Fatal("公开结果泄漏私有载荷")
				}
			}
			files, _ := os.ReadDir(p.config.WorkDir)
			if len(files) != 0 {
				t.Fatal("私有工作目录未清理")
			}
		})
	}
}
func TestClaudeExecutableRejectsUnverifiedMalformedAndUnauthorized(t *testing.T) {
	for _, suffix := range []string{"no-init", "init-tool", "init-plugin", "init-permission", "init-cwd", "builtin", "foreign", "unknown-tool", "unknown-result", "duplicate-call", "session", "subagent", "hook", "pending", "denied", "provider-error", "deferred", "incomplete", "post-terminal", "flood", "bad-tool-summary"} {
		t.Run(suffix, func(t *testing.T) {
			p := executionFixturePlanner(t, "execute-claude-"+suffix)
			in := executionFixtureInput()
			in.Provider = Claude
			if _, err := p.Execute(context.Background(), in, nil); err == nil || strings.Contains(err.Error(), executionFixtureToken) {
				t.Fatal("错误事件被接受或泄漏凭据", err)
			}
		})
	}
	p := executionFixturePlanner(t, "execute-claude-sleep")
	in := executionFixtureInput()
	in.Provider = Claude
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := p.Execute(ctx, in, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("取消未传递", err)
	}
	p.executionInspector = func(context.Context, Provider, ExecutionMCP) Installation {
		return Installation{Installed: true, Isolation: Isolation{Status: "policy_unverified"}}
	}
	if _, err := p.Execute(context.Background(), in, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal("未知策略降级执行", err)
	}
}
func TestClaudeCommandIsFirstPartyEnvOnlyAndNotSafeMode(t *testing.T) {
	in := executionFixtureInput()
	spec := buildClaudeExecutionCommand("/cli", "/private/job", "/private/profile", in.MCP, []byte("prompt"))
	joined := strings.Join(spec.args, "\n")
	for _, flag := range []string{"--restricted", "--tools\n\n", "--strict-mcp-config", "--setting-sources\n\n", "--permission-mode\ndontAsk", "--permission-prompts\nnone", "--disable-slash-commands", "--no-session-persistence", "--output-format\nstream-json"} {
		if !strings.Contains(joined, flag) {
			t.Fatal("缺少执行约束", flag)
		}
	}
	for _, bad := range []string{executionFixtureToken, "--safe-mode", "--bare", "bypassPermissions", "--output-last-message"} {
		if strings.Contains(joined, bad) {
			t.Fatal("Claude构造包含不适用参数或凭据", bad)
		}
	}
	if !strings.Contains(joined, "Bearer ${"+executionTokenVariable+"}") || !strings.Contains(strings.Join(spec.environment, "\n"), "CLAUDE_CONFIG_DIR=/private/profile") {
		t.Fatal("缺少独立认证或env引用")
	}
	p := executionFixturePlanner(t, "execute-claude-sleep")
	base := p.environment
	p.environment = func(provider Provider, profile string) []string {
		return append(base(provider, profile), "CLAUDE_CODE_SAFE_MODE=1")
	}
	in.Provider = Claude
	if _, err := p.Execute(context.Background(), in, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatal("不能覆盖调用环境要求的safe-mode", err)
	}
}
