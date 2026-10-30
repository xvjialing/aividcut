package projectfonts

import (
	"aividcut/internal/editor"
	"golang.org/x/image/font/gofont/goregular"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Go-Regular.ttf")
	if err := os.WriteFile(path, goregular.TTF, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, file
}
func TestFontReadCoverageAndMutation(t *testing.T) {
	path, file := fixture(t)
	if file.Info.Family != "Go" || file.Info.Weight != 400 || len(file.SHA256) != 64 {
		t.Fatal(file.Info)
	}
	if err := file.CheckText("Hello 123\nCafé"); err != nil {
		t.Fatal(err)
	}
	if err := file.CheckText("字体缺字"); err == nil {
		t.Fatal("缺字仍通过")
	}
	p := editor.NewProject("字体")
	p.Assets = []editor.Asset{{ID: "font", Kind: "font", Name: "Go", Path: path, Font: &file.Info, Fingerprint: "sha256:" + file.SHA256}}
	c := editor.DefaultClip("title", "track-text-1", "text")
	c.Text = "Hello"
	c.TextStyle.FontAssetID = "font"
	p.Clips = []editor.Clip{c}
	if _, err := Resolve(p); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(goregular.TTF, byte(0)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(p); err == nil {
		t.Fatal("同名字体内容改变仍被使用")
	}
}
func TestFontRejectsInvalidAndVariableFiles(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string][]byte{"wrong.ttf": []byte(strings.Repeat("x", 100)), "collection.ttf": append([]byte("ttcf"), make([]byte, 24)...), "fake.woff": goregular.TTF} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, data, 0600)
		if _, err := Read(path); err == nil {
			t.Fatal("接受无效文件", name)
		}
	}
	data := append([]byte{}, goregular.TTF...)
	copy(data[12:16], []byte("fvar"))
	path := filepath.Join(dir, "variable.ttf")
	os.WriteFile(path, data, 0600)
	if _, err := Read(path); err == nil {
		t.Fatal("未拒绝可变字体")
	}
	path = filepath.Join(dir, "big.ttf")
	f, _ := os.Create(path)
	f.Truncate(MaxFileBytes + 1)
	f.Close()
	if _, err := Read(path); err == nil {
		t.Fatal("未拒绝超限文件")
	}
}
