package playback

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"

	"aividcut/internal/editor"
)

type sourceClipXML struct {
	producers, playlists, tracks, transitions string
	index                                     int
}

// MLT默认producer只有15000帧；length与out都须显式声明，避免长工程被截断或冻结。
func boundedProducer(id string, frames int64) string {
	return fmt.Sprintf(`<producer id="%s" in="0" out="%d">`, id, frames-1) + prop("length", strconv.FormatInt(frames, 10))
}

// 只有相同处理和放置相位才共用游标；外观与每个片段的输出窗不进入原始源身份。
type videoSourceKey struct {
	AssetID, Path, Fingerprint string
	SourceIn, Frames, Shift    int64
	SpeedN, SpeedD, FPSN, FPSD int
}

func sourceTimeFrame(time editor.AudioTime, settings editor.Settings, ceil bool) (int64, error) {
	value, err := time.Rat()
	if err != nil {
		return 0, err
	}
	value.Mul(value, new(big.Rat).SetFrac64(int64(settings.FPSNum), 1_000_000*int64(settings.FPSDen)))
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(value.Num(), value.Denom(), r)
	if ceil && r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	} else if !ceil && r.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, errors.New("原生源时钟超出工程范围")
	}
	return q.Int64(), nil
}

func sourcePlaylist(id, producer string, start, frames int64) string {
	return sourcePlaylistWindow(id, producer, start, 0, frames)
}
func sourcePlaylistWindow(id, producer string, start, offset, frames int64) string {
	result := `<playlist id="` + id + `">`
	if start > 0 {
		result += fmt.Sprintf(`<blank length="%d"/>`, start)
	}
	return result + fmt.Sprintf(`<entry producer="%s" in="%d" out="%d"/></playlist>`, producer, offset, offset+frames-1)
}

