package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fixtureTransport struct {
	base          *http.Transport
	authorization string
	legacy        bool
	mu            sync.Mutex
	versions      []string
}

func (transport *fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	request.Header.Set("Authorization", transport.authorization)
	var body []byte
	if request.Body != nil {
		var err error
		body, err = io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
	}
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.Unmarshal(body, &message)
	if transport.legacy && message.Method == "server/discover" {
		// SDK没有公开强制旧版本选项：只在fixture中拒绝discover，让官方客户端走真实legacy协商分支。
		data, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32601, "message": "fixture legacy discovery unavailable"}})
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
	}
	if message.Method == "tools/call" {
		transport.mu.Lock()
		transport.versions = append(transport.versions, request.Header.Get("MCP-Protocol-Version"))
		transport.mu.Unlock()
	}
	return transport.base.RoundTrip(request)
}

func connectFixtureClient(t *testing.T, server *Server, legacy bool) (*mcp.ClientSession, *fixtureTransport) {
	t.Helper()
	config, err := server.ConnectionConfig()
	if err != nil {
		t.Fatal(err)
	}
	transport := &fixtureTransport{base: &http.Transport{MaxIdleConns: 8, MaxIdleConnsPerHost: 8, IdleConnTimeout: time.Second}, authorization: config.HTTPHeaders["Authorization"], legacy: legacy}
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture-client", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: config.URL, HTTPClient: &http.Client{Transport: transport}, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(); transport.base.CloseIdleConnections() })
	return session, transport
}
func startFixture(t *testing.T, options Options) *Server {
	t.Helper()
	if options.ProjectID == "" {
		options.ProjectID = "project-1"
	}
	server, err := Start(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}
func fixtureTool(handler func(context.Context, json.RawMessage) (any, error)) Tool {
	return Tool{Name: "project.snapshot", Description: "读取受控工程摘要", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object","properties":{"label":{"type":"string","maxLength":64}},"required":["label"],"additionalProperties":false}`), Handler: handler}
}

func TestOfficialClientCurrentAndLegacyStatelessProtocols(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "2026-07-28"
		if legacy {
			name = "2025-11-25"
		}
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			server := startFixture(t, Options{Tools: []Tool{fixtureTool(func(ctx context.Context, input json.RawMessage) (any, error) {
				calls.Add(1)
				var args struct {
					Label string `json:"label"`
				}
				if err := json.Unmarshal(input, &args); err != nil {
					return nil, err
				}
				return map[string]string{"projectId": "project-1", "label": args.Label}, nil
			})}})
			client, transport := connectFixtureClient(t, server, legacy)
			if actual := client.InitializeResult().ProtocolVersion; actual != name {
				t.Fatalf("协议没有实际协商到目标：%s", actual)
			}
			list, err := client.ListTools(context.Background(), &mcp.ListToolsParams{})
			if err != nil || len(list.Tools) != 1 || list.Tools[0].Annotations == nil || !list.Tools[0].Annotations.ReadOnlyHint {
				t.Fatalf("官方客户端工具目录错误：%+v %v", list, err)
			}
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "project.snapshot", Arguments: map[string]any{"label": "当前工程"}})
			if err != nil || result.IsError || calls.Load() != 1 || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "当前工程") {
				t.Fatalf("官方客户端真实工具调用失败：%+v %v", result, err)
			}
			transport.mu.Lock()
			versions := append([]string(nil), transport.versions...)
			transport.mu.Unlock()
			if len(versions) == 0 || versions[0] != name {
				t.Fatalf("实际HTTP调用版本不符：%v", versions)
			}
			result, err = client.CallTool(context.Background(), &mcp.CallToolParams{Name: "project.snapshot", Arguments: map[string]any{"label": "ok", "unexpected": true}})
			if err != nil || !result.IsError || calls.Load() != 1 {
				t.Fatal("不符合Schema的输入进入业务回调")
			}
			config, _ := server.ConnectionConfig()
			secret := strings.TrimPrefix(config.HTTPHeaders["Authorization"], "Bearer ")
			metadata, _ := json.Marshal(struct {
				Info  Info
				Tools *mcp.ListToolsResult
			}{server.Info(), list})
			if bytes.Contains(metadata, []byte(secret)) || !server.Info().Running || !strings.HasPrefix(server.Info().Address, "http://127.0.0.1:") {
				t.Fatal("公共信息泄漏会话凭据或监听范围错误")
			}
		})
	}
}

