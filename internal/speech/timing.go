package speech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"
)

// CharacterAlignment 保留供应商原始字符与秒数组；nil不转成0，不按文本比例插值。
type CharacterAlignment struct {
	Characters        []string   `json:"characters"`
	StartTimesSeconds []*float64 `json:"startTimesSeconds"`
	EndTimesSeconds   []*float64 `json:"endTimesSeconds"`
}
type TimedAudioResult struct {
	AudioResult
	Alignment           *CharacterAlignment `json:"alignment"`
	NormalizedAlignment *CharacterAlignment `json:"normalizedAlignment"`
	TimingComplete      bool                `json:"timingComplete"`
	Warnings            []string            `json:"warnings"`
	TimingReason        string              `json:"timingReason,omitempty"`
	// 只有字段类型无法映射到数组时保留原始JSON，不用normalized代替原文。
	RawAlignment           json.RawMessage `json:"rawAlignment,omitempty"`
	RawNormalizedAlignment json.RawMessage `json:"rawNormalizedAlignment,omitempty"`
}
type TimedTTSProvider interface {
	TTSProvider
	SynthesizeWithTiming(context.Context, SynthesizeRequest) (TimedAudioResult, error)
}

var _ TimedTTSProvider = (*ElevenLabs)(nil)

type alignmentResponse struct {
	Characters []string   `json:"characters"`
	Starts     []*float64 `json:"character_start_times_seconds"`
	Ends       []*float64 `json:"character_end_times_seconds"`
}

// SynthesizeWithTiming 是独立的显式调用，不由 Synthesize 自动回退、升级或重试。
func (p *ElevenLabs) SynthesizeWithTiming(parent context.Context, request SynthesizeRequest) (TimedAudioResult, error) {
	operation, err := newOperation(request.ID, MultilingualV2)
	result := TimedAudioResult{AudioResult: AudioResult{Operation: operation, VoiceID: request.VoiceID, OutputFormat: "mp3_44100_128", MIME: "audio/mpeg"}, Warnings: []string{}}
	if err != nil {
		return result, err
	}
	if request.ModelID != "" && request.ModelID != MultilingualV2 {
		return result, failure("model", "当前带时间配音仅支持已核查的eleven_multilingual_v2")
	}
	if !safeID.MatchString(request.VoiceID) {
		return result, failure("voice", "需要有效音色标识")
	}
	if !utf8.ValidString(request.Text) || strings.TrimSpace(request.Text) == "" || utf8.RuneCountInString(request.Text) > 10000 {
		return result, failure("text", "带时间配音文本需为非空且不超过10000字符的UTF-8文本")
	}
	if request.OutputFormat != "" && request.OutputFormat != "mp3_44100_128" {
		return result, failure("output_format", "带时间配音当前仅核查mp3_44100_128输出")
	}
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"text": request.Text, "model_id": MultilingualV2})
	data, remoteID, err := p.request(ctx, http.MethodPost, "/v1/text-to-speech/"+request.VoiceID+"/with-timestamps?output_format=mp3_44100_128", "application/json", bytes.NewReader(body), int64(len(body)), 96<<20)
	result.RemoteID = remoteID
	if err != nil {
		result.Operation = failedOperation(result.Operation, err)
		return result, err
	}
	var response struct {
		Audio      string          `json:"audio_base64"`
		Alignment  json.RawMessage `json:"alignment"`
		Normalized json.RawMessage `json:"normalized_alignment"`
	}
	if err = decodeJSON(data, &response); err != nil {
		result.Operation = failedOperation(result.Operation, err)
		return result, err
	}
	if len(response.Audio) == 0 || len(response.Audio) > base64.StdEncoding.EncodedLen(int(MaxResponseBytes)) {
		err = invalidResponse("带时间配音的音频为空或超过64MiB限制")
	} else {
		var audio []byte
		audio, err = base64.StdEncoding.Strict().DecodeString(response.Audio)
		if err != nil || int64(len(audio)) > MaxResponseBytes || len(audio) < 4 || mediaMIME(audio, "audio") != "audio/mpeg" {
			err = invalidResponse("带时间配音返回了无法识别的MP3编码")
		} else {
			// 音频和时间证据独立：合法音频不能因坏时间数组被丢弃并诱发再次付费。
			result.Data = audio
			result.Alignment, result.RawAlignment = decodeAlignmentEvidence(response.Alignment)
			result.NormalizedAlignment, result.RawNormalizedAlignment = decodeAlignmentEvidence(response.Normalized)
			_, result.TimingComplete, result.TimingReason = AssessTiming(result)
			if !result.TimingComplete {
				result.Warnings = append(result.Warnings, result.TimingReason+"；音频保留，未用normalized结果或字符比例替代")
			}
			if _, normalizedErr := ValidateCharacterAlignment(result.NormalizedAlignment); normalizedErr != nil || len(result.RawNormalizedAlignment) > 0 {
				result.Warnings = append(result.Warnings, "规范化字符时间结构无效；原始证据独立保留，未替换原文对齐")
			}
		}
	}
	if err != nil {
		result.Operation = failedOperation(result.Operation, err)
		return result, err
	}
	result.Status = Succeeded
	return result, nil
}

