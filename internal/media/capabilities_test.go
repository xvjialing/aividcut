package media

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const basicFilters = `Filters:
 ... color |->V source
 ... format V->V format
 ... trim V->V trim
 ... setpts V->V pts
 ... fps V->V fps
 ... scale V->V scale
 ... setsar V->V sar
 ... overlay VV->V overlay
 ... anullsrc |->A silence
 ... atrim A->A trim
 ... asetpts A->A pts
 ... aresample A->A sample
 ... adelay A->A delay
 ... apad A->A pad
 ... amix N->A mix
 ... volume A->A volume
 ... atempo A->A tempo
 ... lut3d V->V lut
 ... split V->N split
 ... blend VV->V blend
 ... drawtext_fake V->V not-real-drawtext
`

func fakeCapabilitiesRunner(_ context.Context, binary string, args ...string) ([]byte, error) {
	if args[0] == "-version" {
		return []byte(binary + " version test\n"), nil
	}
	if args[1] == "-filters" {
		return []byte(basicFilters), nil
	}
	return []byte("Encoders:\n V..... libx264 h264\n V..... png png\n A..... aac audio\n V..... = Video\n"), nil
}

func TestCapabilitiesChecksExactFiltersAndEncoders(t *testing.T) {
	report := (Toolchain{FFmpegPath: "ffmpeg", FFprobePath: "ffprobe"}).checkCapabilities(context.Background(), fakeCapabilitiesRunner)
	if !report.FFmpeg.Available || !report.FFprobe.Available || !report.PreviewFrame.Available || !report.ExportVideo.Available || !report.LUT3D.Available {
		t.Fatalf("基础能力探测错误：%+v", report)
	}
	if report.DrawText.Available {
		t.Fatal("相似名字不能被当作 drawtext")
	}
	if report.NativePlayback.Available || report.Encoders["h264_videotoolbox"].Available {
		t.Fatal("未验证能力不应宣称可用")
	}
}

func TestCapabilitiesDoesNotTrustNonemptyPathOrExitZero(t *testing.T) {
	report := (Toolchain{FFmpegPath: "wrong", FFprobePath: "wrong"}).checkCapabilities(context.Background(), func(context.Context, string, ...string) ([]byte, error) {
		return []byte("not the requested tool"), nil
	})
	if report.FFmpeg.Available || report.FFprobe.Available || report.ExportVideo.Available || report.PreviewFrame.Available {
		t.Fatal("非空路径或退出零不能证明工具可用")
	}
}

func TestCapabilitiesRetainsProbeFailure(t *testing.T) {
	report := (Toolchain{FFmpegPath: "ffmpeg", FFprobePath: "ffprobe"}).checkCapabilities(context.Background(), func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("工具被拒绝执行")
	})
	if report.FFmpeg.Available || !strings.Contains(report.FFmpeg.Reason, "拒绝") {
		t.Fatalf("缺少工具失败原因：%+v", report.FFmpeg)
	}
}

func TestFilterNamesAcceptActualFFmpegNineAndLegacyFlagColumns(t *testing.T) {
	// 摘自本机官方Homebrew FFmpeg 9.0.2 -filters输出；不把说明行/相似名字当能力。
	current := `Filters:
  T.. = Timeline support
  .S. = Slice threading
  A = Audio input/output
  ------
 T. adelay            A->A       Delay one or more audio channels.
 .. aresample         A->A       Resample audio data.
 .. color             |->V      Provide an uniformly colored input.
 TS lut3d             V->V      Adjust colors using a 3D LUT.
 .. drawtext_fake     V->V      Not the actual drawtext filter.
`
	filters := parseFilterNames([]byte(current))
	for _, name := range []string{"adelay", "aresample", "color", "lut3d", "drawtext_fake"} {
		if !filters[name] {
			t.Fatalf("FFmpeg9真实两列标记未识别：%s", name)
		}
	}
	if filters["drawtext"] || filters["="] || len(filters) != 5 || !parseFilterNames([]byte(" TSC lut3d V->V Apply 3D LUT."))["lut3d"] {
		t.Fatal("误认相似名字、说明行，或破坏旧三列能力标记")
	}
}

func TestRealCapabilities(t *testing.T) {
	tools := integrationTools(t)
	report := tools.CheckCapabilities(context.Background())
	if !report.FFmpeg.Available || !report.FFprobe.Available || !report.ExportVideo.Available || !report.PreviewFrame.Available || !report.LUT3D.Available {
		t.Fatalf("真实开发工具基础能力未识别：%+v", report)
	}
	if report.NativePlayback.Available {
		t.Fatal("工具存在不代表原生实时播放完成")
	}
	t.Logf("真实工具能力：drawtext=%v encoder=%v", report.DrawText.Available, report.Encoders["libx264"].Available)
}
