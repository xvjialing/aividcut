package media

import (
	"aividcut/internal/managedprocess"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aividcut/internal/editor"
)

// 仅接受自包含媒体容器；不启用playlist、concat、网络或任意目录引用。
const speechInputFormats = "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,wav,mp3,aac,flac,ogg,aiff,avi,mpeg,mpegts"

func normalizeSpeechOptions(options SpeechAudioOptions) (SpeechAudioOptions, error) {
	if options.ChunkDurationUs == 0 {
		options.ChunkDurationUs = DefaultSpeechChunkUs
	}
	if options.MaxDurationUs == 0 {
		options.MaxDurationUs = DefaultSpeechMaxDurationUs
	}
	if options.ChunkDurationUs < 100_000 || options.ChunkDurationUs > 30*60*1_000_000 || options.ChunkDurationUs%1000 != 0 {
		return options, errors.New("语音分块时长需为100ms到30分钟之间的整数毫秒")
	}
	if options.MaxDurationUs < 100_000 || options.MaxDurationUs > SpeechMaxDurationUs {
		return options, errors.New("语音提取总时长限制需在100ms到24小时之间")
	}
	return options, nil
}

// PrepareSpeechAudio 只读取指定素材，用一个解码进程顺序生成有界PCM分块。
// directory 必须不存在；成功发布后才返回路径，失败/取消只清理本次私有结果。
func (t Toolchain) PrepareSpeechAudio(ctx context.Context, asset editor.Asset, directory string, options SpeechAudioOptions) (SpeechAudioPreparation, error) {
	var empty SpeechAudioPreparation
	options, err := normalizeSpeechOptions(options)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if t.FFmpegPath == "" || t.FFprobePath == "" {
		return empty, errors.New("语音提取需要显式配置FFmpeg和ffprobe")
	}
	if asset.ID == "" || (asset.Kind != "audio" && asset.Kind != "video") || !filepath.IsAbs(asset.Path) || asset.DurationUs <= 0 {
		return empty, errors.New("语音提取需要已登记的本地音视频素材和有效源时长")
	}
	expected, err := speechFingerprint(asset.Fingerprint)
	if err != nil {
		return empty, err
	}
	if !filepath.IsAbs(directory) {
		return empty, errors.New("语音准备目录必须为绝对路径且尚不存在")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(directory)))
	if err != nil {
		return empty, errors.New("语音准备目录的父目录不可用")
	}
	target := filepath.Join(parent, filepath.Base(filepath.Clean(directory)))
	if _, err = os.Lstat(target); err == nil || !errors.Is(err, os.ErrNotExist) {
		return empty, errors.New("语音准备目标已存在或不可安全创建，不覆盖原目录")
	}
	source, err := filepath.EvalSymlinks(asset.Path)
	if err != nil {
		return empty, errors.New("已登记源文件不可读取")
	}
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return empty, errors.New("语音提取只接受非空本地普通文件")
	}
	emitSpeechProgress(options, SpeechAudioProgress{Stage: "probing"})
	metadata, err := t.speechSourceDuration(ctx, source)
	if err != nil {
		return empty, err
	}
	duration := metadata.duration
	if duration != asset.DurationUs {
		return empty, errors.New("源素材真实时长与登记不一致，请重新导入确认")
	}
	if duration < 100_000 || duration > options.MaxDurationUs {
		return empty, errors.New("源时长不在本轮100ms到配置上限之间；默认6小时，最高24小时")
	}
	totalSamples := speechSamples(duration)
	emitSpeechProgress(options, SpeechAudioProgress{Stage: "checking_source"})
	before, err := fingerprint(ctx, source)
	if err != nil {
		return empty, err
	}
	if before != expected || !sameSpeechSource(asset.Path, source, info) {
		return empty, errors.New("源素材指纹或文件身份已变化，请重新确认")
	}
	stage, err := os.MkdirTemp(parent, ".aividcut-speech-prepare-")
	if err != nil {
		return empty, errors.New("无法创建私有语音准备目录")
	}
	stageInfo, err := os.Stat(stage)
	if err != nil {
		os.Remove(stage)
		return empty, err
	}
	defer func() {
		current, err := os.Lstat(stage)
		if err == nil && os.SameFile(stageInfo, current) {
			_ = os.RemoveAll(stage)
		}
	}()
	parts, evidence, err := t.decodeSpeechParts(ctx, source, stage, totalSamples, metadata.trimStart, metadata.trimEnd, metadata.toleranceSamples, options)
	if err != nil {
		return empty, err
	}
	if evidence.samples < SpeechSampleRate/10 {
		return empty, errors.New("实际音轨不足100ms，未补造静音以满足转写长度")
	}
	audioOffset := speechSampleDuration(evidence.firstPTS)
	coverageEnd := speechSampleDuration(evidence.firstPTS + evidence.samples)
	if coverageEnd > metadata.trimEnd {
		if coverageEnd-metadata.trimEnd > 63 {
			return empty, errors.New("实际音轨覆盖超出源时长")
		}
		coverageEnd = metadata.trimEnd
	}
	if audioOffset < 0 || audioOffset >= coverageEnd {
		return empty, errors.New("实际音轨覆盖范围无效")
	}
	var consumed int64
	for index := range parts {
		parts[index].OffsetUs = speechSampleDuration(evidence.firstPTS + consumed)
		consumed += parts[index].SampleCount
		end := speechSampleDuration(evidence.firstPTS + consumed)
		if end > coverageEnd {
			end = coverageEnd
		}
		parts[index].DurationUs = end - parts[index].OffsetUs
		path := filepath.Join(stage, parts[index].ID+".wav")
		physical, err := t.verifySpeechPart(ctx, path, parts[index].SampleCount)
		if err != nil {
			return empty, err
		}
		parts[index].AudioDurationUs = physical
		parts[index].Fingerprint, err = fingerprint(ctx, path)
		if err != nil {
			return empty, err
		}
	}
	emitSpeechProgress(options, SpeechAudioProgress{Stage: "checking_source", SampleCount: evidence.samples, TotalSamples: evidence.samples})
	after, err := fingerprint(ctx, source)
	if err != nil {
		return empty, err
	}
	if after != before || !sameSpeechSource(asset.Path, source, info) {
		return empty, errors.New("提取期间源素材发生变化，未发布语音分块")
	}
	emitSpeechProgress(options, SpeechAudioProgress{Stage: "committing", SampleCount: evidence.samples, TotalSamples: evidence.samples})
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	currentParent, parentErr := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(directory)))
	if parentErr != nil || currentParent != parent {
		return empty, errors.New("语音输出父目录在处理期间已变化，未发布结果")
	}
	if err = publishSpeechDirectory(ctx, stage, target, parts, defaultCommitOperations()); err != nil {
		return empty, err
	}
	for index := range parts {
		parts[index].Path = filepath.Join(filepath.Clean(directory), parts[index].ID+".wav")
	}
	warnings := []string{}
	if metadata.toleranceSamples > 2 {
		warnings = append(warnings, fmt.Sprintf("源音轨时间基存在量化，逐帧连续性以%d微秒容差核查", speechSampleDuration(metadata.toleranceSamples)))
	}
	if audioOffset > 0 {
		warnings = append(warnings, fmt.Sprintf("音轨从源时间%d微秒开始，开头无音轨区间未补静音", audioOffset))
	}
	if coverageEnd < duration {
		warnings = append(warnings, fmt.Sprintf("实际音轨覆盖到源时间%d微秒，原素材其后的区间未补静音", coverageEnd))
	}
	if len(parts) > 1 {
		warnings = append(warnings, "固定分块边界可能切断词句，边界转写质量仍待真实人声核查")
	}
	result := SpeechAudioPreparation{SourceAssetID: asset.ID, SourceFingerprint: before, SourceDurationUs: duration, SampleRate: SpeechSampleRate, Channels: SpeechChannels, AudioOffsetUs: audioOffset, CoverageEndUs: coverageEnd, Warnings: warnings, TimestampToleranceUs: speechSampleDuration(metadata.toleranceSamples), Parts: parts}
	// 与媒体导出相同：发布成功为提交点，之后的晚取消不删除已发布结果。
	emitSpeechProgress(options, SpeechAudioProgress{Stage: "completed", PartIndex: len(parts) - 1, SampleCount: evidence.samples, TotalSamples: evidence.samples})
	return result, nil
}