func decodeAlignmentEvidence(data json.RawMessage) (*CharacterAlignment, json.RawMessage) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil
	}
	var value alignmentResponse
	if json.Unmarshal(data, &value) != nil {
		return nil, append(json.RawMessage(nil), data...)
	}
	return &CharacterAlignment{Characters: value.Characters, StartTimesSeconds: value.Starts, EndTimesSeconds: value.Ends}, nil
}

// ValidateCharacterAlignment只检查数组与时间结构，不验证原文匹配或实际音频范围。
func ValidateCharacterAlignment(value *CharacterAlignment) (bool, error) {
	if value == nil {
		return false, nil
	}
	if len(value.Characters) != len(value.StartTimesSeconds) || len(value.Characters) != len(value.EndTimesSeconds) || len(value.Characters) > 100000 {
		return false, invalidResponse("配音字符对齐的数组长度无效或超过100000项")
	}
	complete := len(value.Characters) > 0
	var lastStart, lastEnd float64 = -1, -1
	for index, character := range value.Characters {
		if !utf8.ValidString(character) || character == "" {
			return false, invalidResponse("配音对齐含无效字符")
		}
		start, end := value.StartTimesSeconds[index], value.EndTimesSeconds[index]
		if _, err := seconds(start); err != nil {
			return false, invalidResponse("配音字符开始时间无效")
		}
		if _, err := seconds(end); err != nil {
			return false, invalidResponse("配音字符结束时间无效")
		}
		if start != nil && (*start < lastStart || end != nil && *end < *start) || end != nil && *end < lastEnd {
			return false, invalidResponse("配音字符时间戳乱序或区间倒置")
		}
		if start == nil || end == nil {
			complete = false
		}
		if start != nil {
			lastStart = *start
		}
		if end != nil {
			lastEnd = *end
		}
	}
	return complete, nil
}

// AssessTiming不信任供应商的TimingComplete标记，且绝不以normalized对齐补原文。
func AssessTiming(result TimedAudioResult) (available, complete bool, reason string) {
	available = result.Alignment != nil || len(result.RawAlignment) > 0
	if len(result.RawAlignment) > 0 {
		return available, false, "原文字符时间字段类型无效"
	}
	if result.Alignment == nil {
		return false, false, "供应商未返回原文字符时间戳"
	}
	complete, err := ValidateCharacterAlignment(result.Alignment)
	if err != nil {
		return available, false, "原文字符时间结构无效"
	}
	if !complete {
		return available, false, "原文字符时间戳为空或含缺失时间"
	}
	return available, true, "原文时间数组完整；文字匹配与音频范围仍需核对"
}
