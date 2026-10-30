package mediaruntime

import (
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

func fixture(t *testing.T) (string, Manifest) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "media")
	m := Manifest{SchemaVersion: 1, ID: "fixture", Version: "1", OS: runtime.GOOS, Arch: runtime.GOARCH, Paths: Paths{FFmpeg: "bin/ffmpeg", FFprobe: "bin/ffprobe", Worker: "bin/worker", Repository: "mlt", Profiles: "profiles"}}
	for _, name := range []string{"bin/ffmpeg", "bin/ffprobe", "bin/worker", "mlt/plugin.so", "profiles/profile"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		data := []byte("fixture " + name)
		if e := os.WriteFile(p, data, 0700); e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(data)
		m.Payloads = append(m.Payloads, Payload{Path: name, Kind: "file", Bytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
	}
	write(t, root, m)
	return root, m
}
func write(t *testing.T, root string, m Manifest) {
	t.Helper()
	data, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, Filename), data, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestRuntimeManifestRelocatesWithInternalFrameworkLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("内部链接夹具需要Windows链接创建权限；Windows路径规则另测")
	}
	root, m := fixture(t)
	if e := os.Symlink("ffmpeg", filepath.Join(root, "bin/ffmpeg-link")); e != nil {
		t.Fatal(e)
	}
	m.Paths.FFmpeg = "bin/ffmpeg-link"
	m.Payloads = append(m.Payloads, Payload{Path: "bin/ffmpeg-link", Kind: "symlink", Target: "ffmpeg"})
	os.MkdirAll(filepath.Join(root, "framework/Versions/A"), 0700)
	data := []byte("framework resource")
	os.WriteFile(filepath.Join(root, "framework/Versions/A/resource"), data, 0600)
	sum := sha256.Sum256(data)
	m.Payloads = append(m.Payloads, Payload{Path: "framework/Versions/A/resource", Kind: "file", Bytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
	if e := os.Symlink("A", filepath.Join(root, "framework/Versions/Current")); e != nil {
		t.Fatal(e)
	}
	m.Payloads = append(m.Payloads, Payload{Path: "framework/Versions/Current", Kind: "symlink", Target: "A"})
	m.Paths.Repository = "framework/Versions/Current"
	write(t, root, m)
	before, e := Load(context.Background(), filepath.Join(root, Filename), m.OS, m.Arch)
	if e != nil {
		t.Fatal(e)
	}
	moved := filepath.Join(filepath.Dir(root), "移动 有空格")
	if e = os.Rename(root, moved); e != nil {
		t.Fatal(e)
	}
	after, e := Load(context.Background(), filepath.Join(moved, Filename), m.OS, m.Arch)
	if e != nil || after.Fingerprint != before.Fingerprint || after.Paths.FFmpeg == before.Paths.FFmpeg || !strings.HasPrefix(after.Paths.Worker, moved) {
		t.Fatal(after, e)
	}
	if e = os.WriteFile(filepath.Join(moved, "mlt/plugin.so"), []byte("changed payload"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = Load(context.Background(), filepath.Join(moved, Filename), m.OS, m.Arch); e == nil {
		t.Fatal("模块变化未拒绝")
	}
}
func TestRuntimeManifestRejectsMissingExtraTraversalAndWrongPlatform(t *testing.T) {
	for _, mode := range []string{"missing", "extra", "traversal", "absolute", "platform", "duplicate", "directory-entry", "link-outside", "version", "size"} {
		t.Run(mode, func(t *testing.T) {
			root, m := fixture(t)
			switch mode {
			case "missing":
				os.Remove(filepath.Join(root, "mlt/plugin.so"))
			case "extra":
				os.WriteFile(filepath.Join(root, "unlisted"), []byte("extra"), 0600)
			case "traversal":
				m.Payloads[0].Path = "../escape"
			case "absolute":
				m.Paths.Worker = "/tmp/worker"
			case "platform":
				m.OS = "foreign"
			case "duplicate":
				m.Payloads = append(m.Payloads, m.Payloads[0])
			case "directory-entry":
				m.Paths.Repository = "bin/worker"
			case "link-outside":
				if runtime.GOOS == "windows" {
					t.Skip("需要Windows创建链接权限")
				}
				os.Remove(filepath.Join(root, "bin/worker"))
				os.Symlink("../../outside", filepath.Join(root, "bin/worker"))
				m.Payloads[2] = Payload{Path: "bin/worker", Kind: "symlink", Target: "../../outside"}
			case "version":
				m.SchemaVersion = 2
			case "size":
				m.Payloads[0].Bytes++
			}
			write(t, root, m)
			if _, e := Load(context.Background(), filepath.Join(root, Filename), runtime.GOOS, runtime.GOARCH); e == nil {
				t.Fatal("应拒绝", mode)
			}
		})
	}
}
func TestCandidateProductLayoutAndClosedPackage(t *testing.T) {
	base := t.TempDir()
	app := filepath.Join(base, "AIVidCut.app", "Contents", "MacOS", "AIVidCut")
	os.MkdirAll(filepath.Dir(app), 0700)
	os.WriteFile(app, []byte("test executable"), 0700)
	base, _ = filepath.EvalSymlinks(base)
	p, root, e := Candidate(app, "darwin")
	if e != nil || p != filepath.Join(base, "AIVidCut.app", "Contents", "Resources", "media", Filename) || root != filepath.Join(base, "AIVidCut.app") {
		t.Fatal(p, root, e)
	}
	win := filepath.Join(base, "AIVidCut.exe")
	os.WriteFile(win, []byte("test executable"), 0700)
	p, root, e = Candidate(win, "windows")
	if e != nil || p != filepath.Join(base, "media", Filename) || root != base {
		t.Fatal(p, root, e)
	}
	r, e := Find(context.Background(), app, "darwin", "arm64")
	if e != nil || r.Present {
		t.Fatal("无清单应保持未随包", r, e)
	}
	if _, _, e = Candidate("relative", "darwin"); e == nil {
		t.Fatal("不应按cwd查找产品")
	}
}
func TestManifestBoundsAndCancellation(t *testing.T) {
	root, m := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Load(ctx, filepath.Join(root, Filename), m.OS, m.Arch); e == nil {
		t.Fatal("取消未中断")
	}
	p := filepath.Join(root, Filename)
	os.WriteFile(p, []byte(strings.Repeat(" ", MaxManifestBytes+1)), 0600)
	if _, e := Load(context.Background(), p, m.OS, m.Arch); e == nil {
		t.Fatal("超量清单未拒绝")
	}
}
