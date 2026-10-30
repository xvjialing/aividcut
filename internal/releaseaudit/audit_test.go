package releaseaudit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func hasCode(r Report, code string) bool {
	for _, v := range r.Findings {
		if v.Code == code {
			return true
		}
	}
	return false
}
func writeFixture(t *testing.T, path, contents string) {
	t.Helper()
	if e := os.WriteFile(path, []byte(contents), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestRealMachOAndPEInputsStayUnverified(t *testing.T) {
	for _, target := range []struct{ os, arch string }{{"darwin", "arm64"}, {"windows", "amd64"}} {
		t.Run(target.os, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "main.go")
			writeFixture(t, source, "package main\nfunc main(){}\n")
			app := filepath.Join(root, "app")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", app, source)
			cmd.Env = append(os.Environ(), "GOOS="+target.os, "GOARCH="+target.arch, "CGO_ENABLED=0", "GOWORK=off")
			if output, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("编译夹具失败：%v %s", e, output)
			}
			c := Config{TargetOS: target.os, TargetArch: target.arch, PackageRoot: root, Artifacts: []Input{{"app", app}}}
			if target.os == "darwin" {
				c.Artifacts = append(c.Artifacts, Input{"ocr", app})
			}
			r, e := Audit(ctx, c, Options{})
			if e != nil {
				t.Fatal(e)
			}
			a := r.Artifacts[0]
			if a.Format != target.os || !contains(a.Architectures, target.arch) || a.DebugSections || a.GoVersion == "" || a.GoSettings["-trimpath"] != "true" {
				t.Fatalf("真实产物元数据错误：%+v", a)
			}
			data, e := os.ReadFile(app)
			if e != nil {
				t.Fatal(e)
			}
			sum := sha256.Sum256(data)
			if a.SHA256 != hex.EncodeToString(sum[:]) {
				t.Fatal("产物指纹错误")
			}
			if target.os == "windows" {
				found := false
				for _, v := range a.Imports {
					if strings.EqualFold(v.Name, "kernel32.dll") && v.System {
						found = true
					}
				}
				if !found {
					t.Fatalf("PE不能把未实现的ImportedLibraries当空依赖：%+v", a.Imports)
				}
			}
			if r.ReleaseVerified || !hasCode(r, "missing_role") || !hasCode(r, "license_closure") {
				t.Fatal("不完整输入误称发行通过")
			}
			c.TargetArch = "amd64"
			if target.arch == "amd64" {
				c.TargetArch = "arm64"
			}
			r, e = Audit(ctx, c, Options{})
			if e != nil || !hasCode(r, "target_mismatch") {
				t.Fatal("未拒绝架构错配")
			}
		})
	}
}
func TestFrontendAndRuntimeLinksDoNotMasqueradeAsBundled(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	repo := filepath.Join(root, "mlt")
	front := filepath.Join(root, "dist")
	for _, p := range []string{repo, front} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	writeFixture(t, filepath.Join(external, "lib.so"), "明确的外部SDK")
	if e := os.Symlink(filepath.Join(external, "lib.so"), filepath.Join(repo, "lib.so")); e != nil {
		t.Fatal(e)
	}
	writeFixture(t, filepath.Join(front, "bundle.js"), "console.log('test');\n//# sourceMappingURL=data:application/json;base64,example")
	writeFixture(t, filepath.Join(front, "bundle.js.map"), "{}")
	writeFixture(t, filepath.Join(front, "source.tsx"), "export const a = 1;")
	r, e := Audit(context.Background(), Config{TargetOS: "darwin", TargetArch: "arm64", PackageRoot: root, FrontendDirectory: front, RuntimeDirectories: []Input{{"mlt_repository", repo}, {"mlt_profiles", external}}}, Options{})
	if e != nil {
		t.Fatal(e)
	}
	for _, code := range []string{"external_symlink", "runtime_outside_package", "frontend_source", "symlink_not_walked"} {
		if !hasCode(r, code) {
			t.Fatal("漏报", code)
		}
	}
	if r.Directories[0].Files != 1 || r.Directories[0].Bytes != 0 {
		t.Fatal("不应遍历或读取链接外的SDK内容")
	}
}
func TestConfigRejectsUnknownTrailingOversizeAndResolvesRelative(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "config.json")
	for _, bad := range []string{`{"targetOS":"darwin","unknown":true}`, `{} {}`, strings.Repeat(" ", 65537) + `{}`} {
		writeFixture(t, p, bad)
		if _, e := ReadConfig(p); e == nil {
			t.Fatal("非法配置通过")
		}
	}
	data, _ := json.Marshal(Config{TargetOS: "darwin", TargetArch: "arm64", PackageRoot: "bundle", Artifacts: []Input{{"app", "bundle/app"}}})
	writeFixture(t, p, string(data))
	c, e := ReadConfig(p)
	if e != nil {
		t.Fatal(e)
	}
	if c.PackageRoot != filepath.Join(root, "bundle") || c.Artifacts[0].Path != filepath.Join(root, "bundle/app") {
		t.Fatal("路径应相对配置，不是进程cwd")
	}
}
func TestPEOrdinalOnlyDescriptorAndMalformedRVA(t *testing.T) {
	data := make([]byte, 256)
	binary.LittleEndian.PutUint32(data[12:16], 0x1080)
	binary.LittleEndian.PutUint32(data[16:20], 0x1100)
	copy(data[128:], "custom.dll\x00")
	h := &pe.OptionalHeader64{NumberOfRvaAndSizes: 16}
	h.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_IMPORT] = pe.DataDirectory{VirtualAddress: 0x1000, Size: 40}
	p := &pe.File{OptionalHeader: h, Sections: []*pe.Section{{SectionHeader: pe.SectionHeader{VirtualAddress: 0x1000, Size: 256}}}}
	imports, e := peImports(p, bytes.NewReader(data))
	if e != nil || len(imports) != 1 || imports[0] != "custom.dll" {
		t.Fatal("OriginalFirstThunk=0不是终止描述符", imports, e)
	}
	binary.LittleEndian.PutUint32(data[12:16], 0xfffffff0)
	if _, e = peImports(p, bytes.NewReader(data)); e == nil {
		t.Fatal("越界RVA通过")
	}
	if systemImport("windows", "custom.dll") || systemImport("windows", "../kernel32.dll") || systemImport("darwin", "/usr/lib/../../private/x") {
		t.Fatal("未知或越界库误当系统依赖")
	}
}
func TestFrontendStreamingBoundaryKeepsHash(t *testing.T) {
	data := strings.Repeat("a", 65530) + "sourceMappingURL=data:json"
	h := sha256.New()
	found, e := hashFrontend(context.Background(), strings.NewReader(data), h)
	want := sha256.Sum256([]byte(data))
	if e != nil || !found || !bytes.Equal(h.Sum(nil), want[:]) {
		t.Fatal("跨块映射检测或SHA错误")
	}
}

