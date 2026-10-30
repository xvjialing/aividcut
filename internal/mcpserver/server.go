package mcpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/net/netutil"
)

type Server struct {
	mu                    sync.Mutex
	options               Options
	tools                 []registeredTool
	address, host, secret string
	stopped               bool
	ctx                   context.Context
	cancel                context.CancelFunc
	http                  *http.Server
	listener              net.Listener
	requests, calls       chan struct{}
	wg                    sync.WaitGroup
	serveDone, done       chan struct{}
	stopOnce              sync.Once
}

func normalizeOptions(options Options) (Options, error) {
	if strings.TrimSpace(options.ProjectID) == "" || len(options.ProjectID) > 256 || len(options.Tools) > 64 {
		return options, errors.New("MCP需要工程身份，且工具最多64项")
	}
	if options.RequestTimeout == 0 {
		options.RequestTimeout = 30 * time.Second
	}
	if options.StopTimeout == 0 {
		options.StopTimeout = 2 * time.Second
	}
	if options.MaxInputBytes == 0 {
		options.MaxInputBytes = 1 << 20
	}
	if options.MaxOutputBytes == 0 {
		options.MaxOutputBytes = 2 << 20
	}
	if options.MaxConcurrentRequests == 0 {
		options.MaxConcurrentRequests = 8
	}
	if options.MaxConcurrentCalls == 0 {
		options.MaxConcurrentCalls = min(2, options.MaxConcurrentRequests)
	}
	if options.RequestTimeout < 100*time.Millisecond || options.RequestTimeout > 2*time.Minute || options.StopTimeout < 100*time.Millisecond || options.StopTimeout > 10*time.Second {
		return options, errors.New("MCP超时参数超出本轮范围")
	}
	if options.MaxInputBytes < 1024 || options.MaxInputBytes > 8<<20 || options.MaxOutputBytes < 4096 || options.MaxOutputBytes > 16<<20 {
		return options, errors.New("MCP输入/输出限额无效")
	}
	if options.MaxConcurrentRequests < 1 || options.MaxConcurrentRequests > 32 || options.MaxConcurrentCalls < 1 || options.MaxConcurrentCalls > 8 || options.MaxConcurrentCalls > options.MaxConcurrentRequests {
		return options, errors.New("MCP并发限额无效")
	}
	return options, nil
}

