package media

import (
	"aividcut/internal/editor"
	"aividcut/internal/projectfonts"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFFmpegPlanKeepsDifferentProjectFontFiles(t *testing.T) {
	p := editor.NewProject("两种字体")
	for i, data := range [][]byte{goregular.TTF, gomono.TTF} {
		id := []string{"regular", "mono"}[i]
		path := filepath.Join(t.TempDir(), "same-name.ttf")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		font, err := projectfonts.Read(path)
		if err != nil {
			t.Fatal(err)
		}
		info := font.Info
		p.Assets = append(p.Assets, editor.Asset{ID: id, Kind: "font", Name: id, Path: path, Font: &info, Fingerprint: "sha256:" + font.SHA256})
		c := editor.DefaultClip(id, "track-text-1", "text")
		c.StartFrame = int64(i * 90)
		c.Text = "iiii WWWW"
		c.TextStyle.FontAssetID = id
		p.Clips = append(p.Clips, c)
	}
	work := t.TempDir()
	plan, err := buildPlan(p, ExportOptions{Preset: "project"}, work, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	fonts := 0
	for _, row := range rows {
		if strings.HasPrefix(row.Name(), "font_") {
			fonts++
			if !strings.Contains(plan.Filter, row.Name()) {
				t.Fatal("滤镜未引用实际复制字体")
			}
		}
	}
	if fonts != 2 {
		t.Fatal("多份字体被复制到同名文件", fonts)
	}
	if _, err = buildPlan(p, ExportOptions{Preset: "project", FontPath: p.Assets[0].Path}, t.TempDir(), false, nil); err == nil {
		t.Fatal("单次FontPath静默覆盖工程字体")
	}
}
