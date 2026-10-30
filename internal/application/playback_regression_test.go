package application

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/playback"
)

func playbackRegressionService(t *testing.T, factory func(context.Context, playback.Config) (PlaybackBackend, error)) (*Service, PlaybackStartInput) {
	t.Helper()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	p := s.GetBootstrap().Project
	clip := editor.DefaultClip("regression-picture", "track-text-1", "text")
	clip.Text, clip.DurationFrames = "播放生命周期夹具", 6
	changed, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "regression-picture", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigurePlayback(PlaybackOptions{Factory: factory}); err != nil {
		t.Fatal(err)
	}
	return s, PlaybackStartInput{ProjectID: changed.Project.ID, ProjectRevision: changed.Project.Revision}
}

type playbackLoadGate struct {
	*fixturePlayback
	entered, release chan struct{}
}

func (f *playbackLoadGate) Load(ctx context.Context, p editor.Project) (playback.Info, error) {
	close(f.entered)
	select {
	case <-f.release:
		return f.fixturePlayback.Load(ctx, p)
	case <-ctx.Done():
		return playback.Info{}, ctx.Err()
	}
}

// 在Start的首个Err检查取得结果后通知测试，确保取消确实发生在排队中。
type playbackCheckedContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *playbackCheckedContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func TestPlaybackCancelledQueuedStartDoesNotCloseNewlyCommittedSession(t *testing.T) {
	first := &playbackLoadGate{fixturePlayback: &fixturePlayback{done: make(chan struct{})}, entered: make(chan struct{}), release: make(chan struct{})}
	var calls atomic.Int32
	s, input := playbackRegressionService(t, func(ctx context.Context, _ playback.Config) (PlaybackBackend, error) {
		if calls.Add(1) == 1 {
			return first, nil
		}
		return &fixturePlayback{done: make(chan struct{})}, ctx.Err()
	})
	type started struct {
		value PlaybackStart
		err   error
	}
	firstResult := make(chan started, 1)
	go func() { value, err := s.StartPlayback(context.Background(), input); firstResult <- started{value, err} }()
	select {
	case <-first.entered:
	case <-time.After(time.Second):
		t.Fatal("首个Start没有进入Load")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checked := &playbackCheckedContext{Context: ctx, checked: make(chan struct{})}
	secondResult := make(chan error, 1)
	go func() { _, err := s.StartPlayback(checked, input); secondResult <- err }()
	<-checked.checked
	cancel()
	select {
	case err := <-secondResult:
		if err == nil {
			t.Fatal("已取消的排队Start成功提交")
		}
	case <-time.After(time.Second):
		close(first.release)
		t.Fatal("已取消的排队Start仍在等待其它加载完成")
	}
	close(first.release)
	firstStart := <-firstResult
	if firstStart.err != nil {
		t.Fatal(firstStart.err)
	}
	status := s.GetPlaybackStatus()
	if calls.Load() != 1 || status.ID != firstStart.value.Status.ID || status.Status != "paused" {
		t.Fatalf("已取消的旧请求关闭了刚提交的会话：factory=%d status=%+v", calls.Load(), status)
	}
	select {
	case <-first.Done():
		t.Fatal("排队期间取消的Start释放了正在使用的原生后端")
	default:
	}
}

type playbackSeekGate struct {
	*fixturePlayback
	block     atomic.Bool
	entered   chan struct{}
	cancelled chan struct{}
}

func (f *playbackSeekGate) Seek(ctx context.Context, position int64) (playback.Frame, error) {
	if f.block.CompareAndSwap(true, false) {
		close(f.entered)
		<-ctx.Done()
		close(f.cancelled)
		return playback.Frame{}, ctx.Err()
	}
	return f.fixturePlayback.Seek(ctx, position)
}

func TestPlaybackControlCancellationReachesActiveNativeSeek(t *testing.T) {
	native := &playbackSeekGate{fixturePlayback: &fixturePlayback{done: make(chan struct{})}, entered: make(chan struct{}), cancelled: make(chan struct{})}
	s, input := playbackRegressionService(t, func(context.Context, playback.Config) (PlaybackBackend, error) { return native, nil })
	start, err := s.StartPlayback(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialPlayback(t, start)
	readPlayback(t, connection)
	_, _, poster := readPlayback(t, connection)
	ackPlayback(t, connection, poster)
	native.block.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	position := int64(2)
	go func() {
		_, err := s.ControlPlayback(ctx, PlaybackControlInput{ID: start.Status.ID, Action: "seek", Frame: &position})
		result <- err
	}()
	select {
	case <-native.entered:
	case <-time.After(time.Second):
		t.Fatal("受控Seek未开始")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("调用方取消结果错误：%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消未让控制接口返回")
	}
	select {
	case <-native.cancelled:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("接口虽已取消，原生Seek仍持有会话循环，后续ACK/控制不能处理")
	}
	select {
	case <-native.Done():
	case <-time.After(time.Second):
		t.Fatal("已进入原生调用的取消没有释放会话，可能继续复用乱序管道")
	}
}

type playbackLoadFailure struct{ *playbackDelayedDone }

func (f *playbackLoadFailure) Load(context.Context, editor.Project) (playback.Info, error) {
	return playback.Info{}, errors.New("夹具在加载期间失败")
}

func TestPlaybackFailedLoadMustExitBeforeNextFactory(t *testing.T) {
	old := &playbackLoadFailure{&playbackDelayedDone{fixturePlayback: &fixturePlayback{done: make(chan struct{})}, closeCalled: make(chan struct{})}}
	var calls atomic.Int32
	s, input := playbackRegressionService(t, func(context.Context, playback.Config) (PlaybackBackend, error) {
		calls.Add(1)
		return old, nil
	})
	t.Cleanup(func() { _ = old.fixturePlayback.Close() })
	if _, err := s.StartPlayback(context.Background(), input); err == nil {
		t.Fatal("加载失败却提交了预览会话")
	}
	if _, err := s.StartPlayback(context.Background(), input); err == nil || calls.Load() != 1 {
		t.Fatalf("加载失败的旧进程未退出就启动替代：factory=%d err=%v", calls.Load(), err)
	}
	_ = old.fixturePlayback.Close()
	if _, err := s.StartPlayback(context.Background(), input); err == nil || calls.Load() != 2 {
		t.Fatalf("旧进程退出后未允许再次加载：factory=%d err=%v", calls.Load(), err)
	}
}

type playbackDelayedDone struct {
	*fixturePlayback
	closeCalled chan struct{}
	closeOnce   sync.Once
}

func (f *playbackDelayedDone) Close() error {
	f.closeOnce.Do(func() { close(f.closeCalled) })
	return errors.New("夹具原生进程尚未退出")
}

func TestPlaybackReplacementWaitsForPreviousNativeDone(t *testing.T) {
	old := &playbackDelayedDone{fixturePlayback: &fixturePlayback{done: make(chan struct{})}, closeCalled: make(chan struct{})}
	var calls atomic.Int32
	s, input := playbackRegressionService(t, func(context.Context, playback.Config) (PlaybackBackend, error) {
		if calls.Add(1) == 1 {
			return old, nil
		}
		return &fixturePlayback{done: make(chan struct{})}, nil
	})
	// cleanup期间才让旧worker真正结束，不在测试中触发真实MLT/操作系统进程。
	t.Cleanup(func() { _ = old.fixturePlayback.Close() })
	if _, err := s.StartPlayback(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := s.StartPlayback(context.Background(), input); result <- err }()
	select {
	case <-old.closeCalled:
	case <-time.After(time.Second):
		t.Fatal("替换没有开始释放旧后端")
	}
	select {
	case err := <-result:
		if err == nil || calls.Load() != 1 {
			t.Fatalf("旧worker未Done即启动第二个后端：factory=%d err=%v", calls.Load(), err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("旧worker未退出时应明确返回仍在释放，而不是无限等待")
	}
}

type playbackWrongPosition struct {
	*fixturePlayback
	wrongSeek bool
}

func (f *playbackWrongPosition) Seek(ctx context.Context, position int64) (playback.Frame, error) {
	if f.wrongSeek {
		position++
	}
	return f.fixturePlayback.Seek(ctx, position)
}
func (f *playbackWrongPosition) Next(ctx context.Context) (playback.Frame, error) {
	frame, err := f.fixturePlayback.Next(ctx)
	frame.AudioStartSample++
	return frame, err
}

func TestPlaybackRejectsIncorrectNativeSeekBeforePublishingURL(t *testing.T) {
	native := &playbackWrongPosition{fixturePlayback: &fixturePlayback{done: make(chan struct{})}, wrongSeek: true}
	s, input := playbackRegressionService(t, func(context.Context, playback.Config) (PlaybackBackend, error) { return native, nil })
	start, err := s.StartPlayback(context.Background(), input)
	if err == nil || start.StreamURL != "" {
		t.Fatal("错误定位仍发布了媒体连接")
	}
	select {
	case <-native.Done():
	case <-time.After(time.Second):
		t.Fatal("定位失败没有释放后端")
	}
}

func TestPlaybackRejectsDiscontinuousPCMBeforeSendingLiveFrame(t *testing.T) {
	native := &playbackWrongPosition{fixturePlayback: &fixturePlayback{done: make(chan struct{})}}
	s, input := playbackRegressionService(t, func(context.Context, playback.Config) (PlaybackBackend, error) { return native, nil })
	start, err := s.StartPlayback(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialPlayback(t, start)
	readPlayback(t, connection)
	_, _, poster := readPlayback(t, connection)
	ackPlayback(t, connection, poster)
	if err := connection.WriteJSON(map[string]any{"type": "play", "frame": 0}); err != nil {
		t.Fatal(err)
	}
	readPlayback(t, connection)
	_, _, poster = readPlayback(t, connection)
	ackPlayback(t, connection, poster)
	kind, state, _ := readPlayback(t, connection)
	if kind != "state" || state.Status != "failed" {
		t.Fatal("不连续PCM仍发给设备，而不是先停止", kind, state)
	}
}

func TestPlaybackCapabilityQueriesAreBoundedAndQueuedCancellationReturns(t *testing.T) {
	s, _ := playbackRegressionService(t, func(context.Context, playback.Config) (PlaybackBackend, error) {
		return &fixturePlayback{done: make(chan struct{})}, nil
	})
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s.playbackState.options.Probe = func(ctx context.Context, _ playback.Config) (playback.Capabilities, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return playback.Capabilities{}, nil
		case <-ctx.Done():
			return playback.Capabilities{}, ctx.Err()
		}
	}
	first := make(chan error, 1)
	go func() { _, err := s.GetPlaybackCapabilities(context.Background()); first <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("能力检查未开始")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.GetPlaybackCapabilities(ctx)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("排队检查没有遵守取消/单进程预算：calls=%d err=%v", calls.Load(), err)
	}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}
