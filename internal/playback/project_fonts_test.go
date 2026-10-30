package playback

import (
	"aividcut/internal/editor"
	"aividcut/internal/projectfonts"
	"bytes"
	"context"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeProjectFontLoadsPhysicalFileAndChangesGlyphs(t *testing.T) {
	config := nativeConfig(t)
	config.MaxWidth = 640
	config.MaxHeight = 360
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := editor.NewProject("字体实际字形")
	p.Settings.Width, p.Settings.Height = 640, 360
	c := editor.DefaultClip("title", "track-text-1", "text")
	c.Text = "iiii WWWW 123"
	c.TextStyle.FontSize = 48
	c.DurationFrames = 2
	p.Clips = []editor.Clip{c}
	var frames [][]byte
	for index, data := range [][]byte{goregular.TTF, gomono.TTF} {
		path := filepath.Join(t.TempDir(), "font.ttf")
		os.WriteFile(path, data, 0600)
		file, err := projectfonts.Read(path)
		if err != nil {
			t.Fatal(err)
		}
		p.Assets = []editor.Asset{{ID: "font", Kind: "font", Name: "Test Font", Path: path, Font: &file.Info, Fingerprint: "sha256:" + file.SHA256}}
		p.Clips[0].TextStyle.FontAssetID = "font"
		session, err := Start(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = session.Load(ctx, p); err != nil {
			session.Close()
			t.Fatalf("字体%d: %v", index, err)
		}
		frame, err := session.Preview(ctx, 0)
		if err != nil {
			session.Close()
			t.Fatal(err)
		}
		frames = append(frames, append([]byte{}, frame.RGBA...))
		session.Close()
	}
	if bytes.Equal(frames[0], frames[1]) {
		t.Fatal("不同实际字体生成了相同字形")
	}
}
