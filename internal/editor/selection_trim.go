package editor

import (
	"math"
	"math/big"
)

func trimClipSelection(project *Project, command Command) error {
	ids, err := selectionIDs(project, command)
	if err != nil {
		return err
	}
	if (command.Edge != "start" && command.Edge != "end") || command.FrameDelta == 0 || command.TrackDelta != 0 {
		return problem("selection", "请选择起点或终点，并提供非零整数帧修剪量")
	}
	updates := map[string]Clip{}
	for _, c := range project.Clips {
		if !ids[c.ID] {
			continue
		}
		if err := editableTrack(project, c.TrackID); err != nil {
			return err
		}
		if c.CaptionSource != nil {
			if !ids[c.CaptionSource.SourceClipID] {
				return problem("caption_binding", "绑定字幕需随源片段修剪，或先明确解除绑定")
			}
			continue
		}
		after, err := trimClipCandidate(project, c, command.Edge, command.FrameDelta)
		if err != nil {
			return err
		}
		_, after, err = prepareClipUpdate(project, c, after)
		if err != nil {
			return err
		}
		if after.Kind == "audio" && after.AudioRange != nil && command.Edge == "end" && command.FrameDelta > 0 {
			asset, _ := transcriptAsset(project, after.AssetID)
			anchor, err := audioAnchor(after)
			if err != nil {
				return err
			}
			_, maximum := audioAssetBounds(asset)
			// 视频的声音载体允许保留容器内原有无声区域，真实样本仍由AudioRange裁选。
			if asset.Kind == "video" {
				maximum = new(big.Rat).SetInt64(asset.DurationUs)
			}
			available := new(big.Rat).Sub(maximum, anchor)
			perFrame := new(big.Rat).Mul(frameTimeRat(1, project.Settings), clipSpeed(after))
			frames := new(big.Rat).Quo(available, perFrame)
			limit, err := ceilRat(frames)
			if err != nil {
				return err
			}
			if after.DurationFrames > limit {
				return problem("source_out_of_range", "修剪终点超出真实声音源末端")
			}
		}
		updates[c.ID] = after
	}
	for i := range project.Clips {
		if after, ok := updates[project.Clips[i].ID]; ok {
			project.Clips[i] = after
		}
	}
	return nil
}
func trimClipCandidate(project *Project, c Clip, edge string, delta int64) (Clip, error) {
	after := cloneClip(c)
	if edge == "end" {
		if delta < 1-c.DurationFrames || delta > math.MaxInt64-c.DurationFrames {
			return Clip{}, problem("trim_range", "终点修剪后片段必须至少1帧且不能溢出")
		}
		after.DurationFrames += delta
		if after.StartFrame > math.MaxInt64-after.DurationFrames {
			return Clip{}, problem("trim_range", "修剪后的末端超出范围")
		}
		return after, nil
	}
	if delta < -c.StartFrame || delta >= c.DurationFrames || delta < -(math.MaxInt64-c.DurationFrames) {
		return Clip{}, problem("trim_range", "起点修剪不能越过第0帧或片段终点，也不能溢出")
	}
	after.StartFrame += delta
	after.DurationFrames -= delta
	if mediaSourceKind(c) {
		magnitude := delta
		if magnitude < 0 {
			magnitude = -magnitude
		}
		offset, err := SourceDurationUs(magnitude, project.Settings, c.SpeedNum, c.SpeedDen)
		if err != nil {
			return Clip{}, err
		}
		if delta < 0 {
			if offset > c.SourceInUs {
				return Clip{}, problem("trim_range", "起点修剪超出素材开头")
			}
			after.SourceInUs -= offset
		} else {
			if c.SourceInUs > math.MaxInt64-offset {
				return Clip{}, problem("trim_range", "修剪后的源入点溢出")
			}
			after.SourceInUs += offset
		}
	}
	return after, nil
}
