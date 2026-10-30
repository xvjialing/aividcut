package application

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/media"
	"aividcut/internal/playback"
	"github.com/gorilla/websocket"
)

type fixturePlayback struct {
	mu       sync.Mutex
	info     playback.Info
	position int64
	playing  bool
	done     chan struct{}
	once     sync.Once
	nexts    atomic.Int32
}

func (f *fixturePlayback) Load(ctx context.Context, p editor.Project) (playback.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.info = playback.Info{ProjectID: p.ID, ProjectRevision: p.Revision, DurationFrames: 6, Width: 16, Height: 16, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2}
	return f.info, ctx.Err()
}
func (f *fixturePlayback) Play(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playing = true
	return ctx.Err()
}
func (f *fixturePlayback) Pause(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.playing = false
	return ctx.Err()
}
func (f *fixturePlayback) frame(position int64) playback.Frame {
	image := make([]byte, 16*16*4)
	for i := 3; i < len(image); i += 4 {
		image[i] = 255
	}
	return playback.Frame{Position: position, Width: 16, Height: 16, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, Samples: 1600, AudioStartSample: position * 1600, RGBA: image, PCM: make([]byte, 1600*8), End: position == f.info.DurationFrames-1}
}
func (f *fixturePlayback) Seek(ctx context.Context, position int64) (playback.Frame, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.position = position
	return f.frame(position), ctx.Err()
}
func (f *fixturePlayback) Next(ctx context.Context) (playback.Frame, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.playing {
		return playback.Frame{}, errors.New("fixture paused")
	}
	f.nexts.Add(1)
	frame := f.frame(f.position)
	if frame.End {
		f.playing = false
	} else {
		f.position++
	}
	return frame, ctx.Err()
}
func (f *fixturePlayback) Close() error          { f.once.Do(func() { close(f.done) }); return nil }
func (f *fixturePlayback) Done() <-chan struct{} { return f.done }

