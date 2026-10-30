package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/playback"
	"bytes"
	"context"
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureTimelineOriginalEffectsAndSourceRemainSeparate(t *testing.T) {
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" {
		t.Skip("需要显式worker，不把跳过当合成取图验收")
	}
	tools := preciseApplicationTools(t)
	s, store, control := captureApplication(t, tools)
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "blue.mp4")
	lutPath := filepath.Join(dir, "swap.cube")
	preciseApplicationCommand(t, tools, "-v", "error", "-f", "lavfi", "-i", "color=blue:size=640x360:rate=30", "-t", "2", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", videoPath)
	cube := "LUT_3D_SIZE 2\n"
	for b := 0; b < 2; b++ {
		for g := 0; g < 2; g++ {
			for r := 0; r < 2; r++ {
				cube += fmt.Sprintf("%d %d %d\n", b, g, r)
			}
		}
	}
	if err := os.WriteFile(lutPath, []byte(cube), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := s.ImportPaths(context.Background(), []string{videoPath, lutPath})
	if err != nil {
		t.Fatal(err)
	}
	var video, lut editor.Asset
	for _, a := range b.Project.Assets {
		if a.Kind == "video" {
			video = a
		} else {
			lut = a
		}
	}
	settings := b.Project.Settings
	settings.Width, settings.Height, settings.FPSNum, settings.FPSDen = 640, 360, 30, 1
	clip := editor.DefaultClip("video", "track-video-1", "video")
	clip.AssetID = video.ID
	clip.DurationFrames = 60
	clip.LUT = &editor.LUT{AssetID: lut.ID, Intensity: 1}
	caption := editor.DefaultClip("caption", "track-caption-1", "caption")
	caption.Text = "本地合成字幕"
	caption.TextStyle.FontSize = 32
	caption.DurationFrames = 60
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "capture-fixture", Commands: []editor.Command{{Type: "update_project", Settings: &settings}, {Type: "add_clip", Clip: &clip}, {Type: "add_clip", Clip: &caption}}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := PlaybackEnvironment()
	cfg.RenderQuality = "quarter"
	cfg.VisualSources = map[string]playback.VisualSource{video.ID: {Path: "/invalid-preview-proxy.mp4"}}
	if err = s.ConfigurePlayback(PlaybackOptions{Config: cfg}); err != nil {
		t.Fatal(err)
	}
	input := FrameCaptureInput{ID: "timeline-frame", ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision, SourceEpoch: b.SourceEpoch, Kind: "timeline", Frame: 36}
	captured, err := s.CaptureFrameAsset(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Origin.Asset.Width != 640 || captured.Origin.Asset.Height != 360 || captured.Origin.Renderer != "native-full-original" {
		t.Fatal("取图使用了代理或低精度")
	}
	file, err := os.Open(captured.Origin.Asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	picture, err := png.Decode(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	r, _, blue, _ := picture.At(320, 100).RGBA()
	if r>>8 < 240 || blue>>8 > 20 {
		t.Fatal("合成取图遗漏LUT")
	}
	noCaption := editor.CloneProject(b.Project)
	noCaption.Clips = noCaption.Clips[:1]
	check := PlaybackEnvironment()
	session, err := playback.Start(context.Background(), check)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = session.Load(context.Background(), noCaption); err != nil {
		t.Fatal(err)
	}
	baseline, err := session.Preview(context.Background(), 36)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			a, b, c, d := picture.At(x, y).RGBA()
			at := (y*640 + x) * 4
			if byte(a>>8) != baseline.RGBA[at] || byte(b>>8) != baseline.RGBA[at+1] || byte(c>>8) != baseline.RGBA[at+2] || byte(d>>8) != baseline.RGBA[at+3] {
				changed++
			}
		}
	}
	if changed < 20 {
		t.Fatal("合成取图没有实际字幕像素")
	}
	source := FrameCaptureInput{ID: "source-frame", ProjectID: b.Project.ID, ProjectRevision: captured.Bootstrap.Project.Revision, SourceEpoch: b.SourceEpoch, Kind: "source", AssetID: video.ID, Fingerprint: video.Fingerprint, PositionUs: 1_200_000}
	raw, err := s.CaptureFrameAsset(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(raw.Origin.Asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	red, _, bl, _ := src.At(src.Bounds().Dx()/2, src.Bounds().Dy()/2).RGBA()
	if red>>8 > 20 || bl>>8 < 230 {
		t.Fatal("源取样被时间线效果污染")
	}
	reader, err := generation.NewFileReferences([]generation.RegisteredFile{{ID: captured.AssetID, Path: captured.Origin.Asset.Path, ExpectedSHA256: strings.TrimPrefix(captured.Origin.Asset.Fingerprint, "sha256:")}})
	if err != nil {
		t.Fatal(err)
	}
	reference, err := reader.Read(context.Background(), generation.Reference{AssetID: captured.AssetID, Role: "first_frame"})
	if err != nil || reference.MIME != "image/png" || reference.Width != 640 {
		t.Fatal("取图未能成为实际模型参考字节", err)
	}
	if store.readCount() != 0 || control.posts.Load() != 0 {
		t.Fatal("取图调用模型")
	}
	t.Logf("合成640x360 PNG含交换LUT/字幕%d变化像素；源JPEG保持蓝色；参考读取%d字节，没有模型调用", changed, len(reference.Data))
}
