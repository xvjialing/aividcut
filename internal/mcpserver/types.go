// Package mcpserver 提供受限loopback MCP传输。业务权限、编辑提交与收费授权由应用回调负责。
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type Tool struct {
	Name            string
	Description     string
	InputSchema     json.RawMessage
	Handler         func(context.Context, json.RawMessage) (any, error)
	ReadOnly        bool
	DestructiveHint bool
	IdempotentHint  bool
	OpenWorldHint   bool
}

// ImageResult 只允许内联栅格图片与结构化元数据，图片不进入普通JSON文本。
type ImageResult struct {
	Metadata any
	Images   []Image
}

// 禁止嵌套或误用为普通JSON；根结果由SDK图像编码通道处理。
func (ImageResult) MarshalJSON() ([]byte, error) {
	return nil, errors.New("图像结果必须通过MCP图像内容编码")
}

type Image struct {
	MIME string
	Data []byte
}

type Options struct {
	ProjectID string
	Tools     []Tool
	// 可选入口核查不能替代Handler提交时的工程身份/revision原子校验。
	ValidateProject       func(context.Context, string) error
	RequestTimeout        time.Duration
	MaxInputBytes         int64
	MaxOutputBytes        int64
	MaxConcurrentRequests int
	MaxConcurrentCalls    int
	StopTimeout           time.Duration
}

type Info struct {
	Address   string `json:"address"`
	ProjectID string `json:"projectId"`
	Running   bool   `json:"running"`
	ToolCount int    `json:"toolCount"`
}

// ConnectionConfig 仅由显式getter返回给用户复制，不放入普通Info、项目文件或日志。
type ConnectionConfig struct {
	URL         string            `json:"url"`
	Transport   string            `json:"transport"`
	HTTPHeaders map[string]string `json:"httpHeaders"`
}

// ToolError 只允许应用明确标为可公开的业务诊断，普通error不会原样返回给客户端。
type ToolError struct {
	Code    string
	Message string
}

func (e *ToolError) Error() string { return e.Message }
