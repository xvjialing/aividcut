package playback

import (
	"aividcut/internal/editor"
	"bytes"
	"context"
	"image/color"
	"testing"
	"time"
)

func TestNativeLongCanvasAndTitlesKeepDeclaredLength(t *testing.T) {
	config := nativeConfig(t)
	for _, kind := range []string{"image", "text", "caption"} {
		t.Run(kind, func(t *testing.T) {
			p := editor.NewProject("长时间线画布")
			p.Settings.Width, p.Settings.Height = 160, 90
			track := "track-video-1"
			if kind == "text" {
				track = "track-text-1"
			}
			if kind == "caption" {
				track = "track-caption-1"
			}
			clip := editor.DefaultClip("long-still", track, kind)
			clip.DurationFrames, clip.Text, clip.TextStyle.FontSize = 30001, "Long", 18
			if kind == "image" {
				asset := picture(t, "small.png", color.RGBA{R: 210, G: 40, B: 60, A: 255})
				p.Assets, clip.AssetID, clip.Text = []editor.Asset{asset}, asset.ID, ""
			}
			p.Clips = []editor.Clip{clip}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			session, err := Start(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if _, err = session.Load(ctx, p); err != nil {
				t.Fatal(err)
			}
			first, err := session.Preview(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			pixels := append([]byte{}, first.RGBA...)
			for _, position := range []int64{14999, 15000, 30000} {
				frame, err := session.Preview(ctx, position)
				if err != nil {
					t.Fatalf("第%d帧：%v", position, err)
				}
				if frame.Width != 160 || frame.Height != 90 || !bytes.Equal(pixels, frame.RGBA) {
					t.Fatalf("第%d帧丢失画布或静态字形", position)
				}
			}
		})
	}
}
