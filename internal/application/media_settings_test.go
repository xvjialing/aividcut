package application

import (
	"aividcut/internal/mediatools"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func mediaEnvironmentEmpty(t *testing.T) {
	t.Helper()
	for _, key := range []string{"AIVIDCUT_FFMPEG", "AIVIDCUT_FFPROBE", "AIVIDCUT_MLT_WORKER", "AIVIDCUT_MLT_REPOSITORY", "AIVIDCUT_MLT_PROFILES", "AIVIDCUT_MLT_DATA"} {
		t.Setenv(key, "")
	}
	t.Setenv("PATH", t.TempDir())
}
func fakeMediaProgram(t *testing.T, role string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("固定shell夹具仅用于Unix，Windows由构建和后续实机覆盖")
	}
	path := filepath.Join(t.TempDir(), "有空格 "+role)
	body := "#!/bin/sh\ncase \"$1\" in\n-version) echo '" + role + " version fixture';;\n*) echo 'fixture declaration';;\nesac\n"
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestMediaSettingsRebootWithoutEnvironmentAndNoHotSwap(t *testing.T) {
	mediaEnvironmentEmpty(t)
	dir := t.TempDir()
	s, err := NewWithMediaSettings(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	before := s.GetBootstrap()
	wanted := mediatools.Config{FFmpeg: fakeMediaProgram(t, "ffmpeg"), FFprobe: fakeMediaProgram(t, "ffprobe")}
	checked, err := s.InspectMediaTools(context.Background(), wanted)
	if err != nil || !checked.CanSave || checked.ID == "" {
		t.Fatal(checked, err)
	}
	view, err := s.SaveMediaTools(checked.ID)
	if err != nil || !view.RestartRequired || view.Active.FFmpeg != "" {
		t.Fatal("保存热切换了当前工具", view, err)
	}
	if !reflect.DeepEqual(before, s.GetBootstrap()) {
		t.Fatal("媒体设置改了工程")
	}
	s.Close()
	next, err := NewWithMediaSettings(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	view, err = next.GetMediaTools()
	if err != nil || view.RestartRequired || view.Active.FFmpeg != wanted.FFmpeg || view.Origins["ffmpeg"] != "saved" || !view.Capabilities.FFmpeg.Available {
		t.Fatal("精简PATH重启未恢复保存设置", view, err)
	}
}
func TestMediaSettingsProofBindsFilesAndConfig(t *testing.T) {
	mediaEnvironmentEmpty(t)
	dir := t.TempDir()
	s, err := NewWithMediaSettings(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	wanted := mediatools.Config{FFmpeg: fakeMediaProgram(t, "ffmpeg")}
	checked, err := s.InspectMediaTools(context.Background(), wanted)
	if err != nil || !checked.CanSave {
		t.Fatal(checked, err)
	}
	os.WriteFile(wanted.FFmpeg, []byte("#!/bin/sh\necho changed\n"), 0700)
	if _, err = s.SaveMediaTools(checked.ID); err == nil {
		t.Fatal("工具改变仍用旧检测保存")
	}
	wanted.FFmpeg = fakeMediaProgram(t, "ffmpeg")
	checked, err = s.InspectMediaTools(context.Background(), wanted)
	if err != nil || !checked.CanSave {
		t.Fatal(checked, err)
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{external"), 0600)
	if _, err = s.SaveMediaTools(checked.ID); err == nil {
		t.Fatal("外部设置改变仍被旧检测覆盖")
	}
	bad, err := s.InspectMediaTools(context.Background(), mediatools.Config{FFmpeg: filepath.Join(dir, "missing")})
	if err != nil || bad.CanSave || bad.Errors["ffmpeg"] == "" {
		t.Fatal("缺失程序未明确拒绝", bad, err)
	}
}
func TestCorruptMediaConfigDoesNotPreventEditorStartup(t *testing.T) {
	mediaEnvironmentEmpty(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{broken"), 0600)
	s, err := NewWithMediaSettings(t.TempDir(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	view, err := s.GetMediaTools()
	if err != nil || view.Warning == "" {
		t.Fatal(view, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(data) != "{broken" {
		t.Fatal("启动改写了异常配置")
	}
	if s.GetBootstrap().Project.ID == "" {
		t.Fatal("设置损坏导致编辑器无工程")
	}
}
