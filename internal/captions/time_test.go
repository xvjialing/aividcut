package captions

import (
	"aividcut/internal/editor"
	"math"
	"testing"
)

func TestFrameRoundingUsesSharedNearestBoundary(t *testing.T) {
	settings := editor.NewProject("字幕帧率").Settings
	settings.FPSNum, settings.FPSDen = 30000, 1001
	start, duration, err := CueToFrames(Cue{1000, 2000, "中文"}, settings)
	if err != nil || start != 30 || duration != 30 {
		t.Fatalf("毫秒映射错误：%d/%d, %v", start, duration, err)
	}
	exported, err := FramesToCue(start, duration, "中文", settings)
	if err != nil || exported.StartMS != 1001 || exported.EndMS != 2002 {
		t.Fatalf("帧映射到毫秒错误：%+v, %v", exported, err)
	}
	_, leftDuration, _ := CueToFrames(Cue{0, 1001, "一"}, settings)
	rightStart, _, _ := CueToFrames(Cue{1001, 2002, "二"}, settings)
	if leftDuration != rightStart {
		t.Fatal("共享边界取整造成重叠或空档")
	}
	settings.FPSNum, settings.FPSDen = 25, 1
	half, length, err := CueToFrames(Cue{20, 60, "半帧向后"}, settings)
	if err != nil || half != 1 || length != 1 {
		t.Fatalf("半帧取整规则错误：%d/%d %v", half, length, err)
	}
}

func TestFrameSRTRoundTripsDoNotAccumulateDrift(t *testing.T) {
	for _, rate := range [][2]int{{24, 1}, {25, 1}, {30, 1}, {30000, 1001}, {60000, 1001}, {240, 1}} {
		settings := editor.NewProject("往返").Settings
		settings.FPSNum, settings.FPSDen = rate[0], rate[1]
		for frame := int64(0); frame < 10000; frame += 37 {
			duration := frame%17 + 1
			cue, err := FramesToCue(frame, duration, "中文\n多行", settings)
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				start, length, err := CueToFrames(cue, settings)
				if err != nil || start != frame || length != duration {
					t.Fatalf("帧率 %v 往返漂移：原 %d/%d，得 %d/%d，%v", rate, frame, duration, start, length, err)
				}
				cue, _ = FramesToCue(start, length, cue.Text, settings)
			}
		}
	}
}

func TestInvalidFrameRangesAndCollapsedCueAreRejected(t *testing.T) {
	settings := editor.NewProject("测试").Settings
	if _, _, err := CueToFrames(Cue{0, 1, "不足一帧"}, settings); err == nil {
		t.Fatal("不足一帧的字幕被静默扩长")
	}
	for _, interval := range [][2]int64{{-1, 1}, {1, 0}, {math.MaxInt64, 1}} {
		if _, err := FramesToCue(interval[0], interval[1], "错误", settings); err == nil {
			t.Fatal("非法帧范围被接受")
		}
	}
	if _, err := FramesToCue(math.MaxInt64-1, 1, "时间溢出", settings); err == nil {
		t.Fatal("帧到毫秒溢出未被拒绝")
	}
	settings.FPSDen = 0
	if _, _, err := CueToFrames(Cue{0, 1000, "非法帧率"}, settings); err == nil {
		t.Fatal("非法帧率被接受")
	}
}
