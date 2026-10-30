package editor

// NormalizeCaptionLayout 返回独立规范值；nil保留未指定，readable零字段使用统一默认值。
func NormalizeCaptionLayout(layout *CaptionLayout) (*CaptionLayout, error) {
	if layout == nil {
		return nil, nil
	}
	value := *layout
	if !validKind(value.Mode, "word", "readable") || value.MaxCharsPerLine < 0 || value.MaxCharsPerLine > 120 || value.MaxLines < 0 || value.MaxLines > 4 || value.MaxDurationUs < 0 || value.MaxDurationUs > 60_000_000 || value.PauseThresholdUs < 0 || value.PauseThresholdUs > 10_000_000 {
		return nil, problem("caption_layout", "字幕模式或阅读分句范围无效")
	}
	if value.Mode == "word" {
		return &CaptionLayout{Mode: "word"}, nil
	}
	if value.MaxCharsPerLine == 0 {
		value.MaxCharsPerLine = 18
	}
	if value.MaxLines == 0 {
		value.MaxLines = 2
	}
	if value.MaxDurationUs == 0 {
		value.MaxDurationUs = 4_000_000
	}
	if value.PauseThresholdUs == 0 {
		value.PauseThresholdUs = 700_000
	}
	if value.MaxCharsPerLine < 2 || value.MaxDurationUs < 100_000 {
		return nil, problem("caption_layout", "阅读分句至少需要每行2字、单组时长上限100毫秒")
	}
	return &value, nil
}

func effectiveCaptionLayout(transcript Transcript) (CaptionLayout, error) {
	value, err := NormalizeCaptionLayout(transcript.CaptionLayout)
	if err != nil {
		return CaptionLayout{}, err
	}
	if value == nil {
		return CaptionLayout{Mode: "word"}, nil
	}
	return *value, nil
}

func validateTranscriptOrigin(transcript Transcript) error {
	if !validKind(transcript.Origin, "", "asr", "tts") {
		return problem("transcript_origin", "转写来源仅支持asr或tts，旧工程可为空")
	}
	if transcript.Origin == "tts" {
		if !validKind(transcript.TimingSource, "original", "normalized") {
			return problem("transcript_origin", "生成配音必须明确原文或规范化文本的真实时间来源")
		}
		return nil
	}
	if transcript.TimingSource != "" {
		return problem("transcript_origin", "原文/规范化时间来源仅用于生成配音，不能标到ASR或旧未知来源")
	}
	return nil
}
