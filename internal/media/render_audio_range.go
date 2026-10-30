package media

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"aividcut/internal/editor"
)

// 精确音轨单独规划，视觉仍遵循工程帧；所有端点均为半开区间。
type exactAudioPlan struct {
	args, filters, labels []string
	nextInput             int
}

type exactAudioSource struct {
	path   string
	asset  editor.Asset
	origin *big.Rat
	input  int
	clips  []exactAudioClip
}
type exactAudioClip struct {
	clip                   editor.Clip
	startSample, endSample int64
	duration, crop, length int64
	delay                  int64
}

func renderCeil(value *big.Rat) (int64, error) {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(value.Num(), value.Denom(), r)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, errors.New("音频采样端点超出可表示范围")
	}
	return q.Int64(), nil
}
func renderFrameSample(frame int64, settings editor.Settings) (int64, error) {
	value := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(frame), big.NewInt(int64(settings.FPSDen)*int64(settings.SampleRate))), big.NewInt(int64(settings.FPSNum)))
	return renderCeil(value)
}
func renderSampleSpan(from, to int64, settings editor.Settings) (int64, error) {
	a, err := renderFrameSample(from, settings)
	if err != nil {
		return 0, err
	}
	b, err := renderFrameSample(to, settings)
	return b - a, err
}
func renderTimeSample(us *big.Rat, rate int) (int64, error) {
	return renderCeil(new(big.Rat).Mul(us, new(big.Rat).SetFrac64(int64(rate), 1_000_000)))
}

