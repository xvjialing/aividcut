package media

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"aividcut/internal/editor"
)

// WAV由本测试明确写出样本，count是实际数据长度而非duration反推。
func exactWAV(t *testing.T, name string, rate int, values []int16) editor.Asset {
	t.Helper()
	data := make([]byte, 44+len(values)*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(values)*2))
	for i, value := range values {
		binary.LittleEndian.PutUint16(data[44+i*2:], uint16(value))
	}
	path := filepath.Join(t.TempDir(), name+".wav")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	count := int64(len(values))
	return editor.Asset{ID: name, Name: name, Kind: "audio", Path: path, HasAudio: true, DurationUs: count * 1_000_000 / int64(rate), AudioSampleRate: rate, AudioSampleCount: &count, Fingerprint: "sha256:" + name}
}
func exactAudioProject(t *testing.T, asset editor.Asset) editor.Project {
	t.Helper()
	project := editor.NewProject("实际采样导出")
	project.Settings.Width, project.Settings.Height = 32, 32
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	project.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("sound", "track-audio-1", "audio")
	clip.AssetID = asset.ID
	var err error
	clip, err = editor.AudioClipToSourceEnd(clip, asset, project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	project.Clips = []editor.Clip{clip}
	return project
}
func exactEdit(t *testing.T, project editor.Project, commands ...editor.Command) editor.Project {
	t.Helper()
	engine, err := editor.NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Apply(editor.EditRequest{IdempotencyKey: editor.NewID("pcm-test"), BaseRevision: project.Revision, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func exactRenderPCM(t *testing.T, tools Toolchain, project editor.Project, from, to int64) ([]float32, renderPlan) {
	t.Helper()
	work := t.TempDir()
	plan, err := buildPlan(project, ExportOptions{Preset: "project", FromFrame: from, ToFrame: to}, work, false, func(path string, sourceIn int64, duration float64) (renderAudioTiming, error) {
		return tools.renderAudioTiming(context.Background(), path, sourceIn, duration)
	})
	if err != nil {
		t.Fatal(err)
	}
	filterPath := filepath.Join(work, "filter.graph")
	if err := os.WriteFile(filterPath, []byte(plan.Filter), 0600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-hide_banner", "-nostdin", "-loglevel", "error", "-filter_complex_threads", "1"}, plan.Args...)
	graphOption, err := tools.renderGraphOption(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, graphOption, filterPath, "-map", "[audio_out]", "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1", "-map", "[video_out]", "-an", "-f", "null", "-")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tools.FFmpegPath, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("实际PCM渲染失败：%v\n%s\n%s", err, stderr.String(), plan.Filter)
	}
	if len(data)%8 != 0 || int64(len(data)/8) != plan.AudioSamples {
		t.Fatalf("输出未按全局采样时钟结束：实际%d字节，计划%d stereo samples", len(data), plan.AudioSamples)
	}
	values := make([]float32, len(data)/8)
	for i := range values {
		values[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*8:]))
	}
	return values, plan
}

func TestExactAudioGlobalSampleSpanUsesEndpointCeilDifference(t *testing.T) {
	settings := editor.NewProject("时钟").Settings
	settings.FPSNum, settings.FPSDen = 30000, 1001
	count, err := renderSampleSpan(1, 2, settings)
	if err != nil || count != 1602 { // ceil(3203.2)-ceil(1601.6)，不能round(1601.6)。
		t.Fatalf("导出端点换算错误：%d %v", count, err)
	}
	count, err = renderSampleSpan(2, 3, settings)
	if err != nil || count != 1601 {
		t.Fatalf("未保留29.97的交替输出时钟：%d %v", count, err)
	}
}

func TestRenderGraphFileOptionSupportsOldAndCurrentFFmpeg(t *testing.T) {
	for _, test := range []struct{ version, want string }{
		{"ffmpeg version 6.1.1 Copyright", "-filter_complex_script"},
		{"ffmpeg version 9.0.2 Copyright", "-/filter_complex"},
		{"ffmpeg version 10.0 Copyright", "-/filter_complex"},
	} {
		if got := renderGraphOptionForVersion(test.version); got != test.want {
			t.Fatalf("%s：%s", test.version, got)
		}
	}
}

func TestExactAudioRealLastSampleAndCeilSlotSilence(t *testing.T) {
	tools := speechIntegrationTools(t)
	values := make([]int16, 5777)
	values[len(values)-1] = 28000
	project := exactAudioProject(t, exactWAV(t, "last-impulse", 48000, values))
	pcm, _ := exactRenderPCM(t, tools, project, 0, 0)
	if len(pcm) != 6407 || pcm[5776] < .5 {
		t.Fatalf("末sample被截断或显示尾slot长度错误：len=%d last=%f", len(pcm), pcm[5776])
	}
	for i, value := range pcm[5777:] {
		if value != 0 {
			t.Fatalf("真实末端后继续播放或循环：sample%d=%f", i+5777, value)
		}
	}
	// 第二路真实音轨在第一路结束后仍播放，不能对最终混音统一门控。
	tone := make([]int16, 6400)
	for i := range tone {
		tone[i] = int16(10000 * math.Sin(float64(i)*2*math.Pi*880/48000))
	}
	second := exactWAV(t, "second-tone", 48000, tone)
	project.Assets = append(project.Assets, second)
	project.Tracks = append(project.Tracks, editor.Track{ID: "second-audio", Kind: "audio", Name: "第二声音"})
	clip := editor.DefaultClip("second", "second-audio", "audio")
	clip.AssetID = second.ID
	clip, err := editor.AudioClipToSourceEnd(clip, second, project.Settings)
	if err != nil {
		t.Fatal(err)
	}
	project.Clips = append(project.Clips, clip)
	pcm, _ = exactRenderPCM(t, tools, project, 0, 0)
	var energy float64
	for _, value := range pcm[5800:6300] {
		energy += float64(value * value)
	}
	if energy < 1 {
		t.Fatal("裁第一路尾时误删第二路真实声音")
	}
}

func TestExactAudioRealSplitCropAndTrimHaveNoSampleLoss(t *testing.T) {
	tools := speechIntegrationTools(t)
	for _, rate := range []int{48000, 44100} {
		t.Run(strconv.Itoa(rate), func(t *testing.T) {
			values := make([]int16, rate*12/100+17)
			for i := range values {
				values[i] = int16(11000 * math.Sin(float64(i)*2*math.Pi*737/float64(rate)))
			}
			project := exactAudioProject(t, exactWAV(t, "phase", rate, values))
			whole, _ := exactRenderPCM(t, tools, project, 0, 0)
			split := exactEdit(t, project, editor.Command{Type: "split_clip", ClipID: "sound", AtFrame: 1})
			joined, plan := exactRenderPCM(t, tools, split, 0, 0)
			if !reflect.DeepEqual(whole, joined) {
				for i := range whole {
					if whole[i] != joined[i] {
						t.Fatalf("%dHz切开后PCM变化：sample%d %f != %f", rate, i, whole[i], joined[i])
					}
				}
			}
			if strings.Count(strings.Join(plan.Args, " "), project.Assets[0].Path) != 1 || strings.Count(plan.Filter, "aresample=48000") != 1 {
				t.Fatal("同文件切片没有共享一次解码/重采样")
			}
			cropped, _ := exactRenderPCM(t, tools, split, 1, 4)
			if !reflect.DeepEqual(cropped, whole[1602:]) {
				t.Fatal("局部导出没有保留真实跨帧切口的sample，或从零重新取整")
			}
			// split后删左与直接trim必须一致，44.1k的右段起点有真实子帧偏移。
			rightOnly := exactEdit(t, split, editor.Command{Type: "delete_clip", ClipID: split.Clips[0].ID})
			trim := project.Clips[0]
			trim.StartFrame, trim.DurationFrames = 1, trim.DurationFrames-1
			trim.SourceInUs, _ = editor.SourceDurationUs(1, project.Settings, 1, 1)
			trimmed := exactEdit(t, project, editor.Command{Type: "update_clip", Clip: &trim})
			a, _ := exactRenderPCM(t, tools, rightOnly, 1, 4)
			b, _ := exactRenderPCM(t, tools, trimmed, 1, 4)
			if !reflect.DeepEqual(a, b) {
				t.Fatal("直接trim与split删左的实际PCM不一致")
			}
		})
	}
}

func TestExactAudioDerivedSpeedUsesOriginalProcessingInput(t *testing.T) {
	tools := speechIntegrationTools(t)
	for _, rate := range []int{44100, 48000} {
		values := make([]int16, rate*2)
		for i := range values {
			values[i] = int16(10000 * math.Sin(float64(i)*2*math.Pi*737/float64(rate)))
		}
		asset := exactWAV(t, "speed-phase", rate, values)
		for _, speed := range [][2]int{{1, 2}, {1, 1}, {3, 2}} {
			t.Run(fmt.Sprintf("%d-%d-%d", rate, speed[0], speed[1]), func(t *testing.T) {
				project := exactAudioProject(t, asset)
				clip := project.Clips[0]
				clip.SourceInUs, clip.SpeedNum, clip.SpeedDen = 12345, speed[0], speed[1]
				clip.AudioRange = nil
				var err error
				clip, err = editor.AudioClipToSourceEnd(clip, asset, project.Settings)
				if err != nil {
					t.Fatal(err)
				}
				project.Clips = []editor.Clip{clip}
				whole, _ := exactRenderPCM(t, tools, project, 0, 0)
				split := project
				for _, at := range []int64{1, 2, 5, 10, 20, 30} {
					for _, selected := range split.Clips {
						if selected.StartFrame < at && at < selected.StartFrame+selected.DurationFrames {
							split = exactEdit(t, split, editor.Command{Type: "split_clip", ClipID: selected.ID, AtFrame: at})
							break
						}
					}
				}
				joined, _ := exactRenderPCM(t, tools, split, 0, 0)
				if !reflect.DeepEqual(whole, joined) {
					for i := range whole {
						if whole[i] != joined[i] {
							t.Fatalf("变速分割改变原处理样本%d: %g != %g", i, whole[i], joined[i])
						}
					}
					t.Fatal("输出长度变化")
				}
			})
		}
	}
}

func TestExactAudioRealLateOriginForMeasuredAndMicrosecondRanges(t *testing.T) {
	tools := speechIntegrationTools(t)
	asset := speechSource(t, tools, "late-exact.mov", "-f", "lavfi", "-i", "color=black:s=32x32:r=25:d=2", "-itsoffset", "0.5", "-f", "lavfi", "-i", "sine=frequency=660:duration=0.75:sample_rate=48000", "-c:v", "libx264", "-c:a", "pcm_s16le", "-output_ts_offset", "3")
	for _, measured := range []bool{false, true} {
		t.Run(strconv.FormatBool(measured), func(t *testing.T) {
			source := asset
			if measured {
				// 文件是本测试实际生成的0.75s/48k PCM；显式36000个样本及0.5s源偏移。
				count := int64(36000)
				source.AudioSampleRate, source.AudioSampleCount, source.AudioStartUs = 48000, &count, 500000
			} else {
				source.AudioSampleCount, source.AudioStartUs = nil, 0
			}
			project := exactAudioProject(t, source)
			pcm, _ := exactRenderPCM(t, tools, project, 0, 0)
			var energy float64
			for i, value := range pcm {
				if (i < 24000 || i >= 60000) && math.Abs(float64(value)) > .00001 {
					t.Fatalf("把晚起/早止真实音轨移到源零点或延长：sample%d=%f", i, value)
				}
				if i > 30000 && i < 50000 {
					energy += float64(value * value)
				}
			}
			if energy < 1 {
				t.Fatal("真实音轨覆盖区间没有声音")
			}
		})
	}
}

func TestExactAudioRealSpeedCropIsAppliedAfterTempoAndStopsAtTrueEnd(t *testing.T) {
	tools := speechIntegrationTools(t)
	values := make([]int16, 48017)
	for i := range values {
		values[i] = int16(10000 * math.Sin(float64(i)*2*math.Pi*440/48000))
	}
	for _, speed := range [][2]int{{2, 1}, {1, 2}} {
		project := exactAudioProject(t, exactWAV(t, "tempo", 48000, values))
		clip := project.Clips[0]
		clip.SpeedNum, clip.SpeedDen = speed[0], speed[1]
		project = exactEdit(t, project, editor.Command{Type: "update_clip", Clip: &clip})
		whole, _ := exactRenderPCM(t, tools, project, 0, 0)
		crop, _ := exactRenderPCM(t, tools, project, 2, 5)
		if !reflect.DeepEqual(crop, whole[3204:8008]) {
			t.Fatalf("%v变速选区在atempo前裁源，导致结果不同", speed)
		}
		end := int(math.Ceil(float64(len(values)) * float64(speed[1]) / float64(speed[0])))
		for _, value := range whole[end:] {
			if value != 0 {
				t.Fatal("变速后声音超过真实末端")
			}
		}
	}
}

func TestExactAudioMP4ExportKeepsVoiceNearNonFrameAlignedEnd(t *testing.T) {
	tools := speechIntegrationTools(t)
	values := make([]int16, 5777)
	for i := 5200; i < len(values); i++ {
		values[i] = int16(18000 * math.Sin(float64(i)*2*math.Pi*660/48000))
	}
	project := exactAudioProject(t, exactWAV(t, "mp4-tail", 48000, values))
	path := filepath.Join(t.TempDir(), "tail.mp4")
	if err := tools.Export(context.Background(), project, ExportOptions{Path: path, Preset: "project"}, nil); err != nil {
		t.Fatal(err)
	}
	data := command(t, tools, "-hide_banner", "-loglevel", "error", "-i", path, "-map", "0:a:0", "-ac", "1", "-ar", "48000", "-f", "f32le", "pipe:1")
	if len(data)/4 < 5777 {
		t.Fatalf("MP4编码截断非帧对齐尾端：%d samples", len(data)/4)
	}
	var energy float64
	for i := 5500; i < 5777; i++ {
		value := math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
		energy += float64(value * value)
	}
	if energy < 1 {
		t.Fatal("MP4末尾口播区间被floor帧裁掉")
	}
}
