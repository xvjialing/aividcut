package editor

import (
	"math"
	"math/big"
)

func sameSourcePhase(a, b *SourcePhase) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	x, y := *a, *b
	xr, yr := x.Recipe.AudioRange, y.Recipe.AudioRange
	x.Recipe.AudioRange, y.Recipe.AudioRange = nil, nil
	return x == y && sameAudioRange(xr, yr)
}

func mediaSourceKind(clip Clip) bool { return clip.Kind == "video" || clip.Kind == "audio" }

// recipeWindow重新从原配方选择共享源单位窗口，不能沿递归分割累计整数微秒误差。
func recipeWindow(clip Clip, asset Asset, settings Settings) (Clip, error) {
	if clip.SourcePhase == nil {
		return clip, nil
	}
	if err := ValidateSourcePhase(clip, asset, settings); err != nil {
		return Clip{}, err
	}
	phase := clip.SourcePhase
	root := sourcePhaseRoot(clip, phase)
	offset := new(big.Rat).Mul(frameTimeRat(phase.OffsetFrames, settings), clipSpeed(root))
	visualAnchor := new(big.Rat).Add(new(big.Rat).SetInt64(root.SourceInUs), offset)
	index, err := floorRat(visualAnchor)
	if err != nil || index < 0 {
		return Clip{}, problem("source_phase", "派生源入点超出可表示范围")
	}
	clip.SourceInUs = index
	if root.AudioRange == nil {
		clip.AudioRange = nil
		return clip, nil
	}
	start, end, err := audioRangeSource(root.AudioRange)
	if err != nil {
		return Clip{}, err
	}
	anchor, err := audioAnchor(root)
	if err != nil {
		return Clip{}, err
	}
	currentAnchor := new(big.Rat).Add(new(big.Rat).Set(anchor), offset)
	if phase.OffsetFrames > 0 && currentAnchor.Cmp(start) > 0 {
		start.Set(currentAnchor)
	}
	if phase.OffsetFrames+clip.DurationFrames < phase.Recipe.DurationFrames {
		cut := new(big.Rat).Mul(frameTimeRat(phase.OffsetFrames+clip.DurationFrames, settings), clipSpeed(root))
		cut.Add(cut, anchor)
		if cut.Cmp(end) < 0 {
			end.Set(cut)
		}
	}
	rootStart, rootEnd, _ := audioRangeSource(root.AudioRange)
	if start.Cmp(rootEnd) > 0 {
		start.Set(rootEnd)
	}
	if end.Cmp(rootStart) < 0 {
		end.Set(rootStart)
	}
	if end.Cmp(start) < 0 {
		end.Set(start)
	}
	value, err := audioSelection(*root.AudioRange, start, end, asset)
	if err != nil {
		return Clip{}, err
	}
	if err := installAudioRange(&clip, value, currentAnchor); err != nil {
		return Clip{}, err
	}
	return clip, nil
}

func validateSourcePhaseState(clip Clip, asset Asset, settings Settings) error {
	if clip.SourcePhase == nil {
		return nil
	}
	expected, err := recipeWindow(cloneClip(clip), asset, settings)
	if err != nil {
		return err
	}
	if expected.SourceInUs != clip.SourceInUs || !sameAudioRange(expected.AudioRange, clip.AudioRange) {
		return problem("source_phase", "源入点或声音区间与原处理输出窗不一致，请通过分割、修剪或滑动命令修改")
	}
	return nil
}

func sourceTimingChanged(before, after Clip) bool {
	return before.AssetID != after.AssetID || before.Kind != after.Kind || before.SourceInUs != after.SourceInUs || before.DurationFrames != after.DurationFrames || before.SpeedNum != after.SpeedNum || before.SpeedDen != after.SpeedDen
}

// preparePhaseUpdate在旧帧选区第一次派生时捕获；普通移动/外观修改不扩大旧nil语义。
func preparePhaseUpdate(before Clip, after *Clip, asset Asset, settings Settings) (Clip, error) {
	if after.SourcePhase != nil && !sameSourcePhase(before.SourcePhase, after.SourcePhase) {
		return Clip{}, problem("source_phase", "不能直接改写源处理配方，请使用正式剪辑操作")
	}
	if !mediaSourceKind(before) || !mediaSourceKind(*after) || before.AssetID != after.AssetID || !sourceTimingChanged(before, *after) {
		return before, nil
	}
	owned, err := CaptureSourcePhase(before, asset, settings)
	if err != nil {
		return Clip{}, err
	}
	if sameAudioRange(before.AudioRange, after.AudioRange) {
		after.AudioRange = cloneClip(owned).AudioRange
	}
	return owned, nil
}

