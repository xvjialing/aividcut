package application

import (
	"context"
	"errors"
	"fmt"

	"aividcut/internal/editor"
)

type AddAudioInput struct {
	ProjectID       string `json:"projectId"`
	ProjectRevision int64  `json:"projectRevision"`
	AssetID         string `json:"assetId"`
	ClipID          string `json:"clipId"`
	StartFrame      int64  `json:"startFrame"`
	TrackID         string `json:"trackId,omitempty"`
}

func audioPlacementFree(p editor.Project, candidate editor.Clip, asset editor.Asset, trackID string) bool {
	rangeA, err := editor.ResolveAudioRange(candidate, asset, p.Settings)
	if err != nil {
		return false
	}
	if rangeA.Empty {
		return true
	}
	startA, _ := rangeA.TimelineStart.Rat()
	endA, _ := rangeA.TimelineEnd.Rat()
	for _, clip := range p.Clips {
		if clip.TrackID != trackID {
			continue
		}
		index := assetIndex(p, clip.AssetID)
		if index < 0 {
			return false
		}
		rangeB, err := editor.ResolveAudioRange(clip, p.Assets[index], p.Settings)
		if err != nil {
			return false
		}
		if rangeB.Empty {
			continue
		}
		startB, _ := rangeB.TimelineStart.Rat()
		endB, _ := rangeB.TimelineEnd.Rat()
		if startA.Cmp(endB) < 0 && endA.Cmp(startB) > 0 {
			return false
		}
	}
	return true
}

// AddAudioToTimeline与配音导入共用完整音轨领域helper，前端不复制样本/分数规则。
func (s *Service) AddAudioToTimeline(ctx context.Context, input AddAudioInput) (Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if !agentIDPattern.MatchString(input.ClipID) || input.StartFrame < 0 {
		return Bootstrap{}, errors.New("添加音频需要有效片段标识与非负起点")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.engine.Snapshot()
	if p.ID != input.ProjectID {
		return Bootstrap{}, errors.New("添加音频的原工程已更换")
	}
	for _, clip := range p.Clips {
		if clip.ID == input.ClipID {
			if clip.Kind == "audio" && clip.AssetID == input.AssetID && clip.StartFrame == input.StartFrame && (input.TrackID == "" || clip.TrackID == input.TrackID) {
				return s.bootstrapLocked(), nil
			}
			return Bootstrap{}, errors.New("该片段标识已用于不同内容")
		}
	}
	if p.Revision != input.ProjectRevision {
		return Bootstrap{}, errors.New("工程版本已变化，请重新核对音频添加位置")
	}
	index := assetIndex(p, input.AssetID)
	if index < 0 || p.Assets[index].Missing || !p.Assets[index].HasAudio {
		return Bootstrap{}, errors.New("请选择已导入且可用的音频素材")
	}
	asset := p.Assets[index]
	clip := editor.DefaultClip(input.ClipID, input.TrackID, "audio")
	clip.AssetID, clip.Name, clip.StartFrame = asset.ID, asset.Name, input.StartFrame
	var err error
	clip, err = editor.AudioClipToSourceEnd(clip, asset, p.Settings)
	if err != nil {
		return Bootstrap{}, err
	}
	commands := []editor.Command{}
	if clip.TrackID == "" {
		count := 0
		for _, track := range p.Tracks {
			if track.Kind != "audio" {
				continue
			}
			count++
			if !track.Locked && audioPlacementFree(p, clip, asset, track.ID) {
				clip.TrackID = track.ID
				break
			}
		}
		if clip.TrackID == "" {
			track := editor.Track{ID: editor.NewID("audio-track"), Name: fmt.Sprintf("音频 %d", count+1), Kind: "audio"}
			clip.TrackID = track.ID
			commands = append(commands, editor.Command{Type: "add_track", Track: &track})
		}
	}
	commands = append(commands, editor.Command{Type: "add_clip", Clip: &clip})
	if err := ctx.Err(); err != nil {
		return Bootstrap{}, err
	}
	if _, err := s.engine.Apply(editor.EditRequest{BaseRevision: p.Revision, IdempotencyKey: "add-audio-" + input.ClipID, Label: "添加完整音频", Commands: commands}); err != nil {
		return Bootstrap{}, err
	}
	result := s.bootstrapLocked()
	if asset.AudioSampleCount == nil {
		result.Warnings = []string{"该音轨尚未实测样本数，本次按已声明的微秒末端添加；显示占位向上取整，没有将未知样本标为实测。"}
	}
	return result, nil
}
