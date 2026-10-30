package captions

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestSRTBOMCRLFChineseMultilineAndCanonicalRoundTrip(t *testing.T) {
	input := "\uFEFF7\r\n00:00:03,120 --> 00:00:04,500\r\n第三句：中文与 English\r\n第二行保留\r\n\r\n2\r\n00:00:00,000 --> 00:00:01,010\r\n 开头的空格保留 \r\n\r\n"
	cues, err := ParseSRT(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 || cues[0].StartMS != 3120 || cues[0].Text != "第三句：中文与 English\n第二行保留" || cues[1].Text != " 开头的空格保留 " {
		t.Fatalf("解析丢失文本或时间：%+v", cues)
	}
	before := append([]Cue{}, cues...)
	output, err := FormatSRT(cues)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "\r") || !strings.HasPrefix(output, "1\n00:00:00,000 --> 00:00:01,010\n") || !reflect.DeepEqual(cues, before) {
		t.Fatal("没有规范排序/重编号，或修改了调用方数据")
	}
	parsed, err := ParseSRT(output)
	if err != nil {
		t.Fatal(err)
	}
	expected := []Cue{cues[1], cues[0]}
	if !reflect.DeepEqual(parsed, expected) {
		t.Fatalf("往返丢失字幕：%+v", parsed)
	}
	second, _ := FormatSRT(parsed)
	if second != output {
		t.Fatal("规范 SRT 重复往返发生变化")
	}
}

func TestSameStartAndOverlappingCuesRemainDistinctAndStable(t *testing.T) {
	cues := []Cue{{1000, 3000, "长"}, {1000, 2000, "短一"}, {1000, 2000, "短二"}, {1500, 2500, "重叠"}}
	output, err := FormatSRT(cues)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSRT(output)
	if err != nil {
		t.Fatal(err)
	}
	expected := []Cue{cues[1], cues[2], cues[0], cues[3]}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("相同时间的字幕被合并或丢失：%+v", got)
	}
}

func TestSRTInvalidSyntaxAndIntervalsAreRejected(t *testing.T) {
	valid := "1\n00:00:00,000 --> 00:00:01,000\n文本"
	tests := map[string]string{
		"空内容":     "\uFEFF\r\n ",
		"非UTF8":   string([]byte{0xff, 0xfe}),
		"零序号":     strings.Replace(valid, "1\n", "0\n", 1),
		"序号重复":    valid + "\n\n" + valid,
		"等价序号重复":  "01\n00:00:00,000 --> 00:00:01,000\n一\n\n" + valid,
		"无序号":     "00:00:00,000 --> 00:00:01,000\n文本",
		"没有文本":    "1\n00:00:00,000 --> 00:00:01,000",
		"小数点分隔":   strings.ReplaceAll(valid, ",", "."),
		"分钟超范围":   strings.Replace(valid, "00:00:01,000", "00:60:01,000", 1),
		"小时只有一位":  strings.Replace(valid, "00:00:00,000", "0:00:00,000", 1),
		"倒置时间":    "1\n00:00:02,000 --> 00:00:01,000\n文本",
		"零时长":     "1\n00:00:01,000 --> 00:00:01,000\n文本",
		"小时溢出":    "1\n999999999999999999999:00:00,000 --> 999999999999999999999:00:01,000\n文本",
		"时间乘法溢出":  "1\n3000000000000:00:00,000 --> 3000000000000:00:01,000\n文本",
		"不支持位置扩展": strings.Replace(valid, " --> 00:00:01,000", " --> 00:00:01,000 X1:0", 1),
		"控制字符":    valid + "\x00",
		"相邻段缺空行":  valid + "\n2\n00:00:02,000 --> 00:00:03,000\n二",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSRT(input); err == nil {
				t.Fatal("非法字幕被接受")
			}
		})
	}
}

func TestFormatRejectsUnrepresentablePayloadAndKeepsLargeHours(t *testing.T) {
	for _, cue := range []Cue{{-1, 10, "负数"}, {0, 0, "空区间"}, {0, 10, "空行\n\n新段"}, {0, 10, "   "}, {0, 10, "控制\x1b字符"}, {0, 10, "2\n00:00:01,000 --> 00:00:02,000\n歧义"}} {
		if _, err := FormatSRT([]Cue{cue}); err == nil {
			t.Fatalf("不可靠的格式化被接受：%+v", cue)
		}
	}
	cues := []Cue{{StartMS: math.MaxInt64 - 1, EndMS: math.MaxInt64, Text: "长时长"}}
	output, err := FormatSRT(cues)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSRT(output)
	if err != nil || !reflect.DeepEqual(parsed, cues) {
		t.Fatalf("大时间值往返错误：%+v, %v", parsed, err)
	}
}