func Start(options Options) (*Server, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	tools, err := registerTools(options.Tools, options.MaxOutputBytes)
	if err != nil {
		return nil, err
	}
	if err = validateSDKRegistration(tools); err != nil {
		return nil, err
	}
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, errors.New("无法创建MCP会话凭据")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("无法启动loopback MCP服务")
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{options: options, tools: tools, host: listener.Addr().String(), secret: base64.RawURLEncoding.EncodeToString(random[:]), ctx: ctx, cancel: cancel, requests: make(chan struct{}, options.MaxConcurrentRequests), calls: make(chan struct{}, options.MaxConcurrentCalls), serveDone: make(chan struct{}), done: make(chan struct{})}
	server.address = "http://" + server.host + "/mcp"
	server.listener = netutil.LimitListener(listener, options.MaxConcurrentRequests+2)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server { return server.protocolServer(request.Context()) }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: options.MaxInputBytes, PropagateRequestCancellation: true, Logger: logger})
	server.http = &http.Server{Handler: server.protect(handler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: options.RequestTimeout, WriteTimeout: options.RequestTimeout + time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { _ = server.http.Serve(server.listener); close(server.serveDone); server.beginStop() }()
	return server, nil
}

func (server *Server) Info() Info {
	server.mu.Lock()
	defer server.mu.Unlock()
	return Info{Address: server.address, ProjectID: server.options.ProjectID, Running: !server.stopped, ToolCount: len(server.tools)}
}
func (server *Server) ConnectionConfig() (ConnectionConfig, error) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.stopped {
		return ConnectionConfig{}, errors.New("MCP服务已停止，会话凭据已失效")
	}
	return ConnectionConfig{URL: server.address, Transport: "streamable-http", HTTPHeaders: map[string]string{"Authorization": "Bearer " + server.secret}}, nil
}
func (server *Server) Done() <-chan struct{} { return server.done }
func (server *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), server.options.StopTimeout)
	defer cancel()
	return server.Stop(ctx)
}
func (server *Server) Stop(ctx context.Context) error {
	server.beginStop()
	select {
	case <-server.done:
		return nil
	default:
	}
	select {
	case <-server.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (server *Server) beginStop() {
	server.stopOnce.Do(func() {
		server.mu.Lock()
		server.stopped = true
		server.secret = ""
		server.mu.Unlock()
		server.cancel()
		_ = server.http.Close()
		_ = server.listener.Close()
		go func() { <-server.serveDone; server.wg.Wait(); close(server.done) }()
	})
}

func (server *Server) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		peer, _, err := net.SplitHostPort(request.RemoteAddr)
		ip := net.ParseIP(peer)
		if err != nil || ip == nil || ip.To4() == nil || !ip.IsLoopback() || request.Host != server.host {
			http.Error(writer, "拒绝非本机或不匹配Host请求", http.StatusForbidden)
			return
		}
		// CLI无Origin；网页、file/null Origin和跨站预检均不被本地凭据授权。
		if len(request.Header.Values("Origin")) != 0 || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(writer, "拒绝浏览器跨源访问", http.StatusForbidden)
			return
		}
		if request.URL.EscapedPath() != "/mcp" {
			http.NotFound(writer, request)
			return
		}
		if request.URL.RawQuery != "" {
			http.Error(writer, "MCP地址不接受查询参数", http.StatusBadRequest)
			return
		}
		server.mu.Lock()
		if server.stopped {
			server.mu.Unlock()
			http.Error(writer, "MCP服务已停止", http.StatusServiceUnavailable)
			return
		}
		authorization := request.Header.Values("Authorization")
		valid := len(authorization) == 1 && subtle.ConstantTimeCompare([]byte(authorization[0]), []byte("Bearer "+server.secret)) == 1
		if !valid {
			server.mu.Unlock()
			writer.Header().Set("WWW-Authenticate", `Bearer realm="aividcut-local-mcp"`)
			http.Error(writer, "需要当前MCP会话凭据", http.StatusUnauthorized)
			return
		}
		select {
		case server.requests <- struct{}{}:
			server.wg.Add(1)
		default:
			server.mu.Unlock()
			http.Error(writer, "MCP并发请求达到上限", http.StatusTooManyRequests)
			return
		}
		server.mu.Unlock()
		defer func() { <-server.requests; server.wg.Done() }()
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", "POST")
			http.Error(writer, "仅支持无状态POST", http.StatusMethodNotAllowed)
			return
		}
		if request.Header.Get("Mcp-Session-Id") != "" {
			http.Error(writer, "本服务不接受持久会话标识", http.StatusBadRequest)
			return
		}
		if encoding := request.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
			http.Error(writer, "不支持压缩请求体", http.StatusUnsupportedMediaType)
			return
		}
		if request.ContentLength > server.options.MaxInputBytes {
			http.Error(writer, "MCP输入超过限制", http.StatusRequestEntityTooLarge)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), server.options.RequestTimeout)
		defer cancel()
		stop := context.AfterFunc(server.ctx, cancel)
		defer stop()
		request = request.WithContext(ctx)
		request.Body = http.MaxBytesReader(writer, request.Body, server.options.MaxInputBytes)
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			var maximum *http.MaxBytesError
			if errors.As(readErr, &maximum) {
				http.Error(writer, "MCP输入超过限制", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(writer, "MCP输入读取失败", http.StatusBadRequest)
			}
			return
		}
		if ctx.Err() != nil {
			http.Error(writer, "MCP请求读取已取消或超时", http.StatusGatewayTimeout)
			return
		}
		message, jsonErr := checkedJSON(body)
		if _, ok := message.(map[string]any); jsonErr != nil || !ok {
			http.Error(writer, "仅接受单个有界JSON-RPC对象", http.StatusBadRequest)
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		response := newLimitedResponse(server.options.MaxOutputBytes)
		next.ServeHTTP(response, request)
		if response.err != nil {
			http.Error(writer, "MCP响应超过限制", http.StatusInternalServerError)
			return
		}
		if ctx.Err() != nil {
			http.Error(writer, "MCP请求已取消或超时；不代表业务回调已回滚", http.StatusGatewayTimeout)
			return
		}
		for key, values := range response.header {
			if strings.EqualFold(key, "Mcp-Session-Id") || strings.EqualFold(key, "Content-Length") {
				continue
			}
			writer.Header()[key] = append([]string(nil), values...)
		}
		code := response.code
		if code == 0 {
			code = http.StatusOK
		}
		writer.WriteHeader(code)
		_, _ = writer.Write(response.data)
	})
}
