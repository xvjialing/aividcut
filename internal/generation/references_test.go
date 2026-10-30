package generation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReferenceScopeAndContentValidation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "图.png")
	data := pngData(t, 256, 256)
	os.WriteFile(path, data, 0600)
	source, err := NewFileReferences([]RegisteredFile{{ID: "allowed", Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Read(context.Background(), Reference{AssetID: "http://127.0.0.1/media/private"}); err == nil {
		t.Fatal("未经登记的本地URL成为上传素材")
	}
	value, err := source.Read(context.Background(), Reference{AssetID: "allowed"})
	if err != nil || value.MIME != "image/png" || value.Width != 256 || value.Height != 256 || value.SHA256 == "" {
		t.Fatalf("没有可靠读取参考图：%+v %v", value, err)
	}
	bad, err := NewFileReferences([]RegisteredFile{{ID: "allowed", Path: path, ExpectedSHA256: "wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Read(context.Background(), Reference{AssetID: "allowed"}); err == nil {
		t.Fatal("错误指纹被忽略")
	}
	text := filepath.Join(root, "伪装.png")
	os.WriteFile(text, []byte("not an image"), 0600)
	fake, _ := NewFileReferences([]RegisteredFile{{ID: "fake", Path: text}})
	if _, err := fake.Read(context.Background(), Reference{AssetID: "fake"}); err == nil {
		t.Fatal("仅按扩展名接受了伪造图片")
	}
	replacement := filepath.Join(root, "new.png")
	os.WriteFile(replacement, data, 0600)
	os.Rename(replacement, path)
	if _, err := source.Read(context.Background(), Reference{AssetID: "allowed"}); err == nil {
		t.Fatal("文件被替换后仍静默上传")
	}
}

func TestReferenceSymlinkChangeAndCancelledRead(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.png")
	second := filepath.Join(root, "second.png")
	link := filepath.Join(root, "ref.png")
	os.WriteFile(first, pngData(t, 2, 2), 0600)
	os.WriteFile(second, pngData(t, 3, 3), 0600)
	if err := os.Symlink(first, link); err != nil {
		t.Skip("当前平台不允许符号链接fixture")
	}
	source, err := NewFileReferences([]RegisteredFile{{ID: "ref", Path: link}})
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(link)
	os.Symlink(second, link)
	if _, err := source.Read(context.Background(), Reference{AssetID: "ref"}); err == nil {
		t.Fatal("符号链接改变后越过原授权素材")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Read(ctx, Reference{AssetID: "ref"}); err == nil {
		t.Fatal("已取消的读取仍继续")
	}
}
