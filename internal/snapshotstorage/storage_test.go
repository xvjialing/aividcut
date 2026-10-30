package snapshotstorage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func directoryFixture(t *testing.T) (string, []byte) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "version-a")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte("不可变工程快照")
	if err := os.WriteFile(filepath.Join(path, "project.aividcut"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "metadata.json"), []byte("metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, body
}
func catalog(t *testing.T, root, view string) []Item {
	t.Helper()
	rows, err := Catalog(root, view, true, func(id string) bool { return strings.HasPrefix(id, "version-") }, func(string, string) (Label, error) {
		return Label{Name: "版本", Time: time.Now(), Readable: true}, nil
	}, 200)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestTrashRestorePurgeAndNoOverwrite(t *testing.T) {
	root, body := directoryFixture(t)
	rows := catalog(t, root, "active")
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	if err := Apply(rows[0].Proof, "trash", nil); err != nil {
		t.Fatal(err)
	}
	if len(catalog(t, root, "active")) != 0 {
		t.Fatal("回收后仍计入活动列表")
	}
	trash := catalog(t, root, "trash")
	if len(trash) != 1 {
		t.Fatal(trash)
	}
	if err := os.Mkdir(filepath.Join(root, "version-a"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Apply(trash[0].Proof, "restore", nil); err == nil {
		t.Fatal("还原覆盖已有目录")
	}
	if err := os.Remove(filepath.Join(root, "version-a")); err != nil {
		t.Fatal(err)
	}
	if err := Apply(trash[0].Proof, "restore", nil); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(filepath.Join(root, "version-a", "project.aividcut"))
	if err != nil || string(restored) != string(body) {
		t.Fatal("还原改变了原字节", err)
	}
	rows = catalog(t, root, "active")
	if err := Apply(rows[0].Proof, "trash", nil); err != nil {
		t.Fatal(err)
	}
	trash = catalog(t, root, "trash")
	protected := filepath.Join(root, ".trash", "version-a", "project.aividcut")
	if err := Apply(trash[0].Proof, "purge", []string{protected}); err == nil {
		t.Fatal("清理当前工程文件没有被阻止")
	}
	if err := Apply(trash[0].Proof, "purge", nil); err != nil {
		t.Fatal(err)
	}
	if len(catalog(t, root, "trash")) != 0 {
		t.Fatal("永久删除后仍有记录")
	}
}

func TestChangedProofAndUnknownFilesArePreserved(t *testing.T) {
	root, _ := directoryFixture(t)
	rows := catalog(t, root, "active")
	if err := os.WriteFile(filepath.Join(root, "version-a", "project.aividcut"), []byte("后来更改的版本内容"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(rows[0].Proof, "trash", nil); err == nil {
		t.Fatal("旧身份没有拒绝")
	}
	if err := os.WriteFile(filepath.Join(root, "version-a", "keep.txt"), []byte("未知用户文件"), 0600); err != nil {
		t.Fatal(err)
	}
	rows = catalog(t, root, "active")
	if rows[0].CanPurge {
		t.Fatal("包含额外文件仍允许永久删除")
	}
	if err := Apply(rows[0].Proof, "trash", nil); err != nil {
		t.Fatal(err)
	}
	trash := catalog(t, root, "trash")
	if err := Apply(trash[0].Proof, "purge", nil); err == nil {
		t.Fatal("永久清理额外文件没有被阻止")
	}
	if data, err := os.ReadFile(filepath.Join(root, ".trash", "version-a", "keep.txt")); err != nil || string(data) != "未知用户文件" {
		t.Fatal("误删未知文件")
	}
}

func TestEmptyAndDamagedSnapshotCanBeManagedWithoutFollowingLinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "version-empty"), 0700); err != nil {
		t.Fatal(err)
	}
	rows := catalog(t, root, "active")
	if !rows[0].CanPurge {
		t.Fatal("空损坏目录不应永久占用容量")
	}
	if err := Apply(rows[0].Proof, "trash", nil); err != nil {
		t.Fatal(err)
	}
	rows = catalog(t, root, "trash")
	if err := Apply(rows[0].Proof, "purge", nil); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("保留"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "version-link")); err != nil {
		t.Skip("环境不支持符号链接")
	}
	rows = catalog(t, root, "active")
	if !rows[0].Locked || rows[0].CanPurge {
		t.Fatal("链接应被锁定")
	}
	if err := Apply(rows[0].Proof, "trash", nil); err == nil {
		t.Fatal("不应处理链接")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("改动了链接外原文件")
	}
}
