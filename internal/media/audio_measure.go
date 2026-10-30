package media

import (
	"aividcut/internal/managedprocess"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"aividcut/internal/editor"
)

// AudioMeasurement是实际解码样本证据，SourceStartUs使用与准备/渲染相同的FFmpeg源时间原点。
type AudioMeasurement struct {
	SampleRate                int    `json:"sampleRate"`
	Channels                  int    `json:"channels"`
	SampleCount               int64  `json:"sampleCount"`
	SourceStartUs             int64  `json:"sourceStartUs"`
	FrameCount                int64  `json:"frameCount"`
	SourceTimeBase            string `json:"sourceTimeBase"`
	TimestampToleranceSamples int64  `json:"timestampToleranceSamples"`
}
type measuredAudioLog struct {
	mu       sync.Mutex
	pending  []byte
	prefix   string
	cancel   context.CancelFunc
	evidence AudioMeasurement
	firstPTS int64
	err      error
}

func (log *measuredAudioLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	count := len(data)
	if log.err != nil {
		return count, nil
	}
	if len(log.pending)+len(data) > 64<<10 {
		log.err = errors.New("采样探测日志单行超过64KiB")
		log.cancel()
		return count, nil
	}
	log.pending = append(log.pending, data...)
	for {
		index := bytes.IndexByte(log.pending, '\n')
		if index < 0 {
			break
		}
		line := string(log.pending[:index])
		log.pending = log.pending[index+1:]
		if err := log.line(line); err != nil {
			log.err = err
			log.cancel()
			break
		}
	}
	return count, nil
}
func (log *measuredAudioLog) line(line string) error {
	if !strings.HasPrefix(line, log.prefix) || !strings.Contains(line, "nb_samples:") {
		return nil
	}
	fields := map[string]string{}
	for _, field := range strings.Fields(line) {
		if key, value, ok := strings.Cut(field, ":"); ok {
			fields[key] = value
		}
	}
	pts, ptsErr := strconv.ParseInt(fields["pts"], 10, 64)
	samples, sampleErr := strconv.ParseInt(fields["nb_samples"], 10, 64)
	number, numberErr := strconv.ParseInt(fields["n"], 10, 64)
	rate, rateErr := strconv.Atoi(fields["rate"])
	channels, channelErr := strconv.Atoi(fields["channels"])
	if ptsErr != nil || sampleErr != nil || numberErr != nil || rateErr != nil || channelErr != nil || pts < 0 || samples <= 0 || samples > int64(log.evidence.SampleRate)*10 || number != log.evidence.FrameCount || rate != log.evidence.SampleRate || channels != log.evidence.Channels {
		return errors.New("实际音频帧的PTS、采样率、声道或样本数不一致")
	}
	if log.evidence.FrameCount == 0 {
		log.firstPTS = pts
	}
	delta := pts - log.firstPTS - log.evidence.SampleCount
	if delta < -log.evidence.TimestampToleranceSamples || delta > log.evidence.TimestampToleranceSamples {
		return errors.New("源音轨含内部时间缺口/重叠，未压缩或补造样本")
	}
	if log.evidence.SampleCount > int64(log.evidence.SampleRate)*24*3600-samples {
		return errors.New("单次采样探测超过24小时上限")
	}
	log.evidence.SampleCount += samples
	log.evidence.FrameCount++
	return nil
}
func (log *measuredAudioLog) result() (AudioMeasurement, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.err == nil && len(bytes.TrimSpace(log.pending)) > 0 {
		log.err = log.line(string(log.pending))
	}
	if log.err != nil {
		return log.evidence, log.err
	}
	if log.evidence.FrameCount > 0 {
		value := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(log.firstPTS), big.NewInt(1000000)), big.NewInt(int64(log.evidence.SampleRate)))
		// 整数微秒仅是样本0的源时钟锚，样本长度始终保留整数count/rate，不由时长反推。
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(value.Num(), value.Denom(), r)
		if new(big.Int).Mul(r, big.NewInt(2)).Cmp(value.Denom()) >= 0 {
			q.Add(q, big.NewInt(1))
		}
		if !q.IsInt64() {
			return log.evidence, errors.New("音频起点溢出")
		}
		log.evidence.SourceStartUs = q.Int64()
	}
	return log.evidence, nil
}