func emitSpeechProgress(options SpeechAudioOptions, value SpeechAudioProgress) {
	if options.Progress != nil {
		options.Progress(value)
	}
}
func speechSamples(duration int64) int64 { return (duration*SpeechSampleRate + 999_999) / 1_000_000 }
func speechSampleDuration(samples int64) int64 {
	return (samples*1_000_000 + SpeechSampleRate/2) / SpeechSampleRate
}

func speechFingerprint(value string) (string, error) {
	value = strings.TrimPrefix(strings.ToLower(value), "sha256:")
	if len(value) != 64 {
		return "", errors.New("语音源素材缺少有效SHA256登记指纹")
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return "", errors.New("语音源素材SHA256指纹无效")
		}
	}
	return "sha256:" + value, nil
}
func sameSpeechSource(original, resolved string, before os.FileInfo) bool {
	current, err := filepath.EvalSymlinks(original)
	if err != nil || current != resolved {
		return false
	}
	after, err := os.Stat(resolved)
	return err == nil && after.Mode().IsRegular() && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

type speechSourceMetadata struct{ duration, toleranceSamples, trimStart, trimEnd int64 }

func (t Toolchain) speechSourceDuration(ctx context.Context, path string) (speechSourceMetadata, error) {
	data, err := speechRunOutput(ctx, t.FFprobePath, "-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", speechInputFormats, "-show_streams", "-show_format", "-of", "json", "-i", path)
	if err != nil {
		return speechSourceMetadata{}, fmt.Errorf("语音源媒体探测失败：%w", err)
	}
	var value probeResult
	if json.Unmarshal(data, &value) != nil {
		return speechSourceMetadata{}, errors.New("语音源媒体元数据无法解析")
	}
	duration := secondsToUs(value.Format.Duration)
	hasAudio := false
	var timeBase string
	var audioStart, audioDuration string
	for _, stream := range value.Streams {
		if stream.CodecType == "audio" {
			if !hasAudio {
				timeBase = stream.TimeBase
				audioStart, audioDuration = stream.StartTime, stream.Duration
			}
			hasAudio = true
		}
		if us := secondsToUs(stream.Duration); us > duration {
			duration = us
		}
	}
	if !hasAudio {
		return speechSourceMetadata{}, errors.New("源素材没有可提取的音轨")
	}
	if duration <= 0 {
		return speechSourceMetadata{}, errors.New("源素材没有可核查的真实时长")
	}
	num, den := parseRate(timeBase)
	if num == 0 || float64(num)*1e6/float64(den) > 10_000 {
		return speechSourceMetadata{}, errors.New("源音轨时间基精度不足或无法核查，暂不宣称源时间对齐")
	}
	tolerance := int64(math.Ceil(float64(num)*SpeechSampleRate/float64(den))) + 1
	start, end := int64(0), duration
	if audioStartValue, known := speechSignedTime(audioStart); known {
		formatStart, _ := speechSignedTime(value.Format.StartTime)
		start = max(int64(0), audioStartValue-formatStart)
		if audioLength := secondsToUs(audioDuration); audioLength > 0 {
			end = min(duration, max(int64(0), audioStartValue-formatStart+audioLength))
		}
	}
	if end <= start {
		return speechSourceMetadata{}, errors.New("登记源范围内没有有效音轨区间")
	}
	return speechSourceMetadata{duration: duration, toleranceSamples: tolerance, trimStart: start, trimEnd: end}, nil
}

func speechSignedTime(text string) (int64, bool) {
	seconds, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || math.Abs(seconds) > 1e10 {
		return 0, false
	}
	return int64(math.Round(seconds * 1e6)), true
}

func (t Toolchain) decodeSpeechParts(ctx context.Context, path, directory string, total, start, end, tolerance int64, options SpeechAudioOptions) ([]SpeechAudioPart, speechFrameEvidence, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	filter := "aresample=16000,aformat=sample_fmts=s16:sample_rates=16000:channel_layouts=mono,atrim=start=" + strconv.FormatFloat(float64(start)/1e6, 'f', 6, 64) + ":end=" + strconv.FormatFloat(float64(end)/1e6, 'f', 6, 64) + ",asettb=expr=1/16000,ashowinfo@aividcut_speech"
	args := []string{"-hide_banner", "-loglevel", "info", "-nostats", "-nostdin", "-xerror", "-threads", "1", "-filter_threads", "1", "-copyts", "-start_at_zero", "-protocol_whitelist", "file,pipe", "-format_whitelist", speechInputFormats, "-i", path, "-map", "0:a:0", "-vn", "-sn", "-dn", "-af", filter, "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1"}
	command := managedprocess.CommandContext(child, t.FFmpegPath, args...)
	command.Dir = directory
	command.WaitDelay = 2 * time.Second
	stderr := &speechFrameLog{cancel: cancel, tolerance: tolerance, maxSamples: total}
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, speechFrameEvidence{}, err
	}
	if err = command.Start(); err != nil {
		return nil, speechFrameEvidence{}, err
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			stdout.Close()
			_ = command.Wait()
		}
	}()
	var parts []SpeechAudioPart
	var written int64
	for index := 0; index < SpeechMaxParts; index++ {
		if err = ctx.Err(); err != nil {
			break
		}
		remaining := total - written
		if remaining <= 0 {
			break
		}
		maximum := speechSamples(options.ChunkDurationUs)
		if maximum > remaining {
			maximum = remaining
		}
		id := fmt.Sprintf("part-%06d", index+1)
		filePath := filepath.Join(directory, id+".wav")
		var samples int64
		samples, err = writeSpeechWAV(ctx, stdout, filePath, maximum)
		if err != nil {
			break
		}
		if samples == 0 {
			if err = os.Remove(filePath); err != nil {
				break
			}
			break
		}
		written += samples
		if written > total {
			err = errors.New("语音提取输出超过核定源时长的样本上限")
			break
		}
		parts = append(parts, SpeechAudioPart{ID: id, SampleCount: samples})
		emitSpeechProgress(options, SpeechAudioProgress{Stage: "extracting", PartIndex: index, SampleCount: written})
		if samples < maximum {
			break
		}
	}
	if err == nil {
		var extra [1]byte
		var count int
		count, err = stdout.Read(extra[:])
		if count > 0 {
			if len(parts) == SpeechMaxParts {
				err = errors.New("语音提取超过1024块上限")
			} else {
				err = errors.New("语音提取进程输出超过核定样本总量")
			}
		} else if errors.Is(err, io.EOF) {
			err = nil
		}
	}
	if err != nil {
		cancel()
		stdout.Close()
	}
	waitErr := command.Wait()
	waited = true
	if ctx.Err() != nil {
		return nil, speechFrameEvidence{}, ctx.Err()
	}
	evidence, logErr := stderr.result()
	if logErr != nil {
		return nil, evidence, logErr
	}
	if err != nil {
		return nil, evidence, fmt.Errorf("语音PCM提取失败：%w", err)
	}
	if waitErr != nil {
		return nil, evidence, fmt.Errorf("语音PCM工具失败：%w：%s", waitErr, stderr.tail.String())
	}
	if evidence.samples != written || written == 0 {
		return nil, evidence, errors.New("实际PCM样本数与逐帧时间戳证据不一致或音轨为空")
	}
	if len(parts) > 1 && parts[len(parts)-1].SampleCount < SpeechSampleRate/10 {
		last := parts[len(parts)-1]
		previous := &parts[len(parts)-2]
		if err = mergeSpeechTail(ctx, directory, *previous, last); err != nil {
			return nil, evidence, err
		}
		previous.SampleCount += last.SampleCount
		parts = parts[:len(parts)-1]
	}
	return parts, evidence, nil
}

