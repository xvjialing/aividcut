package application

import (
	"aividcut/internal/connections"
	"aividcut/internal/editor"
	"aividcut/internal/generation"
	"aividcut/internal/generationjobs"
	"aividcut/internal/playback"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerationPlacementActualVideoPCMMatchesManualSource(t *testing.T) {
	if os.Getenv("AIVIDCUT_MLT_WORKER") == "" {
		t.Skip("真实音画验收需要显式原生worker")
	}
	tools := preciseApplicationTools(t)
	s, store, control := captureApplication(t, tools)
	dir := t.TempDir()
	path := filepath.Join(dir, "own-moving-tone.mp4")
	preciseApplicationCommand(t, tools, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30000/1001", "-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000", "-t", "2.002", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-c:a", "aac", path)
	bytesVideo, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	control.submit = func(_ context.Context, r generation.Request) (generation.Task, error) {
		task := appTask(r, generation.Succeeded, "")
		task.Outputs = []generation.Output{{Kind: "video", MIME: "video/mp4", Data: bytesVideo}}
		return task, nil
	}
	profile, err := s.SaveConnection(context.Background(), connections.SaveInput{ProviderID: generation.MiniMax, ModelID: "MiniMax-H3", Name: "自建视频假适配器", APIKey: "fixture-only"})
	if err != nil {
		t.Fatal(err)
	}
	input := generationjobs.SubmitInput{ConnectionID: profile.ID, Consent: true, Request: generation.Request{ID: "placement-video", ProviderID: generation.MiniMax, ModelID: profile.ModelID, Mode: generation.VideoText, Prompt: "自建测试视频，不调用真实模型", Count: 1, Size: "768P", AspectRatio: "16:9", DurationSeconds: 4}}
	if _, err = s.SubmitGeneration(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	job := appAwaitJob(t, s, input.Request.ID, generationjobs.Succeeded)
	b := s.GetBootstrap()
	settings := b.Project.Settings
	settings.Width, settings.Height, settings.FPSNum, settings.FPSDen = 320, 180, 30000, 1001
	if _, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "placement-settings", Commands: []editor.Command{{Type: "update_project", Settings: &settings}}}); err != nil {
		t.Fatal(err)
	}
	request := placementInput(s, job)
	request.AutoDuration = true
	request.DurationFrames = 0
	full, err := s.PreviewGenerationPlacement(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !editor.SourceRangeFits(full.Clip, full.Asset, settings) || full.Clip.SourcePhase == nil {
		t.Fatal("自动长度/原处理配方无效")
	}
	request.AutoDuration = false
	request.DurationFrames = 120
	if _, err = s.PreviewGenerationPlacement(context.Background(), request); err == nil {
		t.Fatal("信任请求4秒而没有拒绝真实短视频")
	}
	request.DurationFrames = 12
	request.SourceInUs = 137000
	request.AudioMode = "embedded"
	reads, posts := store.readCount(), control.posts.Load()
	view, err := s.PreviewGenerationPlacement(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	// 元数据归档不搬动素材，已经核对的候选应用仍可完成。
	if _, err = s.ArchiveGenerationJob(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.ApplyGenerationPlacement(context.Background(), view.ID)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := tools.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	asset.ID = "manual-reference"
	reference := editor.NewProject("手工源窗基线")
	reference.Settings = settings
	reference.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("manual-clip", "track-video-1", "video")
	clip.AssetID = asset.ID
	clip.StartFrame = 7
	clip.DurationFrames = 12
	clip.SourceInUs = 137000
	reference.Clips = []editor.Clip{clip}
	collect := func(project editor.Project) ([]byte, []byte) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		session, e := playback.Start(ctx, PlaybackEnvironment())
		if e != nil {
			t.Fatal(e)
		}
		defer session.Close()
		if _, e = session.Load(ctx, project); e != nil {
			t.Fatal(e)
		}
		if _, e = session.Preview(ctx, 7); e != nil {
			t.Fatal(e)
		}
		if e = session.Play(ctx); e != nil {
			t.Fatal(e)
		}
		var pcm, rgba []byte
		for i := int64(0); i < 12; i++ {
			frame, e := session.Next(ctx)
			if e != nil || frame.Position != 7+i {
				t.Fatal("帧序错误", e)
			}
			pcm = append(pcm, frame.PCM...)
			rgba = append(rgba, frame.RGBA...)
		}
		return pcm, rgba
	}
	expectedPCM, expectedRGBA := collect(reference)
	pcm, rgba := collect(after.Project)
	if len(pcm) == 0 || bytes.Equal(pcm, make([]byte, len(pcm))) || !bytes.Equal(pcm, expectedPCM) || !bytes.Equal(rgba, expectedRGBA) {
		t.Fatalf("实际音画不匹配 PCM%d/%d RGBA%d/%d", len(pcm), len(expectedPCM), len(rgba), len(expectedRGBA))
	}
	t.Logf("任务归档后已准备候选完成上轨：12帧 PCM=%d字节 RGBA=%d字节，与手工源窗完全一致", len(pcm), len(rgba))
	if _, err = s.RestoreArchivedGenerationJob(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Undo(); err != nil {
		t.Fatal(err)
	}
	request = placementInput(s, job)
	request.StartFrame = 7
	request.SourceInUs = 137000
	request.DurationFrames = 12
	silent, err := s.PreviewGenerationPlacement(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	muted, err := s.ApplyGenerationPlacement(context.Background(), silent.ID)
	if err != nil {
		t.Fatal(err)
	}
	mutedPCM, mutedRGBA := collect(muted.Project)
	if !bytes.Equal(mutedPCM, make([]byte, len(mutedPCM))) || !bytes.Equal(mutedRGBA, expectedRGBA) {
		t.Fatal("不用声音改变了画面或仍可听")
	}
	if store.readCount() != reads || control.posts.Load() != posts {
		t.Fatal("上轨触发模型/凭据")
	}
	// 合成词时间只验证既有源字幕规则；不冒充真实ASR。
	b = s.GetBootstrap()
	words := []editor.SourceWord{{ID: "synthetic-word", StartUs: 200000, EndUs: 350000, Text: "合成词时间"}}
	transcript := editor.Transcript{ID: "placement-transcript", AssetID: view.Asset.ID, SourceFingerprint: view.Asset.Fingerprint, SourceDurationUs: view.Asset.DurationUs, Revision: 1, RawWords: words, AcceptedWords: words}
	// 只初始化既有领域状态，应用层禁止普通编辑伪造语音接纳的边界保持。
	s.mu.Lock()
	_, err = s.engine.Apply(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "placement-captions", Commands: []editor.Command{{Type: "accept_transcript", Transcript: &transcript}, {Type: "generate_bound_captions", TranscriptID: transcript.ID, TrackID: "track-caption-1"}}})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b = s.GetBootstrap()
	var manual editor.Clip
	for _, c := range b.Project.Clips {
		if c.Kind == "caption" {
			manual = c
			break
		}
	}
	if manual.ID == "" {
		t.Fatal("没有派生基线字幕")
	}
	manual.Text = "手工保留文字"
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: b.Project.Revision, IdempotencyKey: "manual-caption", Commands: []editor.Command{{Type: "update_clip", Clip: &manual}}})
	if err != nil {
		t.Fatal(err)
	}
	request = placementInput(s, job)
	request.StartFrame = 30
	request.SourceInUs = 137000
	request.DurationFrames = 12
	if _, err = s.PreviewGenerationPlacement(context.Background(), request); err == nil {
		t.Fatal("静音候选绕过了持续源字幕保护")
	}
	request.AudioMode = "embedded"
	captioned, err := s.PreviewGenerationPlacement(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if captioned.CaptionCount != 1 {
		t.Fatal("新增绑定字幕统计不准确")
	}
	final, err := s.ApplyGenerationPlacement(context.Background(), captioned.ID)
	if err != nil {
		t.Fatal(err)
	}
	protected := false
	newCaption := false
	for _, c := range final.Project.Clips {
		if c.ID == manual.ID && c.Text == manual.Text {
			protected = true
		}
		if c.CaptionSource != nil && c.CaptionSource.SourceClipID == captioned.Clip.ID {
			newCaption = true
		}
	}
	if !protected || !newCaption {
		t.Fatal("新实例字幕未派生或旧手改丢失")
	}
	t.Logf("实际29.97fps/137000us入点/第7帧开始：12帧PCM%d字节/RGBA%d字节与手工编排完全相同；静音PCM%d字节为零，自动取长%d帧，拒绝虚报4秒", len(pcm), len(rgba), len(mutedPCM), full.Clip.DurationFrames)
	t.Log("新增实例派生1条绑定字幕，旧实例手改字幕保持；静音路径拒绝持续字幕源")
}