// MeasureAudioSamples解码一次受控本地文件，逐帧累加nb_samples；不使用duration*rate伪报实测值。
func (t Toolchain) MeasureAudioSamples(ctx context.Context, asset editor.Asset) (AudioMeasurement, error) {
	var evidence AudioMeasurement
	if err := ctx.Err(); err != nil {
		return evidence, err
	}
	if t.FFmpegPath == "" || t.FFprobePath == "" || !asset.HasAudio {
		return evidence, errors.New("实际采样探测需要可用的FFmpeg/ffprobe与真实音轨")
	}
	before, err := os.Stat(asset.Path)
	if err != nil || !before.Mode().IsRegular() {
		return evidence, errors.New("采样探测源不是可读普通文件")
	}
	data, err := runOutput(ctx, t.FFprobePath, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=sample_rate,channels,time_base", "-of", "json", "-i", asset.Path)
	if err != nil {
		if ctx.Err() != nil {
			return evidence, ctx.Err()
		}
		return evidence, errors.New("无法读取源音轨采样时钟")
	}
	var metadata struct {
		Streams []struct {
			Rate     string `json:"sample_rate"`
			Channels int    `json:"channels"`
			TimeBase string `json:"time_base"`
		} `json:"streams"`
	}
	if json.Unmarshal(data, &metadata) != nil || len(metadata.Streams) != 1 {
		return evidence, errors.New("源音轨采样元数据无效")
	}
	stream := metadata.Streams[0]
	rate, err := strconv.Atoi(stream.Rate)
	if err != nil || rate < 8000 || rate > 192000 || stream.Channels < 1 || stream.Channels > 8 {
		return evidence, errors.New("当前实际采样探测支持8–192kHz、1–8声道")
	}
	n, d := parseRate(stream.TimeBase)
	if n < 1 || d < 1 {
		return evidence, errors.New("源音轨时间基无效")
	}
	tolerance := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(int64(rate)), big.NewInt(int64(n))), big.NewInt(int64(d)))
	if tolerance.Cmp(new(big.Rat).SetFrac64(int64(rate), 1000)) > 0 {
		return evidence, errors.New("源时间基粗于1ms，当前不能证明连续采样时钟")
	}
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(tolerance.Num(), tolerance.Denom(), r)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	evidence = AudioMeasurement{SampleRate: rate, Channels: stream.Channels, SourceTimeBase: stream.TimeBase, TimestampToleranceSamples: max(int64(1), q.Int64())}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	label := "aividcut_measure_" + newID()
	log := &measuredAudioLog{prefix: "[ashowinfo@" + label + " @ ", cancel: cancel, evidence: evidence}
	command := managedprocess.CommandContext(child, t.FFmpegPath, "-hide_banner", "-loglevel", "info", "-nostats", "-nostdin", "-threads", "1", "-filter_threads", "1", "-protocol_whitelist", "file,pipe", "-i", asset.Path, "-map", "0:a:0", "-vn", "-sn", "-dn", "-af", "asettb=expr=1/"+strconv.Itoa(rate)+",ashowinfo@"+label, "-f", "null", "-")
	command.Stderr = log
	command.WaitDelay = 2 * time.Second
	runErr := command.Run()
	if ctx.Err() != nil {
		return evidence, ctx.Err()
	}
	measured, logErr := log.result()
	if logErr != nil {
		return measured, logErr
	}
	if runErr != nil {
		return measured, errors.New("实际音频解码计数失败，未保存推算的样本数")
	}
	after, err := os.Stat(asset.Path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return measured, errors.New("采样探测期间源文件发生变化")
	}
	return measured, nil
}
