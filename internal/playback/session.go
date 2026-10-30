package playback

import (
	"aividcut/internal/managedprocess"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aividcut/internal/editor"
	"aividcut/internal/projectfonts"
)

type Session struct {
	mu                               sync.Mutex
	config                           Config
	command                          *managedprocess.Cmd
	stdin                            *os.File
	stdout                           *os.File
	cancel                           context.CancelFunc
	done                             chan struct{}
	sequence                         uint64
	info                             Info
	capabilities                     Capabilities
	closed                           bool
	closeOnce                        sync.Once
	imageBuffer, audioBuffer         []byte
	plan                             *graphPlan
	graphFrom, graphTo, nextPosition int64
}

func Start(ctx context.Context, config Config) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.OfflineRender && len(config.VisualSources) > 0 {
		return nil, errors.New("离线导出不允许画面代理")
	}
	copies := map[string]VisualSource{}
	for id, value := range config.VisualSources {
		copies[id] = value
	}
	config.VisualSources = copies
	qualityScale, err := QualityScale(config.RenderQuality)
	if err != nil {
		return nil, err
	}
	if config.OfflineRender && qualityScale != 1 {
		return nil, errors.New("离线导出只允许全精度合成")
	}
	if config.WorkerPath == "" {
		return nil, errors.New("尚未配置AIVidCut原生MLT worker")
	}
	path, err := filepath.Abs(config.WorkerPath)
	if err != nil {
		return nil, errors.New("worker路径无效")
	}
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() {
		return nil, errors.New("MLT worker不存在或不是普通文件")
	}
	if config.MaxWidth == 0 {
		config.MaxWidth = 1280
	}
	if config.WindowClipLimit < 0 || config.WindowClipLimit > 128 || config.WindowFrames < 0 || config.WindowFrames > 7200 {
		return nil, errors.New("原生图窗口预算无效")
	}
	if config.MaxHeight == 0 {
		config.MaxHeight = 720
	}
	if config.OfflineRender {
		config.MaxWidth, config.MaxHeight = 8192, 8192
	}
	if !config.OfflineRender && (config.MaxWidth < 16 || config.MaxWidth > 1920 || config.MaxHeight < 16 || config.MaxHeight > 1080) {
		return nil, errors.New("预览输出需在16–1920/1080范围内")
	}
	if config.Timeout <= 0 {
		config.Timeout = 15 * time.Second
	}
	if config.Timeout > 2*time.Minute {
		return nil, errors.New("单次原生拉帧超时不能超过2分钟")
	}
	processCtx, cancel := context.WithCancel(ctx)
	command := managedprocess.CommandContext(processCtx, path)
	command.Env, err = workerEnvironment(path)
	if err != nil {
		cancel()
		return nil, errors.New("无法确定原生媒体进程的系统目录")
	}
	command.Env = append(command.Env, "QT_QPA_PLATFORM=offscreen", "LC_ALL=C", "MLT_AVFORMAT_PRODUCER_CACHE=8", "MLT_AVFORMAT_CACHE=2")
	if config.MLTRepository != "" {
		command.Env = append(command.Env, "MLT_REPOSITORY="+config.MLTRepository)
	}
	if config.MLTProfilesPath != "" {
		command.Env = append(command.Env, "MLT_PROFILES_PATH="+config.MLTProfilesPath)
	}
	if config.MLTDataPath != "" {
		command.Env = append(command.Env, "MLT_DATA="+config.MLTDataPath)
	}
	inRead, inWrite, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, err
	}
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		inRead.Close()
		inWrite.Close()
		cancel()
		return nil, err
	}
	command.Stdin, command.Stdout, command.Stderr = inRead, outWrite, &boundedStderr{}
	if err := command.Start(); err != nil {
		inRead.Close()
		inWrite.Close()
		outRead.Close()
		outWrite.Close()
		cancel()
		return nil, errors.New("无法启动原生MLT进程")
	}
	inRead.Close()
	outWrite.Close()
	s := &Session{config: config, command: command, stdin: inWrite, stdout: outRead, cancel: cancel, done: make(chan struct{})}
	go func() { _ = command.Wait(); close(s.done) }()
	s.mu.Lock()
	reply, err := s.callLocked(ctx, map[string]any{"op": "inspect"})
	if err == nil && reply.Capabilities != nil {
		s.capabilities = *reply.Capabilities
	} else if err == nil {
		err = errors.New("原生进程未返回插件能力")
	}
	s.mu.Unlock()
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func Inspect(ctx context.Context, config Config) (Capabilities, error) {
	s, err := Start(ctx, config)
	if err != nil {
		return Capabilities{Reason: err.Error(), Protocol: Protocol, Display: "webview_binary_canvas_audio"}, err
	}
	defer s.Close()
	return s.Capabilities(), nil
}

