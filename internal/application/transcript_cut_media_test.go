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

func TestTranscriptCutActualRetainedVideoAndAudio(t *testing.T) {
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" {
		t.Skip("需要显式原生worker，不把跳过当音画删减验收")
	}
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	dir := t.TempDir()
	if err := s.ConfigureWorkflows(filepath.Join(dir, "workflows")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wordcut.mp4")
	preciseApplicationCommand(t, tools, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30000/1001", "-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000", "-t", "2", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-c:a", "aac", path)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	b, err := s.ImportPaths(ctx, []string{path})
	if err != nil {
		t.Fatal(err)
	}
	p := b.Project
	settings := p.Settings
	settings.Width, settings.Height = 320, 180
	v := editor.DefaultClip("wordcut-video", "track-video-1", "video")
	v.AssetID = p.Assets[0].ID
	v.DurationFrames = 24
	v.SourceInUs = 137000
	v.SpeedNum, v.SpeedDen = 3, 2
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "phase", Commands: []editor.Command{{Type: "update_project", Settings: &settings}, {Type: "add_clip", Clip: &v}, {Type: "split_clip", ClipID: v.ID, AtFrame: 2}, {Type: "delete_clip", ClipID: v.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	clip := b.Project.Clips[0]
	tr := editor.Transcript{ID: "timed-words", AssetID: v.AssetID, SourceFingerprint: p.Assets[0].Fingerprint, RawWords: []editor.SourceWord{{ID: "delete", Text: "本地时间夹具", StartUs: 437300, EndUs: 687550}}}
	s.mu.Lock()
	_, err = s.engine.Apply(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "word-timing", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &tr}}})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	before := s.GetBootstrap()
	input := TranscriptCutInput{TranscriptCutInput: workflows.TranscriptCutInput{ProjectID: before.Project.ID, ProjectRevision: before.Project.Revision, TranscriptID: tr.ID, TranscriptRevision: before.Project.Transcripts[0].Revision, ClipID: clip.ID, WordIDs: []string{"delete"}, CloseGap: true}, SourceEpoch: before.SourceEpoch}
	selected, err := s.LocateTranscriptWords(ctx, input)
	if err != nil || selected.StartFrame != 6 || selected.EndFrame != 11 {
		t.Fatal("真实变速/相位词映射", selected, err)
	}
	collect := func(project editor.Project, start int64) ([]byte, []byte) {
		t.Helper()
		session, err := playback.Start(ctx, PlaybackEnvironment())
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if _, err = session.Load(ctx, project); err != nil {
			t.Fatal(err)
		}
		if _, err = session.Preview(ctx, start); err != nil {
			t.Fatal(err)
		}
		if err = session.Play(ctx); err != nil {
			t.Fatal(err)
		}
		var pcm, pixels []byte
		for i := int64(0); i < 6; i++ {
			f, err := session.Next(ctx)
			if err != nil || f.Position != start+i {
				t.Fatal("保留帧读取", err)
			}
			pcm = append(pcm, f.PCM...)
			pixels = append(pixels, f.RGBA...)
		}
		return pcm, pixels
	}
	expectedPCM, expectedPixels := collect(before.Project, 11)
	preview, err := s.PreviewTranscriptCut(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.ApplyWorkflowPreview(ctx, preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	actualPCM, actualPixels := collect(after.Project, 6)
	if !bytes.Equal(expectedPCM, actualPCM) || !bytes.Equal(expectedPixels, actualPixels) {
		t.Fatalf("右保留窗音画改变：PCM %d/%d，RGBA %d/%d", len(expectedPCM), len(actualPCM), len(expectedPixels), len(actualPixels))
	}
	t.Logf("实际30000/1001fps、1.5倍速、137000us入点及已有2帧相位：移除工程[6,11)；右保留6帧PCM%d字节、RGBA%d字节逐字节一致", len(actualPCM), len(actualPixels))
}
