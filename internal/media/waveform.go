package media

import (
	"aividcut/internal/managedprocess"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"aividcut/internal/editor"
)

const WaveformTileUs int64 = 4_000_000

type WaveformPeak struct {
	Min     float32 `json:"min"`
	Max     float32 `json:"max"`
	Samples int     `json:"samples"`
}

// WaveformTile保留源音频时钟与真实样本覆盖；不表示最终混音或变速处理输出。
type WaveformTile struct {
	Index         int64          `json:"index"`
	SourceStartUs float64        `json:"sourceStartUs"`
	SampleRate    int            `json:"sampleRate"`
	Channels      int            `json:"channels"`
	BucketSamples int            `json:"bucketSamples"`
	SampleCount   int64          `json:"sampleCount"`
	Peaks         []WaveformPeak `json:"peaks"`
}

type waveformPCM struct {
	rate, channels, bucket int
	pending                []byte
	count                  int64
	peaks                  []WaveformPeak
}

func (p *waveformPCM) Write(data []byte) (int, error) {
	n := len(data)
	if p.count*int64(p.channels)*4+int64(len(p.pending))+int64(n) > 128<<20 {
		return 0, errors.New("单次波形解码超过128MiB预算")
	}
	p.pending = append(p.pending, data...)
	frameBytes := p.channels * 4
	complete := len(p.pending) / frameBytes * frameBytes
	for offset := 0; offset < complete; offset += frameBytes {
		if p.count >= int64(p.rate)*4 {
			return 0, errors.New("波形解码超过4秒窗口")
		}
		if p.count%int64(p.bucket) == 0 {
			p.peaks = append(p.peaks, WaveformPeak{Min: math.MaxFloat32, Max: -math.MaxFloat32})
		}
		peak := &p.peaks[len(p.peaks)-1]
		for ch := 0; ch < p.channels; ch++ {
			value := math.Float32frombits(binary.LittleEndian.Uint32(p.pending[offset+ch*4:]))
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return 0, errors.New("波形源样本含非有限数值")
			}
			peak.Min = min(peak.Min, value)
			peak.Max = max(peak.Max, value)
		}
		peak.Samples++
		p.count++
	}
	copy(p.pending, p.pending[complete:])
	p.pending = p.pending[:len(p.pending)-complete]
	return n, nil
}

func (t Toolchain) ExtractWaveformTile(ctx context.Context, asset editor.Asset, index int64) (WaveformTile, error) {
	empty := WaveformTile{Index: index, Peaks: []WaveformPeak{}}
	if index < 0 || index >= 24*3600*1_000_000/WaveformTileUs || !asset.HasAudio || (asset.Kind != "audio" && asset.Kind != "video") || !filepath.IsAbs(asset.Path) {
		return empty, errors.New("波形需要已登记的本地音视频素材与有效源窗口")
	}
	if t.FFmpegPath == "" || t.FFprobePath == "" {
		return empty, errors.New("波形需要可用的FFmpeg和ffprobe")
	}
	before, err := os.Stat(asset.Path)
	if err != nil || !before.Mode().IsRegular() {
		return empty, errors.New("波形源文件不可读取，请重新定位素材")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	data, err := runOutput(ctx, t.FFprobePath, "-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", speechInputFormats, "-select_streams", "a:0", "-show_entries", "stream=sample_rate,channels", "-of", "json", "-i", asset.Path)
	if err != nil {
		return empty, err
	}
	var metadata struct {
		Streams []struct {
			Rate     string `json:"sample_rate"`
			Channels int    `json:"channels"`
		} `json:"streams"`
	}
	if json.Unmarshal(data, &metadata) != nil || len(metadata.Streams) != 1 {
		return empty, errors.New("无法读取源音轨采样时钟")
	}
	rate, err := strconv.Atoi(metadata.Streams[0].Rate)
	channels := metadata.Streams[0].Channels
	if err != nil || rate < 8000 || rate > 192000 || channels < 1 || channels > 8 {
		return empty, errors.New("当前波形支持8–192kHz、1–8声道")
	}
	pcm := &waveformPCM{rate: rate, channels: channels, bucket: (rate + 99) / 100, peaks: []WaveformPeak{}}
	label := "aividcut_wave_" + newID()
	log := &measuredAudioLog{prefix: "[ashowinfo@" + label + " @ ", cancel: cancel, evidence: AudioMeasurement{SampleRate: rate, Channels: channels, TimestampToleranceSamples: max(1, int64(rate)/1000)}}
	// MP3 bit reservoir与AAC重叠窗需要预卷；窗口内只保留目标PTS的真实样本。
	seekSeconds := max(int64(0), index*4-1)
	preroll := index*4 - seekSeconds
	filter := "aformat=sample_fmts=flt,asettb=expr=1/" + strconv.Itoa(rate) + ",atrim=start=" + strconv.FormatInt(preroll, 10) + ":end=" + strconv.FormatInt(preroll+4, 10) + ",asetpts=PTS-" + strconv.FormatInt(preroll*int64(rate), 10) + ",ashowinfo@" + label
	args := []string{"-hide_banner", "-loglevel", "info", "-nostats", "-nostdin", "-threads", "1", "-filter_threads", "1", "-protocol_whitelist", "file,pipe", "-format_whitelist", speechInputFormats}
	// 首窗从容器头读取，显式-ss 0也会让AAC丢失负PTS的priming包。
	if seekSeconds > 0 {
		args = append(args, "-ss", strconv.FormatInt(seekSeconds, 10))
	}
	args = append(args, "-i", asset.Path, "-map", "0:a:0", "-vn", "-sn", "-dn", "-af", filter, "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
	command := managedprocess.CommandContext(ctx, t.FFmpegPath, args...)
	command.Stdout = pcm
	command.Stderr = log
	command.WaitDelay = 2 * time.Second
	runErr := command.Run()
	measurement, logErr := log.result()
	if logErr != nil {
		return empty, logErr
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if runErr != nil {
		return empty, errors.New("波形解码失败，请检查媒体工具与源音轨")
	}
	if len(pcm.pending) != 0 || pcm.count != measurement.SampleCount {
		return empty, errors.New("波形样本数与源时钟证据不一致")
	}
	after, err := os.Stat(asset.Path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return empty, errors.New("读取波形期间源文件发生变化，请重新导入或定位素材")
	}
	return WaveformTile{Index: index, SourceStartUs: float64(index*WaveformTileUs) + float64(log.firstPTS)*1e6/float64(rate), SampleRate: rate, Channels: channels, BucketSamples: pcm.bucket, SampleCount: pcm.count, Peaks: pcm.peaks}, nil
}
