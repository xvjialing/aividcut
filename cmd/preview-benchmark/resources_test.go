package main

import (
	"aividcut/internal/processstats"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type fixedResourceCollector struct {
	value processstats.Snapshot
	err   error
}

func (f *fixedResourceCollector) Collect(context.Context) (processstats.Snapshot, error) {
	return f.value, f.err
}
func resourceCounter(value uint64) *uint64 { return &value }
func resourceFixture() (*resourceSampler, *fixedResourceCollector) {
	f := &fixedResourceCollector{value: processstats.Snapshot{
		RootPID:  os.Getpid(),
		RSSBytes: resourceCounter(30 << 20),
		Processes: []processstats.Process{
			{PID: os.Getpid(), Identity: "root:1", RSSBytes: resourceCounter(20 << 20)},
			{PID: 123, Identity: "123:2", Role: "media", RSSBytes: resourceCounter(10 << 20), CPUTimeNS: resourceCounter(1250000000)},
		},
	}}
	s := &resourceSampler{collector: f, start: time.Now(), value: resourceReport{SchemaVersion: 2, GoPID: os.Getpid(), Samples: []resourceSample{}}}
	return s, f
}

func TestNativeResourceRetainsStartupSampleAndExactUnits(t *testing.T) {
	s, _ := resourceFixture()
	s.collect(context.Background())
	if s.value.WorkerPeakRSS != nil {
		t.Fatal("尚未关联worker时猜测其资源")
	}
	s.setWorker(123)
	v := s.snapshot()
	if v.WorkerIdentity != "123:2" || v.WorkerPeakRSS == nil || *v.WorkerPeakRSS != 10<<20 || v.TreePeakRSS == nil || *v.TreePeakRSS != 30<<20 || v.TreePeakCPU != nil {
		t.Fatal("启动记录、字节或首个CPU区间错误", v)
	}
	encoded, err := json.Marshal(v)
	if err != nil || !strings.Contains(string(encoded), `"cpuTimeNs":1250000000`) || strings.Contains(string(encoded), "cpuTimeMs") || !strings.Contains(string(encoded), `"schemaVersion":2`) {
		t.Fatal("原生累计纳秒单位未明确", string(encoded), err)
	}
}

func TestPartialAndReusedWorkerDoNotInflateCompletePeaks(t *testing.T) {
	s, f := resourceFixture()
	s.setWorker(123)
	s.collect(context.Background())
	f.value.Partial = true
	f.value.RSSBytes = resourceCounter(900 << 20)
	f.value.Processes[1].Identity = "123:999"
	f.value.Processes[1].RSSBytes = resourceCounter(800 << 20)
	cpu := 999.0
	f.value.CPUPercent = &cpu
	s.collect(context.Background())
	if *s.value.WorkerPeakRSS != 10<<20 || *s.value.TreePeakRSS != 30<<20 || s.value.TreePeakCPU != nil || s.value.PartialSamples != 1 {
		t.Fatal("部分观测或复用PID被算作原worker/完整峰值", s.value)
	}
}

func TestMissingResourceAndFailureStayUnknownWithReason(t *testing.T) {
	s, f := resourceFixture()
	f.value.RSSBytes = nil
	for i := range f.value.Processes {
		f.value.Processes[i].RSSBytes = nil
	}
	s.setWorker(123)
	s.collect(context.Background())
	f.err = errors.New("权限变化")
	s.collect(context.Background())
	v := s.snapshot()
	if v.GoPeakRSS != nil || v.WorkerPeakRSS != nil || v.TreePeakRSS != nil || v.FailedSamples != 1 || !strings.Contains(v.Reason, "权限变化") || len(v.Samples) != 1 {
		t.Fatal("未知值/失败冒充零或丢掉已有观测", v)
	}
}

func TestResourceRetentionBoundStillUpdatesObservedPeaks(t *testing.T) {
	for _, rows := range []bool{false, true} {
		s, f := resourceFixture()
		s.setWorker(123)
		s.collect(context.Background())
		if rows {
			s.processRows = maxResourceProcessRows - 1
		} else {
			s.value.Samples = make([]resourceSample, maxResourceSamples)
		}
		before := len(s.value.Samples)
		f.value.Processes[1].RSSBytes = resourceCounter(40 << 20)
		s.collect(context.Background())
		if len(s.value.Samples) != before || s.value.SamplesDropped != 1 || *s.value.WorkerPeakRSS != 40<<20 {
			t.Fatal("采样容量没有约束或截断后停止更新峰值", rows, s.value)
		}
	}
}

type blockingResourceCollector struct{ entered chan struct{} }

func (f blockingResourceCollector) Collect(ctx context.Context) (processstats.Snapshot, error) {
	close(f.entered)
	<-ctx.Done()
	return processstats.Snapshot{}, ctx.Err()
}

func TestResourceCloseCancelsInFlightReadAndCanRepeat(t *testing.T) {
	f := blockingResourceCollector{entered: make(chan struct{})}
	s := startResourceSampler(context.Background(), 123, time.Hour, f)
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("采样未启动")
	}
	closed := make(chan struct{})
	go func() { s.close(); s.close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("取消仍等待采样周期")
	}
	if s.snapshot().FailedSamples != 0 {
		t.Fatal("正常关闭被记为采集失败")
	}
}