// CheckProject包含真实字体测量与原生建图，不以静态插件列表代替工程可用性。
func CheckProject(ctx context.Context, config Config, project editor.Project) error {
	session, err := Start(ctx, config)
	if err != nil {
		return err
	}
	defer session.Close()
	_, err = session.Load(ctx, project)
	return err
}
func (s *Session) Capabilities() Capabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.capabilities
	value.Producers = append([]string{}, value.Producers...)
	value.Filters = append([]string{}, value.Filters...)
	value.Transitions = append([]string{}, value.Transitions...)
	return value
}
func (s *Session) Info() Info            { s.mu.Lock(); defer s.mu.Unlock(); return s.info }
func (s *Session) Done() <-chan struct{} { return s.done }

// ProcessID仅供内部只读性能诊断；不进入App、WebSocket或MCP接口。
func (s *Session) ProcessID() int {
	if s.command == nil || s.command.Process == nil {
		return 0
	}
	return s.command.Process.Pid
}

func (s *Session) callLocked(ctx context.Context, request map[string]any) (packet, error) {
	var empty packet
	if s.closed {
		return empty, errors.New("原生播放会话已关闭")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	s.sequence++
	id := s.sequence
	request["requestId"] = id
	data, err := json.Marshal(request)
	if err != nil || len(data) > maxCommandBytes {
		return empty, errors.New("原生播放控制命令超过4MiB或编码失败")
	}
	data = append(data, '\n')
	callCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	type response struct {
		value packet
		err   error
	}
	result := make(chan response, 1)
	go func() {
		_, err := s.stdin.Write(data)
		var reply packet
		if err == nil {
			reply, err = readPacketMode(s.stdout, &s.imageBuffer, &s.audioBuffer, s.config.OfflineRender)
		}
		result <- response{reply, err}
	}()
	select {
	case received := <-result:
		if err := callCtx.Err(); err != nil {
			s.closed = true
			s.cancel()
			return empty, err
		}
		if received.err != nil {
			s.closed = true
			s.cancel()
			return empty, errors.New("原生媒体进程结束或协议读取失败")
		}
		if received.value.RequestID != id {
			s.closed = true
			s.cancel()
			return empty, errors.New("原生媒体响应序号不匹配")
		}
		if received.value.Error != "" {
			return received.value, errors.New(received.value.Error)
		}
		return received.value, nil
	case <-callCtx.Done():
		s.closed = true
		s.cancel()
		s.stdin.Close()
		s.stdout.Close()
		<-result
		return empty, callCtx.Err()
	}
}

func (s *Session) Load(ctx context.Context, project editor.Project) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.OfflineRender && !s.capabilities.OfflineRender.Supported {
		return Info{}, errors.New("当前worker不支持原画幅离线导出，请更新原生媒体工具")
	}
	if err := editor.ValidateProject(project); err != nil {
		return Info{}, err
	}
	if !s.capabilities.FontFiles.Supported {
		for _, c := range project.Clips {
			if c.TextStyle.FontAssetID != "" {
				return Info{}, errors.New("当前worker不支持工程字体，请更新原生媒体工具")
			}
		}
	}
	project = editor.CloneProject(project)
	fonts, err := projectfonts.Resolve(project)
	if err != nil {
		return Info{}, err
	}
	for i, a := range project.Assets {
		if f, ok := fonts[a.ID]; ok {
			if !s.capabilities.FontFiles.Supported {
				return Info{}, errors.New("当前worker不支持工程字体，请更新原生媒体工具")
			}
			reply, err := s.callLocked(ctx, map[string]any{"op": "load_font", "path": f.Path, "sha256": f.SHA256})
			if err != nil {
				return Info{}, err
			}
			if reply.Type != "font_loaded" || reply.Font == nil || reply.Font.Family == "" || reply.Font.Weight < 1 || reply.Font.Weight > 1000 {
				return Info{}, errors.New("原生进程未返回有效字体核对结果")
			}
			a.Font.Family, a.Font.Style, a.Font.Weight, a.Font.Italic = reply.Font.Family, reply.Font.Style, reply.Font.Weight, reply.Font.Italic
			project.Assets[i] = a
		}
	}
	var plan *graphPlan
	var selected graphRange
	var xml string
	var duration int64
	windowed := len(project.Clips) > 128 || s.config.WindowClipLimit > 0 || s.config.WindowFrames > 0
	if windowed {
		plan, err = prepareGraphPlan(project, s.capabilities, s.config.WindowClipLimit, s.config.WindowFrames)
		if err == nil {
			selected, err = plan.window(0)
		}
		if err == nil {
			xml, duration, err = compileSelection(plan.project, selected.clips, s.capabilities, plan.duration, false)
		}
	} else {
		xml, duration, err = compileProject(project, s.capabilities)
	}
	if err != nil {
		return Info{}, err
	}
	if err := s.measureProjectText(ctx, project, 1); err != nil {
		return Info{}, err
	}
	layoutScale, _ := QualityScale(s.config.RenderQuality)
	if layoutScale != 1 {
		project, err = renderProject(project, layoutScale)
		if err != nil {
			return Info{}, err
		}
		if err := s.measureProjectText(ctx, project, layoutScale); err != nil {
			return Info{}, err
		}
		if windowed {
			plan, err = prepareScaledGraphPlan(project, s.capabilities, s.config.WindowClipLimit, s.config.WindowFrames, layoutScale)
			if err == nil {
				selected, err = plan.window(0)
			}
			if err == nil {
				xml, duration, err = compileScaledSelection(plan.project, selected.clips, s.capabilities, plan.duration, false, layoutScale)
			}
		} else {
			xml, duration, err = compileScaledSelection(project, project.Clips, s.capabilities, 0, false, layoutScale)
		}
		if err != nil {
			return Info{}, err
		}
	}
	visuals, err := validateVisualSources(project, s.config.VisualSources, s.capabilities)
	if err != nil {
		return Info{}, err
	}
	if len(visuals) > 0 {
		if plan != nil {
			plan.visualSources = visuals
			xml, duration, err = compileScaledSelection(plan.project, selected.clips, s.capabilities, plan.duration, false, layoutScale, visuals)
		} else {
			xml, duration, err = compileScaledSelection(project, project.Clips, s.capabilities, 0, false, layoutScale, visuals)
		}
		if err != nil {
			return Info{}, err
		}
	}
	request := map[string]any{"op": "load", "xml": xml, "width": project.Settings.Width, "height": project.Settings.Height, "fpsNum": project.Settings.FPSNum, "fpsDen": project.Settings.FPSDen, "sampleRate": project.Settings.SampleRate, "durationFrames": duration, "maxWidth": s.config.MaxWidth, "maxHeight": s.config.MaxHeight, "renderMode": sessionRenderMode(s.config)}
	used := map[string]bool{}
	for _, clip := range project.Clips {
		if clip.AssetID != "" {
			used[clip.AssetID] = true
		}
	}
	var sources strings.Builder
	for _, asset := range project.Assets {
		if used[asset.ID] && asset.Kind != "lut" {
			kind := asset.Kind
			if kind == "video" && asset.HasAudio {
				kind = "video_audio"
			}
			sources.WriteString(kind + "\t" + asset.Path + "\n")
		}
	}
	request["sources"] = sources.String()
	reply, err := s.callLocked(ctx, request)
	if err != nil {
		return Info{}, err
	}
	if reply.Info == nil {
		return Info{}, errors.New("原生进程未确认工程图")
	}
	s.info = *reply.Info
	s.info.ProjectID, s.info.ProjectRevision = project.ID, project.Revision
	s.plan = plan
	s.graphFrom, s.graphTo = selected.from, selected.to
	s.nextPosition = 0
	return s.info, nil
}
func (s *Session) Play(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.callLocked(ctx, map[string]any{"op": "play"})
	if err == nil {
		s.info.Playing = true
	}
	return err
}
func (s *Session) Pause(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.callLocked(ctx, map[string]any{"op": "pause"})
	if err == nil {
		s.info.Playing = false
	}
	return err
}
func (s *Session) Seek(ctx context.Context, position int64) (Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frameLocked(ctx, "seek", position)
}

