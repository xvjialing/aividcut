package application

import (
	"aividcut/internal/media"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIncompatibleEncodingResourcesDoNotCreateJobsOrEdit(t *testing.T) {
	s, err := New(media.Toolchain{FFmpegPath: "not-used"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before := s.GetBootstrap()
	jobs := s.ListJobs()
	for _, mode := range []string{"unknown", "lower_memory"} {
		_, err = s.ExportVideo(media.ExportOptions{Path: filepath.Join(t.TempDir(), "out.mp4"), VideoEncoder: "mpeg4", ResourceMode: mode})
		if err == nil {
			t.Fatal("未知或不兼容档位未拒绝")
		}
	}
	if !reflect.DeepEqual(s.ListJobs(), jobs) || !reflect.DeepEqual(s.GetBootstrap(), before) {
		t.Fatal("拒绝请求创建了任务或修改工程")
	}
}

func TestMCPResourceModePreparedWithoutExportAndRejectsMismatch(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	client := mcpApplicationClient(t, s, false)
	before := s.ListJobs()
	callApplicationMCP(t, client, "aividcut_request_export", map[string]any{"id": "lower-memory-request", "options": map[string]any{"videoEncoder": "libx264", "resourceMode": "lower_memory"}}, false)
	requests := s.ListMCPRequests()
	found := false
	for _, v := range requests {
		if v.ID == "lower-memory-request" {
			found = v.Export != nil && v.Export.ResourceMode == "lower_memory"
		}
	}
	if !found {
		t.Fatal("MCP待核对丢失资源档位")
	}
	callApplicationMCP(t, client, "aividcut_request_export", map[string]any{"id": "incompatible-mode", "options": map[string]any{"videoEncoder": "mpeg4", "resourceMode": "lower_memory"}}, true)
	if !reflect.DeepEqual(before, s.ListJobs()) {
		t.Fatal("仅准备MCP请求启动了导出")
	}
}
