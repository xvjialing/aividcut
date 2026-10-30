package captions

import (
	"sort"

	"aividcut/internal/editor"
)

type BoundWord struct {
	editor.WordSpan
	WordID    string `json:"wordId"`
	ClipID    string `json:"clipId"`
	AssetID   string `json:"assetId"`
	Text      string `json:"text"`
	SpeakerID string `json:"speakerId,omitempty"`
}

// SourceWordFrames 是派生前的纯帧映射辅助；持久绑定和锁轨仍由 editor 命令负责。
// 它使用源词稳定标识，静态 SRT 导入不会调用本函数或伪造这些标识。
func SourceWordFrames(project editor.Project, clipID string, words []editor.SourceWord) ([]BoundWord, error) {
	if err := editor.ValidateProject(project); err != nil {
		return nil, err
	}
	var source *editor.Clip
	for i := range project.Clips {
		if project.Clips[i].ID == clipID {
			source = &project.Clips[i]
			break
		}
	}
	if source == nil || source.Kind != "video" && source.Kind != "audio" {
		return nil, fail("source_clip", "源词帧映射需要有效的音视频片段")
	}
	var duration int64
	for _, asset := range project.Assets {
		if asset.ID == source.AssetID {
			duration = asset.DurationUs
			break
		}
	}
	if len(words) > editor.MaxTranscriptWords {
		return nil, fail("word_limit", "源词数量超过当前限制")
	}
	if err := editor.ValidateSourceWords(words, duration); err != nil {
		return nil, err
	}
	result := []BoundWord{}
	for _, word := range words {
		span, visible, err := editor.MapWordToClip(word, *source, project.Settings)
		if err != nil {
			return nil, err
		}
		if visible {
			result = append(result, BoundWord{WordSpan: span, WordID: word.ID, ClipID: clipID, AssetID: source.AssetID, Text: word.Text, SpeakerID: word.SpeakerID})
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].StartFrame < result[j].StartFrame })
	return result, nil
}
