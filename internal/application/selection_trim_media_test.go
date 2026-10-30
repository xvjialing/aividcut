package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/playback"
	"bytes"
	"context"
	"os"
	"testing"
)

func TestSelectionTrimPreservesActualPCMSliceAndPictures(t *testing.T) {
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" {
		t.Skip("需要显式原生worker，不把跳过当真实修剪验收")
	}
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	state, err := s.ImportPaths(context.Background(), []string{preciseApplicationWAV(t)})
	if err != nil {
		t.Fatal(err)
	}
	p := state.Project
	settings := p.Settings
	settings.Width, settings.Height, settings.FPSNum, settings.FPSDen = 320, 180, 30000, 1001
	audio := editor.DefaultClip("voice", "track-audio-1", "audio")
	audio.AssetID = p.Assets[0].ID
	audio, err = editor.AudioClipToSourceEnd(audio, p.Assets[0], settings)
	if err != nil {
		t.Fatal(err)
	}
	title := editor.DefaultClip("text", "track-text-1", "text")
	title.Text = "同量修剪"
	title.TextStyle.FontSize = 24
	title.DurationFrames = 10
	before, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "trim-media-fixture", Commands: []editor.Command{{Type: "update_project", Settings: &settings}, {Type: "add_clip", Clip: &audio}, {Type: "add_clip", Clip: &title}}})
	if err != nil {
		t.Fatal(err)
	}
	collect := func(project editor.Project, start, count int64) ([]byte, []byte) {
		t.Helper()
		session, err := playback.Start(context.Background(), PlaybackEnvironment())
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if _, err = session.Load(context.Background(), project); err != nil {
			t.Fatal(err)
		}
		if _, err = session.Preview(context.Background(), start); err != nil {
			t.Fatal(err)
		}
		if err = session.Play(context.Background()); err != nil {
			t.Fatal(err)
		}
		var pcm, pixels []byte
		for i := int64(0); i < count; i++ {
			f, err := session.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if f.Position != start+i {
				t.Fatal("帧序错误")
			}
			pcm = append(pcm, f.PCM...)
			pixels = append(pixels, f.RGBA...)
		}
		return pcm, pixels
	}
	expectedPCM, expectedPixels := collect(before.Project, 2, 6)
	head, err := s.ApplyEdit(editor.EditRequest{BaseRevision: before.Project.Revision, IdempotencyKey: "trim-head-media", Commands: []editor.Command{{Type: "trim_clips", ClipIDs: []string{audio.ID, title.ID}, Edge: "start", FrameDelta: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	gotPCM, gotPixels := collect(head.Project, 2, 6)
	if !bytes.Equal(expectedPCM, gotPCM) || !bytes.Equal(expectedPixels, gotPixels) {
		t.Fatal("起点修剪改变保留窗口的真实音画")
	}
	onePCM, onePixels := collect(head.Project, 2, 1)
	tail, err := s.ApplyEdit(editor.EditRequest{BaseRevision: head.Project.Revision, IdempotencyKey: "trim-tail-media", Commands: []editor.Command{{Type: "trim_clips", ClipIDs: []string{audio.ID, title.ID}, Edge: "end", FrameDelta: -1}}})
	if err != nil {
		t.Fatal(err)
	}
	actualPCM, actualPixels := collect(tail.Project, 2, 1)
	if !bytes.Equal(onePCM, actualPCM) || !bytes.Equal(onePixels, actualPixels) {
		t.Fatal("终点修剪改变保留帧真实取样")
	}
	if _, err = s.Undo(); err != nil {
		t.Fatal(err)
	}
	restored, err := s.Undo()
	if err != nil || restored.Project.Clips[0].StartFrame != 0 || restored.Project.Clips[1].DurationFrames != 10 {
		t.Fatal("修剪未各一次撤销", err)
	}
	t.Logf("实际头部保留6帧：PCM%d字节、画面%d字节逐字节一致；终点保留1帧PCM%d字节一致", len(gotPCM), len(gotPixels), len(actualPCM))
}
