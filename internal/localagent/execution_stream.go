package localagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

type executionStream struct {
	tools     []string
	notify    func(Event)
	summary   string
	completed bool
}

func (stream *executionStream) line(data []byte) error {
	if stream.completed {
		return fmt.Errorf("%w: 执行结束后仍出现事件", ErrProtocol)
	}
	var event struct {
		Type string `json:"type"`
		Item struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Server string `json:"server"`
			Tool   string `json:"tool"`
		} `json:"item"`
	}
	if json.Unmarshal(data, &event) != nil {
		return ErrProtocol
	}
	switch event.Type {
	case "thread.started", "turn.started":
		if stream.notify != nil {
			stream.notify(Event{Stage: "executing", Message: "第一方MCP执行会话正在处理当前工程"})
		}
	case "item.started", "item.updated", "item.completed":
		switch event.Item.Type {
		case "reasoning": // 不输出chainofthought。
		case "agent_message":
			if event.Type == "item.completed" {
				if len(event.Item.Text) > 64<<10 || !utf8.ValidString(event.Item.Text) {
					return ErrOutputLimit
				}
				stream.summary = event.Item.Text
			}
		case "mcp_tool_call":
			name := strings.TrimPrefix(event.Item.Tool, "mcp__aividcut__")
			allowed := false
			for _, tool := range stream.tools {
				if tool == name {
					allowed = true
					break
				}
			}
			if event.Item.Server != executionServerName || !allowed {
				return fmt.Errorf("%w: 执行出现非第一方或非白名单工具", ErrUnsupported)
			}
			if stream.notify != nil {
				stream.notify(Event{Stage: "tool", Message: "正在处理已授权的AIVidCut工具；参数与原始结果不转发到事件日志"})
			}
		default:
			return fmt.Errorf("%w: 执行出现未授权内置工具或未知操作事件", ErrUnsupported)
		}
	case "turn.completed":
		stream.completed = true
	case "turn.failed", "error":
		return failure("execution_provider_error", "CLI执行未正常完成，请核查工具记录；不自动回滚已成功操作", nil)
	default:
		return fmt.Errorf("%w: 未识别的执行事件", ErrProtocol)
	}
	return nil
}
func (stream *executionStream) result() (string, error) {
	if !stream.completed || strings.TrimSpace(stream.summary) == "" {
		return "", fmt.Errorf("%w: 未正常结束或缺少执行总结", ErrProtocol)
	}
	return stream.summary, nil
}
