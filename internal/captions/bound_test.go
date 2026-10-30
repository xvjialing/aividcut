package captions

import (
	"reflect"
	"testing"

	"aividcut/internal/editor"
)

func TestBoundSourceWordFramesUseSharedRationalMapping(t *testing.T) {
	project := sourceProject(t)
	words := []editor.SourceWord{{ID: "edge", StartUs: 900_000, EndUs: 1_200_000, Text: "截断词", SpeakerID: "speaker"}, {ID: "full", StartUs: 2_000_000, EndUs: 3_000_000, Text: "完整词"}, {ID: "out", StartUs: 7_000_000, EndUs: 8_000_000, Text: "之外"}}
	before := append([]editor.SourceWord{}, words...)
	got, err := SourceWordFrames(project, "take", words)
	if err != nil || len(got) != 2 || got[0].StartFrame != 30 || got[0].DurationFrames != 3 || !got[0].Partial || got[0].SpeakerID != "speaker" || got[1].StartFrame != 44 || got[1].DurationFrames != 16 {
		t.Fatalf("绑定词帧映射不正确：%+v %v", got, err)
	}
	if !reflect.DeepEqual(before, words) || len(project.Transcripts) != 0 {
		t.Fatal("纯映射篡改输入或冒充已接受转写")
	}
	if _, err := SourceWordFrames(project, "take", append(words, words[0])); err == nil {
		t.Fatal("重复稳定词标识未拒绝")
	}
}