func rawRequest(t *testing.T, server *Server, configure func(*http.Request)) *http.Response {
	t.Helper()
	config, _ := server.ConnectionConfig()
	request, _ := http.NewRequest(http.MethodPost, config.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	request.Header.Set("Authorization", config.HTTPHeaders["Authorization"])
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	if configure != nil {
		configure(request)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestLoopbackAuthenticationOriginHostAndPeer(t *testing.T) {
	server := startFixture(t, Options{})
	for _, test := range []struct {
		name      string
		configure func(*http.Request)
		status    int
	}{
		{"missing-token", func(r *http.Request) { r.Header.Del("Authorization") }, 401},
		{"duplicate-token", func(r *http.Request) { r.Header.Add("Authorization", "Bearer other") }, 401},
		{"foreign-origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, 403},
		{"null-origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, 403},
		{"foreign-host", func(r *http.Request) { r.Host = "attacker.example" }, 403},
		{"query-token", func(r *http.Request) { r.URL.RawQuery = "token=never-accepted" }, 400},
		{"session-id", func(r *http.Request) { r.Header.Set("Mcp-Session-Id", "foreign-session") }, 400},
		{"get", func(r *http.Request) { r.Method = http.MethodGet }, 405},
	} {
		t.Run(test.name, func(t *testing.T) {
			if response := rawRequest(t, server, test.configure); response.StatusCode != test.status {
				t.Fatalf("错误状态：%d", response.StatusCode)
			}
		})
	}
	config, _ := server.ConnectionConfig()
	request := httptest.NewRequest(http.MethodPost, config.URL, strings.NewReader(`{}`))
	request.RemoteAddr = "198.51.100.1:3000"
	request.Header.Set("Authorization", config.HTTPHeaders["Authorization"])
	recorder := httptest.NewRecorder()
	reached := false
	server.protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })).ServeHTTP(recorder, request)
	if reached || recorder.Code != 403 {
		t.Fatal("非loopback peer进入MCP处理")
	}
	config.HTTPHeaders["Authorization"] = "mutated"
	if again, _ := server.ConnectionConfig(); again.HTTPHeaders["Authorization"] == "mutated" {
		t.Fatal("显式复制配置能修改服务器凭据")
	}
}

func TestInputOutputSchemaAndErrorLimits(t *testing.T) {
	var calls atomic.Int32
	server := startFixture(t, Options{MaxInputBytes: 1024, MaxOutputBytes: 4096, Tools: []Tool{fixtureTool(func(context.Context, json.RawMessage) (any, error) {
		calls.Add(1)
		return map[string]string{"data": strings.Repeat("x", 5000)}, nil
	})}})
	response := rawRequest(t, server, func(request *http.Request) {
		body := []byte(`{"data":"` + strings.Repeat("x", 1500) + `"}`)
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = -1
	})
	if response.StatusCode != http.StatusRequestEntityTooLarge || calls.Load() != 0 {
		t.Fatal("chunked超限请求没有在业务之前拒绝")
	}
	client, _ := connectFixtureClient(t, server, false)
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "project.snapshot", Arguments: map[string]any{"label": "ok"}})
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "output_limit") {
		t.Fatalf("超限输出被发出：%+v %v", result, err)
	}
	private := startFixture(t, Options{Tools: []Tool{fixtureTool(func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New("fixture-secret-do-not-publish")
	})}})
	other, _ := connectFixtureClient(t, private, false)
	result, err = other.CallTool(context.Background(), &mcp.CallToolParams{Name: "project.snapshot", Arguments: map[string]any{"label": "ok"}})
	if err != nil || !result.IsError || strings.Contains(result.Content[0].(*mcp.TextContent).Text, "fixture-secret") {
		t.Fatal("原始内部错误泄漏")
	}
	if _, err := Start(Options{ProjectID: "p", Tools: []Tool{{Name: "bad", InputSchema: json.RawMessage(`{"type":"object","$ref":"https://external.example/schema"}`), Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }}}}); err == nil {
		t.Fatal("Schema不能自动加载远端引用")
	}
}

func TestStopCancelsCallbacksForBothProtocols(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "legacy"}[legacy], func(t *testing.T) {
			started, cancelled := make(chan struct{}), make(chan struct{})
			server := startFixture(t, Options{Tools: []Tool{fixtureTool(func(ctx context.Context, _ json.RawMessage) (any, error) {
				close(started)
				<-ctx.Done()
				close(cancelled)
				return nil, ctx.Err()
			})}})
			client, _ := connectFixtureClient(t, server, legacy)
			finished := make(chan struct{})
			go func() {
				_, _ = client.CallTool(context.Background(), &mcp.CallToolParams{Name: "project.snapshot", Arguments: map[string]any{"label": "wait"}})
				close(finished)
			}()
			<-started
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := server.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-cancelled:
			default:
				t.Fatal("Stop没有取消业务回调")
			}
			select {
			case <-server.Done():
			default:
				t.Fatal("协作回调退出后Done未关闭")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("客户端仍在等待已停止服务")
			}
			if server.Info().Running {
				t.Fatal("停止后仍标运行")
			}
			if _, err := server.ConnectionConfig(); err == nil {
				t.Fatal("停止后仍可复制旧会话配置")
			}
		})
	}
}

