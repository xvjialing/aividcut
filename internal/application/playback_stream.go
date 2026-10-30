package application

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"sync"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/playback"
	"github.com/gorilla/websocket"
)

type playbackCommand struct {
	action          string
	frame           *int64
	epoch, sequence uint64
	position        *int64
	connection      *websocket.Conn
	ctx             context.Context
	reply           chan playbackReply
}

var errPlaybackControlNotStarted = errors.New("预览控制在执行前已取消")

type playbackReply struct {
	status PlaybackStatus
	err    error
}
type playbackWireHeader struct {
	playback.Frame
	Type     string `json:"type"`
	Epoch    uint64 `json:"epoch"`
	Sequence uint64 `json:"sequence"`
	Playing  bool   `json:"playing"`
}
type playbackPending struct {
	position     int64
	end, playing bool
}
type playbackRecord struct {
	service            *Service
	engine             *editor.Engine
	native             PlaybackBackend
	info               playback.Info
	ctx                context.Context
	cancel             context.CancelFunc
	mu                 sync.Mutex
	status             PlaybackStatus
	closed             bool
	connection         *websocket.Conn
	server             *http.Server
	host, secret, path string
	serveDone, done    chan struct{}
	cleanupOnce        sync.Once
	handlers           sync.WaitGroup
	commands           chan playbackCommand
	initial            playback.Frame
	sequence, lastAck  uint64
	decodeEnded        bool
	decodePosition     int64
	decodeSample       int64
	pending            map[uint64]playbackPending
}

func validPlaybackFrame(frame playback.Frame, info playback.Info) error {
	if frame.Position < 0 || frame.Position >= info.DurationFrames || frame.Width != info.Width || frame.Height != info.Height || frame.Width > 1920 || frame.Height > 1080 || frame.Width < 1 || frame.Height < 1 || int64(len(frame.RGBA)) != int64(frame.Width)*int64(frame.Height)*4 || frame.FPSNum != info.FPSNum || frame.FPSDen != info.FPSDen || frame.SampleRate != 48000 || frame.Channels != 2 || frame.Samples < 0 || frame.PictureOnly && frame.Samples != 0 || !frame.PictureOnly && frame.Samples == 0 || len(frame.PCM) > playback.MaxAudioBytes || int64(len(frame.PCM)) != int64(frame.Samples)*8 || frame.AudioStartSample < 0 || frame.End != (frame.Position == info.DurationFrames-1) {
		return errors.New("原生画面、PCM样本或位置不符合会话契约")
	}
	for i := 0; i < len(frame.PCM); i += 4 {
		value := math.Float32frombits(binary.LittleEndian.Uint32(frame.PCM[i:]))
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return errors.New("原生PCM存在非有限值")
		}
	}
	return nil
}

