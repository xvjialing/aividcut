package generationjobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
)

type fixtureProvider struct {
	posts  atomic.Int32
	gets   atomic.Int32
	submit func(context.Context, generation.Request) (generation.Task, error)
	get    func(context.Context, generation.Task) (generation.Task, error)
}

func (p *fixtureProvider) Capabilities() []generation.Capability { return nil }
func (p *fixtureProvider) Validate(r generation.Request) error {
	if r.Prompt == "" {
		return errors.New("缺少提示词")
	}
	return nil
}
func (p *fixtureProvider) Submit(ctx context.Context, r generation.Request) (generation.Task, error) {
	p.posts.Add(1)
	return p.submit(ctx, r)
}
func (p *fixtureProvider) Get(ctx context.Context, task generation.Task) (generation.Task, error) {
	p.gets.Add(1)
	return p.get(ctx, task)
}
func (p *fixtureProvider) Cancel(context.Context, generation.Task) (generation.Task, error) {
	panic("停止本地等待不能调用远端取消")
}

func imageOutput() generation.Output {
	var data bytes.Buffer
	_ = png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	return generation.Output{Kind: "image", MIME: "image/png", Data: data.Bytes()}
}
func taskFor(r generation.Request, status, remote string) generation.Task {
	return generation.Task{ID: r.ID, ProviderID: r.ProviderID, ModelID: r.ModelID, RemoteID: remote, Status: status, Recipe: generation.Recipe{ID: r.ID, ProviderID: r.ProviderID, ModelID: r.ModelID, Mode: r.Mode, Prompt: r.Prompt, Count: r.Count, References: []generation.ReferenceRecipe{}, CreatedAt: time.Now().UTC()}}
}
func inputFor(id string) SubmitInput {
	return SubmitInput{ConnectionID: "fixture-connection", Consent: true, Request: generation.Request{ID: id, ProviderID: generation.OpenAI, ModelID: "fixture-model", Mode: generation.ImageGenerate, Prompt: "测试图片"}}
}
func newFixture(t *testing.T, dir string, p *fixtureProvider) (*Manager, Options) {
	t.Helper()
	options := Options{Directory: dir, PollInterval: time.Millisecond, TaskTimeout: time.Second, Resolve: func(context.Context, string) (Binding, error) {
		return Binding{ProviderID: generation.OpenAI, ModelID: "fixture-model", Secrets: generation.SecretFunc(func(context.Context, string) (string, error) { return "fixture-secret", nil }), Release: func() {}}, nil
	}, Factory: func(string, string, generation.SecretSource, generation.ReferenceSource) (generation.Provider, error) {
		return p, nil
	}}
	m, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, options
}
func awaitIdle(t *testing.T, m *Manager, id, status string) Job {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		m.mu.Lock()
		e := m.entries[id]
		job, active := cloneJob(e.record.Job), e.active
		m.mu.Unlock()
		if job.Status == status && !active {
			return job
		}
		time.Sleep(time.Millisecond)
	}
	job, _ := m.Get(id)
	t.Fatalf("未达到 %s: %+v", status, job)
	return Job{}
}

