package media

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAudioMeasurementRealWAVAndMP3SkipPadding(t *testing.T) {
	tools := speechIntegrationTools(t)
	values := make([]int16, 5777)
	for i := range values {
		values[i] = int16(12000 * math.Sin(float64(i)*2*math.Pi*440/48000))
	}
	wav := exactWAV(t, "measured-5777", 48000, values)
	measured, err := tools.MeasureAudioSamples(context.Background(), wav)
	if err != nil || measured.SampleCount != 5777 || measured.SampleRate != 48000 || measured.Channels != 1 || measured.SourceStartUs != 0 || measured.FrameCount < 1 {
		t.Fatalf("WAV真实样本不符：%+v %v", measured, err)
	}
	mp3Path := filepath.Join(t.TempDir(), "measured-5777.mp3")
	command(t, tools, "-hide_banner", "-loglevel", "error", "-i", wav.Path, "-c:a", "libmp3lame", "-b:a", "128k", mp3Path)
	mp3, err := tools.Probe(context.Background(), mp3Path)
	if err != nil {
		t.Fatal(err)
	}
	mp3Count, err := tools.MeasureAudioSamples(context.Background(), mp3)
	if err != nil {
		t.Fatal(err)
	}
	decoded := command(t, tools, "-hide_banner", "-loglevel", "error", "-i", mp3Path, "-map", "0:a:0", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1")
	if len(decoded)%2 != 0 || mp3Count.SampleCount != int64(len(decoded)/2) || mp3Count.SampleCount <= 0 || mp3Count.SourceStartUs != 0 {
		t.Fatalf("MP3未遵守解码器skip/discard padding：%+v 实际PCM=%d", mp3Count, len(decoded)/2)
	}
	if mp3.DurationUs*int64(mp3Count.SampleRate)/1_000_000 == mp3Count.SampleCount {
		t.Fatal("夹具未证明容器duration与解码后样本数不同")
	}
	var packets struct {
		Packets []struct {
			Duration int64 `json:"duration"`
			SideData []struct {
				Skip    int64 `json:"skip_samples"`
				Discard int64 `json:"discard_padding"`
			} `json:"side_data_list"`
		} `json:"packets"`
	}
	raw := commandProbe(t, tools, "-v", "error", "-select_streams", "a:0", "-show_packets", "-show_entries", "packet=duration:packet_side_data=skip_samples,discard_padding", "-of", "json", mp3Path)
	if err := json.Unmarshal(raw, &packets); err != nil {
		t.Fatal(err)
	}
	var duration, skipped int64
	for _, packet := range packets.Packets {
		duration += packet.Duration
		for _, side := range packet.SideData {
			skipped += side.Skip + side.Discard
		}
	}
	num, den := parseRate(mp3Count.SourceTimeBase)
	packetSamples := duration * int64(num) * int64(mp3Count.SampleRate) / int64(den)
	if skipped <= 0 || packetSamples-skipped != mp3Count.SampleCount {
		t.Fatalf("实际计数未匹配逐包skip/discard证据：packet=%d skip/discard=%d measured=%d", packetSamples, skipped, mp3Count.SampleCount)
	}
	// 有损编码器可能保留额外尾样本；只认可当前解码器实际skip/discard后的输出，不冒称还原原WAV长度。
	t.Logf("MP3声明时长=%dµs，实解码=%d/%dHz（编码前WAV5777，非长度保证），起点=%dµs", mp3.DurationUs, mp3Count.SampleCount, mp3Count.SampleRate, mp3Count.SourceStartUs)
}

func TestAudioMeasurementRealLateMOVAndNonzeroFormatOrigin(t *testing.T) {
	tools := speechIntegrationTools(t)
	wav := exactWAV(t, "late-source", 48000, make([]int16, 5777))
	asset := speechSource(t, tools, "late-measured.mov", "-f", "lavfi", "-i", "color=black:s=32x32:r=25:d=2", "-itsoffset", "0.5", "-i", wav.Path, "-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-c:a", "pcm_s16le", "-output_ts_offset", "3")
	measured, err := tools.MeasureAudioSamples(context.Background(), asset)
	if err != nil || measured.SampleCount != 5777 || measured.SourceStartUs != 500000 || measured.SampleRate != 48000 {
		t.Fatalf("容器3s起点误入源零点或真实晚起丢失：%+v %v", measured, err)
	}
	metadata := commandProbe(t, tools, "-v", "error", "-show_entries", "format=start_time", "-of", "default=nw=1:nk=1", asset.Path)
	if !strings.HasPrefix(strings.TrimSpace(string(metadata)), "3.") {
		t.Fatalf("MOV夹具没有非零format起点：%s", metadata)
	}
}

func commandProbe(t *testing.T, tools Toolchain, args ...string) []byte {
	t.Helper()
	data, err := runOutput(context.Background(), tools.FFprobePath, args...)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAudioMeasurementRealInternalGapIsRejected(t *testing.T) {
	tools := speechIntegrationTools(t)
	wav := exactWAV(t, "gap-source", 48000, make([]int16, 4800))
	asset := speechSource(t, tools, "gap-measured.mkv", "-i", wav.Path, "-af", "asetnsamples=n=480:p=0,asetpts=PTS+gte(N\\,2400)*0.1/TB", "-c:a", "pcm_s16le")
	measured, err := tools.MeasureAudioSamples(context.Background(), asset)
	if err == nil || !strings.Contains(err.Error(), "缺口/重叠") {
		t.Fatalf("内部PTS空白被压成连续采样：%+v %v", measured, err)
	}
	if measured.SampleCount >= 4800 {
		t.Fatal("失败后仍报完整成功样本数")
	}
}

func TestAudioMeasurementCancelledContextAndRealDecodeCancellation(t *testing.T) {
	tools := speechIntegrationTools(t)
	wav := exactWAV(t, "cancel-source", 48000, []int16{1, 2, 3})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tools.MeasureAudioSamples(ctx, wav); !errors.Is(err, context.Canceled) {
		t.Fatalf("预先取消丢失context语义：%v", err)
	}
	// 只扩展稀疏WAV的数据区，不在内存构造/向磁盘写入8小时PCM。
	data, err := os.ReadFile(wav.Path)
	if err != nil {
		t.Fatal(err)
	}
	bytes := int64(48000 * 2 * 8 * 3600)
	binary.LittleEndian.PutUint32(data[4:], uint32(bytes+36))
	binary.LittleEndian.PutUint32(data[40:], uint32(bytes))
	if err := os.WriteFile(wav.Path, data[:44], 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(wav.Path, bytes+44); err != nil {
		t.Fatal(err)
	}
	wav.DurationUs = 8 * 3600 * 1_000_000
	ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := tools.MeasureAudioSamples(ctx, wav); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("真实解码取消未返回期限原因：%v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("取消后仍等待完整长音轨解码")
	}
}
