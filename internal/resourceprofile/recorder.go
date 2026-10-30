// Package resourceprofile 保存有界的本机诊断窗口，不包含工程正文或命令行。
package resourceprofile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"aividcut/internal/processstats"
)

const MaxSamples = 300
const MaxReports = 3

type ProjectContext struct {
	ProjectID   string `json:"projectId"`
	Revision    int64  `json:"revision"`
	SourceEpoch uint64 `json:"sourceEpoch"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPSNum      int    `json:"fpsNum"`
	FPSDen      int    `json:"fpsDen"`
	Clips       int    `json:"clips"`
	Assets      int    `json:"assets"`
	Tracks      int    `json:"tracks"`
}
type Sample struct {
	Snapshot processstats.Snapshot `json:"snapshot"`
	Project  ProjectContext        `json:"project"`
}
type Summary struct {
	ID             string     `json:"id"`
	Phase          string     `json:"phase"`
	Status         string     `json:"status"`
	StartedAt      time.Time  `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
	Samples        int        `json:"samples"`
	SampledPeakRSS *uint64    `json:"sampledPeakRss"`
	SampledPeakCPU *float64   `json:"sampledPeakCpu"`
	Error          string     `json:"error,omitempty"`
}
type Report struct {
	IntervalMs    float64  `json:"intervalMs"`
	FormatVersion int      `json:"formatVersion"`
	Summary       Summary  `json:"summary"`
	Build         Build    `json:"build"`
	Samples       []Sample `json:"samples"`
	Limitations   []string `json:"limitations"`
}
type Build struct {
	ExecutableFileSHA256 string `json:"executableFileSha256,omitempty"`
	GoVersion            string `json:"goVersion"`
	OS                   string `json:"os"`
	Architecture         string `json:"architecture"`
	LogicalCPUs          int    `json:"logicalCpus"`
	Wails                string `json:"wails"`
}
type State struct {
	Latest  *Sample   `json:"latest"`
	Active  *Summary  `json:"active"`
	Reports []Summary `json:"reports"`
	Error   string    `json:"error,omitempty"`
}
type record struct {
	report Report
	cancel context.CancelFunc
	done   chan struct{}
}
type Recorder struct {
	mu        sync.Mutex
	readMu    sync.Mutex
	collector interface {
		Collect(context.Context) (processstats.Snapshot, error)
	}
	project   func() ProjectContext
	interval  time.Duration
	records   map[string]*record
	active    string
	latest    *Sample
	lastError string
	closed    bool
}

func New(project func() ProjectContext) *Recorder {
	return newRecorder(processstats.New(), project, time.Second)
}