func writeSpeechWAV(ctx context.Context, source io.Reader, path string, maximum int64) (int64, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	if _, err = file.Write(make([]byte, 44)); err != nil {
		return 0, err
	}
	written, err := io.CopyBuffer(file, &speechContextReader{ctx: ctx, reader: io.LimitReader(source, maximum*2)}, make([]byte, 64<<10))
	if err != nil {
		return 0, err
	}
	if written%2 != 0 {
		return 0, errors.New("PCM输出含不完整的16位采样")
	}
	samples := written / 2
	if _, err = file.WriteAt(speechWAVHeader(samples), 0); err != nil {
		return 0, err
	}
	if err = file.Sync(); err != nil {
		return 0, err
	}
	return samples, file.Close()
}

func speechWAVHeader(samples int64) []byte {
	dataBytes := samples * 2
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+dataBytes))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], SpeechSampleRate)
	binary.LittleEndian.PutUint32(header[28:], SpeechSampleRate*2)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(dataBytes))
	return header
}

type speechContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *speechContextReader) Read(value []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(value)
}

func mergeSpeechTail(ctx context.Context, directory string, previous, last SpeechAudioPart) error {
	if 44+(previous.SampleCount+last.SampleCount)*2 >= SpeechPartMaxBytes {
		return errors.New("合并短尾块将达到64MiB限制")
	}
	path := filepath.Join(directory, previous.ID+".wav")
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	tailPath := filepath.Join(directory, last.ID+".wav")
	tail, err := os.Open(tailPath)
	if err != nil {
		return err
	}
	defer tail.Close()
	if _, err = tail.Seek(44, io.SeekStart); err != nil {
		return err
	}
	if _, err = file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if err = copyContext(ctx, file, io.LimitReader(tail, last.SampleCount*2)); err != nil {
		return err
	}
	if _, err = file.WriteAt(speechWAVHeader(previous.SampleCount+last.SampleCount), 0); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = tail.Close(); err != nil {
		return err
	}
	return os.Remove(tailPath)
}

