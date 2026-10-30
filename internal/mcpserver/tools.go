package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"regexp"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var toolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type registeredTool struct {
	definition Tool
	schema     *jsonschema.Schema
	resolved   *jsonschema.Resolved
}

func registerTools(tools []Tool, maximum int64) ([]registeredTool, error) {
	result := []registeredTool{}
	names := map[string]bool{}
	var catalogBytes int64
	for _, tool := range tools {
		if !toolName.MatchString(tool.Name) || names[tool.Name] || !utf8.ValidString(tool.Description) || len(tool.Description) > 8192 || tool.Handler == nil || len(tool.InputSchema) == 0 || len(tool.InputSchema) > 64<<10 {
			return nil, errors.New("MCP工具名称、说明、回调或Schema无效")
		}
		names[tool.Name] = true
		value, err := checkedJSON(tool.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("工具%s的Schema无效", tool.Name)
		}
		object, ok := value.(map[string]any)
		if !ok || object["type"] != "object" {
			return nil, errors.New("MCP工具输入Schema须明确为object")
		}
		var schema jsonschema.Schema
		if json.Unmarshal(tool.InputSchema, &schema) != nil {
			return nil, errors.New("MCP工具Schema不是受支持结构")
		}
		resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true}) // 不配置Loader，不读取远端引用。
		if err != nil {
			return nil, fmt.Errorf("工具%s的Schema无法在本地解析", tool.Name)
		}
		catalogBytes += int64(len(tool.InputSchema) + len(tool.Description) + len(tool.Name) + 256)
		if catalogBytes > maximum/3 {
			return nil, errors.New("MCP工具目录超过当前响应预算")
		}
		tool.InputSchema = append(json.RawMessage(nil), tool.InputSchema...)
		result = append(result, registeredTool{definition: tool, schema: &schema, resolved: resolved})
	}
	return result, nil
}

func sdkTool(tool Tool) *mcp.Tool {
	destructive, openWorld := tool.DestructiveHint, tool.OpenWorldHint
	return &mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: tool.ReadOnly, DestructiveHint: &destructive, IdempotentHint: tool.IdempotentHint, OpenWorldHint: &openWorld}}
}

func validateSDKRegistration(tools []registeredTool) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("MCP工具声明未通过固定SDK注册校验")
		}
	}()
	protocol := mcp.NewServer(&mcp.Implementation{Name: "aividcut-local-tools", Version: "0.1.0"}, &mcp.ServerOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Capabilities: &mcp.ServerCapabilities{}})
	for _, tool := range tools {
		protocol.AddTool(sdkTool(tool.definition), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return nil, nil })
	}
	return nil
}

func (server *Server) protocolServer(httpContext context.Context) *mcp.Server {
	// 每个无状态请求独立注册，捕获HTTP生命周期；旧协议也不脱离原请求取消。
	protocol := mcp.NewServer(&mcp.Implementation{Name: "aividcut-local-tools", Version: "0.1.0"}, &mcp.ServerOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: false}}, PageSize: 32})
	for _, tool := range server.tools {
		protocol.AddTool(sdkTool(tool.definition), func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return server.callTool(httpContext, ctx, tool, request.Params.Arguments), nil
		})
	}
	return protocol
}

