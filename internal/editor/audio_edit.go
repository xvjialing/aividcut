package editor

import (
	"math/big"
	"sort"
)

func validateAudioTrackOverlap(clips []*Clip, assets map[string]Asset, settings Settings) error {
	type interval struct{ start, end *big.Rat }
	intervals := make([]interval, 0, len(clips))
	for _, clip := range clips {
		start, end := frameTimeRat(clip.StartFrame, settings), frameTimeRat(clip.StartFrame+clip.DurationFrames, settings)
		if clip.AudioRange != nil {
			_, _, actualStart, actualEnd, err := resolveAudioRats(*clip, assets[clip.AssetID], settings)
			if err != nil {
				return err
			}
			start, end = actualStart, actualEnd
		}
		if clip.SourcePhase != nil {
			phase, err := ResolveSourceAudioPhase(*clip, assets[clip.AssetID], settings)
			if err != nil {
				return err
			}
			start, _ = phase.Current.TimelineStart.Rat()
			end, _ = phase.Current.TimelineEnd.Rat()
		}
		if end.Cmp(start) > 0 {
			intervals = append(intervals, interval{start, end})
		}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Cmp(intervals[j].start) < 0 })
	for i := 1; i < len(intervals); i++ {
		if intervals[i].start.Cmp(intervals[i-1].end) < 0 {
			return problem("overlap", "同一音频轨的真实声音区间不能重叠，请移到另一轨道")
		}
	}
	return nil
}

func audioAssetBounds(asset Asset) (*big.Rat, *big.Rat) {
	start := new(big.Rat).SetInt64(asset.AudioStartUs)
	end := new(big.Rat).SetInt64(asset.DurationUs)
	if asset.AudioSampleCount != nil {
		end.SetFrac(new(big.Int).Mul(big.NewInt(*asset.AudioSampleCount), big.NewInt(1_000_000)), big.NewInt(int64(asset.AudioSampleRate)))
		end.Add(end, start)
	}
	return start, end
}

func audioPosition(value *AudioRange, time *big.Rat, ceil bool) (int64, error) {
	position := new(big.Rat).Set(time)
	if value.Unit == "samples" {
		position.Sub(position, new(big.Rat).SetInt64(value.SourceOriginUs))
		position.Mul(position, new(big.Rat).SetFrac64(int64(value.SampleRate), 1_000_000))
	}
	if ceil {
		return ceilRat(position)
	}
	return floorRat(position)
}

func audioAnchor(clip Clip) (*big.Rat, error) {
	start, _, err := audioRangeSource(clip.AudioRange)
	if err != nil {
		return nil, err
	}
	offset, err := audioOffset(clip.AudioRange)
	if err != nil {
		return nil, err
	}
	return new(big.Rat).Sub(start, new(big.Rat).Mul(offset, clipSpeed(clip))), nil
}

func installAudioRange(clip *Clip, value AudioRange, anchor *big.Rat) error {
	start, _, err := audioRangeSource(&value)
	if err != nil {
		return err
	}
	offset := new(big.Rat).Quo(new(big.Rat).Sub(start, anchor), clipSpeed(*clip))
	stamp, err := audioTime(offset)
	if err != nil {
		return err
	}
	value.OffsetNum, value.OffsetDen = stamp.Num, stamp.Den
	if clip.Kind == "audio" {
		index, err := floorRat(anchor)
		if err != nil || index < 0 {
			return problem("audio_range", "声音源锚点超出有效范围")
		}
		clip.SourceInUs = index
	}
	clip.AudioRange = &value
	return nil
}

func audioSelection(value AudioRange, start, end *big.Rat, asset Asset) (AudioRange, error) {
	minimum, maximum := audioAssetBounds(asset)
	if start.Cmp(minimum) < 0 {
		start = new(big.Rat).Set(minimum)
	}
	if start.Cmp(maximum) > 0 {
		start = new(big.Rat).Set(maximum)
	}
	if end.Cmp(maximum) > 0 {
		end = new(big.Rat).Set(maximum)
	}
	if end.Cmp(start) < 0 {
		end = new(big.Rat).Set(start)
	}
	left, err := audioPosition(&value, start, true)
	if err != nil {
		return value, err
	}
	right, err := audioPosition(&value, end, true)
	if err != nil {
		return value, err
	}
	if value.Unit == "samples" {
		left = max(int64(0), min(*asset.AudioSampleCount, left))
		right = max(left, min(*asset.AudioSampleCount, right))
	} else {
		left = max(asset.AudioStartUs, min(asset.DurationUs, left))
		right = max(left, min(asset.DurationUs, right))
	}
	value.Start, value.End = left, right
	return value, nil
}