// NewWithInterval 用于明确的开发验收；客户端保持1秒，不能由界面任意提高频率。
func NewWithInterval(project func() ProjectContext, interval time.Duration) (*Recorder, error) {
	if interval < 100*time.Millisecond || interval > time.Second {
		return nil, errors.New("诊断采样间隔需100ms至1秒")
	}
	return newRecorder(processstats.New(), project, interval), nil
}
func newRecorder(c interface {
	Collect(context.Context) (processstats.Snapshot, error)
}, project func() ProjectContext, interval time.Duration) *Recorder {
	return &Recorder{collector: c, project: project, interval: interval, records: map[string]*record{}}
}
func validPhase(phase string) bool {
	return phase == "idle" || phase == "preview" || phase == "editing" || phase == "export" || phase == "mixed"
}
func build() Build {
	out := Build{GoVersion: runtime.Version(), OS: runtime.GOOS, Architecture: runtime.GOARCH, LogicalCPUs: runtime.NumCPU()}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/wailsapp/wails/v3" {
				out.Wails = dep.Version
			}
		}
	}
	// 仅记录摘要，不导出可执行文件路径；这是当前磁盘文件的标识。
	if path, err := os.Executable(); err == nil {
		if file, err := os.Open(path); err == nil {
			info, err := file.Stat()
			if err == nil && info.Mode().IsRegular() && info.Size() <= 512<<20 {
				sum := sha256.New()
				if _, err = io.Copy(sum, io.LimitReader(file, (512<<20)+1)); err == nil {
					out.ExecutableFileSHA256 = hex.EncodeToString(sum.Sum(nil))
				}
			}
			file.Close()
		}
	}
	return out
}
func (r *Recorder) Start(phase string) (Summary, error) {
	if !validPhase(phase) {
		return Summary{}, errors.New("请选择有效的诊断阶段")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Summary{}, errors.New("资源诊断已关闭")
	}
	if r.active != "" {
		return Summary{}, errors.New("已有资源记录正在进行，请先停止或等待完成")
	}
	if len(r.records) >= MaxReports {
		var oldest string
		var at time.Time
		for id, v := range r.records {
			if at.IsZero() || v.report.Summary.StartedAt.Before(at) {
				oldest, at = id, v.report.Summary.StartedAt
			}
		}
		delete(r.records, oldest)
	}
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return Summary{}, err
	}
	id := "profile-" + hex.EncodeToString(bytes[:])
	ctx, cancel := context.WithCancel(context.Background())
	summary := Summary{ID: id, Phase: phase, Status: "running", StartedAt: time.Now().UTC()}
	rec := &record{report: Report{FormatVersion: 1, IntervalMs: float64(r.interval) / float64(time.Millisecond), Summary: summary, Build: build(), Samples: []Sample{}, Limitations: []string{"RSS含共享页面，合计不等于独占物理内存；仅采样峰值可能遗漏瞬时峰值。", "CPU的100%表示一个逻辑核心，首样本/身份变化不补成零。", "记录覆盖应用及已确认后代，不自动归属独立WebKit XPC、GPU/显存或独立启动的模型/CLI。", "进程阶段由人标记，工程形状只提供当时上下文，不表示所有资源专属于该工程。", "最多300样本/5分钟、128进程；内存仅保留最近3份记录，退出前请导出需要的报告。"}}, cancel: cancel, done: make(chan struct{})}
	r.records[id] = rec
	r.active = id
	go r.run(ctx, id, rec)
	return summary, nil
}
func (r *Recorder) gather(ctx context.Context) (Sample, error) {
	r.readMu.Lock()
	defer r.readMu.Unlock()
	shape := r.project()
	snapshot, err := r.collector.Collect(ctx)
	if err != nil {
		return Sample{}, err
	}
	return Sample{Snapshot: snapshot, Project: shape}, nil
}
func (r *Recorder) run(ctx context.Context, id string, rec *record) {
	defer close(rec.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	status, lastError := "stopped", ""
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		at := time.Now().UTC()
		rec.report.Summary.Status = status
		rec.report.Summary.Error = lastError
		rec.report.Summary.FinishedAt = &at
		if r.active == id {
			r.active = ""
		}
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		query, cancel := context.WithTimeout(ctx, 2*time.Second)
		sample, err := r.gather(query)
		cancel()
		if ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		if err != nil {
			status, lastError = "failed", err.Error()
			r.lastError = lastError
			r.mu.Unlock()
			return
		}
		r.latest = &sample
		r.lastError = ""
		rec.report.Samples = append(rec.report.Samples, sample)
		rec.report.Summary.Samples = len(rec.report.Samples)
		if value := sample.Snapshot.RSSBytes; value != nil && (rec.report.Summary.SampledPeakRSS == nil || *value > *rec.report.Summary.SampledPeakRSS) {
			copy := *value
			rec.report.Summary.SampledPeakRSS = &copy
		}
		if value := sample.Snapshot.CPUPercent; value != nil && (rec.report.Summary.SampledPeakCPU == nil || *value > *rec.report.Summary.SampledPeakCPU) {
			copy := *value
			rec.report.Summary.SampledPeakCPU = &copy
		}
		full := len(rec.report.Samples) >= MaxSamples
		r.mu.Unlock()
		if full {
			status = "complete"
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			status = "complete"
			return
		case <-ticker.C:
		}
	}
}
func (r *Recorder) State(ctx context.Context, refresh bool) (State, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return State{}, errors.New("资源诊断已关闭")
	}
	active := r.active
	r.mu.Unlock()
	if refresh && active == "" {
		query, cancel := context.WithTimeout(ctx, 2*time.Second)
		sample, err := r.gather(query)
		cancel()
		r.mu.Lock()
		if err != nil {
			r.lastError = err.Error()
		} else {
			r.latest = &sample
			r.lastError = ""
		}
		r.mu.Unlock()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := State{Latest: r.latest, Error: r.lastError, Reports: []Summary{}}
	for id, rec := range r.records {
		summary := rec.report.Summary
		out.Reports = append(out.Reports, summary)
		if id == r.active {
			copy := summary
			out.Active = &copy
		}
	}
	sort.Slice(out.Reports, func(i, j int) bool { return out.Reports[i].StartedAt.After(out.Reports[j].StartedAt) })
	return out, nil
}
func (r *Recorder) Stop(id string) (Summary, error) {
	r.mu.Lock()
	rec := r.records[id]
	if rec == nil {
		r.mu.Unlock()
		return Summary{}, errors.New("所选诊断记录已不在内存，请刷新列表")
	}
	rec.cancel()
	done := rec.done
	r.mu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		return Summary{}, errors.New("诊断采集仍在停止中，请稍后查看")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return rec.report.Summary, nil
}
func (r *Recorder) Report(id string) (Report, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.records[id]
	if rec == nil {
		return Report{}, errors.New("所选诊断记录已不在内存，请刷新列表")
	}
	out := rec.report
	out.Samples = append([]Sample{}, out.Samples...)
	out.Limitations = append([]string{}, out.Limitations...)
	return out, nil
}
func (r *Recorder) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	var done chan struct{}
	if rec := r.records[r.active]; rec != nil {
		rec.cancel()
		done = rec.done
	}
	r.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}
