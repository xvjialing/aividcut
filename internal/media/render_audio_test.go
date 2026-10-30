package media

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"aividcut/internal/editor"
)

func renderAudioPCM(t *testing.T, tools Toolchain, asset editor.Asset, sourceIn int64, speedNum, speedDen int, clipStart, clipFrames, from, to int64) []float64 {
	t.Helper()
	project := editor.NewProject("音频时序回归")
	project.Settings.Width, project.Settings.Height, project.Settings.FPSNum = 32, 32, 100
	project.Assets = []editor.Asset{asset}
	clip := editor.DefaultClip("source", "track-video-1", "video")
	clip.AssetID, clip.SourceInUs, clip.SpeedNum, clip.SpeedDen = asset.ID, sourceIn, speedNum, speedDen
	clip.StartFrame, clip.DurationFrames = clipStart, clipFrames
	project.Clips = []editor.Clip{clip}
	path := filepath.Join(t.TempDir(), "rendered.mp4")
	if err := tools.Export(context.Background(), project, ExportOptions{Path: path, Preset: "project", FromFrame: from, ToFrame: to}, nil); err != nil {
		t.Fatal(err)
	}
	data := command(t, tools, "-hide_banner", "-loglevel", "error", "-i", path, "-map", "0:a", "-ar", "16000", "-ac", "1", "-f", "f32le", "pipe:1")
	result := make([]float64, len(data)/4)
	for index := range result {
		result[index] = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[index*4:])))
	}
	return result
}
func renderAudioRMS(samples []float64, from, to float64) float64 {
	a, b := int(from*16000), min(len(samples), int(to*16000))
	if b <= a {
		return math.Inf(1)
	}
	var sum float64
	for _, value := range samples[a:b] {
		sum += value * value
	}
	return math.Sqrt(sum / float64(b-a))
}

func TestSpeechRenderAudioDelayCropSpeedAndExportRange(t *testing.T) {
	tools := speechIntegrationTools(t)
	asset := speechSource(t, tools, "delayed.mov", "-f", "lavfi", "-i", "color=black:s=32x32:r=25:d=2", "-itsoffset", "0.5", "-f", "lavfi", "-i", "sine=frequency=660:duration=0.75:sample_rate=48000", "-c:v", "libx264", "-c:a", "pcm_s16le")
	prepared, err := tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(t.TempDir(), "prepared"), SpeechAudioOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.AudioOffsetUs != 500000 || prepared.CoverageEndUs != 1250000 {
		t.Fatalf("回归源覆盖不符合准备证据：%+v", prepared)
	}
	for _, test := range []struct {
		name                                       string
		sourceIn                                   int64
		speedNum, speedDen                         int
		start, frames, from, to                    int64
		quietStart, quietEnd, voiceStart, voiceEnd float64
	}{
		{"late", 0, 1, 1, 0, 200, 0, 0, .1, .3, .65, .95},
		{"clip-placement", 0, 1, 1, 30, 200, 0, 0, .4, .6, .95, 1.2},
		{"source-before-audio", 250000, 1, 1, 0, 100, 0, 0, .03, .15, .4, .65},
		{"source-within-audio", 750000, 1, 1, 0, 50, 0, 0, -1, -1, .06, .3},
		{"source-before-only-silence", 0, 1, 1, 0, 30, 0, 0, .03, .2, -1, -1},
		{"source-after-audio", 1500000, 1, 1, 0, 30, 0, 0, .03, .2, -1, -1},
		{"double-speed", 0, 2, 1, 0, 100, 0, 0, .04, .15, .35, .5},
		{"half-speed", 0, 1, 2, 0, 400, 0, 0, .2, .6, 1.3, 1.8},
		{"double-speed-range", 0, 2, 1, 0, 100, 20, 80, .005, .025, .13, .3},
		{"range-within-audio", 0, 1, 1, 0, 200, 75, 100, -1, -1, .04, .18},
	} {
		t.Run(test.name, func(t *testing.T) {
			samples := renderAudioPCM(t, tools, asset, test.sourceIn, test.speedNum, test.speedDen, test.start, test.frames, test.from, test.to)
			if test.quietStart >= 0 && renderAudioRMS(samples, test.quietStart, test.quietEnd) > .001 {
				t.Fatalf("源/时间线无音轨区间提前发声：RMS %.4f", renderAudioRMS(samples, test.quietStart, test.quietEnd))
			}
			if test.voiceStart >= 0 && renderAudioRMS(samples, test.voiceStart, test.voiceEnd) < .02 {
				t.Fatalf("真实源音轨区间未发声：RMS %.4f", renderAudioRMS(samples, test.voiceStart, test.voiceEnd))
			}
		})
	}
}

func TestSpeechRenderAudioFormatStartMatchesPreparation(t *testing.T) {
	tools := speechIntegrationTools(t)
	asset := speechSource(t, tools, "nonzero-format.mov", "-f", "lavfi", "-i", "color=black:s=32x32:r=25:d=2", "-itsoffset", "0.5", "-f", "lavfi", "-i", "sine=frequency=660:duration=0.75:sample_rate=48000", "-c:v", "libx264", "-c:a", "pcm_s16le", "-output_ts_offset", "3")
	data, err := speechRunOutput(context.Background(), tools.FFprobePath, "-v", "error", "-show_format", "-of", "json", "-i", asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	var metadata probeResult
	if json.Unmarshal(data, &metadata) != nil {
		t.Fatal("无法读取format起点证据")
	}
	start, known := speechSignedTime(metadata.Format.StartTime)
	if !known || start < 2_900_000 {
		t.Fatalf("回归源没有真实非零格式起点：%s", metadata.Format.StartTime)
	}
	prepared, err := tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(t.TempDir(), "prepared"), SpeechAudioOptions{})
	if err != nil {
		t.Fatal(err)
	}
	samples := renderAudioPCM(t, tools, asset, 0, 1, 1, 0, 200, 0, 0)
	lead := float64(prepared.AudioOffsetUs) / 1e6
	if lead < .4 || renderAudioRMS(samples, .05, lead-.1) > .001 || renderAudioRMS(samples, lead+.1, lead+.4) < .02 {
		t.Fatalf("导出没有遵循Prepare的源零点：format=%s audioOffset=%d", metadata.Format.StartTime, prepared.AudioOffsetUs)
	}
	t.Logf("本机格式起点=%s，Prepare源偏移=%dµs，与导出实测一致", metadata.Format.StartTime, prepared.AudioOffsetUs)
}
