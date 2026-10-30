package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"aividcut/internal/editor"
)

func videoPhaseTools(t *testing.T) Toolchain {
	t.Helper()
	tools := Discover()
	if tools.FFmpegPath == "" || tools.FFprobePath == "" {
		t.Skip("未配置FFmpeg/ffprobe，剪切相位真实回归未执行")
	}
	return tools
}

// 每帧都有独立RGB编码，自建无人物素材；CFR用AVI/FFV1保存精确源帧时钟。
func phaseVideoSource(t *testing.T, tools Toolchain, sourceFPS, frames int, vfr bool) editor.Asset {
	t.Helper()
	const width, height = 16, 16
	raw := make([]byte, width*height*3*frames)
	for n := 0; n < frames; n++ {
		r, g, b := byte(32+(n%16)*12), byte(32+(n/16%16)*12), byte(64+n*7%128)
		for at := n * width * height * 3; at < (n+1)*width*height*3; at += 3 {
			raw[at], raw[at+1], raw[at+2] = r, g, b
		}
	}
	directory := t.TempDir()
	input := filepath.Join(directory, "coded.rgb")
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "coded.avi")
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "rawvideo", "-pixel_format", "rgb24", "-video_size", "16x16", "-framerate", fmt.Sprint(sourceFPS), "-i", input}
	if vfr {
		path = filepath.Join(directory, "coded-vfr.mkv")
		// 两段不同密度及缺帧，PTS不从avg_frame_rate重建。
		args = append(args, "-vf", "settb=1/1000,setpts='if(lt(N,15),N*7,if(lt(N,30),105+(N-15)*31,570+(N-30)*13))'", "-fps_mode", "vfr")
	}
	args = append(args, "-c:v", "ffv1", "-pix_fmt", "bgr0", path)
	command(t, tools, args...)
	asset, err := tools.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

func phaseProject(asset editor.Asset, fpsNum, fpsDen int, frames int64) editor.Project {
	project := editor.NewProject("视频剪切相位夹具")
	project.Settings.Width, project.Settings.Height, project.Settings.FPSNum, project.Settings.FPSDen = 16, 16, fpsNum, fpsDen
	project.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("video", "track-video-1", "video")
	clip.AssetID, clip.DurationFrames = asset.ID, frames
	project.Clips = []editor.Clip{clip}
	return project
}

