package media

import (
	"aividcut/internal/managedprocess"
	"context"
	"errors"
	"fmt"
	"time"
)

type renderAudioKey struct {
	path       string
	sourceInUs int64
	duration   string
}
type renderAudioTiming struct {
	hasSamples  bool
	leadSeconds float64
}

// 使用与渲染相同的input seek和裁剪，读取实际首帧PTS。
// 不生成源长静音；仅将真实首帧延迟映射到已有时间线adelay。
func (t Toolchain) renderAudioTiming(ctx context.Context, path string, sourceInUs int64, duration float64) (renderAudioTiming, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	label := "aividcut_audio_" + newID()
	filter := "aresample=16000,aformat=sample_fmts=s16:sample_rates=16000:channel_layouts=mono,atrim=start=0:end=" + number(duration) + ",asettb=expr=1/16000,ashowinfo@" + label
	args := []string{"-hide_banner", "-loglevel", "info", "-nostats", "-nostdin", "-threads", "1", "-filter_threads", "1", "-protocol_whitelist", "file,pipe", "-ss", number(float64(sourceInUs) / 1e6), "-t", number(duration), "-i", path, "-map", "0:a:0", "-vn", "-sn", "-dn", "-af", filter, "-frames:a", "1", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1"}
	command := managedprocess.CommandContext(child, t.FFmpegPath, args...)
	command.WaitDelay = 2 * time.Second
	log := &speechFrameLog{cancel: cancel, firstOnly: true, prefix: "[ashowinfo@" + label + " @ "}
	output := &speechLimitedWriter{cancel: cancel}
	command.Stderr, command.Stdout = log, output
	err := command.Run()
	if ctx.Err() != nil {
		return renderAudioTiming{}, ctx.Err()
	}
	if err != nil {
		return renderAudioTiming{}, fmt.Errorf("定位源片段音频首帧失败：%w：%s", err, log.tail.String())
	}
	evidence, logErr := log.result()
	if log.err != nil {
		return renderAudioTiming{}, logErr
	}
	if evidence.frames == 0 && len(output.buffer.data) == 0 {
		return renderAudioTiming{}, nil
	} // 选中源区间本身没有音频，不把视频尾补成源音轨。
	if evidence.frames == 0 || int64(len(output.buffer.data)) != evidence.samples*2 {
		return renderAudioTiming{}, errors.New("音频首帧数据与PTS证据不一致，不能忽略实际音轨")
	}
	return renderAudioTiming{hasSamples: true, leadSeconds: float64(evidence.firstPTS) / SpeechSampleRate}, nil
}