func (s *Service) newPlaybackRecord(ctx context.Context, cancel context.CancelFunc, native PlaybackBackend, engine *editor.Engine, info playback.Info, frame playback.Frame, secret string) (*playbackRecord, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("无法启动实时预览的本地二进制通道")
	}
	id := editor.NewID("playback")
	r := &playbackRecord{service: s, engine: engine, native: native, info: info, ctx: ctx, cancel: cancel, host: listener.Addr().String(), secret: secret, path: "/playback/" + id, serveDone: make(chan struct{}), done: make(chan struct{}), commands: make(chan playbackCommand, 16), initial: frame, pending: map[uint64]playbackPending{}, status: PlaybackStatus{ID: id, ProjectID: info.ProjectID, ProjectRevision: info.ProjectRevision, Status: "paused", Frame: frame.Position, DurationFrames: info.DurationFrames, Width: info.Width, Height: info.Height, FPSNum: info.FPSNum, FPSDen: info.FPSDen, SampleRate: info.SampleRate, Channels: info.Channels, Epoch: 1}}
	r.server = &http.Server{Handler: http.HandlerFunc(r.serveHTTP), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	r.decodePosition, r.decodeSample = frame.Position, frame.AudioStartSample
	go func() { _ = r.server.Serve(listener); close(r.serveDone) }()
	return r, nil
}
func (r *playbackRecord) streamURL() string        { return "ws://" + r.host + r.path + "?token=" + r.secret }
func (r *playbackRecord) snapshot() PlaybackStatus { r.mu.Lock(); defer r.mu.Unlock(); return r.status }
func (r *playbackRecord) update(run func(*PlaybackStatus)) {
	r.mu.Lock()
	run(&r.status)
	r.mu.Unlock()
}
func (r *playbackRecord) scopeValid() bool {
	r.service.mu.Lock()
	defer r.service.mu.Unlock()
	_, revision := r.service.engine.Identity()
	return r.service.engine == r.engine && revision == r.info.ProjectRevision
}
func allowedPlaybackOrigin(origin string) bool {
	switch origin {
	case "", "http://127.0.0.1:5173", "http://localhost:5173", "http://127.0.0.1:5174", "http://localhost:5174", "wails://localhost", "http://wails.localhost", "https://wails.localhost":
		return true
	}
	return false
}
func (r *playbackRecord) serveHTTP(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	peer, _, err := net.SplitHostPort(request.RemoteAddr)
	query := request.URL.Query()
	tokens := query["token"]
	if err != nil || net.ParseIP(peer) == nil || !net.ParseIP(peer).IsLoopback() || request.Method != http.MethodGet || request.Host != r.host || request.URL.Path != r.path || request.URL.RawPath != "" || len(query) != 1 || len(tokens) != 1 || subtle.ConstantTimeCompare([]byte(tokens[0]), []byte(r.secret)) != 1 || !allowedPlaybackOrigin(request.Header.Get("Origin")) {
		http.Error(w, "预览连接未授权", http.StatusForbidden)
		return
	}
	if !r.scopeValid() {
		http.Error(w, "预览工程已变化", http.StatusConflict)
		return
	}
	r.mu.Lock()
	if r.closed || r.connection != nil {
		r.mu.Unlock()
		http.Error(w, "已有预览连接或会话已关闭", http.StatusConflict)
		return
	}
	upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 32 << 10, HandshakeTimeout: 3 * time.Second, CheckOrigin: func(req *http.Request) bool { return allowedPlaybackOrigin(req.Header.Get("Origin")) }, EnableCompression: false}
	connection, err := upgrader.Upgrade(w, request, nil)
	if err != nil {
		r.mu.Unlock()
		return
	}
	r.connection = connection
	r.handlers.Add(1)
	r.status.Connected = true
	r.mu.Unlock()
	defer r.handlers.Done()
	connection.SetReadLimit(4096)
	if !r.enqueue(playbackCommand{action: "connect", connection: connection, ctx: r.ctx}) {
		_ = connection.Close()
		return
	}
	for {
		kind, data, err := connection.ReadMessage()
		if err != nil {
			break
		}
		if kind != websocket.TextMessage {
			_ = r.enqueue(playbackCommand{action: "protocol_error", ctx: r.ctx})
			break
		}
		var input struct {
			Type     string `json:"type"`
			Frame    *int64 `json:"frame,omitempty"`
			Epoch    uint64 `json:"epoch,omitempty"`
			Sequence uint64 `json:"sequence,omitempty"`
			Position *int64 `json:"position,omitempty"`
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil {
			_ = r.enqueue(playbackCommand{action: "protocol_error", ctx: r.ctx})
			break
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			_ = r.enqueue(playbackCommand{action: "protocol_error", ctx: r.ctx})
			break
		}
		switch input.Type {
		case "play", "pause", "seek", "ack", "close":
		default:
			_ = r.enqueue(playbackCommand{action: "protocol_error", ctx: r.ctx})
			return
		}
		if !r.enqueue(playbackCommand{action: input.Type, frame: input.Frame, epoch: input.Epoch, sequence: input.Sequence, position: input.Position, ctx: r.ctx}) {
			return
		}
	}
	_ = r.enqueue(playbackCommand{action: "disconnect", ctx: r.ctx})
}
func (r *playbackRecord) enqueue(command playbackCommand) bool {
	select {
	case r.commands <- command:
		return true
	case <-r.ctx.Done():
		return false
	}
}
func (r *playbackRecord) control(ctx context.Context, action string, frame *int64) (PlaybackStatus, error) {
	if action != "play" && action != "pause" && action != "seek" {
		return PlaybackStatus{}, errors.New("不支持的预览操作")
	}
	command := playbackCommand{action: action, frame: frame, ctx: ctx, reply: make(chan playbackReply, 1)}
	select {
	case r.commands <- command:
	case <-ctx.Done():
		return PlaybackStatus{}, ctx.Err()
	case <-r.ctx.Done():
		return r.snapshot(), errors.New("此预览已结束")
	}
	select {
	case reply := <-command.reply:
		return reply.status, reply.err
	case <-ctx.Done():
		return PlaybackStatus{}, ctx.Err()
	case <-r.ctx.Done():
		return r.snapshot(), errors.New("此预览已结束")
	}
}
func (r *playbackRecord) sendState() error {
	r.mu.Lock()
	connection := r.connection
	r.mu.Unlock()
	if connection == nil {
		return nil
	}
	_ = connection.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return connection.WriteJSON(map[string]any{"type": "state", "status": r.snapshot()})
}
func (r *playbackRecord) sendFrame(frame playback.Frame, playing bool) error {
	if playing && frame.PictureOnly {
		return errors.New("纯画面定位帧不能进入声音播放队列")
	}
	if err := validPlaybackFrame(frame, r.info); err != nil {
		return err
	}
	r.mu.Lock()
	connection := r.connection
	r.mu.Unlock()
	if connection == nil {
		return errors.New("预览数据连接尚未就绪")
	}
	r.sequence++
	header := playbackWireHeader{Frame: frame, Type: "frame", Epoch: r.snapshot().Epoch, Sequence: r.sequence, Playing: playing}
	metadata, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if len(metadata) > playback.MaxHeaderBytes {
		return errors.New("预览元数据超过限制")
	}
	var prefix [16]byte
	copy(prefix[:4], playback.Protocol)
	binary.BigEndian.PutUint32(prefix[4:8], uint32(len(metadata)))
	binary.BigEndian.PutUint32(prefix[8:12], uint32(len(frame.RGBA)))
	binary.BigEndian.PutUint32(prefix[12:16], uint32(len(frame.PCM)))
	_ = connection.SetWriteDeadline(time.Now().Add(3 * time.Second))
	writer, err := connection.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}
	for _, part := range [][]byte{prefix[:], metadata, frame.RGBA, frame.PCM} {
		if _, err = writer.Write(part); err != nil {
			_ = writer.Close()
			return err
		}
	}
	if err = writer.Close(); err != nil {
		return err
	}
	r.pending[r.sequence] = playbackPending{position: frame.Position, end: frame.End, playing: playing}
	r.update(func(status *PlaybackStatus) { status.InFlight = len(r.pending) })
	return nil
}
func (r *playbackRecord) fail(phase, reason string) {
	r.update(func(status *PlaybackStatus) { status.Status = phase; status.Reason = reason })
	_ = r.sendState()
}
func (r *playbackRecord) newEpoch() {
	r.sequence = 0
	r.lastAck = 0
	r.decodeEnded = false
	r.pending = map[uint64]playbackPending{}
	r.update(func(status *PlaybackStatus) { status.Epoch++; status.InFlight = 0 })
}