func TestTimeoutDoesNotReleaseUnfinishedCallbackCapacity(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls atomic.Int32
	server := startFixture(t, Options{RequestTimeout: 100 * time.Millisecond, MaxConcurrentCalls: 1, Tools: []Tool{fixtureTool(func(context.Context, json.RawMessage) (any, error) {
		calls.Add(1)
		close(started)
		<-release
		return map[string]bool{"done": true}, nil
	})}})
	client, _ := connectFixtureClient(t, server, false)
	finished := make(chan struct{})
	go func() {
		_, _ = client.CallTool(context.Background(), &mcp.CallToolParams{Name: "project.snapshot", Arguments: map[string]any{"label": "first"}})
		close(finished)
	}()
	<-started
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("超时没有结束请求等待")
	}
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "project.snapshot", Arguments: map[string]any{"label": "second"}})
	if err != nil || !result.IsError || calls.Load() != 1 {
		t.Fatalf("超时的回调仍在执行，不能复用其并发槽：%+v %v", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if !errors.Is(server.Stop(ctx), context.DeadlineExceeded) {
		t.Fatal("未退出的回调不能被声称完全停止")
	}
	select {
	case <-server.Done():
		t.Fatal("Done早于真实回调退出")
	default:
	}
	once.Do(func() { close(release) })
	select {
	case <-server.Done():
	case <-time.After(time.Second):
		t.Fatal("回调退出后未完成清理")
	}
}

func TestProjectGuardRejectsSwitchedContext(t *testing.T) {
	var calls atomic.Int32
	server := startFixture(t, Options{ValidateProject: func(context.Context, string) error { return errors.New("changed") }, Tools: []Tool{fixtureTool(func(context.Context, json.RawMessage) (any, error) { calls.Add(1); return nil, nil })}})
	client, _ := connectFixtureClient(t, server, false)
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "project.snapshot", Arguments: map[string]any{"label": "ok"}})
	if err != nil || !result.IsError || calls.Load() != 0 {
		t.Fatal("工程上下文改变后仍进入业务工具")
	}
}

func TestHTTPConcurrencyAndRestartRotateCredentials(t *testing.T) {
	started := make(chan struct{})
	server := startFixture(t, Options{MaxConcurrentRequests: 1, MaxConcurrentCalls: 1, Tools: []Tool{fixtureTool(func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})}})
	config, _ := server.ConnectionConfig()
	request, _ := http.NewRequest(http.MethodPost, config.URL, strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"project.snapshot","arguments":{"label":"wait"}}}`))
	request.Header.Set("Authorization", config.HTTPHeaders["Authorization"])
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	finished := make(chan struct{})
	go func() {
		response, _ := (&http.Client{Timeout: 2 * time.Second}).Do(request)
		if response != nil {
			response.Body.Close()
		}
		close(finished)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fixture业务未进入")
	}
	if response := rawRequest(t, server, nil); response.StatusCode != 429 {
		t.Fatalf("HTTP临时会话并发未受限：%d", response.StatusCode)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	<-finished
	other := startFixture(t, Options{})
	replacement, _ := other.ConnectionConfig()
	if replacement.HTTPHeaders["Authorization"] == config.HTTPHeaders["Authorization"] {
		t.Fatal("重启复用了旧会话凭据")
	}
}

func TestDuplicateJSONAndPublicBusinessError(t *testing.T) {
	if _, err := checkedJSON([]byte(`{"revision":1,"revision":2}`)); err == nil {
		t.Fatal("重复字段不能进入不同解码器")
	}
	server := startFixture(t, Options{Tools: []Tool{fixtureTool(func(context.Context, json.RawMessage) (any, error) {
		return nil, &ToolError{Code: "revision_conflict", Message: "工程版本已变化，请重新读取"}
	})}})
	client, _ := connectFixtureClient(t, server, false)
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "project.snapshot", Arguments: map[string]any{"label": "ok"}})
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "revision_conflict") {
		t.Fatal("明确公开的业务错误没有保留")
	}
}
