package localagent

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestFrameExecutionStreamDoesNotRetainImagePayloadsOrLogThem(t *testing.T) {
	var events []Event
	stream := &executionStream{tools: []string{"aividcut_read_source_frames"}, notify: func(e Event) { events = append(events, e) }}
	capture := &capture{maximum: 16 << 20, lineLimit: 2 << 20, line: stream.line, cancel: func() {}}
	writer := captureWriter{target: capture}
	picture := bytes.Repeat([]byte("frame-image-bytes"), 40000)
	line, err := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"type": "mcp_tool_call", "server": executionServerName, "tool": "aividcut_read_source_frames", "result": map[string]any{"content": []any{map[string]any{"type": "image", "mimeType": "image/jpeg", "data": picture}}}}})
	if err != nil {
		t.Fatal(err)
	}
	line = append(line, '\n')
	for i := 0; i < 3; i++ {
		for at := 0; at < len(line); at += 32 << 10 {
			if _, err := writer.Write(line[at:min(len(line), at+(32<<10))]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(capture.stdout) != 0 || len(capture.pending) != 0 {
		t.Fatal("图片JSONL累积到原始输出缓冲")
	}
	raw, _ := json.Marshal(events)
	if bytes.Contains(raw, []byte("frame-image-bytes")) || bytes.Contains(raw, []byte("base64")) {
		t.Fatal("图像原始结果进入事件日志")
	}
}
func TestFrameStreamLineAndTotalBudgetsStillRejectOverflow(t *testing.T) {
	for _, values := range [][2]int{{100, 10}, {10, 100}} {
		cancelled := false
		c := &capture{maximum: values[0], lineLimit: values[1], line: func([]byte) error { return nil }, cancel: func() { cancelled = true }}
		if _, err := (captureWriter{target: c}).Write(bytes.Repeat([]byte("a"), 11)); err == nil || !cancelled {
			t.Fatal("执行流限额被放宽为无界")
		}
	}
}
