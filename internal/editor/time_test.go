package editor

import (
	"math"
	"testing"
)

func TestRationalFrameMappingAndSplit(t *testing.T) {
	settings := NewProject("测试").Settings
	settings.FPSNum, settings.FPSDen = 30000, 1001
	us, err := FramesToMicroseconds(30000, settings)
	if err != nil || us != 1_001_000_000 {
		t.Fatalf("29.97fps 映射错误：%d, %v", us, err)
	}
	frames, err := MicrosecondsToFrames(us, settings)
	if err != nil || frames != 30000 {
		t.Fatalf("反向映射错误：%d, %v", frames, err)
	}
	project := NewProject("变速源区间")
	project.Settings = settings
	project.Assets = []Asset{{ID: "source", Name: "source.mp4", Kind: "video", Path: "/tmp/source.mp4", DurationUs: 10_000_000}}
	clip := DefaultClip("take", "track-video-1", "video")
	clip.AssetID, clip.DurationFrames, clip.SourceInUs, clip.SpeedNum, clip.SpeedDen = "source", 120, 1_000_000, 2, 1
	project.Clips = []Clip{clip}
	engine, err := NewEngine(project)
	if err != nil {
		t.Fatal(err)
	}
	split := apply(t, engine, Command{Type: "split_clip", ClipID: "take", AtFrame: 15})
	if split.Clips[1].SourceInUs != 2_001_000 || split.Clips[1].DurationFrames != 105 {
		t.Fatalf("变速分割映射错误：%+v", split.Clips)
	}
}

func TestSourceBoundsKeepFractionalMicroseconds(t *testing.T) {
	settings := NewProject("测试").Settings
	settings.FPSNum, settings.FPSDen = 30000, 1001
	clip := DefaultClip("c", "track-video-1", "video")
	clip.DurationFrames = 1
	asset := Asset{DurationUs: 33366}
	if SourceRangeFits(clip, asset, settings) {
		t.Fatal("截断换算放宽了不足一微秒的源区间")
	}
	asset.DurationUs = 33367
	if !SourceRangeFits(clip, asset, settings) {
		t.Fatal("合法源区间被拒绝")
	}
}

func TestOverflowAndInvalidTimeAreRejected(t *testing.T) {
	settings := NewProject("测试").Settings
	if _, err := FramesToMicroseconds(math.MaxInt64, settings); err == nil {
		t.Fatal("时间换算溢出未被拒绝")
	}
	if _, err := FramesToMicroseconds(-1, settings); err == nil {
		t.Fatal("负数时间未被拒绝")
	}
	settings.FPSDen = 0
	if _, err := FramesToMicroseconds(1, settings); err == nil {
		t.Fatal("非法帧率未被拒绝")
	}
}
