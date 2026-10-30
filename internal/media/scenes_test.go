package media

import (
	"bytes"
	"context"
	"image/jpeg"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSceneStreamBudgetsAndTimelineFields(t *testing.T) {
	cancelled := false
	s := &sceneStream{cancel: func() { cancelled = true }}
	for _, part := range []struct {
		channel int
		data    string
	}{{1, "[showinfo] n:   0 pts: 20 pts_time:2 duration: 1\n"}, {0, "frame:25 pts:30 pts_time:3\nlavfi.scd.time=3\nframe=50\nout_time_us=4000000\nprogress=end\n"}} {
		if _, err := (sceneWriter{state: s, channel: part.channel}).Write([]byte(part.data)); err != nil {
			t.Fatal(err)
		}
	}
	if s.first == nil || *s.first != 2_000_000 || s.end != 4_000_000 || s.frames != 50 || len(s.cuts) != 1 || !s.finished {
		t.Fatal("时间字段解析不完整")
	}
	if _, err := (sceneWriter{state: s}).Write(bytes.Repeat([]byte("x"), 17<<10)); err == nil || !cancelled {
		t.Fatal("残行预算失效")
	}
	for _, raw := range []string{"NaN", "Inf", "-1", "90000"} {
		if _, err := sceneSeconds(raw); err == nil {
			t.Fatal("非有限或越界时间被接受")
		}
	}
}
func TestRealSceneDetectionNormalLateVideoAndAudioTail(t *testing.T) {
	tools := Discover()
	if tools.FFmpegPath == "" || tools.FFprobePath == "" {
		t.Skip("缺少显式媒体工具")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	source := filepath.Join(dir, "red-blue.mp4")
	cmd := exec.CommandContext(ctx, tools.FFmpegPath, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=red:s=640x360:r=25:d=1", "-f", "lavfi", "-i", "color=c=blue:s=640x360:r=25:d=1", "-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]", "-map", "[v]", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-an", "-y", source)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name            string
		offset, tail    string
		start, cut, end int64
	}{{"normal", "0", "2", 0, 1_000_000, 2_000_000}, {"late", "2", "4", 2_000_000, 3_000_000, 4_000_000}, {"audio-tail", "0", "5", 0, 1_000_000, 2_000_000}} {
		path := filepath.Join(dir, test.name+".mp4")
		cmd = exec.CommandContext(ctx, tools.FFmpegPath, "-hide_banner", "-loglevel", "error", "-itsoffset", test.offset, "-i", source, "-f", "lavfi", "-i", "sine=duration="+test.tail+":sample_rate=48000", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "-y", path)
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		scan, err := tools.DetectScenes(ctx, path, 10, nil)
		if err != nil || scan.StartUs != test.start || scan.EndUs != test.end || len(scan.Cuts) != 1 || scan.Cuts[0] != test.cut {
			t.Fatalf("%s覆盖或切换错误: %+v %v", test.name, scan, err)
		}
		asset, err := tools.Probe(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		for i, position := range []int64{test.start + 500000, test.cut + 500000} {
			thumb, err := tools.ExtractThumbnail(ctx, asset, position)
			if err != nil {
				t.Fatal(err)
			}
			picture, err := jpeg.Decode(bytes.NewReader(thumb.JPEG))
			if err != nil {
				t.Fatal(err)
			}
			r, _, b, _ := picture.At(picture.Bounds().Dx()/2, picture.Bounds().Dy()/2).RGBA()
			if i == 0 && r>>8 < 200 || i == 1 && b>>8 < 200 {
				t.Fatal("索引源坐标与现有取样不一致")
			}
		}
	}

	for _, variant := range []string{"rotated", "vfr"} {
		path := filepath.Join(dir, variant+".mp4")
		args := []string{"-hide_banner", "-loglevel", "error", "-i", source}
		if variant == "rotated" {
			args = append(args, "-c", "copy", "-metadata:s:v:0", "rotate=90")
		} else {
			args = append(args, "-vf", "select='eq(n,25)+not(mod(n,2))'", "-fps_mode", "vfr", "-c:v", "libx264", "-pix_fmt", "yuv420p")
		}
		args = append(args, "-y", path)
		if err := exec.CommandContext(ctx, tools.FFmpegPath, args...).Run(); err != nil {
			t.Fatal(err)
		}
		scan, err := tools.DetectScenes(ctx, path, 10, nil)
		if err != nil || scan.StartUs != 0 || len(scan.Cuts) != 1 || scan.Cuts[0] != 1_000_000 || scan.EndUs < 1_900_000 || scan.EndUs > 2_100_000 {
			t.Fatalf("%s时间映射失败: %+v %v", variant, scan, err)
		}
		asset, err := tools.Probe(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		for i, position := range []int64{500000, 1500000} {
			value, err := tools.ExtractThumbnail(ctx, asset, position)
			if err != nil {
				t.Fatal(err)
			}
			picture, err := jpeg.Decode(bytes.NewReader(value.JPEG))
			if err != nil {
				t.Fatal(err)
			}
			r, _, b, _ := picture.At(picture.Bounds().Dx()/2, picture.Bounds().Dy()/2).RGBA()
			if i == 0 && r>>8 < 200 || i == 1 && b>>8 < 200 {
				t.Fatal("旋转/VFR索引与源取样错位")
			}
		}
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := tools.DetectScenes(cancelled, source, 10, nil); err == nil {
		t.Fatal("检测忽略取消")
	}
}
