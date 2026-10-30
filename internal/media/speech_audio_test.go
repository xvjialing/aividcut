package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aividcut/internal/editor"
)

func speechIntegrationTools(t *testing.T) Toolchain {
	t.Helper()
	if os.Getenv("AIVIDCUT_FFMPEG") == "" || os.Getenv("AIVIDCUT_FFPROBE") == "" {
		t.Skip("语音提取真实样本未执行：需显式设置AIVIDCUT_FFMPEG及AIVIDCUT_FFPROBE，不自动依赖其他应用")
	}
	tools := Discover()
	if tools.FFmpegPath == "" || tools.FFprobePath == "" {
		t.Fatal("显式媒体工具路径不可用")
	}
	return tools
}
func speechSource(t *testing.T, tools Toolchain, name string, args ...string) editor.Asset {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	command(t, tools, append(append([]string{"-hide_banner", "-loglevel", "error"}, args...), path)...)
	asset, err := tools.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return asset
}
func readSpeechSamples(t *testing.T, path string) []int16 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[36:40]) != "data" {
		t.Fatal("不是准备好的标准PCM WAV")
	}
	result := make([]int16, (len(data)-44)/2)
	for index := range result {
		result[index] = int16(binary.LittleEndian.Uint16(data[44+index*2:]))
	}
	return result
}
func speechTone(samples []int16, start, end float64) float64 {
	a, b := int(start*SpeechSampleRate), min(len(samples), int(end*SpeechSampleRate))
	crossings := 0
	for index := a + 1; index < b; index++ {
		if samples[index-1] <= 0 && samples[index] > 0 {
			crossings++
		}
	}
	return float64(crossings) / (end - start)
}

