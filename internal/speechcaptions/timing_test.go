package speechcaptions

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"aividcut/internal/speech"
)

func number(value float64) *float64 { return &value }
func aligned(text []string, starts, ends []float64) *speech.CharacterAlignment {
	a := &speech.CharacterAlignment{Characters: text}
	for _, value := range starts {
		a.StartTimesSeconds = append(a.StartTimesSeconds, number(value))
	}
	for _, value := range ends {
		a.EndTimesSeconds = append(a.EndTimesSeconds, number(value))
	}
	return a
}
func timed(a *speech.CharacterAlignment) speech.TimedAudioResult {
	return speech.TimedAudioResult{AudioResult: speech.AudioResult{Operation: speech.Operation{Status: speech.Succeeded}}, Alignment: a}
}

func TestActualCharacterTimesBecomeWordsWithoutInventedGaps(t *testing.T) {
	chars := []string{"H", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d", "!", "你", "好", "。"}
	starts := []float64{0, .07, .14, .21, .28, .35, .8, .9, 1, 1.1, 1.2, 1.3, 1.6, 1.8, 2}
	ends := []float64{.07, .14, .21, .28, .35, .35, .9, 1, 1.1, 1.2, 1.3, 1.3, 1.8, 2, 2}
	r := timed(aligned(chars, starts, ends))
	converted, err := Convert("job-1", strings.Join(chars, ""), r, 2500000, "original")
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Words) != 4 || converted.Words[0].Text != "Hello" || converted.Words[1].Text != " world!" || converted.Words[2].Text != "你" || converted.Words[3].Text != "好。" {
		t.Fatal("英文空格/中文标点未按真实字符保留", converted.Words)
	}
	if converted.Words[0].StartUs != 0 || converted.Words[0].EndUs != 350000 || converted.Words[1].StartUs != 800000 || converted.Words[1].EndUs != 1300000 {
		t.Fatal("空格或文字比例改变了实际词间间隔")
	}
	again, err := Convert("job-1", strings.Join(chars, ""), r, 2500000, "original")
	if err != nil || !reflect.DeepEqual(converted, again) {
		t.Fatal("相同时间证据没有稳定词标识", err)
	}
	if r.Alignment.StartTimesSeconds[6] == nil || *r.Alignment.StartTimesSeconds[6] != .8 {
		t.Fatal("转换修改了原始响应")
	}
}

func TestNormalizedTimingRequiresExplicitChoiceAndOriginalMustMatch(t *testing.T) {
	r := timed(aligned([]string{"1", "2"}, []float64{0, .3}, []float64{.3, .6}))
	r.NormalizedAlignment = aligned([]string{"十", "二"}, []float64{0, .35}, []float64{.35, .7})
	original, err := Convert("job", "12", r, 1000000, "original")
	if err != nil || original.Text != "12" {
		t.Fatal(err)
	}
	normalized, err := Convert("job", "12", r, 1000000, "normalized")
	if err != nil || normalized.Text != "十二" || len(normalized.Warnings) == 0 || normalized.Words[1].EndUs != 700000 {
		t.Fatal("朗读与原文混淆", err)
	}
	if _, err := Convert("job", "不同原文", r, 1000000, "original"); err == nil {
		t.Fatal("原文不匹配仍绑定")
	}
	r.Alignment.StartTimesSeconds[0] = nil
	if _, err := Convert("job", "12", r, 1000000, "original"); err == nil {
		t.Fatal("缺失原文时间被normalized补齐")
	}
	if _, err := Convert("job", "12", r, 1000000, "normalized"); err != nil {
		t.Fatal("显式朗读文本错误依赖原文完整性", err)
	}
}

func TestInvalidTimingNeverBecomesPlausibleCaption(t *testing.T) {
	tests := map[string]func(*speech.TimedAudioResult){
		"缺时间":  func(r *speech.TimedAudioResult) { r.Alignment.EndTimesSeconds[0] = nil },
		"非有限":  func(r *speech.TimedAudioResult) { r.Alignment.StartTimesSeconds[0] = number(math.Inf(1)) },
		"负数":   func(r *speech.TimedAudioResult) { r.Alignment.StartTimesSeconds[0] = number(-.1) },
		"越音频尾": func(r *speech.TimedAudioResult) { r.Alignment.EndTimesSeconds[0] = number(2) },
		"零词长":  func(r *speech.TimedAudioResult) { r.Alignment.EndTimesSeconds[0] = number(0) },
		"数组错长": func(r *speech.TimedAudioResult) { r.Alignment.EndTimesSeconds = nil },
		"未知结果": func(r *speech.TimedAudioResult) { r.SubmissionUncertain = true },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			r := timed(aligned([]string{"字"}, []float64{0}, []float64{.3}))
			change(&r)
			if _, err := Convert("job", "字", r, 1000000, "original"); err == nil {
				t.Fatal("无效时间通过")
			}
		})
	}
}

func TestCombinedGlyphAndOpeningPunctuationAreNotSplitOrEstimated(t *testing.T) {
	r := timed(aligned([]string{"“", "e", "\u0301", "”", " ", "👩", "\u200d", "💻"}, []float64{0, 0, .1, .2, .2, .4, .5, .5}, []float64{0, .1, .2, .2, .2, .5, .5, .7}))
	text := strings.Join(r.Alignment.Characters, "")
	result, err := Convert("job", text, r, 1000000, "original")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Words) != 2 || result.Words[0].Text != "“e\u0301”" || result.Words[1].Text != " 👩\u200d💻" || result.Words[1].StartUs != 400000 || result.Words[1].EndUs != 700000 {
		t.Fatal(result.Words)
	}
}

func TestLongLatinAlignmentKeepsOneWordAndBoundedAllocations(t *testing.T) {
	const size = 10000
	a := &speech.CharacterAlignment{}
	for i := 0; i < size; i++ {
		a.Characters = append(a.Characters, "a")
		a.StartTimesSeconds = append(a.StartTimesSeconds, number(float64(i)/10000))
		a.EndTimesSeconds = append(a.EndTimesSeconds, number(float64(i+1)/10000))
	}
	r := timed(a)
	text := strings.Repeat("a", size)
	allocs := testing.AllocsPerRun(1, func() {
		result, err := Convert("long-word", text, r, 2000000, "original")
		if err != nil || len(result.Words) != 1 || result.Words[0].Text != text || result.Words[0].EndUs != 1000000 {
			t.Fatal("长词转换改变真实文字/时间", err)
		}
	})
	if allocs > 200 {
		t.Fatalf("字符链不应逐项复制整段文字，allocs=%f", allocs)
	}
}
