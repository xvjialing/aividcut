// Package captions 将静态字幕交换与源转写映射分开；导入 SRT 不会自动绑定 ASR。
package captions

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxSRTBytes = 32 << 20
const MaxCues = 100_000

// Cue 的时间区间是 [StartMS, EndMS)，文本保留换行，不解释样式标签。
type Cue struct {
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
	Text    string `json:"text"`
}

type Error struct {
	Code    string `json:"code"`
	Line    int    `json:"line,omitempty"`
	Cue     int    `json:"cue,omitempty"`
	Message string `json:"message"`
}

func (err *Error) Error() string {
	if err.Line > 0 {
		return fmt.Sprintf("字幕第 %d 行：%s", err.Line, err.Message)
	}
	if err.Cue > 0 {
		return fmt.Sprintf("字幕第 %d 项：%s", err.Cue, err.Message)
	}
	return err.Message
}

func fail(code, message string) error { return &Error{Code: code, Message: message} }

var timingPattern = regexp.MustCompile(`^([0-9]{2,}):([0-5][0-9]):([0-5][0-9]),([0-9]{3})[ \t]+-->[ \t]+([0-9]{2,}):([0-5][0-9]):([0-5][0-9]),([0-9]{3})$`)
var indexPattern = regexp.MustCompile(`^[0-9]+$`)

func normalizeNewlines(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// ParseSRT 接受 UTF-8、可选 BOM、CRLF/LF 和多行字幕，拒绝无效区间与歧义结构。
// 序号必须是唯一正整数，可以不连续；返回顺序与文件相同，导出时才排序重编号。
func ParseSRT(text string) ([]Cue, error) {
	if len(text) > MaxSRTBytes {
		return nil, fail("size_limit", "SRT 文件超过 32MiB 限制")
	}
	if !utf8.ValidString(text) {
		return nil, fail("encoding", "SRT 需要使用 UTF-8 编码")
	}
	text = normalizeNewlines(strings.TrimPrefix(text, "\uFEFF"))
	lines := strings.Split(text, "\n")
	cues := make([]Cue, 0)
	indices := make(map[uint64]bool)
	for line := 0; line < len(lines); {
		if strings.TrimSpace(lines[line]) == "" {
			line++
			continue
		}
		start := line
		for line < len(lines) && strings.TrimSpace(lines[line]) != "" {
			line++
		}
		block := lines[start:line]
		if len(block) < 3 {
			return nil, &Error{Code: "syntax", Line: start + 1, Message: "每段需要序号、时间区间和非空文本"}
		}
		indexText := strings.TrimSpace(block[0])
		index, err := strconv.ParseUint(indexText, 10, 64)
		if !indexPattern.MatchString(indexText) || err != nil || index == 0 {
			return nil, &Error{Code: "index", Line: start + 1, Message: "序号必须为有效正整数"}
		}
		if indices[index] {
			return nil, &Error{Code: "index", Line: start + 1, Message: "字幕序号重复"}
		}
		indices[index] = true
		match := timingPattern.FindStringSubmatch(strings.TrimSpace(block[1]))
		if match == nil {
			return nil, &Error{Code: "timestamp", Line: start + 2, Message: "时间格式需要为 HH:MM:SS,mmm --> HH:MM:SS,mmm"}
		}
		from, err := timestampMS(match[1:5])
		if err != nil {
			return nil, &Error{Code: "timestamp", Line: start + 2, Message: err.Error()}
		}
		to, err := timestampMS(match[5:9])
		if err != nil {
			return nil, &Error{Code: "timestamp", Line: start + 2, Message: err.Error()}
		}
		for i := 2; i+1 < len(block); i++ {
			if indexPattern.MatchString(strings.TrimSpace(block[i])) && timingPattern.MatchString(strings.TrimSpace(block[i+1])) {
				return nil, &Error{Code: "syntax", Line: start + i + 1, Message: "相邻字幕段之间缺少空行"}
			}
		}
		cue := Cue{StartMS: from, EndMS: to, Text: strings.Join(block[2:], "\n")}
		if err := validateCue(cue); err != nil {
			return nil, &Error{Code: "cue", Line: start + 2, Message: err.Error()}
		}
		cues = append(cues, cue)
		if len(cues) > MaxCues {
			return nil, fail("cue_limit", "字幕条目超过 100000 条限制")
		}
	}
	if len(cues) == 0 {
		return nil, fail("empty", "字幕文件没有有效内容")
	}
	return cues, nil
}

func timestampMS(parts []string) (int64, error) {
	hours, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fail("time_overflow", "字幕小时数超出可表示范围")
	}
	minutes, _ := strconv.ParseInt(parts[1], 10, 64)
	seconds, _ := strconv.ParseInt(parts[2], 10, 64)
	milliseconds, _ := strconv.ParseInt(parts[3], 10, 64)
	rest := minutes*60_000 + seconds*1000 + milliseconds
	if hours > (math.MaxInt64-rest)/3_600_000 {
		return 0, fail("time_overflow", "字幕时间超出可表示范围")
	}
	return hours*3_600_000 + rest, nil
}

func validateCue(cue Cue) error {
	if cue.StartMS < 0 || cue.EndMS <= cue.StartMS {
		return fail("invalid_interval", "结束时间必须晚于开始时间，且时间不能为负数")
	}
	return validateText(cue.Text)
}

func validateText(text string) error {
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		return fail("text", "字幕文本需要为非空 UTF-8 文本")
	}
	for _, character := range text {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return fail("text", "字幕文本包含不支持的控制字符")
		}
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			return fail("text", "字幕文本不能包含会分隔 SRT 段落的空行")
		}
		if i+1 < len(lines) && indexPattern.MatchString(strings.TrimSpace(line)) && timingPattern.MatchString(strings.TrimSpace(lines[i+1])) {
			return fail("text", "字幕文本含有歧义的序号和时间结构，请拆成独立字幕段落")
		}
	}
	return nil
}

func normalizedCues(cues []Cue) ([]Cue, error) {
	if len(cues) > MaxCues {
		return nil, fail("cue_limit", "字幕条目超过 100000 条限制")
	}
	copy := append([]Cue{}, cues...)
	total := 0
	for i := range copy {
		copy[i].Text = normalizeNewlines(copy[i].Text)
		if err := validateCue(copy[i]); err != nil {
			return nil, &Error{Code: "cue", Cue: i + 1, Message: err.Error()}
		}
		if len(copy[i].Text) > MaxSRTBytes-total-80 {
			return nil, fail("size_limit", "字幕内容超过 32MiB 限制")
		}
		total += len(copy[i].Text) + 80
	}
	sort.SliceStable(copy, func(i, j int) bool {
		if copy[i].StartMS == copy[j].StartMS {
			return copy[i].EndMS < copy[j].EndMS
		}
		return copy[i].StartMS < copy[j].StartMS
	})
	return copy, nil
}

// FormatSRT 稳定按开始/结束时间排序，保留同起点、重叠和相同时间的每一条文本。
func FormatSRT(cues []Cue) (string, error) {
	values, err := normalizedCues(cues)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	for i, cue := range values {
		fmt.Fprintf(&output, "%d\n%s --> %s\n%s\n\n", i+1, formatTimestamp(cue.StartMS), formatTimestamp(cue.EndMS), cue.Text)
	}
	return output.String(), nil
}

func formatTimestamp(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3_600_000, ms/60_000%60, ms/1000%60, ms%1000)
}