// materializeAudioWindow只把已有帧选区量化到真实源单位，不把duration反推成实测count。
func materializeAudioWindow(clip Clip, asset Asset, settings Settings) (Clip, error) {
	if clip.AudioRange != nil {
		return cloneClip(clip), nil
	}
	if !asset.HasAudio {
		return Clip{}, problem("audio_range", "素材没有真实音轨")
	}
	value := AudioRange{Unit: "us", OffsetDen: 1}
	if asset.AudioSampleCount != nil {
		value.Unit, value.SampleRate, value.SourceOriginUs = "samples", asset.AudioSampleRate, asset.AudioStartUs
	}
	anchor := new(big.Rat).SetInt64(clip.SourceInUs)
	end := new(big.Rat).Mul(frameTimeRat(clip.DurationFrames, settings), clipSpeed(clip))
	end.Add(end, anchor)
	var err error
	value, err = audioSelection(value, anchor, end, asset)
	if err != nil {
		return Clip{}, err
	}
	clip = cloneClip(clip)
	if err := installAudioRange(&clip, value, anchor); err != nil {
		return Clip{}, err
	}
	return clip, nil
}

func audioBoxFrames(clip Clip, settings Settings) (int64, error) {
	start, end, err := audioRangeSource(clip.AudioRange)
	if err != nil {
		return 0, err
	}
	offset, err := audioOffset(clip.AudioRange)
	if err != nil {
		return 0, err
	}
	duration := new(big.Rat).Quo(new(big.Rat).Sub(end, start), clipSpeed(clip))
	duration.Add(duration, offset)
	if duration.Sign() <= 0 {
		return 1, nil
	}
	return ceilRat(new(big.Rat).Quo(duration, frameTimeRat(1, settings)))
}

func adaptAudioUpdate(before Clip, after *Clip, asset Asset, settings Settings, linkedVisual bool) error {
	if before.AudioRange == nil && after.AudioRange == nil {
		return nil
	}
	if !validKind(after.Kind, "audio", "video") {
		return problem("audio_range", "声音精确范围不能跟到静态图或字幕")
	}
	if before.AssetID != after.AssetID && sameAudioRange(before.AudioRange, after.AudioRange) {
		return problem("audio_range", "更换声音素材需要重新指定该素材的精确范围")
	}
	if after.AudioRange != nil && !sameAudioRange(before.AudioRange, after.AudioRange) {
		_, err := ResolveAudioRange(*after, asset, settings)
		return err
	}
	if before.AudioRange == nil {
		return nil
	}
	after.AudioRange = cloneClip(before).AudioRange
	speedChanged := before.SpeedNum != after.SpeedNum || before.SpeedDen != after.SpeedDen
	sourceChanged := before.SourceInUs != after.SourceInUs
	durationChanged := before.DurationFrames != after.DurationFrames
	if !speedChanged && !sourceChanged && !durationChanged {
		return nil
	}
	anchor, err := audioAnchor(before)
	if err != nil {
		return err
	}
	start, end, err := audioRangeSource(before.AudioRange)
	if err != nil {
		return err
	}
	value := *before.AudioRange
	deltaStart := after.StartFrame - before.StartFrame
	leftTrim := deltaStart != 0 && after.DurationFrames-before.DurationFrames == -deltaStart && (sourceChanged || linkedVisual) && !speedChanged
	if leftTrim {
		anchor.Add(anchor, new(big.Rat).Mul(frameTimeRatSigned(deltaStart, settings), clipSpeed(before)))
		value, err = audioSelection(value, anchor, end, asset)
	} else if speedChanged && !linkedVisual {
		if sourceChanged {
			return problem("audio_range", "同时改变音频速度与源入点需要明确提供新范围")
		}
	} else {
		if sourceChanged {
			delta := new(big.Rat).SetInt64(after.SourceInUs - before.SourceInUs)
			anchor.Add(anchor, delta)
			if !durationChanged && !speedChanged && !linkedVisual {
				start.Add(start, delta)
				end.Add(end, delta)
				minimum, maximum := audioAssetBounds(asset)
				if start.Cmp(minimum) < 0 || end.Cmp(maximum) > 0 {
					return problem("source_out_of_range", "滑动声音选区超出真实源余量")
				}
				value, err = audioSelection(value, start, end, asset)
			} else {
				end.Mul(frameTimeRat(after.DurationFrames, settings), clipSpeed(*after))
				end.Add(end, anchor)
				value, err = audioSelection(value, anchor, end, asset)
			}
		} else if durationChanged || speedChanged {
			end.Mul(frameTimeRat(after.DurationFrames, settings), clipSpeed(*after))
			end.Add(end, anchor)
			value, err = audioSelection(value, start, end, asset)
		}
	}
	if err != nil {
		return err
	}
	if err := installAudioRange(after, value, anchor); err != nil {
		return err
	}
	if speedChanged && !linkedVisual && !durationChanged {
		frames, err := audioBoxFrames(*after, settings)
		if err != nil {
			return err
		}
		after.DurationFrames = max(int64(1), frames)
	}
	_, err = ResolveAudioRange(*after, asset, settings)
	return err
}

