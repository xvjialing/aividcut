package application

import (
	"aividcut/internal/media"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResourceRecordingDoesNotChangeProjectAndExportsOwnReport(t *testing.T) {
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before := s.GetBootstrap()
	summary, err := s.StartResourceProfile(StartResourceProfileInput{ProjectScope: versionScope(before), Phase: "editing"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, err := s.GetResourceState(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if state.Active != nil && state.Active.Samples > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	done, err := s.StopResourceProfile(summary.ID)
	if err != nil || done.Samples == 0 {
		t.Fatal(done, err)
	}
	if after := s.GetBootstrap(); !reflect.DeepEqual(before, after) {
		t.Fatal("只读诊断改变工程")
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if _, err = s.ExportResourceProfile(context.Background(), ExportResourceProfileInput{ID: summary.ID, Path: path}); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if json.Unmarshal(bytes, &report) != nil || report["formatVersion"] != float64(1) {
		t.Fatal("导出不是后端记录")
	}
	for _, forbidden := range []string{"projectPath", "commandLine", "Authorization", "environment", "assetPath"} {
		if strings.Contains(string(bytes), forbidden) {
			t.Fatal("诊断包含不必要资料", forbidden)
		}
	}
	if _, err = s.ExportResourceProfile(context.Background(), ExportResourceProfileInput{ID: summary.ID, Path: path}); err == nil {
		t.Fatal("静默覆盖同名报告")
	}
	if _, err = s.ExportResourceProfile(context.Background(), ExportResourceProfileInput{ID: summary.ID, Path: path, Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "project.json")
	if _, err = s.SaveProjectTo(project); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExportResourceProfile(context.Background(), ExportResourceProfileInput{ID: summary.ID, Path: project, Overwrite: true}); err == nil {
		t.Fatal("诊断覆盖了工程")
	}
}
