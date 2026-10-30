package shotindex

import (
	"strings"
	"testing"
)

func TestShotIntervalsPreserveVideoCoverageAndSamples(t *testing.T) {
	index, err := Build(strings.Repeat("a", 64), "sha256:fixture", "ffmpeg fixture", 5_000_000, 2_000_000, 4_000_000, 50, []int64{3_000_000}, Config{})
	if err != nil || len(index.Shots) != 2 || index.Shots[0].StartUs != 2_000_000 || index.Shots[1].EndUs != 4_000_000 || index.Shots[1].SampleUs != 3_500_000 {
		t.Fatalf("补齐了非视频覆盖或取样错误: %+v %v", index, err)
	}
	copy := index
	copy.Shots = append([]Shot{}, index.Shots...)
	copy.Shots[0].SampleUs = copy.Shots[0].EndUs
	if copy.Validate() == nil {
		t.Fatal("段外取样被接受")
	}
}
func TestCutsAreCandidatesWithExplicitMinimumAndBounds(t *testing.T) {
	index, err := Build(strings.Repeat("b", 64), "sha256:fixture", "ffmpeg fixture", 2_000_000, 0, 2_000_000, 50, []int64{1_010_000, 1_000_000, 1_000_000}, Config{})
	if err != nil || len(index.Shots) != 2 || index.RawCutCount != 3 {
		t.Fatal("近邻/重复候选没有归并", err)
	}
	for _, cuts := range [][]int64{{-1}, {2_000_000}, make([]int64, MaxShots)} {
		if _, err := Build(strings.Repeat("a", 64), "fp", "decoder", 2_000_000, 0, 2_000_000, 50, cuts, Config{}); err == nil {
			t.Fatal("越界或超量候选被接纳")
		}
	}
	for _, c := range []Config{{Threshold: 51}, {Threshold: -1}, {MinDurationUs: 999}} {
		if _, err := Normalize(c); err == nil {
			t.Fatal("无效参数被接受")
		}
	}
}
