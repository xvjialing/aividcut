package relinkbatch

import (
	"aividcut/internal/editor"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func asset(t *testing.T, old, new, relative string, data []byte) editor.Asset {
	t.Helper()
	path := filepath.Join(new, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return editor.Asset{ID: relative, Name: filepath.Base(relative), Kind: "image", Path: filepath.Join(old, relative), Fingerprint: fmt.Sprintf("sha256:%x", sha256.Sum256(data)), Missing: true}
}
func TestMappingPreservesSubdirectoriesAndRejectsDifferentUnprovenAndEscape(t *testing.T) {
	old, new := filepath.Join(t.TempDir(), "old"), t.TempDir()
	p := editor.NewProject("恢复")
	p.Assets = []editor.Asset{asset(t, old, new, "camera/a.png", []byte("A")), asset(t, old, new, "other/a.png", []byte("B")), asset(t, old, new, "changed.png", []byte("C")), asset(t, old, new, "legacy.png", []byte("D"))}
	p.Assets[2].Fingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("other")))
	p.Assets[3].Fingerprint = ""
	escaped := asset(t, old, t.TempDir(), "secret.png", []byte("secret"))
	p.Assets = append(p.Assets, escaped)
	os.Symlink(filepath.Join(filepath.Dir(escaped.Path), "..", "not-there"), filepath.Join(new, "secret.png"))
	out, err := Prepare(context.Background(), p, old, new)
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary.Matched != 2 || out.Summary.Entries[2].Status != "different" || out.Summary.Entries[3].Status != "unproven" || out.Summary.Entries[4].Status != "missing" {
		t.Fatal(out.Summary)
	}
	if err := out.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	commands, err := out.Commands(p)
	if err != nil || len(commands) != 2 || commands[0].Asset.Path == commands[1].Asset.Path {
		t.Fatal(commands, err)
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "actual.png"), []byte("secret"), 0600)
	os.Remove(filepath.Join(new, "secret.png"))
	os.Symlink(filepath.Join(outside, "actual.png"), filepath.Join(new, "secret.png"))
	out, err = Prepare(context.Background(), p, old, new)
	if err != nil || out.Summary.Entries[4].Status != "missing" {
		t.Fatal(out, err)
	}
}
func TestFileModificationIncludingRestoredMtimeIsRejected(t *testing.T) {
	old, new := filepath.Join(t.TempDir(), "old"), t.TempDir()
	p := editor.NewProject("修改保护")
	p.Assets = []editor.Asset{asset(t, old, new, "a.png", []byte("same-size-a"))}
	plan, err := Prepare(context.Background(), p, old, new)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(new, "a.png")
	info, _ := os.Stat(path)
	os.WriteFile(path, []byte("same-size-b"), 0600)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	if err := plan.Verify(context.Background()); err == nil {
		t.Fatal("恢复mtime仍需重读摘要")
	}
}
func TestInventoryScopeAndCancellationLimits(t *testing.T) {
	old, new := filepath.Join(t.TempDir(), "old"), t.TempDir()
	p := editor.NewProject("范围")
	p.Assets = []editor.Asset{asset(t, old, new, "one/a.png", []byte("a")), asset(t, old, new, "two/b.png", []byte("b"))}
	outside := editor.Asset{ID: "outside", Path: filepath.Join(old+"-other", "c.png")}
	p.Assets = append(p.Assets, outside)
	inv, err := Inspect(context.Background(), p)
	if err != nil || inv.MissingCount != 3 || inv.SuggestedOldRoot != filepath.Dir(old) {
		t.Fatal(inv, err)
	}
	plan, err := Prepare(context.Background(), p, old, new)
	if err != nil || plan.Summary.Outside != 1 || plan.Summary.Matched != 2 {
		t.Fatal(plan, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Prepare(ctx, p, old, new); err == nil {
		t.Fatal("取消后不得继续读取")
	}
	if _, err := Prepare(context.Background(), p, "relative", new); err == nil {
		t.Fatal("旧目录须完整路径")
	}
	for i := 0; i < MaxAssets; i++ {
		a := p.Assets[0]
		a.ID = fmt.Sprint(i)
		p.Assets = append(p.Assets, a)
	}
	if _, err := Prepare(context.Background(), p, old, new); err == nil {
		t.Fatal("超过单批数量应拒绝")
	}
}

func TestReadByteBudgetRejectsBeforeConsumingCandidate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.bin")
	if err := os.WriteFile(path, []byte("four"), 0600); err != nil {
		t.Fatal(err)
	}
	_, bytes, err := read(context.Background(), root, path, 3)
	if err != errBudget || bytes != 0 {
		t.Fatal("读取前应依据剩余字节预算拒绝", bytes, err)
	}
}
