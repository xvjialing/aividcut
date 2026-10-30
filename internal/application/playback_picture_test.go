package application

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"aividcut/internal/playback"
)

type fixturePictures struct {
	*fixturePlayback
	pictures atomic.Int32
}

func (f *fixturePictures) Preview(ctx context.Context, position int64) (playback.Frame, error) {
	f.pictures.Add(1)
	frame, err := f.fixturePlayback.Seek(ctx, position)
	frame.PictureOnly = true
	frame.Samples = 0
	frame.PCM = nil
	return frame, err
}
func (f *fixturePictures) Seek(context.Context, int64) (playback.Frame, error) {
	return playback.Frame{}, errors.New("应用不应为暂停画面重放音频")
}

func TestPlaybackUsesPictureBackendForPreparationPauseAndSeek(t *testing.T) {
	native := &fixturePictures{fixturePlayback: &fixturePlayback{done: make(chan struct{})}}
	s, input := playbackRegressionService(t, func(context.Context, playback.Config) (PlaybackBackend, error) { return native, nil })
	start, err := s.StartPlayback(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialPlayback(t, start)
	readPlayback(t, connection)
	_, _, poster := readPlayback(t, connection)
	if !poster.PictureOnly || poster.Samples != 0 || poster.Playing {
		t.Fatal("准备时未发送纯画面快照")
	}
	ackPlayback(t, connection, poster)
	position := int64(2)
	if _, err := s.ControlPlayback(context.Background(), PlaybackControlInput{ID: start.Status.ID, Action: "seek", Frame: &position}); err != nil {
		t.Fatal(err)
	}
	readPlayback(t, connection)
	_, _, poster = readPlayback(t, connection)
	if poster.Position != 2 || !poster.PictureOnly || poster.Samples != 0 {
		t.Fatal("暂停定位返回了错误帧或多余PCM")
	}
	ackPlayback(t, connection, poster)
	if _, err := s.ControlPlayback(context.Background(), PlaybackControlInput{ID: start.Status.ID, Action: "play", Frame: &position}); err != nil {
		t.Fatal(err)
	}
	readPlayback(t, connection)
	_, _, poster = readPlayback(t, connection)
	ackPlayback(t, connection, poster)
	_, _, live := readPlayback(t, connection)
	if !live.Playing || live.PictureOnly || live.Samples < 1 || live.Position != 2 {
		t.Fatal("播放未从目标帧恢复真实音频")
	}
	if native.pictures.Load() < 3 {
		t.Fatal("应用未使用分离的画面定位")
	}
	position = 3
	paused, err := s.ControlPlayback(context.Background(), PlaybackControlInput{ID: start.Status.ID, Action: "pause", Frame: &position})
	if err != nil || paused.Status != "paused" {
		t.Fatal("暂停未成功", err)
	}
	for i := 0; i < 5; i++ {
		kind, state, _ := readPlayback(t, connection)
		if kind == "state" && state.Epoch == paused.Epoch {
			break
		}
		if i == 4 {
			t.Fatal("未收到暂停后的新epoch")
		}
	}
	_, _, poster = readPlayback(t, connection)
	if !poster.PictureOnly || poster.Position != 3 || poster.Samples != 0 {
		t.Fatal("暂停操作仍重放声音或定位错误")
	}
}
