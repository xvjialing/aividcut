package media

import (
	"aividcut/internal/managedprocess"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Toolchain 只引用外部工具，不将其他应用内的二进制打包进发行物。
type Toolchain struct {
	FFmpegPath  string `json:"ffmpegPath"`
	FFprobePath string `json:"ffprobePath"`
}

type ExportOptions struct {
	ResourceMode string `json:"resourceMode,omitempty"`
	Renderer     string `json:"renderer,omitempty"`
	Path         string `json:"path"`
	Preset       string `json:"preset,omitempty"`
	FromFrame    int64  `json:"fromFrame,omitempty"`
	ToFrame      int64  `json:"toFrame,omitempty"`
	Overwrite    bool   `json:"overwrite,omitempty"`
	VideoEncoder string `json:"videoEncoder,omitempty"`
	FontPath     string `json:"fontPath,omitempty"`
}

type Progress struct {
	Fraction float64 `json:"fraction"`
	Stage    string  `json:"stage"`
	Frame    int64   `json:"frame,omitempty"`
}

type LUTInfo struct {
	Title     string     `json:"title"`
	Size      int        `json:"size"`
	Entries   int        `json:"entries"`
	DomainMin [3]float64 `json:"domainMin"`
	DomainMax [3]float64 `json:"domainMax"`
}

// Discover 优先使用用户显式配置，其次使用 PATH；不会自动依赖竞品安装目录。
func Discover() Toolchain {
	return DiscoverConfigured(os.Getenv("AIVIDCUT_FFMPEG"), os.Getenv("AIVIDCUT_FFPROBE"))
}

// DiscoverConfigured 对明确候选不读取环境覆盖；空值沿PATH。
func DiscoverConfigured(ffmpeg, ffprobe string) Toolchain {
	return Toolchain{FFmpegPath: executable(ffmpeg, "ffmpeg"), FFprobePath: executable(ffprobe, "ffprobe")}
}

// DiscoverExplicit 不回落PATH，供已发现但校验失败的随包运行时保留明确失败状态。
func DiscoverExplicit(ffmpeg, ffprobe string) Toolchain {
	var t Toolchain
	if ffmpeg != "" {
		t.FFmpegPath = executable(ffmpeg, "ffmpeg")
	}
	if ffprobe != "" {
		t.FFprobePath = executable(ffprobe, "ffprobe")
	}
	return t
}

func executable(configured, name string) string {
	if configured != "" {
		if path, err := exec.LookPath(configured); err == nil {
			absolute, _ := filepath.Abs(path)
			return absolute
		}
		return ""
	}
	if path, err := exec.LookPath(name); err == nil {
		absolute, _ := filepath.Abs(path)
		return absolute
	}
	return ""
}

// DefaultFont 返回可供开发验证的系统字体，正式发行需另行验证字体与排版一致性。
func DefaultFont() string {
	if p := os.Getenv("AIVIDCUT_FONT"); p != "" {
		return p
	}
	for _, p := range []string{
		"/System/Library/Fonts/PingFang.ttc",
		"/System/Library/Fonts/STHeiti Light.ttc",
		"/System/Library/Fonts/Supplemental/Arial.ttf",
		filepath.Join(os.Getenv("WINDIR"), "Fonts", "msyh.ttc"),
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	} {
		if s, err := os.Stat(p); err == nil && !s.IsDir() {
			return p
		}
	}
	return ""
}

func runOutput(ctx context.Context, binary string, args ...string) ([]byte, error) {
	command := managedprocess.CommandContext(ctx, binary, args...)
	command.WaitDelay = 2 * time.Second
	var output limitedBuffer
	var stderr tailBuffer
	command.Stdout, command.Stderr = &output, &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %s", err, stderr.String())
	}
	return output.data, nil
}

type limitedBuffer struct{ data []byte }

func (b *limitedBuffer) Write(data []byte) (int, error) {
	const limit = 4 << 20
	if len(data) > limit-len(b.data) {
		return 0, errors.New("媒体工具输出超过 4 MiB 限制")
	}
	b.data = append(b.data, data...)
	return len(data), nil
}