// Preview只定位画面；声音在后续Next中准备，旧worker仍返回完整真实Seek。
func (s *Session) Preview(ctx context.Context, position int64) (Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation := "preview"
	if !s.capabilities.PictureSeek.Supported {
		operation = "seek"
	}
	return s.frameLocked(ctx, operation, position)
}
func (s *Session) Next(ctx context.Context) (Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frameLocked(ctx, "next", 0)
}
func (s *Session) frameLocked(ctx context.Context, op string, position int64) (Frame, error) {
	at := position
	if op == "next" {
		at = s.nextPosition
	}
	if s.plan != nil && (op != "next" || s.info.Playing) && (at < s.graphFrom || at >= s.graphTo) {
		if err := s.replaceGraphLocked(ctx, at); err != nil {
			return Frame{}, err
		}
	}
	reply, err := s.callLocked(ctx, map[string]any{"op": op, "position": position})
	if err != nil {
		return Frame{}, err
	}
	if reply.Type != "frame" {
		return Frame{}, errors.New("原生进程没有返回媒体帧")
	}
	if op == "preview" && (!reply.PictureOnly || reply.Samples != 0 || len(reply.PCM) != 0) || op != "preview" && (reply.PictureOnly || reply.Samples < 1) {
		return Frame{}, errors.New("原生返回的画面/声音范围与请求不一致")
	}
	s.info.Position = reply.Position
	s.nextPosition = reply.Position
	if op == "next" {
		s.nextPosition = min(reply.Position+1, s.info.DurationFrames-1)
	}
	if reply.End && op == "next" {
		s.info.Playing = false
	}
	return reply.Frame, nil
}