func TestSpeechAudioRealResampleChunkAndSourceMapping(t *testing.T) {
	tools := speechIntegrationTools(t)
	asset := speechSource(t, tools, "two-tones.wav", "-f", "lavfi", "-i", "sine=frequency=440:duration=1:sample_rate=48000", "-f", "lavfi", "-i", "sine=frequency=880:duration=1.350125:sample_rate=48000", "-filter_complex", "[0:a][1:a]concat=n=2:v=0:a=1", "-ac", "2", "-c:a", "pcm_s16le")
	target := filepath.Join(t.TempDir(), "prepared")
	var completed SpeechAudioProgress
	result, err := tools.PrepareSpeechAudio(context.Background(), asset, target, SpeechAudioOptions{ChunkDurationUs: 1_000_000, Progress: func(value SpeechAudioProgress) {
		if value.Stage == "completed" {
			completed = value
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceFingerprint != asset.Fingerprint || result.SourceDurationUs != asset.DurationUs || result.SampleRate != 16000 || result.Channels != 1 || result.AudioOffsetUs != 0 || len(result.Parts) != 3 {
		t.Fatalf("准备结果无效：%+v", result)
	}
	var total int64
	previous := result.AudioOffsetUs
	for _, part := range result.Parts {
		if part.OffsetUs != previous || part.DurationUs <= 0 || part.Fingerprint == "" || part.Path != filepath.Join(target, part.ID+".wav") {
			t.Fatalf("分块源映射错误：%+v", part)
		}
		previous = part.OffsetUs + part.DurationUs
		samples := readSpeechSamples(t, part.Path)
		if int64(len(samples)) != part.SampleCount || math.Abs(float64(part.AudioDurationUs-speechSampleDuration(part.SampleCount))) > 1 {
			t.Fatal("物理样本与ffprobe时长不一致")
		}
		info, _ := os.Stat(part.Path)
		if info.Size() >= SpeechPartMaxBytes {
			t.Fatal("分块达到64MiB上限")
		}
		total += part.SampleCount
	}
	if previous != result.CoverageEndUs || result.CoverageEndUs > asset.DurationUs || completed.SampleCount != total || completed.TotalSamples != total {
		t.Fatal("覆盖末尾或实测进度不一致")
	}
	first := readSpeechSamples(t, result.Parts[0].Path)
	second := readSpeechSamples(t, result.Parts[1].Path)
	if math.Abs(speechTone(first, .1, .9)-440) > 3 || math.Abs(speechTone(second, .1, .9)-880) > 3 {
		t.Fatal("分块的真实声音或源区间错误")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(target)
		if info.Mode().Perm() != 0700 {
			t.Fatal("准备目录必须私有")
		}
	}
}

func TestSpeechAudioRealVideoShortAndDelayedTrack(t *testing.T) {
	tools := speechIntegrationTools(t)
	for _, test := range []struct {
		name       string
		offset     string
		start, end int64
	}{
		{"short-track", "0", 0, 750000},
		{"delayed-track", "0.5", 500000, 1250000},
	} {
		t.Run(test.name, func(t *testing.T) {
			asset := speechSource(t, tools, "source.mov", "-f", "lavfi", "-i", "color=black:s=32x32:r=25:d=2", "-itsoffset", test.offset, "-f", "lavfi", "-i", "sine=frequency=660:duration=0.75:sample_rate=48000", "-c:v", "libx264", "-c:a", "pcm_s16le")
			result, err := tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(t.TempDir(), "prepared"), SpeechAudioOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if result.SourceDurationUs != 2_000_000 || math.Abs(float64(result.AudioOffsetUs-test.start)) > 125 || math.Abs(float64(result.CoverageEndUs-test.end)) > 125 || len(result.Parts) != 1 || len(result.Warnings) == 0 {
				t.Fatalf("未保留实际音轨覆盖：%+v", result)
			}
			if len(readSpeechSamples(t, result.Parts[0].Path)) != 12000 || math.Abs(speechTone(readSpeechSamples(t, result.Parts[0].Path), .1, .6)-660) > 3 {
				t.Fatal("错误补了视频头尾静音或改变真实声音")
			}
		})
	}
}

func TestSpeechAudioRealAACDelayAndMatroskaTimebase(t *testing.T) {
	tools := speechIntegrationTools(t)
	asset := speechSource(t, tools, "delayed-aac.mp4", "-f", "lavfi", "-i", "color=black:s=32x32:r=25:d=2", "-itsoffset", "0.5", "-f", "lavfi", "-i", "sine=frequency=660:duration=0.75:sample_rate=48000", "-c:v", "libx264", "-c:a", "aac")
	result, err := tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(t.TempDir(), "prepared"), SpeechAudioOptions{})
	if err != nil {
		t.Fatal(err)
	}
	samples := readSpeechSamples(t, result.Parts[0].Path)
	firstSignal := -1
	for index, value := range samples {
		if value > 800 || value < -800 {
			firstSignal = index
			break
		}
	}
	if firstSignal < 0 || math.Abs(float64(result.AudioOffsetUs)+float64(firstSignal)*1e6/SpeechSampleRate-500000) > 5000 || result.CoverageEndUs > 1_270_000 || len(samples) > 13000 {
		t.Fatalf("AAC延迟或编码填充误作整段视频音频：%+v first=%d count=%d", result, firstSignal, len(samples))
	}
	asset = speechSource(t, tools, "coarse-timebase.mkv", "-f", "lavfi", "-i", "sine=frequency=440:duration=1.23456:sample_rate=48000", "-c:a", "pcm_s16le")
	result, err = tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(t.TempDir(), "prepared"), SpeechAudioOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.TimestampToleranceUs < 1000 || len(result.Warnings) == 0 || math.Abs(speechTone(readSpeechSamples(t, result.Parts[0].Path), .1, .9)-440) > 3 {
		t.Fatal("普通Matroska时间基量化没有真实核查或记录")
	}
}

func TestSpeechAudioRealPartCountLimit(t *testing.T) {
	tools := speechIntegrationTools(t)
	asset := speechSource(t, tools, "too-many.wav", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "103", "-c:a", "pcm_s16le")
	parent := t.TempDir()
	_, err := tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(parent, "prepared"), SpeechAudioOptions{ChunkDurationUs: 100000})
	if err == nil || !strings.Contains(err.Error(), "1024") {
		t.Fatalf("未限制实际分块总量：%v", err)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 0 {
		t.Fatal("块数超限后遗留未发布数据")
	}
}

func TestSpeechAudioRealADTSInput(t *testing.T) {
	tools := speechIntegrationTools(t)
	asset := speechSource(t, tools, "raw.aac", "-f", "lavfi", "-i", "sine=frequency=550:duration=1.5:sample_rate=48000", "-c:a", "aac", "-f", "adts")
	result, err := tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(t.TempDir(), "prepared"), SpeechAudioOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceFingerprint != asset.Fingerprint || len(result.Parts) != 1 || result.Parts[0].SampleCount <= 16000 || math.Abs(speechTone(readSpeechSamples(t, result.Parts[0].Path), .2, 1.2)-550) > 3 {
		t.Fatal("裸ADTS AAC未提取为真实16k PCM声音")
	}
}

func TestSpeechAudioRealQuantizationAndShortTailMerge(t *testing.T) {
	tools := speechIntegrationTools(t)
	asset := speechSource(t, tools, "fractional.wav", "-f", "lavfi", "-i", "sine=frequency=440:duration=1.03:sample_rate=44100", "-c:a", "pcm_s16le")
	result, err := tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(t.TempDir(), "prepared"), SpeechAudioOptions{ChunkDurationUs: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Parts) != 1 || result.Parts[0].SampleCount <= 16000 {
		t.Fatal("小于100ms的尾块未并入前块")
	}
	asset = speechSource(t, tools, "fractional-quantized.wav", "-f", "lavfi", "-i", "sine=frequency=440:duration=1.00004:sample_rate=44100", "-c:a", "pcm_s16le")
	result, err = tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(t.TempDir(), "prepared"), SpeechAudioOptions{})
	if err != nil {
		t.Fatal(err)
	}
	part := result.Parts[0]
	if math.Abs(float64(part.AudioDurationUs-part.DurationUs)) > 63 || result.CoverageEndUs > asset.DurationUs {
		t.Fatalf("采样量化尾差未明确受限：%+v", result)
	}
}

func TestSpeechAudioRejectsNoTrackAndInternalGap(t *testing.T) {
	tools := speechIntegrationTools(t)
	asset := speechSource(t, tools, "silent.mp4", "-f", "lavfi", "-i", "color=black:s=32x32:r=25:d=1", "-an", "-c:v", "libx264")
	parent := t.TempDir()
	if _, err := tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(parent, "prepared"), SpeechAudioOptions{}); err == nil {
		t.Fatal("没有音轨不能伪造语音")
	}
	asset = speechSource(t, tools, "gapped.mkv", "-f", "lavfi", "-i", "sine=frequency=440:duration=1.2:sample_rate=48000", "-af", "aselect=not(between(t\\,0.4\\,0.8))", "-c:a", "pcm_s16le")
	_, err := tools.PrepareSpeechAudio(context.Background(), asset, filepath.Join(parent, "prepared"), SpeechAudioOptions{})
	if err == nil || !strings.Contains(err.Error(), "PTS") {
		t.Fatalf("内部时间缺口不能被压缩或填静音而宣称对齐：%v", err)
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatal("拒绝结果遗留未发布准备目录")
	}
}

func TestSpeechAudioCancellationChangeAndNoOverwrite(t *testing.T) {
	tools := speechIntegrationTools(t)
	for _, stage := range []string{"extracting", "committing", "completed"} {
		t.Run(stage, func(t *testing.T) {
			asset := speechSource(t, tools, "source.wav", "-f", "lavfi", "-i", "sine=frequency=440:duration=2.3:sample_rate=48000", "-c:a", "pcm_s16le")
			parent := t.TempDir()
			target := filepath.Join(parent, "prepared")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result, err := tools.PrepareSpeechAudio(ctx, asset, target, SpeechAudioOptions{ChunkDurationUs: 1_000_000, Progress: func(value SpeechAudioProgress) {
				if value.Stage == stage {
					cancel()
				}
			}})
			if stage == "completed" {
				if err != nil || len(result.Parts) == 0 {
					t.Fatalf("发布成功后晚取消不能撤回已发布结果：%v", err)
				}
			} else {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("取消没有真实传播：%v", err)
				}
				entries, _ := os.ReadDir(parent)
				if len(entries) != 0 {
					t.Fatal("取消遗留未发布文件")
				}
			}
		})
	}
	asset := speechSource(t, tools, "changed.wav", "-f", "lavfi", "-i", "sine=frequency=440:duration=1:sample_rate=48000", "-c:a", "pcm_s16le")
	parent := t.TempDir()
	target := filepath.Join(parent, "prepared")
	_, err := tools.PrepareSpeechAudio(context.Background(), asset, target, SpeechAudioOptions{Progress: func(value SpeechAudioProgress) {
		if value.Stage == "checking_source" && value.SampleCount > 0 {
			file, err := os.OpenFile(asset.Path, os.O_WRONLY, 0)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = file.WriteAt([]byte{42}, 100)
			file.Close()
		}
	}})
	if err == nil {
		t.Fatal("源文件提取期间改变不能发布结果")
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatal("源变化后未清理准备文件")
	}
	asset = speechSource(t, tools, "race.wav", "-f", "lavfi", "-i", "sine=frequency=440:duration=1:sample_rate=48000", "-c:a", "pcm_s16le")
	_, err = tools.PrepareSpeechAudio(context.Background(), asset, target, SpeechAudioOptions{Progress: func(value SpeechAudioProgress) {
		if value.Stage == "committing" {
			if err := os.Mkdir(target, 0700); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(filepath.Join(target, "old"), []byte("preserved"), 0600); err != nil {
				t.Error(err)
			}
		}
	}})
	old, _ := os.ReadFile(filepath.Join(target, "old"))
	if err == nil || string(old) != "preserved" {
		t.Fatal("并发出现的原目标被覆盖")
	}
}

