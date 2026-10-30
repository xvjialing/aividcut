package localagent

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type streamParser struct {
	provider      Provider
	final         json.RawMessage
	completed     bool
	notify        func(Event)
	internalTools map[string]bool
}

func (p *streamParser) emit(stage, message string) {
	if p.notify != nil {
		p.notify(Event{Stage: stage, Message: message})
	}
}

func (p *streamParser) line(data []byte) error {
	if p.completed {
		return fmt.Errorf("%w: 完成事件后出现额外数据", ErrProtocol)
	}
	var envelope struct {
		Type       string          `json:"type"`
		Subtype    string          `json:"subtype"`
		IsError    bool            `json:"is_error"`
		Result     string          `json:"result"`
		Structured json.RawMessage `json:"structured_output"`
		Item       json.RawMessage `json:"item"`
		Message    json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("%w: 事件不是 JSON", ErrProtocol)
	}
	if envelope.Type == "" {
		return fmt.Errorf("%w: 缺少事件类型", ErrProtocol)
	}
	if p.provider == Codex {
		switch envelope.Type {
		case "thread.started", "turn.started":
			p.emit("planning", "本地 CLI 正在生成候选剪辑计划")
		case "item.started", "item.updated", "item.completed":
			var item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(envelope.Item, &item); err != nil {
				return ErrProtocol
			}
			if item.Type != "agent_message" && item.Type != "reasoning" && item.Type != "plan" {
				return fmt.Errorf("%w: 规划模式出现工具或文件操作事件", ErrUnsupported)
			}
			if item.Type == "agent_message" && envelope.Type == "item.completed" {
				p.final = json.RawMessage(item.Text)
			}
		case "turn.completed":
			p.completed = true
		case "turn.failed", "error":
			return failure("provider_error", "本地 Codex 规划失败，未返回可提交的计划", nil)
		default:
			return fmt.Errorf("%w: 未识别的 Codex 事件", ErrProtocol)
		}
		return nil
	}
	switch envelope.Type {
	case "system":
		if envelope.Subtype == "permission_denied" || envelope.Subtype == "hook_started" || envelope.Subtype == "hook_response" {
			return fmt.Errorf("%w: 规划模式出现权限或 hook 事件", ErrUnsupported)
		}
		var init struct {
			Tools []string          `json:"tools"`
			MCP   []json.RawMessage `json:"mcp_servers"`
		}
		_ = json.Unmarshal(data, &init)
		if len(init.MCP) > 0 {
			return fmt.Errorf("%w: 初始化仍包含 MCP 服务", ErrUnsupported)
		}
		for _, tool := range init.Tools {
			if tool != "StructuredOutput" {
				return fmt.Errorf("%w: 初始化暴露了非规划工具", ErrUnsupported)
			}
		}
	case "assistant":
		var message struct {
			Content []struct {
				Type string `json:"type"`
				Name string `json:"name"`
				ID   string `json:"id"`
			} `json:"content"`
		}
		if err := json.Unmarshal(envelope.Message, &message); err != nil {
			return ErrProtocol
		}
		for _, content := range message.Content {
			if content.Type == "tool_use" && content.Name != "StructuredOutput" {
				return fmt.Errorf("%w: Claude 请求执行工具", ErrUnsupported)
			}
			if content.Type == "tool_use" && content.Name == "StructuredOutput" && content.ID != "" {
				if p.internalTools == nil {
					p.internalTools = map[string]bool{}
				}
				p.internalTools[content.ID] = true
			}
		}
	case "stream_event":
		var event struct {
			Event struct {
				Type  string `json:"type"`
				Block struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content_block"`
			} `json:"event"`
		}
		_ = json.Unmarshal(data, &event)
		if event.Event.Type == "content_block_start" && event.Event.Block.Type == "tool_use" && event.Event.Block.Name != "StructuredOutput" {
			return fmt.Errorf("%w: Claude 请求执行工具", ErrUnsupported)
		}
		p.emit("planning", "本地 CLI 正在生成候选剪辑计划")
	case "result":
		if envelope.IsError || envelope.Subtype != "success" {
			return failure("provider_error", "本地 Claude 规划失败，未返回可提交的计划", nil)
		}
		if len(envelope.Structured) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Structured), []byte("null")) {
			return fmt.Errorf("%w: 缺少 structured_output", ErrProtocol)
		}
		p.final = append([]byte(nil), envelope.Structured...)
		p.completed = true
	case "user":
		var message struct {
			Content []struct {
				Type      string `json:"type"`
				ToolUseID string `json:"tool_use_id"`
			} `json:"content"`
		}
		if err := json.Unmarshal(envelope.Message, &message); err != nil || len(message.Content) == 0 {
			return ErrProtocol
		}
		for _, content := range message.Content {
			if content.Type != "tool_result" || !p.internalTools[content.ToolUseID] {
				return fmt.Errorf("%w: 无工具规划收到外部工具结果", ErrUnsupported)
			}
		}
	default:
		return fmt.Errorf("%w: 未识别的 Claude 事件", ErrProtocol)
	}
	return nil
}

func (p *streamParser) result() ([]byte, error) {
	if !p.completed || len(p.final) == 0 {
		return nil, fmt.Errorf("%w: 流未正常完成或缺少最终计划", ErrProtocol)
	}
	return p.final, nil
}