func (r *playbackRecord) handle(command playbackCommand) error {
	if command.ctx != nil && command.ctx.Err() != nil {
		return errors.Join(errPlaybackControlNotStarted, command.ctx.Err())
	}
	if !r.scopeValid() {
		r.fail("stale", "工程已更改，旧媒体图已停止；请先处理草稿并重新加载")
		return errors.New("预览工程已变化")
	}
	state := r.snapshot()
	switch command.action {
	case "connect":
		if err := r.sendState(); err != nil {
			return err
		}
		return r.sendFrame(r.initial, false)
	case "ack":
		if command.epoch != state.Epoch {
			return nil
		}
		pending, ok := r.pending[command.sequence]
		if !ok || command.sequence != r.lastAck+1 || command.position == nil || *command.position != pending.position {
			return errors.New("预览确认顺序或位置无效")
		}
		delete(r.pending, command.sequence)
		r.lastAck = command.sequence
		r.update(func(status *PlaybackStatus) {
			status.InFlight = len(r.pending)
			if pending.playing {
				status.Frame = min(pending.position+1, status.DurationFrames)
			}
			if pending.playing && pending.end {
				status.Status = "paused"
				status.Frame = status.DurationFrames
			}
		})
		if pending.playing && pending.end {
			return r.sendState()
		}
		return nil
	case "play", "pause", "seek":
		if !state.Connected {
			return errors.New("请先连接预览数据通道")
		}
		position := state.Frame
		if command.frame != nil {
			position = *command.frame
		}
		if position < 0 || position > state.DurationFrames {
			return errors.New("预览位置不在工程内")
		}
		if command.action == "seek" && command.frame == nil {
			return errors.New("定位需要明确帧位置")
		}
		if command.action == "play" && position == state.DurationFrames {
			position = 0
		}
		operationCtx, cancel := context.WithCancel(r.ctx)
		defer cancel()
		if command.ctx != nil {
			stop := context.AfterFunc(command.ctx, cancel)
			defer stop()
		}
		if err := r.native.Pause(operationCtx); err != nil {
			return err
		}
		frame, err := previewPlaybackFrame(r.native, operationCtx, min(position, state.DurationFrames-1))
		if err != nil {
			return err
		}
		if err := operationCtx.Err(); err != nil {
			return err
		}
		if frame.Position != min(position, state.DurationFrames-1) {
			return errors.New("原生媒体定位结果不匹配，已停止")
		}
		if !r.scopeValid() {
			r.fail("stale", "定位期间工程已更改，旧媒体画面已丢弃")
			return errors.New("预览工程已变化")
		}
		playing := command.action == "play" || command.action == "seek" && state.Status == "playing" && position < state.DurationFrames
		r.newEpoch()
		r.decodePosition, r.decodeSample = frame.Position, frame.AudioStartSample
		r.update(func(status *PlaybackStatus) {
			status.Frame = position
			status.Status = "paused"
			if playing {
				status.Status = "playing"
			}
			status.Reason = ""
		})
		if playing {
			if err := r.native.Play(operationCtx); err != nil {
				return err
			}
		}
		if err := r.sendState(); err != nil {
			return err
		}
		// Seek永不消耗声音；播放PCM随后仅由Next取得，免重复目标帧。
		return r.sendFrame(frame, false)
	case "close", "disconnect":
		return errors.New("预览连接已关闭")
	default:
		return errors.New("不支持的预览数据消息")
	}
}
func (r *playbackRecord) run() {
	defer r.cleanup()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	connectTimeout := time.NewTimer(20 * time.Second)
	defer connectTimeout.Stop()
	for {
		if r.ctx.Err() != nil {
			return
		}
		if !r.scopeValid() {
			r.fail("stale", "工程已更改，旧媒体图已停止；请先核对后重新加载")
			return
		}
		var command playbackCommand
		hasCommand := false
		select {
		case command = <-r.commands:
			hasCommand = true
		default:
		}
		state := r.snapshot()
		if !hasCommand && state.Status == "playing" && state.Connected && !r.decodeEnded && len(r.pending) < 3 {
			frame, err := r.native.Next(r.ctx)
			if err != nil {
				if r.ctx.Err() == nil {
					r.fail("failed", "原生媒体拉帧失败；播放已停止，没有用假时钟代替")
				}
				return
			}
			if !r.scopeValid() {
				r.fail("stale", "工程已更改，旧媒体帧已丢弃")
				return
			}
			if frame.Position != r.decodePosition || frame.AudioStartSample != r.decodeSample {
				r.fail("failed", "原生画面或音频样本不连续，旧数据已丢弃")
				return
			}
			if err := r.sendFrame(frame, true); err != nil {
				r.fail("failed", "预览二进制输出或音频数据未通过校验")
				return
			}
			r.decodePosition++
			r.decodeSample += int64(frame.Samples)
			if frame.End {
				r.decodeEnded = true
			}
			continue
		}
		if !hasCommand {
			select {
			case command = <-r.commands:
				hasCommand = true
			case <-tick.C:
				continue
			case <-connectTimeout.C:
				if !r.snapshot().Connected {
					r.fail("failed", "预览数据通道未连接，已释放媒体进程")
					return
				}
				continue
			case <-r.ctx.Done():
				return
			}
		}
		if hasCommand {
			err := r.handle(command)
			if command.reply != nil {
				command.reply <- playbackReply{status: r.snapshot(), err: err}
			}
			if err != nil {
				if command.action == "disconnect" || command.action == "close" {
					return
				}
				if errors.Is(err, errPlaybackControlNotStarted) {
					continue
				}
				if r.snapshot().Status != "stale" {
					r.fail("failed", "预览控制或确认协议失败；会话已停止")
				}
				return
			}
		}
	}
}
func (r *playbackRecord) cleanup() {
	r.cleanupOnce.Do(func() {
		r.cancel()
		r.mu.Lock()
		r.closed = true
		connection := r.connection
		r.status.Connected = false
		r.status.InFlight = 0
		if r.status.Status != "stale" && r.status.Status != "failed" {
			r.status.Status = "closed"
		}
		r.mu.Unlock()
		_ = r.server.Close()
		if connection != nil {
			_ = connection.Close()
		}
		_ = r.native.Close()
		// Close返回错误或超时不代表子进程已退出。保留record，阻止替代会话占用第二份媒体资源。
		<-r.native.Done()
		<-r.serveDone
		r.handlers.Wait()
		r.service.mu.Lock()
		if evidence := r.service.playbackEvidence.Load(); evidence != nil && evidence.engine == r.engine && evidence.revision == r.info.ProjectRevision {
			r.service.playbackEvidence.CompareAndSwap(evidence, nil)
		}
		r.engine = nil
		r.service.mu.Unlock()
		close(r.done)
	})
}
func (r *playbackRecord) closeUnstarted() error { return r.close() }
func (r *playbackRecord) close() error {
	r.cancel()
	go r.cleanup()
	select {
	case <-r.done:
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("媒体进程仍在释放，未启动重复实例")
	}
}
