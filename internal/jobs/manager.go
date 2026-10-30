package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Job struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Status     string   `json:"status"`
	Progress   *float64 `json:"progress,omitempty"`
	Message    string   `json:"message"`
	OutputPath string   `json:"outputPath,omitempty"`
	CreatedAt  string   `json:"createdAt"`
}

type Work func(context.Context, func(float64, string)) (string, error)

type entry struct {
	job    Job
	cancel context.CancelFunc
}

type Manager struct {
	mu       sync.Mutex
	entries  map[string]*entry
	sema     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closed   bool
	done     chan struct{}
	onFinish func(Job)
}

// 后台任务有界排队；正式百分比只来自工作进程的测量回调。
func New() *Manager {
	return NewWithObserver(nil)
}

// NewWithObserver 在终态发布后通知应用，避免业务记录依赖短期队列缓存。
// 回调在队列锁外执行，完成后该任务才退出工作组。
func NewWithObserver(observer func(Job)) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{entries: map[string]*entry{}, sema: make(chan struct{}, 1), ctx: ctx, cancel: cancel, done: make(chan struct{}), onFinish: observer}
}

func (m *Manager) Start(kind, message string, work Work) (Job, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Job{}, errors.New("应用正在关闭，不能启动新任务")
	}
	pending := 0
	for _, e := range m.entries {
		if e.job.Status == "queued" || e.job.Status == "running" {
			pending++
		}
	}
	if pending >= 8 {
		m.mu.Unlock()
		return Job{}, errors.New("后台任务较多，请等待或取消现有任务")
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		m.mu.Unlock()
		return Job{}, err
	}
	id := hex.EncodeToString(random[:])
	ctx, cancel := context.WithCancel(m.ctx)
	job := Job{ID: id, Type: kind, Status: "queued", Message: message, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	m.entries[id] = &entry{job: job, cancel: cancel}
	m.wg.Add(1)
	m.pruneLocked()
	m.mu.Unlock()
	go m.run(ctx, id, work)
	return job, nil
}

func (m *Manager) run(ctx context.Context, id string, work Work) {
	defer m.wg.Done()
	select {
	case m.sema <- struct{}{}:
		defer func() { <-m.sema }()
	case <-ctx.Done():
		m.finish(id, "cancelled", "已取消", "")
		return
	}
	if ctx.Err() != nil {
		m.finish(id, "cancelled", "已取消", "")
		return
	}
	m.mu.Lock()
	m.entries[id].job.Status = "running"
	m.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			m.finish(id, "failed", fmt.Sprintf("后台任务异常：%v", r), "")
		}
	}()
	output, err := work(ctx, func(progress float64, message string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		job := &m.entries[id].job
		if progress >= 0 && progress <= 1 {
			p := progress
			job.Progress = &p
		}
		if message != "" {
			job.Message = message
		}
	})
	if err != nil && (ctx.Err() != nil || errors.Is(err, context.Canceled)) {
		m.finish(id, "cancelled", "已取消", "")
	} else if err != nil {
		m.finish(id, "failed", err.Error(), "")
	} else {
		m.finish(id, "succeeded", "已完成", output)
	}
}

func (m *Manager) finish(id, status, message, output string) {
	m.mu.Lock()
	var result *Job
	if e := m.entries[id]; e != nil {
		e.job.Status, e.job.Message, e.job.OutputPath = status, message, output
		if status == "succeeded" {
			p := 1.0
			e.job.Progress = &p
		}
		e.cancel()
		value := e.job
		if value.Progress != nil {
			p := *value.Progress
			value.Progress = &p
		}
		result = &value
	}
	observer := m.onFinish
	m.mu.Unlock()
	if result != nil && observer != nil {
		// 业务观察者失败不能再次进入同一终态回调或使工作组无法退出。
		func() { defer func() { _ = recover() }(); observer(*result) }()
	}
}

func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Job, 0, len(m.entries))
	for _, e := range m.entries {
		j := e.job
		if j.Progress != nil {
			p := *j.Progress
			j.Progress = &p
		}
		result = append(result, j)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt > result[j].CreatedAt })
	return result
}

func (m *Manager) Cancel(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok {
		return Job{}, errors.New("找不到该任务")
	}
	if e.job.Status == "queued" || e.job.Status == "running" {
		e.job.Message = "正在取消…"
		e.cancel()
	}
	return e.job, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	go func() { m.wg.Wait(); close(m.done) }()
	select {
	case <-m.done:
	case <-time.After(3 * time.Second):
	}
}

func (m *Manager) Done() <-chan struct{} { return m.done }

func (m *Manager) pruneLocked() {
	if len(m.entries) <= 100 {
		return
	}
	var done []Job
	for _, e := range m.entries {
		if e.job.Status != "running" && e.job.Status != "queued" {
			done = append(done, e.job)
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].CreatedAt < done[j].CreatedAt })
	for _, j := range done {
		if len(m.entries) <= 100 {
			break
		}
		delete(m.entries, j.ID)
	}
}