func playbackApplication(t *testing.T) (*Service, *fixturePlayback, PlaybackStartInput) {
	t.Helper()
	s, err := New(media.Toolchain{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	p := s.GetBootstrap().Project
	clip := editor.DefaultClip("fixture-text", "track-text-1", "text")
	clip.Text = "自建播放帧"
	clip.DurationFrames = 6
	changed, err := s.ApplyEdit(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "playback-fixture-text", Commands: []editor.Command{{Type: "add_clip", Clip: &clip}}})
	if err != nil {
		t.Fatal(err)
	}
	native := &fixturePlayback{done: make(chan struct{})}
	caps := playback.Capabilities{Available: true, Protocol: playback.Protocol, MLTVersion: "fixture", Text: playback.Feature{Supported: true}, Video: playback.Feature{Supported: true}, Audio: playback.Feature{Supported: true}, Image: playback.Feature{Supported: true}, Transform: playback.Feature{Supported: true}, Producers: []string{"colour", "avformat"}, Filters: []string{"qtext", "volume"}, Transitions: []string{"qtblend", "mix"}, Display: "webview_binary_canvas_audio"}
	if err := s.ConfigurePlayback(PlaybackOptions{Config: playback.Config{WorkerPath: "/fixture/worker"}, Probe: func(context.Context, playback.Config) (playback.Capabilities, error) { return caps, nil }, CheckProject: func(context.Context, playback.Config, editor.Project) error { return nil }, Factory: func(ctx context.Context, _ playback.Config) (PlaybackBackend, error) {
		go func() { <-ctx.Done(); _ = native.Close() }()
		return native, nil
	}}); err != nil {
		t.Fatal(err)
	}
	return s, native, PlaybackStartInput{ProjectID: changed.Project.ID, ProjectRevision: changed.Project.Revision}
}
func dialPlayback(t *testing.T, start PlaybackStart) *websocket.Conn {
	t.Helper()
	connection, response, err := websocket.DefaultDialer.Dial(start.StreamURL, http.Header{"Origin": []string{"wails://localhost"}})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("二进制会话连接失败 %d: %v", status, err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}
func readPlayback(t *testing.T, connection *websocket.Conn) (string, PlaybackStatus, playbackWireHeader) {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, data, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if kind == websocket.TextMessage {
		var event struct {
			Type   string         `json:"type"`
			Status PlaybackStatus `json:"status"`
		}
		if json.Unmarshal(data, &event) != nil || event.Type != "state" {
			t.Fatal("非法状态事件")
		}
		return "state", event.Status, playbackWireHeader{}
	}
	if kind != websocket.BinaryMessage || len(data) < 16 || string(data[:4]) != playback.Protocol {
		t.Fatal("不是有界AIV1二进制帧")
	}
	metadata, image, audio := int(binary.BigEndian.Uint32(data[4:8])), int(binary.BigEndian.Uint32(data[8:12])), int(binary.BigEndian.Uint32(data[12:16]))
	if len(data) != 16+metadata+image+audio {
		t.Fatal("包长不匹配")
	}
	var header playbackWireHeader
	if json.Unmarshal(data[16:16+metadata], &header) != nil || image != header.Width*header.Height*4 || audio != header.Samples*8 {
		t.Fatal("实际RGBA/PCM与元信息不匹配")
	}
	return "frame", PlaybackStatus{}, header
}
func ackPlayback(t *testing.T, connection *websocket.Conn, frame playbackWireHeader) {
	t.Helper()
	if err := connection.WriteJSON(map[string]any{"type": "ack", "epoch": frame.Epoch, "sequence": frame.Sequence, "position": frame.Position}); err != nil {
		t.Fatal(err)
	}
}

func TestPlaybackBinaryScopeAuthenticationBackpressureAndMasterPause(t *testing.T) {
	s, native, input := playbackApplication(t)
	before := s.GetBootstrap().Project
	start, err := s.StartPlayback(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(s.GetPlaybackStatus())
	if strings.Contains(string(encoded), "token") || strings.Contains(string(encoded), "ws://") {
		t.Fatal("普通状态泄漏会话连接")
	}
	bad, err := url.Parse(start.StreamURL)
	if err != nil {
		t.Fatal(err)
	}
	bad.RawQuery = "token=wrong"
	if c, response, err := websocket.DefaultDialer.Dial(bad.String(), nil); err == nil {
		c.Close()
		t.Fatal("错误token连接成功")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("鉴权未明确拒绝", err)
	}
	if c, response, err := websocket.DefaultDialer.Dial(start.StreamURL, http.Header{"Origin": []string{"https://outside.invalid"}}); err == nil {
		c.Close()
		t.Fatal("外站Origin连接成功")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("Origin未拒绝", err)
	}
	connection := dialPlayback(t, start)
	kind, state, _ := readPlayback(t, connection)
	if kind != "state" || state.Epoch != 1 {
		t.Fatal("state未先于epoch帧")
	}
	_, _, snapshot := readPlayback(t, connection)
	if snapshot.Playing || snapshot.Position != 0 {
		t.Fatal("暂停快照伪播放")
	}
	ackPlayback(t, connection, snapshot)
	if err := connection.WriteJSON(map[string]any{"type": "play", "frame": 0}); err != nil {
		t.Fatal(err)
	}
	kind, state, _ = readPlayback(t, connection)
	if kind != "state" || state.Status != "playing" || state.Epoch != 2 {
		t.Fatal("播放epoch状态未先发")
	}
	_, _, poster := readPlayback(t, connection)
	if poster.Playing {
		t.Fatal("Seek快照不应二次播放PCM")
	}
	ackPlayback(t, connection, poster)
	live := []playbackWireHeader{}
	for len(live) < 3 {
		kind, _, frame := readPlayback(t, connection)
		if kind != "frame" || !frame.Playing {
			t.Fatal("缺实际播放帧")
		}
		live = append(live, frame)
	}
	time.Sleep(30 * time.Millisecond)
	if native.nexts.Load() != 3 || s.GetPlaybackStatus().Frame != 0 || s.GetPlaybackStatus().InFlight != 3 {
		t.Fatal("没有按消费背压或把预取游标冒充播放进度", native.nexts.Load(), s.GetPlaybackStatus())
	}
	frame := int64(1)
	paused, err := s.ControlPlayback(context.Background(), PlaybackControlInput{ID: start.Status.ID, Action: "pause", Frame: &frame})
	if err != nil || paused.Frame != 1 || paused.Epoch != 3 {
		t.Fatal("暂停没定位音频主时钟", err)
	}
	kind, state, _ = readPlayback(t, connection)
	if kind != "state" || state.Frame != 1 || state.Status != "paused" {
		t.Fatal("暂停state不符")
	}
	_, _, pausedFrame := readPlayback(t, connection)
	if pausedFrame.Position != 1 || pausedFrame.Playing {
		t.Fatal("暂停抢先到预取队尾")
	}
	ackPlayback(t, connection, live[0])
	ackPlayback(t, connection, pausedFrame)
	if _, err := s.StopPlayback(start.Status.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-native.Done():
	case <-time.After(time.Second):
		t.Fatal("关闭未释放worker")
	}
	if projectHash(s.GetBootstrap().Project) != projectHash(before) || s.GetBootstrap().Project.Revision != before.Revision {
		t.Fatal("预览修改工程")
	}
}

func TestPlaybackEndWaitsForActualConsumptionAcknowledgement(t *testing.T) {
	s, _, input := playbackApplication(t)
	input.Frame = 5
	start, err := s.StartPlayback(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialPlayback(t, start)
	readPlayback(t, connection)
	_, _, poster := readPlayback(t, connection)
	ackPlayback(t, connection, poster)
	if err := connection.WriteJSON(map[string]any{"type": "play", "frame": 5}); err != nil {
		t.Fatal(err)
	}
	readPlayback(t, connection)
	_, _, poster = readPlayback(t, connection)
	ackPlayback(t, connection, poster)
	_, _, last := readPlayback(t, connection)
	if !last.End || !last.Playing {
		t.Fatal("没有实际尾帧PCM")
	}
	if state := s.GetPlaybackStatus(); state.Status != "playing" || state.Frame != 5 {
		t.Fatal("PCM未消费就假称播放结束", state)
	}
	ackPlayback(t, connection, last)
	kind, state, _ := readPlayback(t, connection)
	if kind != "state" || state.Status != "paused" || state.Frame != 6 {
		t.Fatal("尾PCM消费后未到exclusive end", state)
	}
}

func TestPlaybackSameIDReopenInvalidatesOriginalEngineAndStopsNative(t *testing.T) {
	s, native, input := playbackApplication(t)
	path := filepath.Join(t.TempDir(), "same-project.aividcut")
	if _, err := s.SaveProjectTo(path); err != nil {
		t.Fatal(err)
	}
	start, err := s.StartPlayback(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	connection := dialPlayback(t, start)
	readPlayback(t, connection)
	_, _, poster := readPlayback(t, connection)
	ackPlayback(t, connection, poster)
	if _, err := s.OpenProjectPath(path); err != nil {
		t.Fatal(err)
	}
	kind, state, _ := readPlayback(t, connection)
	if kind != "state" || state.Status != "stale" {
		t.Fatal("原工程实例已换却仍播放旧图", state)
	}
	select {
	case <-native.Done():
	case <-time.After(time.Second):
		t.Fatal("切工程仍驻留旧worker")
	}
	if s.GetBootstrap().Capabilities["nativePlayback"].Available {
		t.Fatal("重开同ID仍沿用旧已验帧能力")
	}
}
