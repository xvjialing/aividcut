package application

import (
	"aividcut/internal/editor"
	"aividcut/internal/playback"
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitProxy(t *testing.T, s *Service, input PreviewProxyInput) PreviewProxyView {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		view, err := s.GetPreviewProxy(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == "ready" {
			return view
		}
		if view.Status == "failed" || view.Status == "cancelled" {
			t.Fatalf("代理任务终止 %+v", view)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("代理任务超时")
	return PreviewProxyView{}
}
func TestPreviewProxyActualTimingPCMAndCacheLifecycle(t *testing.T) {
	tools := preciseApplicationTools(t)
	s := preciseApplication(t, tools)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	preciseApplicationCommand(t, tools, "-v", "error", "-f", "lavfi", "-i", "nullsrc=s=1280x720:r=30000/1001,geq=lum='40+mod(N,6)*30':cb=100:cr=150", "-f", "lavfi", "-i", "sine=frequency=660:sample_rate=48000", "-t", "2", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-c:a", "aac", source)
	b, err := s.ImportPaths(context.Background(), []string{source})
	if err != nil {
		t.Fatal(err)
	}
	p := b.Project
	settings := p.Settings
	settings.Width, settings.Height = 320, 180
	clip := editor.DefaultClip("video", "track-video-1", "video")
	clip.AssetID = p.Assets[0].ID
	clip.DurationFrames = 24
	clip.SourceInUs = 137000
	clip.SpeedNum, clip.SpeedDen = 3, 2
	b, err = s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "phase", Commands: []editor.Command{{Type: "update_project", Settings: &settings}, {Type: "add_clip", Clip: &clip}, {Type: "split_clip", ClipID: clip.ID, AtFrame: 2}, {Type: "delete_clip", ClipID: clip.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(b.Project)
	input := PreviewProxyInput{ProjectID: b.Project.ID, SourceEpoch: b.SourceEpoch, AssetID: p.Assets[0].ID, Fingerprint: p.Assets[0].Fingerprint, Profile: "540p"}
	first, err := s.GetPreviewProxy(context.Background(), input)
	if err != nil || first.Status != "missing" {
		t.Fatal(first, err)
	}
	if len(s.ListJobs()) != 0 {
		t.Fatal("读取自动生成代理")
	}
	started, err := s.StartPreviewProxy(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.StartPreviewProxy(context.Background(), input)
	if err != nil || again.Job.ID != started.Job.ID {
		t.Fatal("重复启动没有去重", err)
	}
	view := waitProxy(t, s, input)
	if view.Proof.Width != 960 || view.Proof.Height != 540 || view.Proof.Frames != 60 {
		t.Fatal("代理几何/帧数错误", view)
	}
	actual, _ := json.Marshal(s.GetBootstrap().Project)
	if !bytes.Equal(before, actual) {
		t.Fatal("代理操作改变工程")
	}
	config := PlaybackEnvironment()
	sources, missing := s.playbackVisualSources(b.Project, "540p", b.SourceEpoch)
	if len(sources) != 1 || missing != 0 {
		t.Fatal("已核查代理未接入播放")
	}
	if os.Getenv("AIVIDCUT_MLT_WORKER") != "" {
		original, err := playback.Start(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		defer original.Close()
		config.VisualSources = sources
		config.WindowFrames = 3
		proxy, err := playback.Start(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		defer proxy.Close()
		if _, err = original.Load(context.Background(), b.Project); err != nil {
			t.Fatal(err)
		}
		if _, err = proxy.Load(context.Background(), b.Project); err != nil {
			t.Fatal(err)
		}
		heard := false
		for _, frame := range []int64{2, 3, 8, 13, 2} {
			a, err := original.Seek(context.Background(), frame)
			if err != nil {
				t.Fatal(err)
			}
			pcm := append([]byte{}, a.PCM...)
			for _, value := range pcm {
				if value != 0 {
					heard = true
					break
				}
			}
			pixels := append([]byte{}, a.RGBA...)
			q, err := proxy.Seek(context.Background(), frame)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(pcm, q.PCM) || q.Position != frame || len(pixels) != len(q.RGBA) {
				t.Fatal("代理改变原片声音/全局时钟或画幅")
			}
			var sum float64
			for i := 0; i < len(pixels); i++ {
				sum += math.Abs(float64(pixels[i]) - float64(q.RGBA[i]))
			}
			mean := sum / float64(len(pixels))
			if mean > 3 {
				t.Fatalf("代理帧身份/显示异常 MAE %.6f", mean)
			}
			t.Logf("帧%d PCM%d字节相同，RGBA平均误差%.6f", frame, len(pcm), mean)
		}
		if !heard {
			t.Fatal("没有读取到实际非零原声，不能证明代理保留音频")
		}
	}
	// 同ID实例重开后须重新核对，缓存文件不重新转码。
	s.resetThumbnails(false)
	input.SourceEpoch = s.GetBootstrap().SourceEpoch
	cached, err := s.GetPreviewProxy(context.Background(), input)
	if err != nil || cached.Status != "cached" {
		t.Fatal("冷缓存未要求来源核对", cached, err)
	}
	_, err = s.StartPreviewProxy(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	verified := waitProxy(t, s, input)
	if verified.Proof.ProxyFingerprint != view.Proof.ProxyFingerprint {
		t.Fatal("缓存核对重新编码了代理")
	}
	if err = s.ClearPreviewProxy(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	removed, err := s.GetPreviewProxy(context.Background(), input)
	if err != nil || removed.Status != "missing" {
		t.Fatal("清理未移除代理", removed, err)
	}
	t.Log("代理生成/重复/逐帧证明/原片PCM/图窗口/源实例复核/清理与工程不变通过")
}
