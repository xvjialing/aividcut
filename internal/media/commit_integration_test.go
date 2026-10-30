package media

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
)

func TestRealExportCancellationAtCommitBoundaries(t *testing.T) {
	tools := integrationTools(t)
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "source.png")
	writeImage(t, imagePath, color.NRGBA{R: 255, A: 255})
	asset, err := tools.Probe(context.Background(), imagePath)
	if err != nil {
		t.Fatal(err)
	}
	project := testProject()
	project.Assets = []editor.Asset{asset}
	clip := mediaClip("still", asset.ID, 30)
	clip.Kind = "image"
	project.Clips = []editor.Clip{clip}
	old := []byte("仍需保留的旧输出")
	for _, stage := range []string{"committing", "completed"} {
		t.Run(stage, func(t *testing.T) {
			output := filepath.Join(dir, stage+".mp4")
			if err := os.WriteFile(output, old, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := false
			err := tools.Export(ctx, project, ExportOptions{Path: output, Preset: "project", Overwrite: true}, func(progress Progress) {
				if progress.Stage == stage {
					observed = true
					cancel()
				}
			})
			if !observed {
				t.Fatalf("未观察到 %s 状态", stage)
			}
			actual, readErr := os.ReadFile(output)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if stage == "committing" {
				if !errors.Is(err, context.Canceled) || !bytes.Equal(actual, old) {
					t.Fatalf("提交前取消应保留旧输出：%v", err)
				}
			} else {
				if err != nil || bytes.Equal(actual, old) {
					t.Fatalf("成功提交后的取消不能回滚结果：%v", err)
				}
				if result, err := tools.Probe(context.Background(), output); err != nil || result.Width != 160 {
					t.Fatalf("提交的输出无效：%+v %v", result, err)
				}
			}
			partials, _ := filepath.Glob(filepath.Join(dir, ".aividcut-output-*"))
			if len(partials) != 0 {
				t.Fatalf("遗留半成品：%v", partials)
			}
		})
	}
}