func TestSuccessPersistentIdempotencyAndPartialImport(t *testing.T) {
	p := &fixtureProvider{submit: func(_ context.Context, r generation.Request) (generation.Task, error) {
		task := taskFor(r, generation.Succeeded, "")
		task.Outputs = []generation.Output{imageOutput(), imageOutput()}
		return task, nil
	}}
	dir := t.TempDir()
	m, options := newFixture(t, dir, p)
	project := editor.NewProject("测试")
	input := inputFor("image-result")
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := awaitIdle(t, m, input.Request.ID, Succeeded)
	if len(job.Assets) != 2 || job.Imported {
		t.Fatalf("素材状态无效: %+v", job)
	}
	if _, err := os.Stat(job.Assets[0].Path); err != nil {
		t.Fatal(err)
	}
	project.Revision++
	input.Consent = false
	input.Request.Count = 1
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	if p.posts.Load() != 1 {
		t.Fatal("重复请求重新生成")
	}
	changed := input
	changed.Request.Prompt = "其他内容"
	if _, err := m.Submit(context.Background(), project, changed); err == nil {
		t.Fatal("接受相同ID不同内容")
	}
	job, err := m.MarkImported(job.ID, []string{job.Assets[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if job.Imported || len(job.ImportedAssetIDs) != 1 {
		t.Fatal("部分导入被标记为全部")
	}
	job, err = m.MarkImported(job.ID, []string{job.Assets[1].ID})
	if err != nil || !job.Imported {
		t.Fatal("全部导入状态未更新", err)
	}
	m.Close()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	if p.posts.Load() != 1 {
		t.Fatal("重开后重新生成")
	}
	data, err := os.ReadFile(filepath.Join(dir, "jobs", job.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("fixture-secret")) || bytes.Contains(data, []byte("iVBOR")) {
		t.Fatal("记录泄漏密钥或内联媒体")
	}
}

func TestConsentValidationAndJournalBeforeNetwork(t *testing.T) {
	p := &fixtureProvider{submit: func(_ context.Context, r generation.Request) (generation.Task, error) {
		return taskFor(r, generation.Succeeded, ""), nil
	}}
	m, _ := newFixture(t, t.TempDir(), p)
	project := editor.NewProject("测试")
	input := inputFor("write-failure")
	input.Consent = false
	if _, err := m.Submit(context.Background(), project, input); err == nil {
		t.Fatal("缺少明确生成动作")
	}
	input.Consent = true
	input.Request.Prompt = ""
	if _, err := m.Submit(context.Background(), project, input); err == nil {
		t.Fatal("未校验请求")
	}
	input.Request.Prompt = "测试"
	if err := os.Mkdir(filepath.Join(m.journal, input.Request.ID+".json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(context.Background(), project, input); err == nil {
		t.Fatal("记录不能写入却提交了生成")
	}
	if p.posts.Load() != 0 {
		t.Fatal("持久化前执行网络")
	}
}

func TestUnknownNeverResubmitsAndCorruptRecordReserved(t *testing.T) {
	p := &fixtureProvider{submit: func(_ context.Context, r generation.Request) (generation.Task, error) {
		task := taskFor(r, generation.Unknown, "")
		task.SubmissionUncertain = true
		return task, &generation.Error{Code: "transport", Message: "fixture", SubmissionUncertain: true}
	}}
	dir := t.TempDir()
	m, options := newFixture(t, dir, p)
	project := editor.NewProject("测试")
	input := inputFor("uncertain")
	input.Estimate = &EstimateInput{Currency: "CNY", Unit: "request", UnitPrice: "2.50"}
	if _, err := m.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := awaitIdle(t, m, "uncertain", Unknown)
	if job.Estimate == nil || job.Estimate.Amount != "2.50" {
		t.Fatal("未知结果丢失提交时估价")
	}
	if job.CanResume {
		t.Fatal("未知同步请求可恢复")
	}
	if _, err := m.Resume(context.Background(), job.ID); err == nil {
		t.Fatal("未知请求恢复后可能重新扣费")
	}
	if _, err := m.AcknowledgeUnknown(job.ID); err != nil {
		t.Fatal(err)
	}
	m.Close()
	if err := os.WriteFile(filepath.Join(dir, "jobs", "broken.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Submit(context.Background(), project, inputFor("broken")); err == nil {
		t.Fatal("损坏任务被当作未提交")
	}
	if p.posts.Load() != 1 {
		t.Fatal("未知请求重新提交")
	}
}

func TestAsyncStopRestartResumeOnlyQueries(t *testing.T) {
	queried := make(chan struct{}, 1)
	p := &fixtureProvider{submit: func(_ context.Context, r generation.Request) (generation.Task, error) {
		return taskFor(r, generation.Running, "remote-1"), nil
	}, get: func(ctx context.Context, task generation.Task) (generation.Task, error) {
		queried <- struct{}{}
		<-ctx.Done()
		return task, ctx.Err()
	}}
	dir := t.TempDir()
	m, options := newFixture(t, dir, p)
	project := editor.NewProject("测试")
	in := inputFor("remote")
	in.Estimate = &EstimateInput{Currency: "USD", Unit: "request", UnitPrice: "0.29"}
	if _, err := m.Submit(context.Background(), project, in); err != nil {
		t.Fatal(err)
	}
	select {
	case <-queried:
	case <-time.After(time.Second):
		t.Fatal("未开始查询")
	}
	if _, err := m.StopWaiting("remote"); err != nil {
		t.Fatal(err)
	}
	job := awaitIdle(t, m, "remote", Waiting)
	if job.RemoteID != "remote-1" || !job.CanResume {
		t.Fatal("丢失远端恢复信息")
	}
	if job.Estimate == nil || job.Estimate.Amount != "0.29" {
		t.Fatal("停止等待丢失提交时估价")
	}
	m.Close()
	p.get = func(_ context.Context, task generation.Task) (generation.Task, error) {
		task.Status = generation.Succeeded
		task.Outputs = []generation.Output{imageOutput()}
		return task, nil
	}
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.Resume(context.Background(), "remote"); err != nil {
		t.Fatal(err)
	}
	result := awaitIdle(t, restored, "remote", Succeeded)
	if !reflect.DeepEqual(result.Estimate, job.Estimate) {
		t.Fatal("恢复原任务改变提交时估价")
	}
	if p.posts.Load() != 1 || p.gets.Load() != 2 {
		t.Fatal("恢复未遵守只查原任务", p.posts.Load(), p.gets.Load())
	}
}

func TestCancelQueuedAndInFlightSynchronous(t *testing.T) {
	entered := make(chan struct{})
	p := &fixtureProvider{submit: func(ctx context.Context, r generation.Request) (generation.Task, error) {
		close(entered)
		<-ctx.Done()
		task := taskFor(r, generation.Unknown, "")
		task.SubmissionUncertain = true
		return task, ctx.Err()
	}}
	m, _ := newFixture(t, t.TempDir(), p)
	project := editor.NewProject("测试")
	if _, err := m.Submit(context.Background(), project, inputFor("first")); err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := m.Submit(context.Background(), project, inputFor("second")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.StopWaiting("second"); err != nil {
		t.Fatal(err)
	}
	awaitIdle(t, m, "second", Cancelled)
	if _, err := m.StopWaiting("first"); err != nil {
		t.Fatal(err)
	}
	awaitIdle(t, m, "first", Unknown)
	if p.posts.Load() != 1 {
		t.Fatal("取消的排队任务仍然提交")
	}
}

func TestConcurrentDuplicateAndDirectoryOwnership(t *testing.T) {
	p := &fixtureProvider{submit: func(_ context.Context, r generation.Request) (generation.Task, error) {
		task := taskFor(r, generation.Succeeded, "")
		task.Outputs = []generation.Output{imageOutput()}
		return task, nil
	}}
	m, options := newFixture(t, t.TempDir(), p)
	if other, err := New(options); err == nil {
		other.Close()
		t.Fatal("两个实例同时拥有生成数据")
	}
	project := editor.NewProject("测试")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Submit(context.Background(), project, inputFor("same")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	awaitIdle(t, m, "same", Succeeded)
	if p.posts.Load() != 1 {
		t.Fatal("并发重复提交", p.posts.Load())
	}
}

func TestRecoverCommittedFilesWithoutResubmitOrSecret(t *testing.T) {
	p := &fixtureProvider{submit: func(_ context.Context, r generation.Request) (generation.Task, error) {
		task := taskFor(r, generation.Succeeded, "")
		task.Outputs = []generation.Output{imageOutput()}
		return task, nil
	}}
	m, options := newFixture(t, t.TempDir(), p)
	project := editor.NewProject("测试")
	if _, err := m.Submit(context.Background(), project, inputFor("committed")); err != nil {
		t.Fatal(err)
	}
	awaitIdle(t, m, "committed", Succeeded)
	m.mu.Lock()
	e := m.entries["committed"]
	e.record.Job.Status = Importing
	e.record.Job.Assets = nil
	err := m.saveLocked(e)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	options.Resolve = func(context.Context, string) (Binding, error) {
		t.Error("恢复本地已提交结果不应读取凭据")
		return Binding{}, errors.New("不能读取")
	}
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err := restored.Get("committed")
	if err != nil || job.Status != Succeeded || job.CanResume || len(job.Assets) != 1 || p.posts.Load() != 1 {
		t.Fatal("已提交素材未恢复")
	}
}

func TestRecoveredQueueAndTamperedSignature(t *testing.T) {
	p := &fixtureProvider{}
	m, options := newFixture(t, t.TempDir(), p)
	project := editor.NewProject("测试")
	input := inputFor("queued")
	input.Request.Count = 1
	now := time.Now().UTC()
	e := &entry{record: record{Version: 1, Signature: signature(input.ConnectionID, project.ID, input.Request), Job: Job{ID: "queued", ConnectionID: input.ConnectionID, ProjectID: project.ID, Request: input.Request, Status: Queued, CreatedAt: now, UpdatedAt: now, Assets: []generation.ImportedAsset{}}}}
	m.mu.Lock()
	err := m.saveLocked(e)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	job, err := restored.Get("queued")
	if err != nil || job.Status != Cancelled {
		t.Fatal("未开始的排队任务恢复后被提交", err)
	}
	restored.Close()
	e.record.Job.Request.Prompt = "篡改"
	data, _ := json.Marshal(e.record)
	if err := os.WriteFile(filepath.Join(m.journal, "queued.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	other, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Submit(context.Background(), project, input); err == nil {
		t.Fatal("损坏签名允许重新提交")
	}
	if p.posts.Load() != 0 {
		t.Fatal("载入记录触发生成")
	}
}
