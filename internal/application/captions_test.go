package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aividcut/internal/media"
)

func TestSubtitleImportExportAndWholeBatchUndo(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	input := filepath.Join(dir, "中文.srt")
	content := "1\r\n00:00:00,500 --> 00:00:01,500\r\n第一句\r\n\r\n2\r\n00:00:00,500 --> 00:00:01,000\r\n同时字幕\r\n"
	if err := os.WriteFile(input, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := s.ImportCaptions(input, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Project.Clips) != 2 || state.Project.Clips[0].Kind != "caption" || !state.Dirty {
		t.Fatal("字幕未完整导入")
	}
	output := filepath.Join(dir, "输出.srt")
	if _, err := s.ExportCaptionsTo(output, ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(output)
	if !strings.Contains(string(data), "第一句") || !strings.Contains(string(data), "同时字幕") {
		t.Fatal("导出丢失重叠字幕")
	}
	undo, err := s.Undo()
	if err != nil || len(undo.Project.Clips) != 0 || undo.Dirty {
		t.Fatal("整批撤销后未恢复初始工程", err)
	}
}
