package captions

import (
	"aividcut/internal/editor"
	"fmt"
	"math/big"
	"sort"
)

type Word struct {
	StartUs int64  `json:"startUs"`
	EndUs   int64  `json:"endUs"`
	Text    string `json:"text"`
}

type MappedWord struct {
	ClipID        string `json:"clipId"`
	AssetID       string `json:"assetId"`
	WordIndex     int    `json:"wordIndex"`
	SourceStartUs int64  `json:"sourceStartUs"`
	SourceEndUs   int64  `json:"sourceEndUs"`
	StartUs       int64  `json:"startUs"`
	EndUs         int64  `json:"endUs"`
	Text          string `json:"text"`
	Partial       bool   `json:"partial"`
}

// MapSourceWords 是纯源时间映射，不选择可听轨道、不持久化绑定，也不将静态 SRT 冒充 ASR。
// SourceStartUs/SourceEndUs 保留原始词区间，Partial 表示边界被裁切；StartUs/EndUs 为裁切后的工程时间。
// 输出按工程时间稳定排序，重复使用同一素材会产生多次映射，不修改调用方输入。
func MapSourceWords(project editor.Project, assetID string, words []Word) ([]MappedWord, error) {
	if err := editor.ValidateProject(project); err != nil {
		return nil, fmt.Errorf("转写映射前工程校验失败：%w", err)
	}
	var source *editor.Asset
	for i := range project.Assets {
		if project.Assets[i].ID == assetID {
			source = &project.Assets[i]
		}
	}
	if source == nil || (source.Kind != "video" && source.Kind != "audio") {
		return nil, fail("source_asset", "转写映射需要有效的音频或视频素材")
	}
	if len(words) > 1_000_000 {
		return nil, fail("word_limit", "词时间条目超过 1000000 条限制")
	}
	textBytes := 0
	for i, word := range words {
		if len(word.Text) > MaxSRTBytes-textBytes {
			return nil, fail("size_limit", "源转写文本超过 32MiB 限制")
		}
		textBytes += len(word.Text)
		if word.StartUs < 0 || word.EndUs <= word.StartUs || word.EndUs > source.DurationUs {
			return nil, &Error{Code: "word_interval", Cue: i + 1, Message: "词时间必须位于源素材有效区间内"}
		}
		if err := validateText(normalizeNewlines(word.Text)); err != nil {
			return nil, &Error{Code: "word_text", Cue: i + 1, Message: err.Error()}
		}
	}
	type indexedWord struct {
		Word
		index        int
		prefixMaxEnd int64
	}
	ordered := make([]indexedWord, len(words))
	for i, word := range words {
		ordered[i] = indexedWord{Word: word, index: i}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StartUs < ordered[j].StartUs })
	var maxEnd int64
	for i := range ordered {
		maxEnd = max(maxEnd, ordered[i].EndUs)
		ordered[i].prefixMaxEnd = maxEnd
	}
	timelineFrameUs := new(big.Rat).SetFrac64(1_000_000*int64(project.Settings.FPSDen), int64(project.Settings.FPSNum))
	mapped := make([]MappedWord, 0)
	visits := 0
	for _, clip := range project.Clips {
		if clip.AssetID != assetID || (clip.Kind != "video" && clip.Kind != "audio") {
			continue
		}
		speed := new(big.Rat).SetFrac64(int64(clip.SpeedNum), int64(clip.SpeedDen))
		clipStart := new(big.Rat).Mul(new(big.Rat).SetInt64(clip.StartFrame), timelineFrameUs)
		sourceIn := new(big.Rat).SetInt64(clip.SourceInUs)
		sourceLength := new(big.Rat).Mul(new(big.Rat).SetInt64(clip.DurationFrames), timelineFrameUs)
		sourceLength.Mul(sourceLength, speed)
		sourceOut := new(big.Rat).Add(sourceIn, sourceLength)
		first := sort.Search(len(ordered), func(i int) bool { return ordered[i].prefixMaxEnd > clip.SourceInUs })
		for _, word := range ordered[first:] {
			visits++
			if visits > 5_000_000 {
				return nil, fail("mapping_limit", "源词区间过于复杂，请限定轨道或片段范围后映射")
			}
			wordStart, wordEnd := new(big.Rat).SetInt64(word.StartUs), new(big.Rat).SetInt64(word.EndUs)
			if wordStart.Cmp(sourceOut) >= 0 {
				break
			}
			if wordEnd.Cmp(sourceIn) <= 0 {
				continue
			}
			partial := false
			if wordStart.Cmp(sourceIn) < 0 {
				wordStart.Set(sourceIn)
				partial = true
			}
			if wordEnd.Cmp(sourceOut) > 0 {
				wordEnd.Set(sourceOut)
				partial = true
			}
			startTime := new(big.Rat).Sub(wordStart, sourceIn)
			startTime.Quo(startTime, speed).Add(startTime, clipStart)
			endTime := new(big.Rat).Sub(wordEnd, sourceIn)
			endTime.Quo(endTime, speed).Add(endTime, clipStart)
			startUs, err := roundedRatio(startTime.Num(), startTime.Denom())
			if err != nil {
				return nil, err
			}
			endUs, err := roundedRatio(endTime.Num(), endTime.Denom())
			if err != nil {
				return nil, err
			}
			// 不足一个可表示微秒的边缘不制造零时长字幕，正常 ASR 词仍以真实区间保留。
			if endUs <= startUs {
				continue
			}
			if len(mapped) >= 1_000_000 {
				return nil, fail("mapping_limit", "映射结果超过 1000000 条限制，请限定轨道或片段范围")
			}
			mapped = append(mapped, MappedWord{ClipID: clip.ID, AssetID: assetID, WordIndex: word.index,
				SourceStartUs: word.StartUs, SourceEndUs: word.EndUs, StartUs: startUs, EndUs: endUs, Text: normalizeNewlines(word.Text), Partial: partial})
		}
	}
	sort.SliceStable(mapped, func(i, j int) bool {
		if mapped[i].StartUs == mapped[j].StartUs {
			return mapped[i].EndUs < mapped[j].EndUs
		}
		return mapped[i].StartUs < mapped[j].StartUs
	})
	return mapped, nil
}
