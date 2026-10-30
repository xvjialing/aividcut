package application

import (
	"context"
	"testing"

	"aividcut/internal/playback"
)

func TestPlaybackQualityIsSessionScopedAndInvalidInputKeepsCurrent(t *testing.T) {
	s, native, input := playbackApplication(t)
	before := s.GetBootstrap()
	factory := s.playbackState.options.Factory
	var quality string
	calls := 0
	s.playbackState.options.Factory = func(ctx context.Context, cfg playback.Config) (PlaybackBackend, error) {
		quality = cfg.RenderQuality
		calls++
		return factory(ctx, cfg)
	}
	input.RenderQuality = "quarter"
	started, err := s.StartPlayback(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if quality != "quarter" || s.nativeExportConfig.Load().RenderQuality != "" {
		t.Fatal("精度没有传到会话或污染导出设置", quality)
	}
	bad := input
	bad.RenderQuality = "fast"
	if _, err := s.StartPlayback(context.Background(), bad); err == nil {
		t.Fatal("未知精度被接受")
	}
	select {
	case <-native.Done():
		t.Fatal("错误请求关闭了现有播放")
	default:
	}
	if calls != 1 {
		t.Fatal("错误请求启动了新进程")
	}
	after := s.GetBootstrap()
	if before.Project.Revision != after.Project.Revision || before.Dirty != after.Dirty || before.CanUndo != after.CanUndo {
		t.Fatal("精度变更修改工程")
	}
	if _, err := s.StopPlayback(started.Status.ID); err != nil {
		t.Fatal(err)
	}
}
