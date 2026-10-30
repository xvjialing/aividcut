package captions

import (
	"aividcut/internal/editor"
	"reflect"
	"testing"
)

func sourceProject(t *testing.T) editor.Project {
	t.Helper()
	project := editor.NewProject("源转写映射")
	project.Settings.FPSNum, project.Settings.FPSDen = 30000, 1001
	project.Assets = []editor.Asset{{ID: "source", Name: "原片.mp4", Kind: "video", Path: "/tmp/原片.mp4", DurationUs: 30_000_000, HasAudio: true}}
	clip := editor.DefaultClip("take", "track-video-1", "video")
	clip.AssetID, clip.StartFrame, clip.DurationFrames, clip.SourceInUs = "source", 30, 60, 1_000_000
	clip.SpeedNum, clip.SpeedDen = 2, 1
	project.Clips = []editor.Clip{clip}
	if err := editor.ValidateProject(project); err != nil {
		t.Fatal(err)
	}
	return project
}

func TestSourceMappingTracksTrimMoveSpeedAndRepeatedOccurrences(t *testing.T) {
	project := sourceProject(t)
	repeated := project.Clips[0]
	repeated.ID, repeated.StartFrame, repeated.DurationFrames, repeated.SourceInUs = "repeated", 150, 30, 2_000_000
	repeated.SpeedNum = 1
	project.Clips = append(project.Clips, repeated)
	words := []Word{{0, 900_000, "之前"}, {900_000, 1_200_000, "左边被切"}, {2_000_000, 3_000_000, "完整词"}, {4_900_000, 5_100_000, "右边被切"}, {5_004_000, 5_100_000, "恰好在区间外"}}
	beforeWords := append([]Word{}, words...)
	beforeProject := editor.CloneProject(project)
	got, err := MapSourceWords(project, "source", words)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("有效范围选择错误：%+v", got)
	}
	if got[0].StartUs != 1_001_000 || got[0].EndUs != 1_101_000 || !got[0].Partial || got[0].SourceStartUs != 900_000 {
		t.Fatalf("左边界截断或原始词证据错误：%+v", got[0])
	}
	if got[1].StartUs != 1_501_000 || got[1].EndUs != 2_001_000 || got[1].Partial || got[1].WordIndex != 2 {
		t.Fatalf("29.97fps 与二倍速映射错误：%+v", got[1])
	}
	if got[2].StartUs != 2_951_000 || got[2].EndUs != 3_003_000 || !got[2].Partial {
		t.Fatalf("右边界截断错误：%+v", got[2])
	}
	if got[3].ClipID != "repeated" || got[3].StartUs != 5_005_000 || got[3].EndUs != 6_005_000 || got[3].Text != "完整词" {
		t.Fatalf("重复源片段映射丢失：%+v", got[3])
	}
	if !reflect.DeepEqual(words, beforeWords) || !reflect.DeepEqual(project, beforeProject) {
		t.Fatal("纯映射修改了原转写或工程")
	}
}

func TestFractionalSourceEndDoesNotAdmitFollowingWord(t *testing.T) {
	project := sourceProject(t)
	project.Clips[0].StartFrame, project.Clips[0].DurationFrames, project.Clips[0].SourceInUs = 0, 1, 0
	project.Clips[0].SpeedNum = 1
	got, err := MapSourceWords(project, "source", []Word{{33366, 33367, "边界部分"}, {33367, 40000, "区间之外"}})
	if err != nil || len(got) != 1 || !got[0].Partial || got[0].StartUs != 33366 || got[0].EndUs != 33367 {
		t.Fatalf("分数微秒边界被提前截断或放宽：%+v, %v", got, err)
	}
}

func TestSlowMotionUnsortedWordsAndHalfOpenBoundaries(t *testing.T) {
	project := sourceProject(t)
	project.Clips[0].StartFrame = 0
	project.Clips[0].SpeedNum, project.Clips[0].SpeedDen = 1, 2
	words := []Word{{1_500_000, 2_500_000, "慢放"}, {999_000, 1_000_000, "结束即入点"}, {1_000_000, 1_100_000, "开始词"}}
	got, err := MapSourceWords(project, "source", words)
	if err != nil || len(got) != 2 {
		t.Fatalf("慢放映射失败：%+v %v", got, err)
	}
	if got[0].Text != "开始词" || got[0].WordIndex != 2 || got[0].StartUs != 0 || got[0].EndUs != 200_000 {
		t.Fatalf("乱序词或左边界处理错误：%+v", got)
	}
	if got[1].StartUs != 1_000_000 || got[1].EndUs != 2_002_000 || !got[1].Partial {
		t.Fatalf("慢放右边界没有正确裁切：%+v", got[1])
	}
}

func TestSourceMappingRejectsInvalidWordsAndDoesNotPretendBinding(t *testing.T) {
	project := sourceProject(t)
	for _, words := range [][]Word{{{0, 0, "零长度"}}, {{-1, 1, "负时间"}}, {{0, 30_000_001, "越界"}}, {{0, 1, ""}}} {
		if _, err := MapSourceWords(project, "source", words); err == nil {
			t.Fatal("非法源转写被接受")
		}
	}
	if _, err := MapSourceWords(project, "unknown", []Word{{0, 1, "未知素材"}}); err == nil {
		t.Fatal("未知素材被接受")
	}
	project.Clips = nil
	got, err := MapSourceWords(project, "source", []Word{{0, 1000, "未使用的原片"}})
	if err != nil || len(got) != 0 {
		t.Fatal("未使用原片凭空生成了工程字幕")
	}
}
