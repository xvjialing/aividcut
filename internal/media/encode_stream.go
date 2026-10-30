package media

import (
	"aividcut/internal/managedprocess"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"aividcut/internal/editor"
)

// RawEncoder只接受一个受控原始输入，写入受子进程背压约束，不排队缓存整片。
type RawEncoder struct {
	ctx     context.Context
	cancel  context.CancelFunc
	command *managedprocess.Cmd
	input   io.WriteCloser
	stderr  tailBuffer
	once    sync.Once
	err     error
}

func (t Toolchain) startRawEncoder(ctx context.Context, args []string) (*RawEncoder, error) {
	if t.FFmpegPath == "" {
		return nil, errors.New("未配置FFmpeg编码工具")
	}
	child, cancel := context.WithCancel(ctx)
	value := &RawEncoder{ctx: child, cancel: cancel}
	value.command = managedprocess.CommandContext(child, t.FFmpegPath, append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-threads", "1", "-filter_threads", "1"}, args...)...)
	value.command.Stderr = &value.stderr
	value.command.WaitDelay = 2 * time.Second
	input, err := value.command.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	value.input = input
	if err = value.command.Start(); err != nil {
		input.Close()
		cancel()
		return nil, errors.New("无法启动导出编码器")
	}
	return value, nil
}
func (e *RawEncoder) Write(data []byte) error {
	if err := e.ctx.Err(); err != nil {
		return err
	}
	for len(data) > 0 {
		n, err := e.input.Write(data)
		if err != nil {
			if e.ctx.Err() != nil {
				return e.ctx.Err()
			}
			if encodingErr := e.Finish(); encodingErr != nil {
				return encodingErr
			}
			return fmt.Errorf("导出编码输入失败：%w", err)
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
func (e *RawEncoder) Finish() error {
	e.once.Do(func() {
		defer e.cancel()
		e.input.Close()
		e.err = e.command.Wait()
		if e.ctx.Err() != nil {
			e.err = e.ctx.Err()
		} else if e.err != nil {
			e.err = fmt.Errorf("媒体编码失败：%s", e.stderr.String())
		}
	})
	return e.err
}
func (e *RawEncoder) Abort() { e.cancel(); _ = e.Finish() }

func encoderArgs(name string) ([]string, error) {
	switch name {
	case "", "libx264":
		return []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20"}, nil
	case "h264_videotoolbox":
		return []string{"-c:v", name, "-b:v", "8M"}, nil
	case "mpeg4":
		return []string{"-c:v", name, "-q:v", "3"}, nil
	default:
		return nil, errors.New("未支持的导出编码器")
	}
}
func (t Toolchain) StartRawVideo(ctx context.Context, settings editor.Settings, outputWidth, outputHeight int, frames int64, encoder, path string, resourceMode ...string) (*RawEncoder, error) {
	mode := ""
	if len(resourceMode) > 1 {
		return nil, errors.New("导出只能选择一个编码资源档位")
	}
	if len(resourceMode) == 1 {
		mode = resourceMode[0]
	}
	resources, err := EncodingResourceArgs(encoder, mode)
	if err != nil {
		return nil, err
	}
	codec, err := encoderArgs(encoder)
	if err != nil {
		return nil, err
	}
	if settings.Width < 1 || settings.Height < 1 || settings.FPSNum < 1 || settings.FPSDen < 1 || outputWidth < 2 || outputHeight < 2 || outputWidth > 8192 || outputHeight > 8192 || outputWidth%2 != 0 || outputHeight%2 != 0 || frames < 1 {
		return nil, errors.New("原始画面编码尺寸或时间无效")
	}
	args := []string{"-f", "rawvideo", "-pixel_format", "rgba", "-video_size", fmt.Sprintf("%dx%d", settings.Width, settings.Height), "-framerate", fmt.Sprintf("%d/%d", settings.FPSNum, settings.FPSDen), "-i", "pipe:0", "-an", "-vf", fmt.Sprintf("scale=%d:%d:flags=bicubic,setsar=1", outputWidth, outputHeight)}
	args = append(args, codec...)
	args = append(args, "-threads", "2")
	args = append(args, resources...)
	args = append(args, "-pix_fmt", "yuv420p", "-frames:v", strconv.FormatInt(frames, 10), "-f", "mp4", path)
	return t.startRawEncoder(ctx, args)
}
func (t Toolchain) StartRawAudio(ctx context.Context, inputRate, outputRate int, samples int64, path string) (*RawEncoder, error) {
	if inputRate < 8000 || inputRate > 192000 || outputRate < 8000 || outputRate > 192000 || samples < 1 {
		return nil, errors.New("音频编码采样率或实际区间无效")
	}
	filter := fmt.Sprintf("aresample=%d,apad=whole_len=%d,atrim=end_sample=%d,asetpts=PTS-STARTPTS", outputRate, samples, samples)
	return t.startRawEncoder(ctx, []string{"-f", "f32le", "-ar", strconv.Itoa(inputRate), "-ac", "2", "-i", "pipe:0", "-vn", "-af", filter, "-ar", strconv.Itoa(outputRate), "-c:a", "aac", "-b:a", "192k", "-threads", "1", "-movie_timescale", strconv.Itoa(outputRate), "-f", "ipod", path})
}
func (t Toolchain) MuxEncoded(ctx context.Context, video, audio, output string, sampleRate int) error {
	command := managedprocess.CommandContext(ctx, t.FFmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-protocol_whitelist", "file,pipe", "-i", video, "-i", audio, "-map", "0:v:0", "-map", "1:a:0", "-c", "copy", "-movie_timescale", strconv.Itoa(sampleRate), "-movflags", "+faststart", "-f", "mp4", output)
	command.WaitDelay = 2 * time.Second
	var stderr tailBuffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("封装导出音画失败：%s", stderr.String())
	}
	return nil
}

// OutputSize与原FFmpeg合成共用预设，工程合成坐标不随输出预设变化。
func OutputSize(width, height int, preset string) (int, int) {
	return outputSize(width, height, preset)
}

// CommitOutput复用原有跨平台无覆盖、原子替换和提交点取消语义。
func CommitOutput(ctx context.Context, temporary, output string, overwrite bool) error {
	return commitOutput(ctx, temporary, output, overwrite, defaultCommitOperations())
}
