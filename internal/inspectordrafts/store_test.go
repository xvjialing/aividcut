package inspectordrafts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
)

func field(key, raw string, base any) Field {
	data, _ := json.Marshal(base)
	return Field{Key: key, Raw: raw, Base: data}
}
func TestImmutableBaseRollingRawAndIntegrity(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := editor.NewProject("原工程")
	v, err := s.Save(Record{ID: editor.NewID("draft"), Fields: []Field{field("projectName", "未提交", p.Name)}}, &p)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, v.ID, "base.aividcut")
	before, _ := os.ReadFile(path)
	v.Fields[0].Raw = "另一个未提交值"
	v, err = s.Save(v, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("输入更新重写了基础工程")
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored, meta, _, err := s.Base(v.ID, v.SHA256)
	if err != nil || restored.Name != p.Name || meta.Fields[0].Raw != "另一个未提交值" {
		t.Fatal(restored, meta, err)
	}
	if _, err = s.Load(v.ID, "old"); err == nil {
		t.Fatal("旧草稿证明未拒绝")
	}
	if err = os.WriteFile(path, append(after, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.Base(v.ID, v.SHA256); err == nil {
		t.Fatal("基础工程损坏未拒绝")
	}
}
func TestInvalidAndOversizedInputsPreserveRecord(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := editor.NewProject("原工程")
	v, err := s.Save(Record{ID: editor.NewID("draft"), Fields: []Field{field("projectName", "", p.Name)}}, &p)
	if err != nil {
		t.Fatal(err)
	}
	original := v.SHA256
	v.Fields[0].Raw = string(bytes.Repeat([]byte("a"), 65<<10))
	if _, err = s.Save(v, nil); err == nil {
		t.Fatal("超大输入未拒绝")
	}
	if _, err = s.Load(v.ID, original); err != nil {
		t.Fatal("已保护输入丢失", err)
	}
	for _, f := range []Field{field("password", "secret", p.Name), field("volume", "NaN", 0.5), field("sourceInUs", "", 137001)} {
		if f.Key == "password" {
			if ValidateFields([]Field{f}) == nil {
				t.Fatal("未登记字段未拒绝")
			}
		} else if _, err := Converted(f); err == nil {
			t.Fatal("无效数字被转为有效值", f)
		}
	}
	for _, raw := range []string{"1e-999999999", "1e999999999", "1/2"} {
		if _, err := Converted(field("sourceInUs", raw, 0)); err == nil {
			t.Fatal("未拒绝不受限格式", raw)
		}
	}
	scientific, err := Converted(field("sourceInUs", "1.37001e-1", 0))
	if err != nil || string(scientific) != "137001" {
		t.Fatal("有限指数解析失败", string(scientific), err)
	}
	value, err := Converted(field("sourceInUs", "0.137001", 0))
	if err != nil || string(value) != "137001" {
		t.Fatal("源入点精度错误", string(value), err)
	}
}
func TestSymlinkAndUnknownFilesArePreserved(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := editor.NewProject("测试")
	v, err := s.Save(Record{ID: editor.NewID("draft"), Fields: []Field{field("projectName", "输入", p.Name)}}, &p)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "keep")
	os.WriteFile(outside, []byte("保留"), 0600)
	path := filepath.Join(dir, v.ID, "base.aividcut")
	os.Remove(path)
	if err = os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.Base(v.ID, v.SHA256); err == nil {
		t.Fatal("接受了符号链接")
	}
	os.WriteFile(filepath.Join(dir, v.ID, "unknown"), []byte("资料"), 0600)
	if err = s.Delete(v.ID, v.SHA256); err == nil {
		t.Fatal("含未知文件仍被删除")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "保留" {
		t.Fatal("外部文件发生变化")
	}
}

func TestCapacityFailureKeepsProtectedDraft(t *testing.T) {
	for _, kind := range []string{"records", "bytes"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p := editor.NewProject("保留工程")
			v, err := s.Save(Record{ID: editor.NewID("draft"), Fields: []Field{field("projectName", "保留输入", p.Name)}}, &p)
			if err != nil {
				t.Fatal(err)
			}
			count := 49
			if kind == "bytes" {
				count = 8
			}
			for i := 0; i < count; i++ {
				id := editor.NewID("draft")
				path := filepath.Join(dir, id)
				if err = os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "bytes" {
					f, err := os.Create(filepath.Join(path, "base.aividcut"))
					if err != nil {
						t.Fatal(err)
					}
					err = f.Truncate(64 << 20)
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err = s.Save(Record{ID: editor.NewID("draft"), Fields: []Field{field("projectName", "不应覆盖", p.Name)}}, &p); err == nil {
				t.Fatal("容量超限仍创建资料")
			}
			if _, err = s.Load(v.ID, v.SHA256); err != nil {
				t.Fatal("容量失败破坏了已有记录", err)
			}
		})
	}
}