func (t Toolchain) verifySpeechPart(ctx context.Context, path string, samples int64) (int64, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() != 44+samples*2 || info.Size() >= SpeechPartMaxBytes {
		return 0, errors.New("提取WAV尺寸或样本总量无效")
	}
	data, err := speechRunOutput(ctx, t.FFprobePath, "-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", "wav", "-show_streams", "-of", "json", "-i", path)
	if err != nil {
		return 0, err
	}
	var value struct {
		Streams []struct {
			Codec    string `json:"codec_name"`
			Rate     string `json:"sample_rate"`
			Channels int    `json:"channels"`
			TimeBase string `json:"time_base"`
			Samples  *int64 `json:"duration_ts"`
			Duration string `json:"duration"`
		} `json:"streams"`
	}
	if json.Unmarshal(data, &value) != nil || len(value.Streams) != 1 {
		return 0, errors.New("提取WAV缺少唯一可核查音轨")
	}
	stream := value.Streams[0]
	duration := secondsToUs(stream.Duration)
	if stream.Codec != "pcm_s16le" || stream.Rate != "16000" || stream.Channels != 1 || stream.TimeBase != "1/16000" || stream.Samples == nil || *stream.Samples != samples || duration <= 0 || math.Abs(float64(duration-speechSampleDuration(samples))) > 1 {
		return 0, errors.New("ffprobe核查的PCM格式、样本或时长不一致")
	}
	return duration, nil
}

type speechLimitedWriter struct {
	buffer limitedBuffer
	cancel context.CancelFunc
	err    error
}

func (w *speechLimitedWriter) Write(data []byte) (int, error) {
	n, err := w.buffer.Write(data)
	if err != nil {
		w.err = err
		w.cancel()
	}
	return n, err
}

func speechRunOutput(ctx context.Context, binary string, args ...string) ([]byte, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	command := managedprocess.CommandContext(child, binary, args...)
	command.WaitDelay = 2 * time.Second
	output := &speechLimitedWriter{cancel: cancel}
	var stderr tailBuffer
	command.Stdout, command.Stderr = output, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if output.err != nil {
		return nil, output.err
	}
	if err != nil {
		return nil, fmt.Errorf("%w：%s", err, stderr.String())
	}
	return output.buffer.data, nil
}
