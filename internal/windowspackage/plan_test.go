package windowspackage

import (
	"aividcut/internal/mediaruntime"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func realPE(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	os.WriteFile(source, []byte("package main\nfunc main(){}\n"), 0600)
	file := filepath.Join(dir, "fixture.exe")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", file, source)
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if data, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(data))
	}
	return file
}
func copyPE(t *testing.T, source, dest string) {
	t.Helper()
	data, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(dest, data, 0700); e != nil {
		t.Fatal(e)
	}
}
func completeFixture(t *testing.T) Config {
	t.Helper()
	source := realPE(t)
	dir := t.TempDir()
	for _, name := range []string{"worker.exe", "ffmpeg.exe", "ffprobe.exe", "plugin.dll", "qoffscreen.dll"} {
		copyPE(t, source, filepath.Join(dir, name))
	}
	data := filepath.Join(dir, "data")
	os.MkdirAll(filepath.Join(data, "core"), 0700)
	os.MkdirAll(filepath.Join(data, "profiles"), 0700)
	os.WriteFile(filepath.Join(data, "core/loader.dict"), []byte("fixture"), 0600)
	os.WriteFile(filepath.Join(data, "profiles/test"), []byte("fixture"), 0600)
	return Config{ID: "copy-fixture-not-media-acceptance", Version: "test", Arch: "amd64", Worker: filepath.Join(dir, "worker.exe"), FFmpeg: filepath.Join(dir, "ffmpeg.exe"), FFprobe: filepath.Join(dir, "ffprobe.exe"), Modules: []string{filepath.Join(dir, "plugin.dll")}, Plugins: []Root{{filepath.Join(dir, "qoffscreen.dll"), "plugins/platforms/qoffscreen.dll"}}, DLLDirectories: []string{dir}, SystemDLLs: []string{"kernel32.dll"}, Data: data, Output: filepath.Join(dir, "new-package")}
}
func TestRealPEPlanAndCopyDoNotClaimMediaRuntime(t *testing.T) {
	c := completeFixture(t)
	p, e := Scan(context.Background(), c)
	if e != nil || len(p.Missing) != 0 || p.WorkerMissing || p.ReleaseVerified {
		t.Fatal(p, e)
	}
	out, e := Assemble(context.Background(), c)
	if e != nil {
		t.Fatal(out, e)
	}
	v, e := mediaruntime.Load(context.Background(), filepath.Join(out, "media/runtime.json"), "windows", "amd64")
	if e != nil || v.ID != c.ID {
		t.Fatal(v, e)
	}
	if _, e = Assemble(context.Background(), c); e == nil {
		t.Fatal("不能覆盖已有候选包")
	}
}
func TestMissingDLLAndFilenameCollisionPreventAssembly(t *testing.T) {
	c := completeFixture(t)
	c.SystemDLLs = nil
	p, e := Scan(context.Background(), c)
	if e != nil || len(p.Missing) == 0 {
		t.Fatal(p, e)
	}
	if _, e = Assemble(context.Background(), c); e == nil {
		t.Fatal("缺DLL不能装配")
	}
	if _, e = os.Stat(c.Output); !os.IsNotExist(e) {
		t.Fatal("失败不应写输出")
	}
	other := t.TempDir()
	copyPE(t, c.FFmpeg, filepath.Join(other, "same.dll"))
	copyPE(t, c.FFmpeg, filepath.Join(c.DLLDirectories[0], "SAME.DLL"))
	f, e := os.OpenFile(filepath.Join(other, "same.dll"), os.O_APPEND|os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	f.Write([]byte("changed"))
	f.Close()
	c.DLLDirectories = append(c.DLLDirectories, other)
	if _, e = Scan(context.Background(), c); e == nil {
		t.Fatal("同名不同内容DLL不能猜选")
	}
}
func TestArchitectureMismatchAndWorkerMissingRemainExplicit(t *testing.T) {
	c := completeFixture(t)
	c.Worker = ""
	p, e := Scan(context.Background(), c)
	if e != nil || !p.WorkerMissing {
		t.Fatal(p, e)
	}
	if _, e = Assemble(context.Background(), c); e == nil {
		t.Fatal("不能用其它exe冒充缺worker")
	}
	data, e := os.ReadFile(c.FFmpeg)
	if e != nil {
		t.Fatal(e)
	}
	offset := binary.LittleEndian.Uint32(data[0x3c:0x40])
	binary.LittleEndian.PutUint16(data[offset+4:offset+6], 0xaa64)
	os.WriteFile(c.FFmpeg, data, 0700)
	if _, e = Scan(context.Background(), c); e == nil {
		t.Fatal("PE架构不符未拒绝")
	}
}

func TestAssemblyDoesNotCopyInputTreeIntoItself(t *testing.T) {
	c := completeFixture(t)
	c.Output = filepath.Join(c.Data, "nested-output")
	if _, e := Assemble(context.Background(), c); e == nil {
		t.Fatal("不得递归复制输入目录到自身")
	}
	if _, e := os.Stat(c.Output); !os.IsNotExist(e) {
		t.Fatal("无效输出不应创建")
	}
}