func toolFailure(code, message string) *mcp.CallToolResult {
	data, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

func (server *Server) callTool(httpContext, sdkContext context.Context, tool registeredTool, arguments json.RawMessage) *mcp.CallToolResult {
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	if int64(len(arguments)) > server.options.MaxInputBytes {
		return toolFailure("input_limit", "工具输入超过限制")
	}
	value, err := checkedJSON(arguments)
	if err != nil {
		return toolFailure("invalid_arguments", "工具参数JSON无效或结构超过限制")
	}
	if _, ok := value.(map[string]any); !ok || tool.resolved.Validate(value) != nil {
		return toolFailure("invalid_arguments", "工具参数不符合声明的Schema")
	}
	ctx, cancel := context.WithTimeout(httpContext, server.options.RequestTimeout)
	defer cancel()
	stopSDK := context.AfterFunc(sdkContext, cancel)
	defer stopSDK()
	stopServer := context.AfterFunc(server.ctx, cancel)
	defer stopServer()
	server.mu.Lock()
	if server.stopped || ctx.Err() != nil {
		server.mu.Unlock()
		return toolFailure("stopped", "MCP请求已停止")
	}
	select {
	case server.calls <- struct{}{}:
		server.wg.Add(1)
	default:
		server.mu.Unlock()
		return toolFailure("busy", "工具并发已达上限，请等待现有回调结束")
	}
	server.mu.Unlock()
	arguments = append(json.RawMessage(nil), arguments...)
	result := make(chan *mcp.CallToolResult, 1)
	go func() {
		defer func() { <-server.calls; server.wg.Done() }()
		defer func() {
			if recover() != nil {
				result <- toolFailure("tool_failed", "工具回调异常，未返回内部诊断")
			}
		}()
		if server.options.ValidateProject != nil {
			if err := server.options.ValidateProject(ctx, server.options.ProjectID); err != nil {
				result <- toolFailure("project_changed", "当前工程与MCP会话不一致，请重新连接")
				return
			}
		}
		if ctx.Err() != nil {
			result <- toolFailure("cancelled", "工具请求已取消")
			return
		}
		output, err := tool.definition.Handler(ctx, arguments)
		if err != nil {
			var public *ToolError
			if errors.As(err, &public) && len(public.Code) <= 64 && len(public.Message) <= 1024 && utf8.ValidString(public.Message) {
				result <- toolFailure(public.Code, public.Message)
			} else {
				result <- toolFailure("tool_failed", "工具处理失败，未返回原始内部错误")
			}
			return
		}
		if ctx.Err() != nil {
			result <- toolFailure("cancelled", "工具请求已停止，未继续编码过期结果")
			return
		}
		if pointer, ok := output.(*ImageResult); ok {
			if pointer == nil {
				result <- toolFailure("image_count", "图像结果为空")
				return
			}
			output = *pointer
		}
		if visual, ok := output.(ImageResult); ok {
			result <- encodeImageResult(visual, server.options.MaxOutputBytes)
			return
		}
		data, err := json.Marshal(output)
		if err != nil || int64(len(data)) > server.options.MaxOutputBytes/4 {
			result <- toolFailure("output_limit", "工具结果无法编码或超过响应预算，请缩小范围/分页")
			return
		}
		if _, err = checkedJSON(data); err != nil {
			result <- toolFailure("output_invalid", "工具结果不是受支持的有界JSON")
			return
		}
		if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
			data = append(append([]byte(`{"result":`), data...), '}')
		}
		result <- &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}, StructuredContent: json.RawMessage(data)}
	}()
	select {
	case value := <-result:
		return value
	case <-ctx.Done():
		return toolFailure("cancelled_or_timeout", "请求已取消或超时；不代表回调已回滚，勿盲目重复业务操作")
	}
}

// 使用最终SDK结果估算预算，不能绕过普通JSON/有界HTTP响应的防护。
func encodeImageResult(value ImageResult, maximum int64) *mcp.CallToolResult {
	if len(value.Images) == 0 || len(value.Images) > 3 {
		return toolFailure("image_count", "图像工具每次需要1–3张图片")
	}
	metadata, err := json.Marshal(value.Metadata)
	if err != nil || int64(len(metadata)) > maximum/4 {
		return toolFailure("output_limit", "图像元数据超过响应预算")
	}
	if _, err = checkedJSON(metadata); err != nil || !bytes.HasPrefix(bytes.TrimSpace(metadata), []byte("{")) {
		return toolFailure("output_invalid", "图像元数据须为有界对象")
	}
	output := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(metadata)}}, StructuredContent: json.RawMessage(metadata)}
	for _, item := range value.Images {
		if len(item.Data) == 0 || len(item.Data) > 256<<10 || (item.MIME != "image/jpeg" && item.MIME != "image/png") {
			return toolFailure("image_type", "图像工具仅支持256KiB以内的JPEG或PNG")
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(item.Data))
		if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 1024 || config.Height > 1024 || item.MIME != "image/"+format {
			return toolFailure("image_type", "图像内容、尺寸与声明不一致")
		}
		if _, _, err = image.Decode(bytes.NewReader(item.Data)); err != nil {
			return toolFailure("image_type", "图像内容不完整")
		}
		output.Content = append(output.Content, &mcp.ImageContent{MIMEType: item.MIME, Data: append([]byte{}, item.Data...)})
	}
	encoded, err := json.Marshal(output)
	if err != nil || int64(len(encoded)) > maximum/3 {
		return toolFailure("output_limit", "图像结果超过响应预算，请减少帧数或使用overview")
	}
	return output
}
