package mediatools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigPersistenceAndRawStamp(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings := Config{FFmpeg: filepath.Join(dir, "有空格 工具", "ffmpeg")}
	if err = s.Save(settings); err != nil {
		t.Fatal(err)
	}
	stamp, err := s.Stamp()
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, warning := s.Load()
	if warning != "" || !Equal(settings, got) {
		t.Fatal(got, warning)
	}
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte("{bad-a"), 0600)
	_, warning = s.Load()
	if warning == "" {
		t.Fatal("损坏设置没有报告")
	}
	first, _ := s.Stamp()
	os.WriteFile(path, []byte("{bad-b"), 0600)
	second, _ := s.Stamp()
	if first == second || second == stamp {
		t.Fatal("损坏设置变化未绑定原文")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{bad-b" {
		t.Fatal("读取改写损坏原资料")
	}
}
func TestConfigRejectsUnsafeShapeAndConcurrentWriter(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = Open(s.directory); err == nil {
		t.Fatal("允许同目录双写")
	}
	for _, c := range []Config{{FFmpeg: "relative/ffmpeg"}, {Worker: " https://host/tool"}, {Profiles: "\x00"}} {
		if Validate(c) == nil {
			t.Fatal("路径格式未拒绝", c)
		}
	}
	target := filepath.Join(t.TempDir(), "keep")
	os.WriteFile(target, []byte("保留"), 0600)
	if err = os.Symlink(target, filepath.Join(s.directory, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if err = s.Save(Config{}); err == nil {
		t.Fatal("覆盖链接设置")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "保留" {
		t.Fatal("外部资料被覆盖")
	}
}
func TestEnvironmentPrecedenceIsExplicit(t *testing.T) {
	saved := Config{FFmpeg: "/saved/ffmpeg", FFprobe: "/saved/ffprobe", Worker: "/saved/worker"}
	effective, origins := Requested(saved, func(key string) string {
		if key == "AIVIDCUT_FFMPEG" {
			return "/env/ffmpeg"
		}
		return ""
	})
	if effective.FFmpeg != "/env/ffmpeg" || effective.FFprobe != "/saved/ffprobe" || origins["ffmpeg"] != "environment" || origins["ffprobe"] != "saved" || origins["repository"] != "unset" {
		t.Fatal(effective, origins)
	}
}

func TestLegacyEmptyMeansManualAndNewDefaultUsesBundle(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	value, w := s.Load()
	if w != "" || !value.UseBundled {
		t.Fatal("无设置应自动随包", value, w)
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"version":1,"settings":{"ffmpeg":"","ffprobe":"","worker":"","repository":"","profiles":""}}`), 0600)
	value, w = s.Load()
	if w != "" || value.UseBundled {
		t.Fatal("旧空路径不能激活随包引擎", value, w)
	}
	if e = s.Save(Config{UseBundled: true}); e != nil {
		t.Fatal(e)
	}
	value, w = s.Load()
	if w != "" || !value.UseBundled {
		t.Fatal(value, w)
	}
	if Validate(Config{UseBundled: true, Worker: "/manual/worker"}) == nil {
		t.Fatal("混合模式不应保存")
	}
}
