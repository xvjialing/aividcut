package generationjobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"aividcut/internal/editor"
	"aividcut/internal/generation"
)

// 仅使用已有的内存连接及假 Provider，回归验证失败后的恢复能力，不发起真实请求。
func TestResumeJournalFailureRemainsRetryableWithoutAnotherSubmit(t *testing.T) {
	provider := &fixtureProvider{
		submit: func(_ context.Context, request generation.Request) (generation.Task, error) {
			return taskFor(request, generation.Running, "remote-write-retry"), &generation.Error{Code: "fixture_disconnect", Message: "假服务提交回执中断", SubmissionUncertain: true}
		},
		get: func(_ context.Context, task generation.Task) (generation.Task, error) {
			task.Status = generation.Succeeded
			task.Outputs = []generation.Output{imageOutput()}
			return task, nil
		},
	}
	manager, _ := newFixture(t, t.TempDir(), provider)
	project := editor.NewProject("恢复写失败")
	input := inputFor("resume-journal-retry")
	if _, err := manager.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	awaitIdle(t, manager, input.Request.ID, Waiting)

	// 已空闲任务的 journal 暂时不可写，模拟磁盘不可用；不触及真实应用目录。
	manager.mu.Lock()
	originalJournal := manager.journal
	manager.journal = filepath.Join(t.TempDir(), "missing", "jobs")
	manager.mu.Unlock()
	_, resumeErr := manager.Resume(context.Background(), input.Request.ID)
	manager.mu.Lock()
	manager.journal = originalJournal
	manager.mu.Unlock()
	if resumeErr == nil {
		t.Fatal("恢复状态无法落盘时应向调用方报告错误")
	}
	job, err := manager.Get(input.Request.ID)
	if err != nil || job.Status != Waiting || !job.CanResume {
		t.Fatalf("临时写入失败不应消耗原任务的恢复能力：%+v，%v", job, err)
	}
	if _, err := manager.Resume(context.Background(), input.Request.ID); err != nil {
		t.Fatal("写入恢复正常后应可重试查询原任务：", err)
	}
	job = awaitIdle(t, manager, input.Request.ID, Succeeded)
	if len(job.Assets) != 1 || provider.posts.Load() != 1 || provider.gets.Load() != 1 {
		t.Fatal("恢复应只查询原远端任务并取回一个结果，不再次提交")
	}
}

func TestAsyncCommittedLocalResultRecoversWithoutCredentialsOrQuery(t *testing.T) {
	provider := &fixtureProvider{
		submit: func(_ context.Context, request generation.Request) (generation.Task, error) {
			task := taskFor(request, generation.Succeeded, "remote-already-committed")
			task.Outputs = []generation.Output{imageOutput()}
			return task, nil
		},
		get: func(_ context.Context, task generation.Task) (generation.Task, error) {
			return task, errors.New("本地完整结果不应再访问远端")
		},
	}
	manager, options := newFixture(t, t.TempDir(), provider)
	project := editor.NewProject("恢复本地异步结果")
	input := inputFor("async-local-committed")
	if _, err := manager.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	completed := awaitIdle(t, manager, input.Request.ID, Succeeded)
	if len(completed.Assets) != 1 {
		t.Fatal("假服务没有生成完整本地结果")
	}
	if _, err := os.Stat(completed.Assets[0].Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(completed.Assets[0].Path), "recipe.json")); err != nil {
		t.Fatal(err)
	}
	// 模拟目录已原子提交、成功任务状态尚未保存时进程退出。
	manager.mu.Lock()
	entry := manager.entries[input.Request.ID]
	entry.record.Job.Status, entry.record.Job.Assets = Importing, nil
	err := manager.saveLocked(entry)
	manager.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	manager.Close()
	var credentialReads atomic.Int32
	options.Resolve = func(context.Context, string) (Binding, error) {
		credentialReads.Add(1)
		return Binding{}, errors.New("假设凭据已过期或系统库当前锁定")
	}
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.Resume(context.Background(), input.Request.ID); err != nil {
		t.Fatal("完整的本地原子结果应可独立恢复：", err)
	}
	job := awaitIdle(t, restored, input.Request.ID, Succeeded)
	if len(job.Assets) != 1 || job.Assets[0].SHA256 != completed.Assets[0].SHA256 || job.Assets[0].ID != completed.Assets[0].ID {
		t.Fatal("恢复后的本地素材身份或指纹发生变化")
	}
	if credentialReads.Load() != 0 || provider.gets.Load() != 0 || provider.posts.Load() != 1 {
		t.Fatal("恢复已提交本地结果不应读取凭据、查询远端或重新提交")
	}
}

func TestRestartOfUncommittedSynchronousOutputIsUnknownImmediately(t *testing.T) {
	provider := &fixtureProvider{submit: func(_ context.Context, request generation.Request) (generation.Task, error) {
		task := taskFor(request, generation.Succeeded, "")
		task.Outputs = []generation.Output{imageOutput()}
		return task, nil
	}}
	manager, options := newFixture(t, t.TempDir(), provider)
	project := editor.NewProject("未落盘的同步结果")
	input := inputFor("sync-uncommitted-output")
	// 制造一次本地发布失败：已有同名空目录无法通过配方校验；原始字节仍暂留内存。
	conflict := filepath.Join(manager.assets, input.Request.ID)
	if err := os.Mkdir(conflict, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Submit(context.Background(), project, input); err != nil {
		t.Fatal(err)
	}
	job := awaitIdle(t, manager, input.Request.ID, Waiting)
	if !job.CanResume {
		t.Fatal("同进程内保留内联字节时应允许重试落盘")
	}
	if err := os.Remove(conflict); err != nil {
		t.Fatal(err)
	}
	manager.Close()
	restored, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	job, err = restored.Get(input.Request.ID)
	if err != nil || job.Status != Unknown || job.CanResume {
		t.Fatalf("重启后既无远端标识也无本地完整结果，应立即说明无法恢复：%+v，%v", job, err)
	}
	if _, err := restored.Resume(context.Background(), input.Request.ID); err == nil {
		t.Fatal("丢失内联输出后不应再提供虚假的恢复操作")
	}
	input.Consent = false
	if _, err := restored.Submit(context.Background(), project, input); err != nil {
		t.Fatal("相同请求标识应返回原未知任务：", err)
	}
	if provider.posts.Load() != 1 || provider.gets.Load() != 0 {
		t.Fatal("未知同步任务不可通过恢复或重复请求重新生成")
	}
}
