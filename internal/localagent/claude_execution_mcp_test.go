package localagent

import (
	"aividcut/internal/editor"
	"aividcut/internal/mcpserver"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type claudeFixtureTransport struct{ authorization string }

func (t claudeFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("Authorization", t.authorization)
	return http.DefaultTransport.RoundTrip(r)
}

// 此可执行文件只模拟CLI协议，业务调用走真实官方MCP客户端与核心；不访问模型。
func fixtureClaudeMCPCLI(mode string) int {
	var config struct {
		Servers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	var names []string
	for i, arg := range os.Args[1:] {
		if arg == "--mcp-config" {
			if json.Unmarshal([]byte(os.Args[i+2]), &config) != nil {
				return 70
			}
		}
		if arg == "--allowedTools" {
			names = append(names, os.Args[i+2:]...)
			break
		}
	}
	server, ok := config.Servers[executionServerName]
	if !ok {
		return 71
	}
	token := os.Getenv(executionTokenVariable)
	if token == "" {
		return 72
	}
	prompt, _ := io.ReadAll(io.LimitReader(os.Stdin, 256<<10))
	if strings.Contains(string(prompt), token) {
		return 73
	}
	auth := strings.ReplaceAll(server.Headers["Authorization"], "${"+executionTokenVariable+"}", token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "claude-protocol-fixture", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: claudeFixtureTransport{auth}}, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return 74
	}
	defer session.Close()
	emit := func(v map[string]any) {
		v["session_id"] = "claude-mcp-fixture"
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
	}
	cwd, _ := os.Getwd()
	emit(map[string]any{"type": "system", "subtype": "init", "claude_code_version": "fixture", "cwd": cwd, "permissionMode": "dontAsk", "tools": names, "mcp_servers": []any{map[string]string{"name": "aividcut", "status": "connected"}}, "skills": []string{}, "plugins": []string{}})
	call := func(id, name string, args any) (*mcp.CallToolResult, error) {
		emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": "mcp__aividcut__" + name, "input": args}}}})
		r, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e == nil && !r.IsError {
			emit(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "内部MCP往返完成，原结果不写入输出"}}}})
		}
		return r, e
	}
	read, err := call("read-1", "aividcut_get_project", map[string]any{})
	if err != nil || read.IsError {
		return 75
	}
	data, _ := json.Marshal(read.StructuredContent)
	var project struct {
		Revision int64 `json:"revision"`
	}
	if json.Unmarshal(data, &project) != nil {
		return 76
	}
	c := editor.DefaultClip("claude-mcp-title", "track-text-1", "text")
	c.Text = "Claude协议经第一方MCP写入"
	c.DurationFrames = 60
	req := editor.EditRequest{BaseRevision: project.Revision, IdempotencyKey: "claude-fixture-edit", Label: "受控Claude协议回归", Commands: []editor.Command{{Type: "add_clip", Clip: &c}}}
	write, err := call("edit-1", "aividcut_apply_edit", map[string]any{"request": req})
	if err != nil || write.IsError {
		return 77
	}
	if mode == "execute-claude-mcp-sleep" {
		time.Sleep(60 * time.Second)
	}
	emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "permission_denials": []any{}, "result": "内部协议已通过真实第一方MCP添加文字，实际编辑可撤销；不是Claude模型推理验收。"})
	return 0
}

func TestClaudeTransportRealMCPAppliesCoreAndCanUndo(t *testing.T) {
	for _, cancelAfterWrite := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelAfterWrite), func(t *testing.T) {
			engine, err := editor.NewEngine(editor.NewProject("Claude MCP回归"))
			if err != nil {
				t.Fatal(err)
			}
			var reads, writes atomic.Int32
			committed := make(chan struct{})
			server, err := mcpserver.Start(mcpserver.Options{ProjectID: engine.Snapshot().ID, Tools: []mcpserver.Tool{
				{Name: "aividcut_get_project", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), Handler: func(context.Context, json.RawMessage) (any, error) {
					reads.Add(1)
					return map[string]any{"revision": engine.Snapshot().Revision}, nil
				}},
				{Name: "aividcut_apply_edit", InputSchema: json.RawMessage(`{"type":"object","required":["request"],"properties":{"request":{"type":"object"}},"additionalProperties":false}`), Handler: func(_ context.Context, data json.RawMessage) (any, error) {
					var input struct {
						Request editor.EditRequest `json:"request"`
					}
					if err := json.Unmarshal(data, &input); err != nil {
						return nil, err
					}
					p, err := engine.Apply(input.Request)
					if err == nil {
						writes.Add(1)
						close(committed)
					}
					return map[string]any{"revision": p.Revision}, err
				}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			connection, err := server.ConnectionConfig()
			if err != nil {
				t.Fatal(err)
			}
			mode := "execute-claude-mcp"
			if cancelAfterWrite {
				mode += "-sleep"
			}
			p := executionFixturePlanner(t, mode)
			in := executionFixtureInput()
			in.Provider = Claude
			in.MCP = ExecutionMCP{URL: connection.URL, Token: strings.TrimPrefix(connection.HTTPHeaders["Authorization"], "Bearer "), Tools: []string{"aividcut_get_project", "aividcut_apply_edit"}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := p.Execute(ctx, in, nil); done <- err }()
			if cancelAfterWrite {
				select {
				case <-committed:
					cancel()
				case <-time.After(5 * time.Second):
					t.Fatal("MCP写入未发生")
				}
			}
			select {
			case err := <-done:
				if !cancelAfterWrite && err != nil {
					t.Fatal(err)
				}
				if cancelAfterWrite && err == nil {
					t.Fatal("取消被称为成功")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("协议进程未退出")
			}
			if reads.Load() != 1 || writes.Load() != 1 || len(engine.Snapshot().Clips) != 1 {
				t.Fatal("真实MCP没有进入统一核心")
			}
			beforeUndo := engine.Snapshot().Revision
			restored, err := engine.Undo()
			if err != nil || len(restored.Clips) != 0 || restored.Revision <= beforeUndo {
				t.Fatal("实际操作不可撤销", err)
			}
		})
	}
}
