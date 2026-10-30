package media

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
)

type speechFrameEvidence struct{ firstPTS, samples, frames int64 }
type speechFrameLog struct {
	mu         sync.Mutex
	tail       tailBuffer
	pending    []byte
	evidence   speechFrameEvidence
	err        error
	tolerance  int64
	maxSamples int64
	firstOnly  bool
	prefix     string
	cancel     context.CancelFunc
}

func (log *speechFrameLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	count := len(data)
	_, _ = log.tail.Write(data)
	if log.err != nil {
		return count, nil
	}
	if len(log.pending)+len(data) > 64<<10 {
		log.err = errors.New("媒体工具日志单行超过64KiB，未继续解析")
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
		if err := log.frame(line); err != nil {
			log.err = err
			log.cancel()
			break
		}
	}
	return count, nil
}

func (log *speechFrameLog) frame(line string) error {
	prefix := log.prefix
	if prefix == "" {
		prefix = "[ashowinfo@aividcut_speech @ "
	}
	if !strings.HasPrefix(line, prefix) || !strings.Contains(line, "nb_samples:") {
		return nil
	}
	if log.firstOnly && log.evidence.frames > 0 {
		return nil
	}
	fields := map[string]string{}
	for _, field := range strings.Fields(line) {
		if key, value, found := strings.Cut(field, ":"); found {
			fields[key] = value
		}
	}
	pts, ptsErr := strconv.ParseInt(fields["pts"], 10, 64)
	samples, sampleErr := strconv.ParseInt(fields["nb_samples"], 10, 64)
	number, numberErr := strconv.ParseInt(fields["n"], 10, 64)
	if ptsErr != nil || sampleErr != nil || numberErr != nil || pts < 0 || samples <= 0 || samples > SpeechSampleRate*10 || fields["rate"] != "16000" || fields["channels"] != "1" || fields["fmt"] != "s16" || number != log.evidence.frames {
		return errors.New("音频逐帧PTS/采样证据无效，不能宣称源时间对齐")
	}
	if log.evidence.frames == 0 {
		log.evidence.firstPTS = pts
	}
	delta := pts - log.evidence.firstPTS - log.evidence.samples
	if delta < -log.tolerance || delta > log.tolerance {
		return errors.New("音轨含内部PTS缺口或重叠；当前不压缩时间或补长静音，需单独处理")
	}
	log.evidence.samples += samples
	if log.maxSamples > 0 && log.evidence.samples > log.maxSamples {
		return errors.New("逐帧音频证据超过源时长的样本总上限")
	}
	log.evidence.frames++
	return nil
}

func (log *speechFrameLog) result() (speechFrameEvidence, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.err == nil && len(bytes.TrimSpace(log.pending)) > 0 {
		log.err = log.frame(string(log.pending))
	}
	if log.err != nil {
		return log.evidence, log.err
	}
	if log.evidence.frames == 0 {
		return log.evidence, errors.New("未取得真实音频帧时间戳，不能确认提取覆盖")
	}
	return log.evidence, nil
}
