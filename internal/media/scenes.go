package media

import (
	"aividcut/internal/managedprocess"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SceneScan保留FFmpeg实际输出的源时间覆盖，不用容器/音频时长补齐视频。
type SceneScan struct {
	StartUs, EndUs, Frames int64
	Cuts                   []int64
}

var sceneFirstTime = regexp.MustCompile(`\bn:\s*0\s+pts:\s*[-0-9]+\s+pts_time:([-.0-9e+]+)`)

type sceneStream struct {
	mu          sync.Mutex
	pending     [2][]byte
	total       int
	first       *int64
	end, frames int64
	cuts        []int64
	finished    bool
	err         error
	cancel      context.CancelFunc
	progress    func(int64)
}
type sceneWriter struct {
	state   *sceneStream
	channel int
}

func (w sceneWriter) Write(data []byte) (int, error) {
	s := w.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	if len(data) > 4<<20-s.total {
		s.err = errors.New("镜头检测日志超过4MiB预算")
		s.cancel()
		return 0, s.err
	}
	s.total += len(data)
	buffer := append(s.pending[w.channel], data...)
	for {
		at := bytes.IndexByte(buffer, '\n')
		if at < 0 {
			break
		}
		line := string(bytes.TrimSpace(buffer[:at]))
		buffer = buffer[at+1:]
		if len(line) > 16<<10 {
			s.err = errors.New("镜头检测单行超过预算")
			s.cancel()
			return 0, s.err
		}
		if err := s.line(line, w.channel); err != nil {
			s.err = err
			s.cancel()
			return 0, err
		}
	}
	if len(buffer) > 16<<10 {
		s.err = errors.New("镜头检测残行超过预算")
		s.cancel()
		return 0, s.err
	}
	s.pending[w.channel] = append([]byte{}, buffer...)
	return len(data), nil
}
func sceneSeconds(raw string) (int64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 86400 {
		return 0, errors.New("镜头源时间无效")
	}
	return int64(math.Round(value * 1e6)), nil
}
func (s *sceneStream) line(line string, channel int) error {
	if channel == 1 {
		if match := sceneFirstTime.FindStringSubmatch(line); len(match) == 2 {
			value, err := sceneSeconds(match[1])
			if err != nil {
				return err
			}
			s.first = &value
		}
		return nil
	}
	if strings.HasPrefix(line, "lavfi.scd.time=") {
		value, err := sceneSeconds(strings.TrimPrefix(line, "lavfi.scd.time="))
		if err != nil {
			return err
		}
		if len(s.cuts) >= 1999 {
			return errors.New("镜头候选过多，超过2000段上限，请提高阈值或拆分素材")
		}
		s.cuts = append(s.cuts, value)
	}
	if strings.HasPrefix(line, "out_time_us=") {
		value, err := strconv.ParseInt(strings.TrimPrefix(line, "out_time_us="), 10, 64)
		if err != nil || value < 0 || value > 86400_000_000 {
			return errors.New("解码进度时间无效")
		}
		s.end = max(s.end, value)
		if s.progress != nil {
			s.progress(value)
		}
	}
	if strings.HasPrefix(line, "frame=") {
		value, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "frame=")), 10, 64)
		if err != nil || value < 0 {
			return errors.New("解码帧计数无效")
		}
		s.frames = max(s.frames, value)
	}
	if line == "progress=end" {
		s.finished = true
	}
	return nil
}
func (t Toolchain) DetectScenes(ctx context.Context, path string, threshold int, progress func(int64)) (SceneScan, error) {
	if t.FFmpegPath == "" || !filepath.IsAbs(path) || threshold < 1 || threshold > 50 {
		return SceneScan{}, errors.New("本地镜头检测需要FFmpeg、登记源路径与有效阈值")
	}
	ctx, timeout := context.WithTimeout(ctx, 30*time.Minute)
	defer timeout()
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	stream := &sceneStream{cancel: cancel, progress: progress, cuts: []int64{}}
	filter := fmt.Sprintf("[0:v:0]scale=w='max(2,trunc(min(320,180*dar)/2)*2)':h='max(2,trunc(min(180,320/dar)/2)*2)',setsar=1,format=yuv420p,split=2[detect][first];[first]select='eq(n,0)',showinfo=checksum=0,nullsink;[detect]scdet=threshold=%d,metadata=mode=print:key=lavfi.scd.time:file='pipe\\:1'[out]", threshold)
	args := []string{"-hide_banner", "-loglevel", "info", "-nostdin", "-xerror", "-threads", "1", "-filter_threads", "1", "-filter_complex_threads", "1", "-protocol_whitelist", "file,pipe", "-format_whitelist", speechInputFormats, "-i", path, "-filter_complex", filter, "-map", "[out]", "-an", "-sn", "-dn", "-fps_mode", "passthrough", "-progress", "pipe:1", "-nostats", "-f", "null", "-"}
	command := managedprocess.CommandContext(child, t.FFmpegPath, args...)
	command.Stdout = sceneWriter{state: stream}
	command.Stderr = sceneWriter{state: stream, channel: 1}
	command.WaitDelay = 2 * time.Second
	err := command.Run()
	if stream.err != nil {
		return SceneScan{}, stream.err
	}
	if ctx.Err() != nil {
		return SceneScan{}, ctx.Err()
	}
	if err != nil {
		return SceneScan{}, errors.New("本地镜头检测失败，请检查FFmpeg滤镜和素材，不会返回伪造边界")
	}
	if len(bytes.TrimSpace(stream.pending[0])) > 0 || len(bytes.TrimSpace(stream.pending[1])) > 0 || stream.first == nil || !stream.finished || stream.end <= *stream.first || stream.frames == 0 {
		return SceneScan{}, errors.New("镜头检测缺少完整解码覆盖或结束回执")
	}
	return SceneScan{StartUs: *stream.first, EndUs: stream.end, Frames: stream.frames, Cuts: append([]int64{}, stream.cuts...)}, nil
}