func compileSourceClip(settings editor.Settings, clip editor.Clip, asset editor.Asset, assets map[string]editor.Asset, track editor.Track, caps Capabilities, index int, duration int64, videoSources map[videoSourceKey]string, proxy *VisualSource) (sourceClipXML, error) {
	result := sourceClipXML{index: index}
	visualPath, visualFingerprint := asset.Path, asset.Fingerprint
	if proxy != nil {
		visualPath, visualFingerprint = proxy.Path, proxy.Fingerprint
	}
	var phase editor.ResolvedSourcePhase
	if clip.Kind == "audio" || clip.Kind == "video" {
		var err error
		clip, err = editor.CaptureSourcePhase(clip, asset, settings)
		if err != nil {
			return result, err
		}
		phase, err = editor.ResolveSourcePhase(clip, asset, settings)
		if err != nil {
			return result, err
		}
	}
	visual := clip.Kind != "audio" && !track.Hidden
	audible := asset.HasAudio && clip.Kind != "image" && !clip.Muted && !track.Muted && clip.Volume > 0
	if visual {
		result.index++
		id, list := fmt.Sprintf("clip%d", result.index), fmt.Sprintf("playlist%d", result.index)
		service := "loader"
		producerFrames, offset := clip.DurationFrames, int64(0)
		shared := false
		if clip.Kind == "video" {
			service = "aividcut.source_time"
			if !contains(caps.Producers, service) {
				return result, errors.New("worker缺少按实际视频PTS读取源时间的服务")
			}
			if phase.Root.DurationFrames > math.MaxInt32-1 {
				return result, errors.New("原视频处理范围超出当前原生帧索引上限")
			}
			producerFrames, offset = phase.Root.DurationFrames, phase.WindowStartFrame
			key := videoSourceKey{AssetID: asset.ID, Path: visualPath, Fingerprint: visualFingerprint, SourceIn: phase.Root.SourceInUs, Frames: phase.Root.DurationFrames, Shift: clip.StartFrame - phase.WindowStartFrame, SpeedN: phase.Root.SpeedNum, SpeedD: phase.Root.SpeedDen, FPSN: settings.FPSNum, FPSD: settings.FPSDen}
			if previous, found := videoSources[key]; found {
				id, shared = previous, true
			} else {
				videoSources[key] = id
			}
		}
		if !shared {
			result.producers += boundedProducer(id, producerFrames) + prop("mlt_service", service) + prop("resource", visualPath) + prop("eof", "pause") + prop("audio_index", "-1") + prop("threads", "1") + prop("cache", "2")
			if clip.Kind == "video" {
				result.producers += prop("asset_id", asset.ID) + prop("source_fingerprint", visualFingerprint) + prop("processing_start_frame", strconv.FormatInt(clip.StartFrame-phase.WindowStartFrame, 10)) + prop("source_in_us", strconv.FormatInt(phase.Root.SourceInUs, 10)) + prop("speed_num", strconv.Itoa(phase.Root.SpeedNum)) + prop("speed_den", strconv.Itoa(phase.Root.SpeedDen)) + prop("output_offset_frames", "0") + prop("processing_duration_frames", strconv.FormatInt(phase.Root.DurationFrames, 10))
			}
			if proxy != nil {
				result.producers += prop("source_origin_us", strconv.FormatInt(proxy.OriginUs, 10))
			}
			result.producers += `</producer>`
		}
		result.playlists += sourcePlaylistWindow(list, id, clip.StartFrame, offset, clip.DurationFrames)
		result.tracks += `<track producer="` + list + `" hide="audio"/>`
		transition, err := compileVisualTransition(settings, clip, asset, assets, caps, result.index, duration)
		if err != nil {
			return result, err
		}
		result.transitions += transition
	}
	if !audible {
		return result, nil
	}
	if !contains(caps.Filters, "aividcut.audio_source") {
		return result, errors.New("worker缺少有界流式源音轨/采样端点服务")
	}
	if !contains(caps.Transitions, "aividcut.audio_mix") {
		return result, errors.New("worker缺少受控共享采样窗混音，请更新本项目worker")
	}
	audioPhase, err := editor.ResolveSourceAudioPhase(clip, asset, settings)
	if err != nil || audioPhase.Current.Empty {
		return result, err
	}
	resolved := audioPhase.Current
	start, err := sourceTimeFrame(resolved.TimelineStart, settings, false)
	if err != nil {
		return result, err
	}
	end, err := sourceTimeFrame(resolved.TimelineEnd, settings, true)
	if err != nil {
		return result, err
	}
	end = min(end, duration)
	if start >= end {
		return result, nil
	}
	result.index++
	id, list := fmt.Sprintf("audio%d", result.index), fmt.Sprintf("playlist%d", result.index)
	// MLT会把in==out的单帧cut当冻结图像并屏蔽音频；内部给两帧，真实声音仍由半开端点裁切。
	audioFrames := max(int64(2), end-start)
	fadeProperties := ""
	if clip.AudioFade != nil {
		if !caps.AudioFades.Supported {
			return result, errors.New("当前worker不支持音频淡入淡出，请更新原生媒体工具")
		}
		a, b, in, out := editor.AudioFadeSampleBounds(clip, settings, settings.SampleRate)
		fadeProperties = prop("audio_fade_enabled", "1") + prop("audio_fade_start", decimal(a)) + prop("audio_fade_end", decimal(b)) + prop("audio_fade_in", decimal(in)) + prop("audio_fade_out", decimal(out))
	}
	filter := `<filter>` + prop("mlt_service", "aividcut.audio_source") + prop("resource", asset.Path) + prop("asset_id", asset.ID) + prop("source_fingerprint", asset.Fingerprint) + prop("canvas_start_frame", strconv.FormatInt(start, 10)) + prop("source_start_num", strconv.FormatInt(audioPhase.Root.SourceStart.Num, 10)) + prop("source_start_den", strconv.FormatInt(audioPhase.Root.SourceStart.Den, 10)) + prop("source_end_num", strconv.FormatInt(audioPhase.Root.SourceEnd.Num, 10)) + prop("source_end_den", strconv.FormatInt(audioPhase.Root.SourceEnd.Den, 10)) + prop("processing_start_num", strconv.FormatInt(audioPhase.ProcessingTimelineStart.Num, 10)) + prop("processing_start_den", strconv.FormatInt(audioPhase.ProcessingTimelineStart.Den, 10)) + prop("timeline_start_num", strconv.FormatInt(resolved.TimelineStart.Num, 10)) + prop("timeline_start_den", strconv.FormatInt(resolved.TimelineStart.Den, 10)) + prop("timeline_end_num", strconv.FormatInt(resolved.TimelineEnd.Num, 10)) + prop("timeline_end_den", strconv.FormatInt(resolved.TimelineEnd.Den, 10)) + prop("speed_num", strconv.Itoa(phase.Root.SpeedNum)) + prop("speed_den", strconv.Itoa(phase.Root.SpeedDen)) + fadeProperties + `</filter>`
	result.producers += boundedProducer(id, audioFrames) + prop("mlt_service", "colour") + prop("resource", "0x00000000") + filter
	if clip.Volume != 1 {
		if !contains(caps.Filters, "volume") {
			return result, errors.New("MLT缺少音量滤镜")
		}
		result.producers += `<filter>` + prop("mlt_service", "volume") + prop("level", decimal(20*math.Log10(clip.Volume))+"dB") + `</filter>`
	}
	result.producers += `</producer>`
	result.playlists += sourcePlaylist(list, id, start, audioFrames)
	result.tracks += `<track producer="` + list + `" hide="video"/>`
	result.transitions += `<transition in="0" out="` + strconv.FormatInt(duration-1, 10) + `">` + prop("mlt_service", "aividcut.audio_mix") + prop("a_track", "0") + prop("b_track", strconv.Itoa(result.index)) + prop("always_active", "1") + prop("accepts_blanks", "1") + `</transition>`
	return result, nil
}
