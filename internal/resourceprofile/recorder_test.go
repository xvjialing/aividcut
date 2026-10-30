package resourceprofile

import (
	"aividcut/internal/processstats"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeCollector struct {
	mu    sync.Mutex
	calls int
	fail  bool
}

func (f *fakeCollector) Collect(context.Context) (processstats.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return processstats.Snapshot{}, errors.New("读取失败")
	}
	memory := uint64(f.calls * 10)
	cpu := float64(f.calls)
	return processstats.Snapshot{At: time.Now(), RootPID: 1, Processes: []processstats.Process{{PID: 1, Name: "app", Role: "application"}}, RSSBytes: &memory, CPUPercent: &cpu}, nil
}
func await(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second * 2)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("未观察到诊断状态")
}
func newFake() (*Recorder, *fakeCollector) {
	f := &fakeCollector{}
	r := newRecorder(f, func() ProjectContext {
		return ProjectContext{ProjectID: "fixture", Revision: 3, Width: 640, Height: 360}
	}, time.Millisecond)
	return r, f
}
func TestCaptureStartStopExportAndNoAutomaticWork(t *testing.T) {
	r, f := newFake()
	defer r.Close()
	if f.calls != 0 {
		t.Fatal("未开启就自动采集")
	}
	if _, err := r.Start("untrusted-free-text"); err == nil {
		t.Fatal("阶段没有白名单")
	}
	start, err := r.Start("preview")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Start("export"); err == nil {
		t.Fatal("重复记录没有拒绝")
	}
	await(t, func() bool {
		state, _ := r.State(context.Background(), false)
		return len(state.Reports) > 0 && state.Reports[0].Samples >= 2
	})
	done, err := r.Stop(start.ID)
	if err != nil || done.Status != "stopped" || done.Samples < 2 || done.FinishedAt == nil {
		t.Fatal(done, err)
	}
	report, err := r.Report(start.ID)
	if err != nil || len(report.Samples) != done.Samples || report.Summary.SampledPeakRSS == nil || report.Samples[0].Project.ProjectID != "fixture" {
		t.Fatal(report, err)
	}
	before := len(report.Samples)
	time.Sleep(5 * time.Millisecond)
	after, _ := r.Report(start.ID)
	if len(after.Samples) != before {
		t.Fatal("停止后继续采样")
	}
}
func TestCaptureHasRealSampleLimitAndRetainsFailedData(t *testing.T) {
	r, f := newFake()
	defer r.Close()
	r.interval = 100 * time.Microsecond
	start, err := r.Start("mixed")
	if err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { report, _ := r.Report(start.ID); return report.Summary.Status == "complete" })
	report, _ := r.Report(start.ID)
	if len(report.Samples) != MaxSamples || report.Summary.Samples != MaxSamples {
		t.Fatal("记录上界错误", len(report.Samples))
	}
	next, err := r.Start("export")
	if err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { value, _ := r.Report(next.ID); return len(value.Samples) > 0 })
	f.mu.Lock()
	f.fail = true
	f.mu.Unlock()
	await(t, func() bool { value, _ := r.Report(next.ID); return value.Summary.Status == "failed" })
	value, _ := r.Report(next.ID)
	if len(value.Samples) == 0 || value.Summary.Error == "" {
		t.Fatal("失败丢失已采数据")
	}
}
func TestRecentRecordsAndCloseAreBounded(t *testing.T) {
	r, _ := newFake()
	var first string
	for i := 0; i < 4; i++ {
		summary, err := r.Start("idle")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = summary.ID
		}
		if _, err = r.Stop(summary.ID); err != nil {
			t.Fatal(err)
		}
	}
	state, err := r.State(context.Background(), false)
	if err != nil || len(state.Reports) != MaxReports {
		t.Fatal(state, err)
	}
	if _, err = r.Report(first); err == nil {
		t.Fatal("旧诊断缓存未退役")
	}
	r.Close()
	if _, err = r.Start("idle"); err == nil {
		t.Fatal("关闭后仍可启动")
	}
	if _, err = r.State(context.Background(), true); err == nil {
		t.Fatal("关闭后仍采集")
	}
}