func frameTimeRatSigned(frames int64, settings Settings) *big.Rat {
	return frameTimeRat(frames, settings)
}

func adaptAudioDuplicate(before Clip, after *Clip, asset Asset, settings Settings) error {
	if before.AudioRange == nil {
		return nil
	}
	if after.AudioRange != nil && !sameAudioRange(before.AudioRange, after.AudioRange) {
		_, err := ResolveAudioRange(*after, asset, settings)
		return err
	}
	anchor, err := audioAnchor(before)
	if err != nil {
		return err
	}
	start, end, err := audioRangeSource(before.AudioRange)
	if err != nil {
		return err
	}
	delta := new(big.Rat).SetInt64(after.SourceInUs - before.SourceInUs)
	frameSource := new(big.Rat).Mul(frameTimeRat(1, settings), clipSpeed(before))
	frames := new(big.Rat).Quo(delta, frameSource)
	rounded := new(big.Rat).Add(frames, new(big.Rat).SetFrac64(1, 2))
	index, err := floorRat(rounded)
	if err != nil {
		return err
	}
	exactDelta := new(big.Rat).Mul(new(big.Rat).SetInt64(index), frameSource)
	difference := new(big.Rat).Sub(exactDelta, delta)
	if difference.Sign() < 0 {
		difference.Neg(difference)
	}
	if difference.Cmp(new(big.Rat).SetInt64(1)) < 0 {
		delta = exactDelta
	}
	anchor.Add(anchor, delta)
	windowEnd := new(big.Rat).Mul(frameTimeRat(after.DurationFrames, settings), clipSpeed(*after))
	windowEnd.Add(windowEnd, anchor)
	if start.Cmp(anchor) < 0 {
		start.Set(anchor)
	}
	if windowEnd.Cmp(end) < 0 {
		end.Set(windowEnd)
	}
	if start.Cmp(end) > 0 {
		start.Set(end)
	}
	value, err := audioSelection(*before.AudioRange, start, end, asset)
	if err != nil {
		return err
	}
	if err := installAudioRange(after, value, anchor); err != nil {
		return err
	}
	_, err = ResolveAudioRange(*after, asset, settings)
	return err
}

func splitAudioRange(before Clip, left, right *Clip, asset Asset, settings Settings, atFrame int64) error {
	if before.Kind != "audio" && before.AudioRange == nil {
		return nil
	}
	materialized, err := materializeAudioWindow(before, asset, settings)
	if err != nil {
		return err
	}
	start, _, actualStart, _, err := resolveAudioRats(materialized, asset, settings)
	if err != nil {
		return err
	}
	cut := new(big.Rat).Sub(frameTimeRat(atFrame, settings), actualStart)
	cut.Mul(cut, clipSpeed(before))
	cut.Add(cut, start)
	boundary, err := audioPosition(materialized.AudioRange, cut, true)
	if err != nil {
		return err
	}
	boundary = max(materialized.AudioRange.Start, min(materialized.AudioRange.End, boundary))
	leftValue, rightValue := *materialized.AudioRange, *materialized.AudioRange
	leftValue.End, rightValue.Start = boundary, boundary
	anchor, err := audioAnchor(materialized)
	if err != nil {
		return err
	}
	if err := installAudioRange(left, leftValue, anchor); err != nil {
		return err
	}
	rightAnchor := new(big.Rat).Add(anchor, new(big.Rat).Mul(frameTimeRat(atFrame-before.StartFrame, settings), clipSpeed(before)))
	if err := installAudioRange(right, rightValue, rightAnchor); err != nil {
		return err
	}
	if _, err := ResolveAudioRange(*left, asset, settings); err != nil {
		return err
	}
	if _, err := ResolveAudioRange(*right, asset, settings); err != nil {
		return err
	}
	return nil
}

func sameResolvedAudio(a, b Clip, asset Asset, settings Settings) bool {
	left, err := materializeAudioWindow(a, asset, settings)
	if err != nil {
		return false
	}
	right, err := materializeAudioWindow(b, asset, settings)
	if err != nil {
		return false
	}
	ls, le, lt, lend, err := resolveAudioRats(left, asset, settings)
	if err != nil {
		return false
	}
	rs, re, rt, rend, err := resolveAudioRats(right, asset, settings)
	if err != nil {
		return false
	}
	return ls.Cmp(rs) == 0 && le.Cmp(re) == 0 && lt.Cmp(rt) == 0 && lend.Cmp(rend) == 0
}
