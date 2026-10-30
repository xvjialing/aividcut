package application

import (
	"aividcut/internal/mediaruntime"
	"aividcut/internal/mediatools"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runtimeFixture(t *testing.T, base string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("真实shell版本夹具仅用于Unix，Windows静态规则和交叉构建另验")
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(base, "aividcut")
	os.WriteFile(executable, []byte("fixture executable"), 0700)
	root := filepath.Join(base, "media")
	m := mediaruntime.Manifest{SchemaVersion: 1, ID: "runtime-fixture", Version: "1", OS: runtime.GOOS, Arch: runtime.GOARCH, Paths: mediaruntime.Paths{FFmpeg: "bin/ffmpeg", FFprobe: "bin/ffprobe", Worker: "bin/worker", Repository: "mlt", Profiles: "profiles"}}
	for name, body := range map[string]string{"bin/ffmpeg": "#!/bin/sh\necho 'ffmpeg version fixture'\n", "bin/ffprobe": "#!/bin/sh\necho 'ffprobe version fixture'\n", "bin/worker": "#!/bin/sh\nexit 1\n", "mlt/module": "fixture module", "profiles/test": "fixture profile"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0700)
		data := []byte(body)
		if e := os.WriteFile(p, data, 0700); e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(data)
		m.Payloads = append(m.Payloads, mediaruntime.Payload{Path: name, Kind: "file", Bytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
	}
	data, _ := json.Marshal(m)
	if e := os.WriteFile(filepath.Join(root, mediaruntime.Filename), data, 0600); e != nil {
		t.Fatal(e)
	}
	return executable
}
func TestMediaRuntimeStartupAndManualLegacyPrecedence(t *testing.T) {
	mediaEnvironmentEmpty(t)
	executable := runtimeFixture(t, filepath.Join(t.TempDir(), "pkg"))
	executable, _ = filepath.EvalSymlinks(executable)
	dir := t.TempDir()
	s, e := NewWithMediaSettingsAtExecutable(t.TempDir(), dir, executable)
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.GetMediaTools()
	if e != nil || !v.Saved.UseBundled || v.Origins["ffmpeg"] != "bundled" || !strings.HasPrefix(v.Active.Worker, filepath.Dir(executable)) || !v.Capabilities.FFmpeg.Available {
		t.Fatal(v, e)
	}
	s.Close()
	store, e := mediatools.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Save(mediatools.Config{}); e != nil {
		t.Fatal(e)
	}
	store.Close()
	s, e = NewWithMediaSettingsAtExecutable(t.TempDir(), dir, executable)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v, _ = s.GetMediaTools()
	if v.Saved.UseBundled || v.Active.Worker != "" || v.Origins["worker"] != "unset" {
		t.Fatal("手动空值不能激活随包程序", v)
	}
}
func TestBrokenBundleDoesNotExecutePATHAndExplicitOverrideIsSeparate(t *testing.T) {
	mediaEnvironmentEmpty(t)
	executable := runtimeFixture(t, filepath.Join(t.TempDir(), "pkg"))
	root := filepath.Dir(executable)
	if e := os.WriteFile(filepath.Join(root, "media/mlt/module"), []byte("tampered"), 0700); e != nil {
		t.Fatal(e)
	}
	pathDir := t.TempDir()
	sentinel := filepath.Join(pathDir, "ffmpeg")
	os.WriteFile(sentinel, []byte("#!/bin/sh\necho unexpected > '"+filepath.Join(pathDir, "called")+"'\n"), 0700)
	t.Setenv("PATH", pathDir)
	s, e := NewWithMediaSettingsAtExecutable(t.TempDir(), t.TempDir(), executable)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v, _ := s.GetMediaTools()
	if v.Warning == "" || v.Active.FFmpeg != "" || v.Origins["ffmpeg"] != "bundled_invalid" {
		t.Fatal(v)
	}
	if _, e = os.Stat(filepath.Join(pathDir, "called")); !os.IsNotExist(e) {
		t.Fatal("损坏bundle不应调用PATH程序")
	}
	checked, e := s.InspectMediaTools(context.Background(), mediatools.Config{UseBundled: true})
	if e != nil || checked.CanSave || checked.Errors["runtime"] == "" {
		t.Fatal(checked, e)
	}
}
func TestRuntimeEnvironmentDoesNotMixNativeTriplet(t *testing.T) {
	mediaEnvironmentEmpty(t)
	executable := runtimeFixture(t, filepath.Join(t.TempDir(), "pkg"))
	t.Setenv("AIVIDCUT_MLT_WORKER", "/explicit/worker")
	s, e := NewWithMediaSettingsAtExecutable(t.TempDir(), t.TempDir(), executable)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	v, _ := s.GetMediaTools()
	if v.Active.Worker != "/explicit/worker" || v.Active.Repository != "" || v.Active.Profiles != "" || v.Origins["ffmpeg"] != "bundled" {
		t.Fatal("不应混用随包插件与自定义worker", v)
	}
}
func TestAutoSaveRecordsModeAndRejectsChangedBundlePresence(t *testing.T) {
	mediaEnvironmentEmpty(t)
	base := t.TempDir()
	executable := filepath.Join(base, "aividcut")
	os.WriteFile(executable, []byte("fixture"), 0700)
	settings := t.TempDir()
	s, e := NewWithMediaSettingsAtExecutable(t.TempDir(), settings, executable)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	checked, e := s.InspectMediaTools(context.Background(), mediatools.Config{UseBundled: true})
	if e != nil || !checked.CanSave {
		t.Fatal(checked, e)
	}
	if _, e = s.SaveMediaTools(checked.ID); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(settings, "settings.json"))
	if e != nil || strings.Contains(string(data), base) || !strings.Contains(string(data), `"useBundled": true`) {
		t.Fatal("不能保存安装绝对路径", string(data), e)
	}
	checked, e = s.InspectMediaTools(context.Background(), mediatools.Config{UseBundled: true})
	if e != nil || !checked.CanSave {
		t.Fatal(checked, e)
	}
	runtimeFixture(t, base)
	if _, e = s.SaveMediaTools(checked.ID); e == nil {
		t.Fatal("随包状态变化后旧检测应失效")
	}
}
