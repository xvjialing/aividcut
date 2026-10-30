package application

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"aividcut/internal/editor"
	"aividcut/internal/localagent"
)

type regressionLoginRunner struct {
	*fixtureAgent
	login func(context.Context, localagent.Provider) (localagent.LoginCommand, error)
}

func (runner *regressionLoginRunner) LoginInstructions(ctx context.Context, provider localagent.Provider) (localagent.LoginCommand, error) {
	return runner.login(ctx, provider)
}

func TestAgentCancelledLoginInstructionsNeverCallRunner(t *testing.T) {
	var calls atomic.Int32
	runner := &regressionLoginRunner{fixtureAgent: &fixtureAgent{}, login: func(context.Context, localagent.Provider) (localagent.LoginCommand, error) {
		calls.Add(1)
		return localagent.LoginCommand{Executable: "/fixture/cli", Args: []string{"login"}}, nil
	}}
	service := newAgentService(t, t.TempDir(), runner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.GetAgentLoginInstructions(ctx, localagent.Codex)
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("已取消的登录说明请求不得调用runner：calls=%d err=%v", calls.Load(), err)
	}
}

func TestAgentLoginInstructionsRecheckCancellationAfterRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	runner := &regressionLoginRunner{fixtureAgent: &fixtureAgent{}, login: func(context.Context, localagent.Provider) (localagent.LoginCommand, error) {
		calls.Add(1)
		// 模拟检测过程中被取消，但runner仍返回一个语法有效的说明。
		cancel()
		return localagent.LoginCommand{Executable: "/fixture/cli", Args: []string{"login"}}, nil
	}}
	service := newAgentService(t, t.TempDir(), runner)
	_, err := service.GetAgentLoginInstructions(ctx, localagent.Codex)
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("runner返回后仍须保留取消状态：calls=%d err=%v", calls.Load(), err)
	}
}

func TestAgentCancellationDuringSecondMetadataProviderReturnsContextError(t *testing.T) {
	secondStarted := make(chan struct{})
	runner := &fixtureAgent{inspect: func(ctx context.Context, provider localagent.Provider) localagent.Installation {
		if provider == localagent.Claude {
			close(secondStarted)
			<-ctx.Done()
		}
		return localagent.Installation{Provider: provider}
	}}
	service := newAgentService(t, t.TempDir(), runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := service.GetAgentStatus(ctx)
		finished <- err
	}()
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("未进入第二个CLI元数据检测")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("第二个CLI检测期间的取消不能返回成功：%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消后元数据检测未结束")
	}
}

func TestAgentQueuedCancellationSurvivesQueueHistoryPruningAndIsIdempotent(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	runner := &fixtureAgent{plan: func(context.Context, localagent.PlanInput) (localagent.PlanResult, error) {
		close(started)
		<-release
		return localagent.PlanResult{}, errors.New("fixture 已释放")
	}}
	service := newAgentService(t, t.TempDir(), runner)
	if _, err := service.StartAgentPlan(context.Background(), agentInput("cancel-history-blocker")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("阻塞任务未启动")
	}
	service.agentMu.Lock()
	queue := service.agents.queue
	service.agentMu.Unlock()
	target := ""
	for index := 0; index < 105; index++ {
		id := editor.NewID("cancel-history")
		if target == "" {
			target = id
		}
		if _, err := service.StartAgentPlan(context.Background(), agentInput(id)); err != nil {
			t.Fatal(err)
		}
		if _, err := service.CancelAgentPlan(id); err != nil {
			t.Fatal(err)
		}
		service.agentMu.Lock()
		queueID := service.agents.entries[id].queueID
		service.agentMu.Unlock()
		// 仅等待底层fixture结束，始终不调用应用ListAgentPlans刷新持久记录。
		until := time.Now().Add(2 * time.Second)
		cancelled := false
		for time.Now().Before(until) {
			for _, job := range queue.List() {
				if job.ID == queueID && job.Status == "cancelled" {
					cancelled = true
				}
			}
			if cancelled {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if !cancelled {
			t.Fatal("排队fixture取消未完成")
		}
	}
	close(release)
	released = true
	queue.Close()
	select {
	case <-queue.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("关闭后队列或终态通知仍未完成")
	}
	// 此时最早的队列记录已经被100条历史上限清理；应用记录仍须独立保持终态。
	for repeat := 0; repeat < 2; repeat++ {
		job, err := service.CancelAgentPlan(target)
		if err != nil || job.Status != "cancelled" {
			t.Fatalf("被清理的旧取消记录不能卡住或破坏取消幂等：status=%s err=%v", job.Status, err)
		}
	}
	if runner.plans.Load() != 1 {
		t.Fatal("已取消的排队任务执行了CLI规划fixture")
	}
}