func phaseHeadFrames(before, after Clip, settings Settings) (int64, bool) {
	if before.SourceInUs == after.SourceInUs {
		return 0, true
	}
	delta := new(big.Rat).SetInt64(after.SourceInUs - before.SourceInUs)
	perFrame := new(big.Rat).Mul(frameTimeRat(1, settings), clipSpeed(before))
	delta.Quo(delta, perFrame)
	delta.Add(delta, new(big.Rat).SetFrac64(1, 2))
	frames, err := floorRat(delta)
	if err != nil || before.SourcePhase == nil || frames < -before.SourcePhase.OffsetFrames {
		return 0, false
	}
	offset := before.SourcePhase.OffsetFrames + frames
	anchor := new(big.Rat).SetInt64(before.SourcePhase.Recipe.SourceInUs)
	if before.Kind == "audio" && before.SourcePhase.Recipe.AudioRange != nil {
		root := sourcePhaseRoot(before, before.SourcePhase)
		anchor, err = audioAnchor(root)
		if err != nil {
			return 0, false
		}
	}
	anchor.Add(anchor, new(big.Rat).Mul(frameTimeRat(offset, settings), clipSpeed(before)))
	expected, err := floorRat(anchor)
	if err != nil {
		return 0, false
	}
	errorUs := new(big.Int).Sub(big.NewInt(expected), big.NewInt(after.SourceInUs))
	errorUs.Abs(errorUs)
	return frames, errorUs.Cmp(big.NewInt(1)) <= 0
}

func finishPhaseUpdate(before Clip, after *Clip, asset Asset, settings Settings, duplicate bool, explicitRange bool) (err error) {
	defer func() {
		if err == nil {
			if !asset.HasAudio {
				after.AudioFade = nil
				return
			}
			finishAudioFadeUpdate(before, after, settings)
		}
	}()
	if !mediaSourceKind(*after) {
		after.SourcePhase = nil
		return nil
	}
	reset := before.AssetID != after.AssetID || before.Kind != after.Kind || before.SpeedNum != after.SpeedNum || before.SpeedDen != after.SpeedDen || explicitRange
	if reset {
		after.SourcePhase = nil
		owned, err := CaptureSourcePhase(*after, asset, settings)
		if err != nil {
			return err
		}
		*after = owned
		return nil
	}
	if before.SourcePhase == nil {
		after.SourcePhase = nil
		return nil
	}
	phase := CloneSourcePhase(before.SourcePhase)
	head, aligned := phaseHeadFrames(before, *after, settings)
	if before.SourceInUs != after.SourceInUs && (!aligned || !duplicate && after.DurationFrames-before.DurationFrames != -head) {
		reset = true
	}
	if !reset {
		if head > 0 && phase.OffsetFrames > math.MaxInt64-head {
			reset = true
		} else {
			phase.OffsetFrames += head
		}
		if phase.OffsetFrames < 0 || after.DurationFrames < 1 || phase.OffsetFrames > math.MaxInt64-after.DurationFrames || phase.OffsetFrames+after.DurationFrames > phase.Recipe.DurationFrames {
			reset = true
		}
	}
	if reset {
		after.SourcePhase = nil
		owned, err := CaptureSourcePhase(*after, asset, settings)
		if err != nil {
			return err
		}
		*after = owned
		return nil
	}
	after.SourcePhase = phase
	owned, err := recipeWindow(*after, asset, settings)
	if err != nil {
		return err
	}
	*after = owned
	return nil
}

func splitSourcePhase(original Clip, left, right *Clip, asset Asset, settings Settings, leftFrames int64) error {
	owned, err := CaptureSourcePhase(original, asset, settings)
	if err != nil {
		return err
	}
	left.SourcePhase = CloneSourcePhase(owned.SourcePhase)
	right.SourcePhase = CloneSourcePhase(owned.SourcePhase)
	if right.SourcePhase.OffsetFrames > math.MaxInt64-leftFrames {
		return problem("source_phase", "分割输出偏移超出范围")
	}
	right.SourcePhase.OffsetFrames += leftFrames
	if original.AudioFade != nil {
		left.AudioFade = CloneAudioFade(original.AudioFade)
		right.AudioFade = CloneAudioFade(original.AudioFade)
		right.AudioFade.OffsetFrames += leftFrames
	}
	value, err := recipeWindow(*left, asset, settings)
	if err != nil {
		return err
	}
	*left = value
	value, err = recipeWindow(*right, asset, settings)
	if err != nil {
		return err
	}
	*right = value
	return nil
}