func TestRelativeImportTargetsDoNotGuessRpathOrWindowsSearch(t *testing.T) {
	base := t.TempDir()
	target, ok := importTarget(filepath.Join(base, "bin", "worker"), "@loader_path/../lib/core.dylib")
	if !ok || target != filepath.Join(base, "lib", "core.dylib") {
		t.Fatal(target, ok)
	}
	for _, name := range []string{"@rpath/core.dylib", "unknown.dll", "@executable_path/core.dylib"} {
		if _, ok = importTarget("/package/lib/plugin", name); ok {
			t.Fatal("未知加载上下文不能猜测", name)
		}
	}
}
func TestProbeVersionFixedArgsBoundedAndBoundToDigest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX受控进程夹具；产品跨端不执行外来二进制")
	}
	p := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\n[ \"$#\" -eq 1 ] && [ \"$1\" = '-version' ] || exit 9\nprintf '%s\\n' 'ffmpeg version fixture' \"configuration: '--enable-gpl' --enable-shared\"\n"
	if e := os.WriteFile(p, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256([]byte(script))
	digest := hex.EncodeToString(sum[:])
	probe, e := probeVersion(context.Background(), p, "ffmpeg", digest)
	if e != nil || !probe.GPL || probe.Nonfree || probe.ArtifactSHA256 != digest {
		t.Fatal(probe, e)
	}
	if _, e = probeVersion(context.Background(), p, "ffprobe", digest); e == nil {
		t.Fatal("角色不匹配应拒绝")
	}
	if _, e = probeVersion(context.Background(), p, "ffmpeg", strings.Repeat("0", 64)); e == nil {
		t.Fatal("不匹配指纹应拒绝")
	}
	flood := "#!/bin/sh\nwhile :; do printf 'abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789'; done\n"
	if e = os.WriteFile(p, []byte(flood), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = probeVersion(context.Background(), p, "ffmpeg", digest); e == nil || !strings.Contains(e.Error(), "64KiB") {
		t.Fatal("输出越界未中断", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Audit(ctx, Config{TargetOS: "darwin", TargetArch: "arm64", PackageRoot: filepath.Dir(p), Artifacts: []Input{{"app", p}}}, Options{}); e == nil {
		t.Fatal("取消应中断检查")
	}
}
