// Package speechcaptions 将供应商真实字符时间转换为工程源词，不执行识别或按文本估算时间。
package speechcaptions

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"aividcut/internal/editor"
	"aividcut/internal/speech"
)

type Result struct {
	Source   string
	Text     string
	Words    []editor.SourceWord
	Warnings []string
}

type token struct {
	text        strings.Builder
	start, end  int64
	first, last int
	latin       bool
}

// Convert 只使用明确选择的一套时间，原文必须与提交文本完全匹配。
// 规范化朗读文本可以不同，但不会被冒充为原文，也不会从另一套时间补洞。
func Convert(jobID, requestedText string, result speech.TimedAudioResult, durationUs int64, source string) (Result, error) {
	out := Result{Source: source, Words: []editor.SourceWord{}, Warnings: []string{}}
	if result.Status != speech.Succeeded || result.SubmissionUncertain || durationUs <= 0 {
		return out, errors.New("配音结果或实际音频时长尚未核对")
	}
	var alignment *speech.CharacterAlignment
	switch source {
	case "original":
		alignment = result.Alignment
	case "normalized":
		alignment = result.NormalizedAlignment
	default:
		return out, errors.New("请选择配音原文或朗读文本的时间来源")
	}
	if alignment == nil || len(alignment.Characters) == 0 {
		return out, errors.New("所选文本没有服务商返回的字符时间；仍可单独导入音频")
	}
	if len(alignment.Characters) > 100000 || len(alignment.Characters) != len(alignment.StartTimesSeconds) || len(alignment.Characters) != len(alignment.EndTimesSeconds) {
		return out, errors.New("所选文本的时间数组长度不一致或超过当前限制")
	}
	text := strings.Join(alignment.Characters, "")
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || len(text) > 16<<20 {
		return out, errors.New("所选配音文本为空、编码无效或超过当前限制")
	}
	if source == "original" && text != requestedText {
		return out, errors.New("原文时间对应的字符与提交文本不一致，不能据此生成原文字幕")
	}
	for _, char := range text {
		if unicode.IsControl(char) && char != '\n' && char != '\t' {
			return out, errors.New("所选文本包含当前字幕不支持的控制字符")
		}
	}
	var tokens []*token
	var pending strings.Builder
	var lastStart, lastEnd int64 = -1, -1
	for i, item := range alignment.Characters {
		if item == "" || !utf8.ValidString(item) {
			return out, errors.New("配音时间包含无效字符项")
		}
		start, err := microseconds(alignment.StartTimesSeconds[i], durationUs)
		if err != nil {
			return out, err
		}
		end, err := microseconds(alignment.EndTimesSeconds[i], durationUs)
		if err != nil {
			return out, err
		}
		if start < lastStart || end < lastEnd || end < start {
			return out, errors.New("配音时间乱序或区间倒置，未调整原始时间")
		}
		lastStart, lastEnd = start, end
		if strings.TrimSpace(item) == "" {
			pending.WriteString(item)
			continue
		}
		punct := allPunctuation(item)
		if punct && (len(tokens) == 0 || openingPunctuation(item)) {
			pending.WriteString(item)
			continue
		}
		if punct {
			last := tokens[len(tokens)-1]
			last.text.WriteString(pending.String())
			last.text.WriteString(item)
			last.end = max(last.end, end)
			last.last = i
			last.latin = last.latin && (item == "'" || item == "’" || item == "-") && pending.Len() == 0
			pending.Reset()
			continue
		}
		latin := latinPiece(item)
		if len(tokens) > 0 && pending.Len() == 0 && (latin && tokens[len(tokens)-1].latin || continuation(item, tokens[len(tokens)-1].text.String())) {
			last := tokens[len(tokens)-1]
			last.text.WriteString(item)
			last.end = max(last.end, end)
			last.last = i
			last.latin = latin
			continue
		}
		value := &token{start: start, end: end, first: i, last: i, latin: latin}
		value.text.WriteString(pending.String())
		value.text.WriteString(item)
		tokens = append(tokens, value)
		pending.Reset()
	}
	if len(tokens) == 0 {
		return out, errors.New("配音时间没有可绑定的有声词条，音频仍可单独导入")
	}
	tokens[len(tokens)-1].text.WriteString(pending.String())
	sum := sha256.Sum256([]byte(jobID + "\x00" + source))
	prefix := hex.EncodeToString(sum[:12])
	for _, value := range tokens {
		if value.end <= value.start {
			return out, errors.New("配音存在没有有效持续时间的词条，不能按比例补齐")
		}
		out.Words = append(out.Words, editor.SourceWord{ID: fmt.Sprintf("tts-%s-%d-%d", prefix, value.first, value.last), StartUs: value.start, EndUs: value.end, Text: value.text.String()})
	}
	if err := editor.ValidateSourceWords(out.Words, durationUs); err != nil {
		return out, err
	}
	out.Text = text
	if source == "normalized" && text != requestedText {
		out.Warnings = append(out.Warnings, "字幕采用服务商返回的朗读文本，数字或缩写可能与配音原文不同。")
	}
	return out, nil
}

func microseconds(value *float64, durationUs int64) (int64, error) {
	if value == nil {
		return 0, errors.New("所选文本有缺失时间，未填零或借用另一套对齐")
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > float64(math.MaxInt64)/1e6 {
		return 0, errors.New("配音字符时间不是有效的有限秒数")
	}
	rounded := math.Round(*value * 1e6)
	if rounded > float64(durationUs) {
		return 0, errors.New("配音字符时间超出本机核对的音频时长，未裁切或拉伸")
	}
	return int64(rounded), nil
}

func allPunctuation(value string) bool {
	for _, char := range value {
		if !unicode.IsPunct(char) {
			return false
		}
	}
	return true
}
func openingPunctuation(value string) bool {
	for _, char := range value {
		if !strings.ContainsRune("([{“‘《「『【〈（［｛", char) {
			return false
		}
	}
	return true
}
func latinPiece(value string) bool {
	for _, char := range value {
		if !(unicode.Is(unicode.Latin, char) || unicode.IsDigit(char) || unicode.Is(unicode.Mn, char) || unicode.Is(unicode.Mc, char)) {
			return false
		}
	}
	return true
}
func continuation(value, previous string) bool {
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(previous)
	return first == '\u200d' || last == '\u200d' || unicode.Is(unicode.Mn, first) || unicode.Is(unicode.Mc, first) || first == '\ufe0f' || first == '\ufe0e'
}
