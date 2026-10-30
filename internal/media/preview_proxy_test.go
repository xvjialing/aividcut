package media

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"testing"
)

func TestProxyTimelineRequiresEveryActualFrameTime(t *testing.T) {
	state := func() *proxyTimeline { return &proxyTimeline{digest: sha256.New(), timeBase: big.NewRat(1, 30000)} }
	for _, line := range []string{"best_effort_timestamp=N/A|duration=1001", "best_effort_timestamp=0|duration=0", "duration=1001", "best_effort_timestamp=0"} {
		if err := state().line(line); err == nil {
			t.Fatalf("错误时间被接受: %s", line)
		}
	}
	s := state()
	if err := s.line("best_effort_timestamp=0|duration=1001|side_data=ignored"); err != nil {
		t.Fatal(err)
	}
	if err := s.line("best_effort_timestamp=0|duration=1001"); err == nil {
		t.Fatal("重复PTS通过")
	}
}

func TestProxyTimelineUsesPresentationHoldsAndExactTail(t *testing.T) {
	digest := func(durations []int64) string {
		s := &proxyTimeline{digest: sha256.New(), timeBase: big.NewRat(1, 30)}
		for i, d := range durations {
			if err := s.line(fmt.Sprintf("best_effort_timestamp=%d|duration=%d", i*3, d)); err != nil {
				t.Fatal(err)
			}
		}
		s.finish()
		return fmt.Sprintf("%x", s.digest.Sum(nil))
	}
	if digest([]int64{1, 2, 1}) != digest([]int64{3, 3, 1}) {
		t.Fatal("把B帧包持续时间当作展示保持区间")
	}
	if digest([]int64{3, 3, 1}) == digest([]int64{3, 3, 2}) {
		t.Fatal("忽略尾帧真实持续时间")
	}
}