func renderPhaseFrames(t *testing.T, tools Toolchain, project editor.Project, from, to int64) [][3]byte {
	t.Helper()
	work := t.TempDir()
	plan, err := buildPlan(project, ExportOptions{Path: filepath.Join(work, "unused.mp4"), Preset: "project", FromFrame: from, ToFrame: to}, work, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Frames > 240 {
		t.Fatal("测试捕获不得超过240帧")
	}
	filter := filepath.Join(work, "graph.txt")
	if err := os.WriteFile(filter, []byte(plan.Filter), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	graphOption, err := tools.renderGraphOption(ctx)
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-filter_complex_threads", "1"}, plan.Args...)
	args = append(args, graphOption, filter, "-map", "[video_out]", "-an", "-frames:v", fmt.Sprint(plan.Frames), "-fps_mode", "passthrough", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1")
	cmd := exec.CommandContext(ctx, tools.FFmpegPath, args...)
	var output limitedBuffer
	var diagnostic tailBuffer
	cmd.Stdout, cmd.Stderr = &output, &diagnostic
	if err := cmd.Run(); err != nil {
		t.Fatalf("真实视频相位渲染失败: %v %s", err, diagnostic.String())
	}
	frameBytes := plan.Width * plan.Height * 3
	if len(output.data) != int(plan.Frames)*frameBytes {
		t.Fatalf("实际帧数不符: gotBytes=%d wantFrames=%d", len(output.data), plan.Frames)
	}
	result := make([][3]byte, plan.Frames)
	for n := range result {
		at := n*frameBytes + (plan.Height/2*plan.Width+plan.Width/2)*3
		copy(result[n][:], output.data[at:at+3])
	}
	return result
}

func TestRealVideoSelectedWindowPreservesWholePipelinePhase(t *testing.T) {
	tools := videoPhaseTools(t)
	for _, test := range []struct {
		name                      string
		sourceFPS, fpsNum, fpsDen int
		vfr                       bool
	}{{"30-to-30", 30, 30, 1, false}, {"60-to-2997", 60, 30000, 1001, false}, {"240-to-60", 240, 60, 1, false}, {"vfr-to-2997", 60, 30000, 1001, true}} {
		t.Run(test.name, func(t *testing.T) {
			asset := phaseVideoSource(t, tools, test.sourceFPS, test.sourceFPS, test.vfr)
			project := phaseProject(asset, test.fpsNum, test.fpsDen, 12)
			whole := renderPhaseFrames(t, tools, project, 0, 0)
			for frame, value := range whole {
				if value[0] < 16 && value[1] < 16 && value[2] < 16 {
					t.Fatalf("完整源出现未定义黑帧: frame=%d value=%v", frame, value)
				}
			}
			for _, cut := range []int64{1, 2, 3, 7} {
				cropped := renderPhaseFrames(t, tools, project, cut, 12)
				for n := range cropped {
					if cropped[n] != whole[int(cut)+n] {
						t.Fatalf("选区在源/fps前裁剪重置相位: cut=%d frame=%d got=%v want=%v", cut, n, cropped[n], whole[int(cut)+n])
					}
				}
			}
		})
	}
}

func derivedVideoWindow(t *testing.T, root editor.Clip, asset editor.Asset, settings editor.Settings, offset, length, start int64) editor.Clip {
	t.Helper()
	owned, err := editor.CaptureSourcePhase(root, asset, settings)
	if err != nil {
		t.Fatal(err)
	}
	owned.SourcePhase = editor.CloneSourcePhase(owned.SourcePhase)
	owned.SourcePhase.OffsetFrames += offset
	owned.StartFrame, owned.DurationFrames = start, length
	used, err := editor.SourceDurationUs(offset, settings, root.SpeedNum, root.SpeedDen)
	if err != nil {
		t.Fatal(err)
	}
	owned.SourceInUs += used
	if err := editor.ValidateSourcePhase(owned, asset, settings); err != nil {
		t.Fatal(err)
	}
	return owned
}

func assertPhasePixels(t *testing.T, got, want [][3]byte, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s帧数不同: got=%d want=%d", label, len(got), len(want))
	}
	for frame := range got {
		if got[frame] != want[frame] {
			t.Fatalf("%s画面取样改变: frame=%d got=%v want=%v", label, frame, got[frame], want[frame])
		}
	}
}

func TestRealVideoDerivedSegmentsRetainRootFPSAndSpeedPhase(t *testing.T) {
	tools := videoPhaseTools(t)
	for _, source := range []struct {
		name                      string
		fps, targetNum, targetDen int
		vfr                       bool
	}{{"60-to-2997", 60, 30000, 1001, false}, {"240-to-60", 240, 60, 1, false}, {"vfr-to-2997", 60, 30000, 1001, true}} {
		t.Run(source.name, func(t *testing.T) {
			asset := phaseVideoSource(t, tools, source.fps, source.fps, source.vfr)
			for _, speed := range [][2]int{{1, 2}, {1, 1}, {3, 2}, {2, 1}} {
				t.Run(fmt.Sprintf("speed-%d-%d", speed[0], speed[1]), func(t *testing.T) {
					project := phaseProject(asset, source.targetNum, source.targetDen, 12)
					project.Clips[0].SourceInUs = 12345
					project.Clips[0].SpeedNum, project.Clips[0].SpeedDen = speed[0], speed[1]
					whole := renderPhaseFrames(t, tools, project, 0, 0)
					root := project.Clips[0]
					cuts := []int64{0, 1, 2, 3, 5, 7, 10, 12}
					joined := project
					joined.Clips = nil
					for i := 0; i < len(cuts)-1; i++ {
						clip := derivedVideoWindow(t, root, asset, project.Settings, cuts[i], cuts[i+1]-cuts[i], cuts[i])
						clip.ID = fmt.Sprintf("part-%d", i)
						joined.Clips = append(joined.Clips, clip)
					}
					assertPhasePixels(t, renderPhaseFrames(t, tools, joined, 0, 0), whole, "多切点拼接")
					assertPhasePixels(t, renderPhaseFrames(t, tools, joined, 4, 11), whole[4:11], "派生段跨边选区")
					actual := project
					for _, at := range cuts[1 : len(cuts)-1] {
						for _, selected := range actual.Clips {
							if selected.StartFrame < at && at < selected.StartFrame+selected.DurationFrames {
								actual = exactEdit(t, actual, editor.Command{Type: "split_clip", ClipID: selected.ID, AtFrame: at})
								break
							}
						}
					}
					assertPhasePixels(t, renderPhaseFrames(t, tools, actual, 0, 0), whole, "实际核心递归分割")
					for _, cut := range []int64{1, 2, 5, 10} {
						right := derivedVideoWindow(t, root, asset, project.Settings, cut, 12-cut, 0)
						trimmed := project
						trimmed.Clips = []editor.Clip{right}
						assertPhasePixels(t, renderPhaseFrames(t, tools, trimmed, 0, 0), whole[cut:], fmt.Sprintf("trim/删左移动cut=%d", cut))
					}
				})
			}
		})
	}
}
