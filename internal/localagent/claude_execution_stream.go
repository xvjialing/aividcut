package localagent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type claudeExecutionStream struct {
	tools                  []string
	directory, version     string
	notify                 func(Event)
	initialized, completed bool
	session, summary       string
	seen                   map[string]bool
	pending                map[string]string
}
type claudeExecutionBlock struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
}

func (s *claudeExecutionStream) allowed(name string) bool {
	for _, tool := range s.tools {
		if name == "mcp__"+executionServerName+"__"+tool {
			return true
		}
	}
	return false
}
func sameExecutionDirectory(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	x, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	y, err := filepath.EvalSymlinks(b)
	return err == nil && x == y
}
func (s *claudeExecutionStream) line(data []byte) error {
	if s.completed {
		return fmt.Errorf("%w: Claude终态后仍有事件", ErrProtocol)
	}
	var event struct {
		Type           string                          `json:"type"`
		Subtype        string                          `json:"subtype"`
		SessionID      string                          `json:"session_id"`
		Parent         *string                         `json:"parent_tool_use_id"`
		AgentID        string                          `json:"agent_id"`
		SubagentType   string                          `json:"subagent_type"`
		Version        string                          `json:"claude_code_version"`
		CWD            string                          `json:"cwd"`
		PermissionMode string                          `json:"permissionMode"`
		Tools          []string                        `json:"tools"`
		Servers        []struct{ Name, Status string } `json:"mcp_servers"`
		Skills         []string                        `json:"skills"`
		Plugins        []json.RawMessage               `json:"plugins"`
		PluginErrors   []json.RawMessage               `json:"plugin_errors"`
		Message        struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		ToolUseID         string            `json:"tool_use_id"`
		ToolName          string            `json:"tool_name"`
		IsError           *bool             `json:"is_error"`
		Deferred          json.RawMessage   `json:"deferred_tool_use"`
		Queued            int               `json:"queued_turn_count"`
		Result            string            `json:"result"`
		PermissionDenials []json.RawMessage `json:"permission_denials"`
		PrecedingIDs      []string          `json:"preceding_tool_use_ids"`
	}
	if !utf8.Valid(data) || json.Unmarshal(data, &event) != nil {
		return ErrProtocol
	}
	if event.Parent != nil || event.AgentID != "" || event.SubagentType != "" {
		return fmt.Errorf("%w: Claude出现非主会话事件", ErrUnsupported)
	}
	if event.Type == "system" && event.Subtype == "init" {
		if s.initialized || event.SessionID == "" || len(event.SessionID) > 128 || event.Version != s.version || event.PermissionMode != "dontAsk" || !sameExecutionDirectory(event.CWD, s.directory) {
			return fmt.Errorf("%w: Claude启动身份/目录/权限不匹配", ErrProtocol)
		}
		if len(event.Servers) != 1 || event.Servers[0].Name != executionServerName || event.Servers[0].Status != "connected" || len(event.Tools) != len(s.tools) || len(event.Skills) > 0 || len(event.Plugins) > 0 || len(event.PluginErrors) > 0 {
			return fmt.Errorf("%w: Claude启动工具或扩展面不符合执行契约", ErrUnsupported)
		}
		known := map[string]bool{}
		for _, name := range event.Tools {
			if !s.allowed(name) || known[name] {
				return ErrUnsupported
			}
			known[name] = true
		}
		s.initialized = true
		s.session = event.SessionID
		s.seen = map[string]bool{}
		s.pending = map[string]string{}
		if s.notify != nil {
			s.notify(Event{Stage: "executing", Message: "Claude结构化会话已报告第一方工具面；启动前策略仍需独立核查"})
		}
		return nil
	}
	if !s.initialized {
		return fmt.Errorf("%w: Claude缺少有效启动事件", ErrProtocol)
	}
	if event.SessionID != s.session && !(event.Type == "user" && event.SessionID == "") {
		return fmt.Errorf("%w: Claude会话身份变化或缺失", ErrProtocol)
	}
	switch event.Type {
	case "assistant", "user":
		var blocks []claudeExecutionBlock
		if json.Unmarshal(event.Message.Content, &blocks) != nil || len(blocks) == 0 || len(blocks) > 256 {
			return ErrProtocol
		}
		for _, b := range blocks {
			if event.Type == "user" {
				if b.Type != "tool_result" || s.pending[b.ToolUseID] == "" {
					return fmt.Errorf("%w: Claude工具结果没有匹配调用", ErrProtocol)
				}
				delete(s.pending, b.ToolUseID)
				continue
			}
			switch b.Type {
			case "text", "thinking", "redacted_thinking": // 不保留原始思考、正文或工具载荷。
			case "tool_use":
				if !s.allowed(b.Name) {
					return fmt.Errorf("%w: Claude请求非第一方白名单工具", ErrUnsupported)
				}
				if b.ID == "" || len(b.ID) > 256 || s.seen[b.ID] || len(s.seen) >= 4096 || len(s.pending) >= 128 {
					return ErrProtocol
				}
				s.seen[b.ID] = true
				s.pending[b.ID] = b.Name
				if s.notify != nil {
					s.notify(Event{Stage: "tool", Message: "Claude正在调用已授权AIVidCut工具，参数和原始结果不进入事件日志"})
				}
			default:
				return fmt.Errorf("%w: Claude出现未知内容块", ErrUnsupported)
			}
		}
	case "tool_progress":
		if !s.allowed(event.ToolName) || s.pending[event.ToolUseID] != event.ToolName {
			return ErrProtocol
		}
	case "tool_use_summary":
		if len(event.PrecedingIDs) == 0 || len(event.PrecedingIDs) > 4096 {
			return ErrProtocol
		}
		for _, id := range event.PrecedingIDs {
			if !s.seen[id] {
				return ErrProtocol
			}
		}
	case "result":
		if event.Subtype != "success" || event.IsError == nil || *event.IsError {
			return failure("execution_provider_error", "Claude执行未正常完成，请核查工具记录；已成功操作不自动回滚", nil)
		}
		if len(event.PermissionDenials) > 0 {
			return failure("execution_permission_denied", "Claude报告权限拒绝，不将本次会话标为成功", ErrUnsupported)
		}
		if len(s.pending) > 0 {
			return fmt.Errorf("%w: Claude终态仍有未完成工具调用", ErrProtocol)
		}
		if event.Queued != 0 || len(event.Deferred) > 0 && string(event.Deferred) != "null" {
			return fmt.Errorf("%w: Claude仍有排队或延迟工作", ErrProtocol)
		}
		if len(event.Result) > 64<<10 {
			return ErrOutputLimit
		}
		if !utf8.ValidString(event.Result) || strings.TrimSpace(event.Result) == "" {
			return ErrProtocol
		}
		s.summary = strings.TrimSpace(event.Result)
		s.completed = true
	case "system":
		switch event.Subtype {
		case "informational", "status", "compact_boundary":
		default:
			return fmt.Errorf("%w: Claude出现未知或hook生命周期事件", ErrUnsupported)
		}
	case "rate_limit_event": // 仅接纳同会话元数据，不转发原始内容。
	default:
		return fmt.Errorf("%w: 未识别的Claude执行事件", ErrProtocol)
	}
	return nil
}
func (s *claudeExecutionStream) result() (string, error) {
	if !s.completed || s.summary == "" {
		return "", fmt.Errorf("%w: Claude流未完整结束", ErrProtocol)
	}
	return s.summary, nil
}
