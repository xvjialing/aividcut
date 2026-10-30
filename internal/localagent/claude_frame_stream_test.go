package localagent

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestClaudeFramePayloadAndOptionalUserSessionNotRetained(t *testing.T) {
	dir := t.TempDir()
	var events []Event
	s := &claudeExecutionStream{tools: []string{"aividcut_read_source_frames"}, directory: dir, version: claudeExecutionVersion, notify: func(e Event) { events = append(events, e) }}
	init, _ := json.Marshal(map[string]any{"type": "system", "subtype": "init", "session_id": "fixture", "cwd": dir, "claude_code_version": claudeExecutionVersion, "permissionMode": "dontAsk", "tools": []string{"mcp__aividcut__aividcut_read_source_frames"}, "mcp_servers": []any{map[string]string{"name": "aividcut", "status": "connected"}}})
	if err := s.line(init); err != nil {
		t.Fatal(err)
	}
	if err := s.line([]byte(`{"type":"assistant","session_id":"fixture","message":{"content":[{"type":"tool_use","id":"image-1","name":"mcp__aividcut__aividcut_read_source_frames"}]}}`)); err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "image-1", "content": []any{map[string]any{"type": "image", "data": bytes.Repeat([]byte("fixture-image-secret"), 30000)}}}}}})
	c := &capture{maximum: 16 << 20, lineLimit: 2 << 20, line: s.line, cancel: func() {}}
	if _, err := (captureWriter{target: c}).Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	if len(c.stdout) != 0 || len(c.pending) != 0 || len(s.pending) != 0 {
		t.Fatal("保留原始图像JSON或未完成调用")
	}
	public, _ := json.Marshal(events)
	if bytes.Contains(public, []byte("image-secret")) {
		t.Fatal("事件泄漏图像载荷")
	}
}
