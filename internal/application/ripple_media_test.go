package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/playback"
	"aividcut/internal/workflows"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRippleActualAllTrackRetainedWindow(t *testing.T) {
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" {
		t.Skip("需显式worker，不把跳过当全轨音画验收")
	}
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	dir := t.TempDir()
	if err := s.ConfigureWorkflows(filepath.Join(dir, "workflows")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "source.mp4")
	preciseApplicationCommand(t, tools, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30000/1001", "-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000", "-t", "2", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-c:a", "aac", path)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	b, err := s.ImportPaths(ctx, []string{path, preciseApplicationWAV(t)})
	if err != nil {
		t.Fatal(err)
	}
	settings := b.Project.Settings
	settings.Width, settings.Height = 320, 180
	var video, audio editor.Asset
	for _, a := range b.Project.Assets {
		if a.Kind == "video" {
			video = a
		} else {
			audio = a
		}
	}
	c := editor.DefaultClip("moving", "track-video-1", "video")
	c.AssetID = video.ID
	c.DurationFrames = 24
	c.SourceInUs = 137000
	c.SpeedNum, c.SpeedDen = 3, 2
	voice := editor.DefaultClip("later-voice", "track-audio-1", "audio")
	voice.AssetID = audio.ID
	voice.StartFrame = 13
	voice, err = editor.AudioClipToSourceEnd(voice, audio, settings)
	if err != nil {
		t.Fatal(err)
	}
	voice.Volume = .4
	title := editor.DefaultClip("later-title", "track-text-1", "text")
	title.Text = "后段同步"
	title.TextStyle.FontSize = 24
	title.StartFrame = 13
	title.DurationFrames = 4
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "range-media", Commands: []editor.Command{{Type: "update_project", Settings: &settings}, {Type: "add_clip", Clip: &c}, {Type: "split_clip", ClipID: c.ID, AtFrame: 2}, {Type: "delete_clip", ClipID: c.ID}, {Type: "add_clip", Clip: &voice}, {Type: "add_clip", Clip: &title}}})
	if err != nil {
		t.Fatal(err)
	}
	before := b
	collect := func(p editor.Project, start int64) ([]byte, []byte) {
		t.Helper()
		session, err := playback.Start(ctx, PlaybackEnvironment())
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if _, err = session.Load(ctx, p); err != nil {
			t.Fatal(err)
		}
		if _, err = session.Preview(ctx, start); err != nil {
			t.Fatal(err)
		}
		if err = session.Play(ctx); err != nil {
			t.Fatal(err)
		}
		var pcm, rgba []byte
		for i := int64(0); i < 6; i++ {
			frame, err := session.Next(ctx)
			if err != nil || frame.Position != start+i {
				t.Fatal("实际帧序异常", err)
			}
			pcm = append(pcm, frame.PCM...)
			rgba = append(rgba, frame.RGBA...)
		}
		return pcm, rgba
	}
	oldPCM, oldRGBA := collect(before.Project, 11)
	plan, err := s.PreviewRippleDelete(ctx, RippleInput{RippleInput: workflows.RippleInput{ProjectID: b.Project.ID, ProjectRevision: b.Project.Revision, FromFrame: 6, ToFrame: 11}, SourceEpoch: b.SourceEpoch})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.ApplyWorkflowPreview(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	newPCM, newRGBA := collect(after.Project, 6)
	if !bytes.Equal(oldPCM, newPCM) || !bytes.Equal(oldRGBA, newRGBA) {
		t.Fatalf("全轨保留音画改变 PCM%d/%d RGBA%d/%d", len(oldPCM), len(newPCM), len(oldRGBA), len(newRGBA))
	}
	for _, clip := range after.Project.Clips {
		if clip.ID == voice.ID || clip.ID == title.ID {
			if clip.StartFrame != 8 {
				t.Fatal("未选后段未同步前移")
			}
		}
	}
	t.Logf("实际29.97fps/1.5倍速/137000us入点/已有2帧相位；全轨删除[6,11)，后段原声+独立短音尾+文字同步，6帧PCM%d字节/RGBA%d字节完全相同", len(newPCM), len(newRGBA))
}
