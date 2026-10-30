package main

import (
	"aividcut/internal/processstats"
	"context"
	"os"
	"sync"
	"time"
)

const maxResourceSamples = 10000
const maxResourceProcessRows = 20000

type resourceSample struct {
	AtMs float64 `json:"atMs"`
	processstats.Snapshot
}
type resourceReport struct {
	SchemaVersion  int              `json:"schemaVersion"`
	Method         string           `json:"method"`
	Reason         string           `json:"reason,omitempty"`
	GoPID          int              `json:"goPid"`
	WorkerPID      int              `json:"workerPid"`
	WorkerIdentity string           `json:"workerIdentity,omitempty"`
	GoPeakRSS      *uint64          `json:"goSampledPeakRssBytes"`
	WorkerPeakRSS  *uint64          `json:"workerSampledPeakRssBytes"`
	TreePeakRSS    *uint64          `json:"confirmedTreeSampledPeakRssBytes"`
	TreePeakCPU    *float64         `json:"confirmedTreeSampledPeakCpuPercent"`
	Samples        []resourceSample `json:"samples"`
	SamplesDropped uint64           `json:"samplesDropped"`
	FailedSamples  uint64           `json:"failedSamples"`
	PartialSamples uint64           `json:"partialSamples"`
}
type resourceCollector interface {
	Collect(context.Context) (processstats.Snapshot, error)
}
type resourceSampler struct {
	mu          sync.Mutex
	value       resourceReport
	start       time.Time
	processRows int
	collector   resourceCollector
	cancel      context.CancelFunc
	done        chan struct{}
	once        sync.Once
}

func newResourceSampler(parent context.Context, worker int, interval time.Duration) *resourceSampler {
	return startResourceSampler(parent, worker, interval, processstats.New())
}

func startResourceSampler(parent context.Context, worker int, interval time.Duration, collector resourceCollector) *resourceSampler {
	ctx, cancel := context.WithCancel(parent)
	s := &resourceSampler{
		value: resourceReport{
			SchemaVersion: 2, GoPID: os.Getpid(), WorkerPID: worker, Samples: []resourceSample{},
			Method: "复用客户端processstats原生读取，仅含身份已确认的本进程及后代；RSS为采样观测值，GPU/WebView/设备体验不在此诊断内。",
		},
		start: time.Now(), cancel: cancel, done: make(chan struct{}), collector: collector,
	}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			s.collect(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return s
}

func (s *resourceSampler) collect(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	observation, err := s.collector.Collect(ctx)
	if parent.Err() != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.value.FailedSamples++
		s.value.Reason = "原生进程资源采样未完成：" + err.Error()
		return
	}
	if observation.Partial {
		s.value.PartialSamples++
	}
	for _, row := range observation.Processes {
		if row.PID == s.value.GoPID {
			updateRSSPeak(&s.value.GoPeakRSS, row.RSSBytes)
		}
		s.observeWorker(row)
	}
	// 只有一次完整观测才可更新树合计；knownRssBytes不能冒充缺失进程的总量。
	if !observation.Partial {
		updateRSSPeak(&s.value.TreePeakRSS, observation.RSSBytes)
		if observation.CPUPercent != nil && (s.value.TreePeakCPU == nil || *observation.CPUPercent > *s.value.TreePeakCPU) {
			value := *observation.CPUPercent
			s.value.TreePeakCPU = &value
		}
	}
	// 后代可多于原先两个PID，同时约束原始行数，避免长录制放大报告内存。
	if len(s.value.Samples) >= maxResourceSamples || s.processRows+len(observation.Processes) > maxResourceProcessRows {
		s.value.SamplesDropped++
		return
	}
	s.processRows += len(observation.Processes)
	s.value.Samples = append(s.value.Samples, resourceSample{AtMs: milliseconds(time.Since(s.start)), Snapshot: observation})
}

func updateRSSPeak(peak **uint64, value *uint64) {
	if value != nil && (*peak == nil || *value > **peak) {
		copy := *value
		*peak = &copy
	}
}
func (s *resourceSampler) observeWorker(row processstats.Process) {
	if s.value.WorkerPID <= 0 || row.PID != s.value.WorkerPID || row.Identity == "" {
		return
	}
	if s.value.WorkerIdentity == "" {
		s.value.WorkerIdentity = row.Identity
	}
	if s.value.WorkerIdentity == row.Identity {
		updateRSSPeak(&s.value.WorkerPeakRSS, row.RSSBytes)
	}
}

// Start完成后才有worker PID，补看已采到的启动记录，不依赖程序名称猜归属。
func (s *resourceSampler) setWorker(pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value.WorkerPID = pid
	for _, sample := range s.value.Samples {
		for _, row := range sample.Processes {
			s.observeWorker(row)
		}
	}
}
func (s *resourceSampler) close() { s.once.Do(func() { s.cancel(); <-s.done }) }
func (s *resourceSampler) snapshot() resourceReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.value
	value.Samples = append([]resourceSample{}, value.Samples...)
	if len(value.Samples) == 0 && value.Reason == "" {
		value.Reason = "本次诊断窗口未采到进程资源，不把缺失值记为零内存。"
	} else if value.WorkerPeakRSS == nil && value.Reason == "" {
		value.Reason = "本次窗口未确认worker资源，可能早于采样退出；缺失值不补零。"
	}
	return value
}
