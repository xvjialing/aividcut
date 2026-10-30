package projectrecovery

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
)

func TestRollingRecoveryProofReopenAndRetire(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := editor.NewProject("未保存版本")
	id := editor.NewID("recovery")
	a, err := s.Write(id, p, "", false)
	if err != nil {
		t.Fatal(err)
	}
	p.Name = "最新恢复点"
	p.Revision++
	b, err := s.Write(id, p, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if a.SHA256 == b.SHA256 {
		t.Fatal("滚动点未改变")
	}
	if _, _, _, err := s.Load(id, a.SHA256); err == nil {
		t.Fatal("旧内容证明未拒绝")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page, err := s.List("", 0, 20)
	if err != nil || page.Total != 1 || page.Records[0].Name != p.Name {
		t.Fatal(page, err)
	}
	loaded, _, _, err := s.Load(id, b.SHA256)
	if err != nil || loaded.Name != p.Name || loaded.Revision != p.Revision {
		t.Fatal(loaded, err)
	}
	if err := s.Retire(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(id, p, "", false); !errors.Is(err, ErrRetired) {
		t.Fatal("退役点被复活", err)
	}
	page, err = s.List("", 0, 20)
	if err != nil || page.Total != 0 {
		t.Fatal(page, err)
	}
}

func TestRecoveryCorruptMetadataAndBodyURLIsolation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := editor.NewProject("正文")
	source := filepath.Join(t.TempDir(), "source.png")
	if err := os.WriteFile(source, []byte("本地源"), 0600); err != nil {
		t.Fatal(err)
	}
	p.Assets = []editor.Asset{{ID: "image", Name: "image", Kind: "image", Path: source, Width: 32, Height: 18, URL: "http://local/?token=private", ThumbnailURL: "http://local/?token=thumb"}}
	id := editor.NewID("recovery")
	v, err := s.Write(id, p, filepath.Join(t.TempDir(), "original.aividcut"), false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("token=")) {
		t.Fatal("会话地址写入恢复文件")
	}
	data = bytes.Replace(data, []byte(`"name": "正文"`), []byte(`"name": "篡改"`), 1)
	if err := os.WriteFile(s.path(id), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Load(id, v.SHA256); err == nil {
		t.Fatal("正文修改未拒绝")
	}
	page, err := s.List("", 0, 20)
	if err != nil || page.Total != 1 {
		t.Fatal("列表不应读取整份正文", page, err)
	}
	if err := os.WriteFile(s.path(id), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err = s.List("", 0, 20)
	if err != nil || page.Total != 0 || page.Unreadable != 1 {
		t.Fatal(page, err)
	}
	if _, _, _, err := s.Load("../escape", v.SHA256); err == nil {
		t.Fatal("路径逃逸未拒绝")
	}
}

func TestRecoveryCapacityAndSingleWriter(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if other, err := Open(s.directory); err == nil {
		other.Close()
		t.Fatal("恢复目录被两个实例写入")
	}
	p := editor.NewProject("容量")
	var first Summary
	for i := 0; i < MaxRecords; i++ {
		v, err := s.Write(editor.NewID("recovery"), p, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = v
		}
	}
	if _, err := s.Write(editor.NewID("recovery"), p, "", false); err == nil {
		t.Fatal("数量没有上限")
	}
	p.Revision++
	if _, err := s.Write(first.ID, p, "", false); err != nil {
		t.Fatal("容量上限不应阻止已有点滚动更新", err)
	}
	if _, err := s.List("", -1, 20); err == nil {
		t.Fatal("负分页未拒绝")
	}
	if _, err := s.List("", 0, 51); err == nil {
		t.Fatal("无界分页未拒绝")
	}
}

func TestRecoveryByteBudgetPreservesExistingFiles(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := editor.NewProject("预算")
	v, err := s.Write(editor.NewID("recovery"), p, "", false)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(s.path(v.ID), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxTotalBytes); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	id := editor.NewID("recovery")
	if _, err := s.Write(id, p, "", false); err == nil {
		t.Fatal("字节预算没有拒绝新记录")
	}
	if _, err := os.Stat(s.path(v.ID)); err != nil {
		t.Fatal("预算拒绝删除了旧资料")
	}
	if _, err := os.Stat(s.path(id)); !os.IsNotExist(err) {
		t.Fatal("预算拒绝仍写入新记录")
	}
}