func TestSpeechAudioLimitsAndFallbackCleanup(t *testing.T) {
	for _, options := range []SpeechAudioOptions{{ChunkDurationUs: 99999}, {ChunkDurationUs: 100001}, {ChunkDurationUs: 31 * 60 * 1_000_000}, {MaxDurationUs: SpeechMaxDurationUs + 1}} {
		if _, err := normalizeSpeechOptions(options); err == nil {
			t.Fatal("非法资源限制未拒绝")
		}
	}
	for _, cancelSecond := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		parent := t.TempDir()
		stage := filepath.Join(parent, "stage")
		_ = os.Mkdir(stage, 0700)
		parts := []SpeechAudioPart{{ID: "part-1"}, {ID: "part-2"}}
		for _, part := range parts {
			if err := os.WriteFile(filepath.Join(stage, part.ID+".wav"), []byte("owned-pcm"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		ops := defaultCommitOperations()
		ops.renameNoReplace = func(string, string) error { return errNoReplaceUnsupported }
		count := 0
		ops.copy = func(ctx context.Context, dst io.Writer, src io.Reader) error {
			count++
			if count == 2 && cancelSecond {
				cancel()
				return ctx.Err()
			}
			return copyContext(ctx, dst, src)
		}
		target := filepath.Join(parent, "prepared")
		err := publishSpeechDirectory(ctx, stage, target, parts, ops)
		if cancelSecond {
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("fallback取消状态错误：%v", err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("fallback取消遗留目录或完整前块")
			}
		} else {
			data, _ := os.ReadFile(filepath.Join(target, "part-1.wav"))
			if err != nil || !bytes.Equal(data, []byte("owned-pcm")) {
				t.Fatal("不支持排他rename时不能安全发布")
			}
		}
		cancel()
	}
}

func TestSpeechAudioToolEvidenceBounds(t *testing.T) {
	cancelled := false
	log := &speechFrameLog{cancel: func() { cancelled = true }, tolerance: 2, maxSamples: 320}
	_, _ = log.Write([]byte("[ashowinfo@aividcut_speech @ 0x123] n:0 pts:0 fmt:s16 channels:1 rate:16000 nb_samples:160\n"))
	_, _ = log.Write([]byte("[ashowinfo@aividcut_speech @ 0x123] n:1 pts:900 fmt:s16 channels:1 rate:16000 nb_samples:160\n"))
	if _, err := log.result(); err == nil || !cancelled {
		t.Fatal("真实PTS证据出现缺口必须停止工具")
	}
	cancelled = false
	log = &speechFrameLog{cancel: func() { cancelled = true }}
	_, _ = log.Write(bytes.Repeat([]byte("x"), (64<<10)+1))
	if _, err := log.result(); err == nil || !cancelled || len(log.pending) > 64<<10 {
		t.Fatal("工具日志行必须有界并取消生产者")
	}
	cancelled = false
	limited := &speechLimitedWriter{cancel: func() { cancelled = true }}
	if _, err := limited.Write(make([]byte, (4<<20)+1)); err == nil || !cancelled || len(limited.buffer.data) > 4<<20 {
		t.Fatal("ffprobe JSON必须有界并停止生产者")
	}
}