func (s *Session) replaceGraphLocked(ctx context.Context, at int64) (err error) {
	selected, err := s.plan.window(at)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			s.closed = true
			s.cancel()
		}
	}()
	xml, _, err := compileScaledSelection(s.plan.project, selected.clips, s.capabilities, s.plan.duration, false, s.plan.layoutScale, s.plan.visualSources)
	if err != nil {
		return err
	}
	p := s.plan.project
	reply, err := s.callLocked(ctx, map[string]any{"op": "load_window", "xml": xml, "width": p.Settings.Width, "height": p.Settings.Height, "fpsNum": p.Settings.FPSNum, "fpsDen": p.Settings.FPSDen, "sampleRate": p.Settings.SampleRate, "durationFrames": s.plan.duration, "maxWidth": s.config.MaxWidth, "maxHeight": s.config.MaxHeight, "renderMode": sessionRenderMode(s.config)})
	if err != nil {
		return err
	}
	i := reply.Info
	if reply.Type != "loaded" || i == nil || i.Position != s.nextPosition || i.Playing != s.info.Playing || i.DurationFrames != s.info.DurationFrames || i.Width != s.info.Width || i.Height != s.info.Height || i.FPSNum != s.info.FPSNum || i.FPSDen != s.info.FPSDen || i.SampleRate != s.info.SampleRate || i.Channels != s.info.Channels {
		s.closed = true
		s.cancel()
		return errors.New("原生图替换改变全局游标或输出时钟，已停止会话")
	}
	s.graphFrom, s.graphTo = selected.from, selected.to
	return nil
}
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		if s.mu.TryLock() {
			if !s.closed {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_, _ = s.callLocked(ctx, map[string]any{"op": "close"})
				cancel()
			}
			s.closed = true
			s.cancel()
			s.stdin.Close()
			s.stdout.Close()
			s.mu.Unlock()
		} else {
			// 先取消进程和管道，不能等持锁的阻塞pull自行用尽15秒/2分钟预算。
			s.cancel()
			s.stdin.Close()
			s.stdout.Close()
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
		}
	})
	select {
	case <-s.done:
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("原生媒体进程尚未退出")
	}
}

type boundedStderr struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedStderr) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const limit = 32 << 10
	if len(data) >= limit {
		b.data = append(b.data[:0], data[len(data)-limit:]...)
	} else {
		b.data = append(b.data, data...)
		if len(b.data) > limit {
			b.data = append([]byte{}, b.data[len(b.data)-limit:]...)
		}
	}
	return len(data), nil
}

var _ io.Writer = (*boundedStderr)(nil)

func sessionRenderMode(config Config) string {
	if config.OfflineRender {
		return "offline"
	}
	return "preview"
}

func (s *Session) measureProjectText(ctx context.Context, project editor.Project, scale float64) error {
	for _, clip := range project.Clips {
		if clip.Kind != "text" && clip.Kind != "caption" {
			continue
		}
		if utf8.RuneCountInString(clip.Text) > 10000 {
			return errors.New("当前实验原生文字单片段不超过10000字符；此为资源上限，不用字符数推算字形")
		}
		pixels, err := scaledTextFontPixels(clip, scale)
		if err != nil {
			return err
		}
		command := map[string]any{"op": "measure_text", "text": clip.Text, "fontPixels": pixels}
		if clip.TextStyle.FontAssetID != "" {
			for _, asset := range project.Assets {
				if asset.ID == clip.TextStyle.FontAssetID {
					command["fontKey"] = strings.TrimPrefix(asset.Fingerprint, "sha256:")
					break
				}
			}
		}
		reply, err := s.callLocked(ctx, command)
		if err != nil {
			return err
		}
		if reply.Measurement == nil || reply.Measurement.Width < 1 || reply.Measurement.Height < 1 {
			return errors.New("原生进程没有返回实际字形边界")
		}
		if reply.Measurement.Width > project.Settings.Width-int(math.Round(64*scale)) || reply.Measurement.Height > project.Settings.Height {
			if scale != 1 {
				return errors.New("此预览精度下文字无法保持完整，请提高精度；原文字未修改")
			}
			return errors.New("文字实际字形超出当前qtext框；为避免自动缩小字号，原生预览拒绝此片段，请调整文字或排版")
		}
	}
	return nil
}