func buildExactAudio(project editor.Project, clips []editor.Clip, assets map[string]editor.Asset, tracks map[string]editor.Track, from, to int64, firstInput int, resolveAudio func(string, int64, float64) (renderAudioTiming, error)) (exactAudioPlan, error) {
	plan := exactAudioPlan{nextInput: firstInput}
	settings := project.Settings
	fromSample, err := renderFrameSample(from, settings)
	if err != nil {
		return plan, err
	}
	toSample, err := renderFrameSample(to, settings)
	if err != nil {
		return plan, err
	}
	sources := []*exactAudioSource{}
	byPath := map[string]*exactAudioSource{}
	for _, clip := range clips {
		if clip.AudioRange == nil {
			continue
		}
		track, found := tracks[clip.TrackID]
		asset, foundAsset := assets[clip.AssetID]
		if !found || !foundAsset || asset.Missing || !asset.HasAudio || clip.Kind != "audio" && clip.Kind != "video" || clip.Kind == "audio" && clip.LUT != nil {
			return plan, errors.New("精确音频区间没有有效的音轨或源素材")
		}
		if !finite(clip.Volume) || clip.Volume < 0 || clip.Volume > 16 || clip.SpeedNum <= 0 || clip.SpeedDen <= 0 || float64(clip.SpeedNum)/float64(clip.SpeedDen) < .125 || float64(clip.SpeedNum)/float64(clip.SpeedDen) > 8 {
			return plan, errors.New("精确音频的音量或速度无效")
		}
		phase, err := editor.ResolveSourceAudioPhase(clip, asset, settings)
		if err != nil {
			return plan, err
		}
		resolved := phase.Current
		if resolved.Empty || clip.Muted || track.Muted || clip.Volume == 0 {
			continue
		}
		start, _ := phase.Root.SourceStart.Rat()
		end, _ := phase.Root.SourceEnd.Rat()
		processingStart, _ := phase.ProcessingTimelineStart.Rat()
		processingEnd, _ := phase.ProcessingTimelineEnd.Rat()
		timelineStart, _ := resolved.TimelineStart.Rat()
		timelineEnd, _ := resolved.TimelineEnd.Rat()
		// 先按真实端点判断，允许共同样本切口越过显示帧边界不足一个源样本。
		timelineA, err := renderTimeSample(timelineStart, settings.SampleRate)
		if err != nil {
			return plan, err
		}
		timelineB, err := renderTimeSample(timelineEnd, settings.SampleRate)
		if err != nil {
			return plan, err
		}
		if timelineA >= toSample || timelineB <= fromSample {
			continue
		}
		local, err := filepath.Abs(asset.Path)
		if err != nil {
			return plan, err
		}
		if stat, err := os.Stat(local); err != nil || !stat.Mode().IsRegular() {
			return plan, fmt.Errorf("素材不可读取: %s", asset.Name)
		}
		count := "unknown"
		if asset.AudioSampleCount != nil {
			count = strconv.FormatInt(*asset.AudioSampleCount, 10)
		}
		key := fmt.Sprintf("%s\x00%d/%s/%d", local, asset.AudioSampleRate, count, asset.AudioStartUs)
		source := byPath[key]
		if source == nil {
			origin := new(big.Rat).SetInt64(asset.AudioStartUs)
			if asset.AudioSampleCount == nil {
				if resolveAudio == nil {
					return plan, errors.New("未实测样本数的音轨需要读取实际首帧时间")
				}
				timing, err := resolveAudio(local, 0, float64(asset.DurationUs)/1e6)
				if err != nil {
					return plan, err
				}
				if !timing.hasSamples {
					continue
				}
				// 复用实际PTS证据；不能把晚起的音轨悄悄搬到源零点。
				if _, ok := origin.SetString(number(timing.leadSeconds * 1e6)); !ok {
					return plan, errors.New("音轨首帧时间无法表示")
				}
			}
			source = &exactAudioSource{path: local, asset: asset, origin: origin, input: plan.nextInput}
			plan.nextInput++
			byPath[key] = source
			sources = append(sources, source)
		}
		if start.Cmp(source.origin) < 0 {
			gap := new(big.Rat).Sub(source.origin, start)
			gap.Mul(gap, new(big.Rat).SetFrac64(int64(clip.SpeedDen), int64(clip.SpeedNum)))
			processingStart.Add(processingStart, gap)
			start.Set(source.origin)
			firstReal, err := renderTimeSample(processingStart, settings.SampleRate)
			if err != nil {
				return plan, err
			}
			timelineA = max(timelineA, firstReal)
		}
		if start.Cmp(end) >= 0 || timelineA >= toSample || timelineB <= fromSample {
			continue
		}
		startSample, err := renderTimeSample(new(big.Rat).Sub(start, source.origin), settings.SampleRate)
		if err != nil {
			return plan, err
		}
		endSample, err := renderTimeSample(new(big.Rat).Sub(end, source.origin), settings.SampleRate)
		if err != nil {
			return plan, err
		}
		left, right := max(timelineA, fromSample), min(timelineB, toSample)
		if left >= right || startSample >= endSample {
			continue
		}
		processingA, err := renderTimeSample(processingStart, settings.SampleRate)
		if err != nil {
			return plan, err
		}
		processingB, err := renderTimeSample(processingEnd, settings.SampleRate)
		if err != nil {
			return plan, err
		}
		if processingA > left || processingB < right {
			return plan, errors.New("音频派生输出超出原完整处理范围")
		}
		source.clips = append(source.clips, exactAudioClip{clip: clip, startSample: startSample, endSample: endSample, duration: processingB - processingA, crop: left - processingA, length: right - left, delay: left - fromSample})
	}
	// 同一文件只解码、重采样一次，切开片段不会重启重采样器相位。
	for _, source := range sources {
		if len(source.clips) == 0 {
			// 已分配的输入仍需保留索引，但不建立未连接的过滤输出。
			plan.args = append(plan.args, "-i", source.path)
			continue
		}
		plan.args = append(plan.args, "-i", source.path)
		chain := fmt.Sprintf("[%d:a:0]", source.input)
		if source.asset.AudioSampleCount != nil {
			chain += fmt.Sprintf("atrim=start_sample=0:end_sample=%d,", *source.asset.AudioSampleCount)
		}
		chain += fmt.Sprintf("asetpts=N/SR/TB,aresample=%d,aformat=sample_fmts=fltp:channel_layouts=stereo", settings.SampleRate)
		var outputs []string
		for i := range source.clips {
			outputs = append(outputs, fmt.Sprintf("[exact_src_%d_%d]", source.input, i))
		}
		if len(outputs) > 1 {
			chain += fmt.Sprintf(",asplit=%d", len(outputs))
		}
		plan.filters = append(plan.filters, chain+strings.Join(outputs, ""))
		for i, value := range source.clips {
			label := fmt.Sprintf("exact_audio_%d_%d", source.input, i)
			chain := fmt.Sprintf("[exact_src_%d_%d]atrim=start_sample=%d:end_sample=%d,asetpts=N/SR/TB", source.input, i, value.startSample, value.endSample)
			if value.clip.SpeedNum != value.clip.SpeedDen {
				chain += tempoChain(float64(value.clip.SpeedNum) / float64(value.clip.SpeedDen))
			}
			// 先限制真实声音长度，再在变速后裁输出选区；负相对起点不能变成负adelay。
			chain += fmt.Sprintf(",apad=whole_len=%d,atrim=end_sample=%d,atrim=start_sample=%d:end_sample=%d,asetpts=N/SR/TB", value.duration, value.duration, value.crop, value.crop+value.length)
			if value.clip.AudioFade != nil {
				a, b, in, out := editor.AudioFadeSampleBounds(value.clip, settings, settings.SampleRate)
				at := "(n+" + strconv.FormatInt(value.delay+fromSample, 10) + ")"
				gain := "1"
				if in > 0 {
					gain = "min(" + gain + ",(" + at + "-(" + number(a) + "))/" + number(in) + ")"
				}
				if out > 0 {
					gain = "min(" + gain + ",(" + number(b) + "-" + at + ")/" + number(out) + ")"
				}
				chain += ",aeval='val(ch)*max(0," + gain + ")':c=same"
			}
			chain += fmt.Sprintf(",volume=%s,adelay=%dS:all=1,apad=whole_len=%d,atrim=end_sample=%d,asetpts=N/SR/TB[%s]", number(value.clip.Volume), value.delay, toSample-fromSample, toSample-fromSample, label)
			plan.filters = append(plan.filters, chain)
			plan.labels = append(plan.labels, "["+label+"]")
		}
	}
	return plan, nil
}
