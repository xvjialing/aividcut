package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func terminal(t *testing.T, m *Manager, id string) Job {
	t.Helper()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		for _, j := range m.List() {
			if j.ID == id && j.Status != "running" && j.Status != "queued" {
				return j
			}
		}
		select {
		case <-deadline:
			t.Fatal("任务未结束")
		case <-tick.C:
		}
	}
}

func TestQueueCancellationDoesNotRunWork(t *testing.T) {
	m := New()
	defer m.Close()
	started, release := make(chan struct{}), make(chan struct{})
	first, _ := m.Start("export", "开始", func(context.Context, func(float64, string)) (string, error) {
		close(started)
		<-release
		return "one.mp4", nil
	})
	<-started
	called := false
	second, _ := m.Start("export", "排队", func(context.Context, func(float64, string)) (string, error) {
		called = true
		return "", nil
	})
	_, _ = m.Cancel(second.ID)
	if j := terminal(t, m, second.ID); j.Status != "cancelled" {
		t.Fatalf("状态：%s", j.Status)
	}
	close(release)
	if j := terminal(t, m, first.ID); j.Status != "succeeded" || j.OutputPath != "one.mp4" {
		t.Fatalf("任务结果：%+v", j)
	}
	if called {
		t.Fatal("已取消的排队任务仍执行了工作")
	}
}

func TestFailedWorkNeverBecomesSuccess(t *testing.T) {
	m := New()
	defer m.Close()
	j, _ := m.Start("export", "开始", func(_ context.Context, report func(float64, string)) (string, error) {
		report(.4, "编码")
		return "bad.mp4", errors.New("编码器失败")
	})
	got := terminal(t, m, j.ID)
	if got.Status != "failed" || got.OutputPath != "" || got.Message != "编码器失败" {
		t.Fatalf("错误状态：%+v", got)
	}
}

func TestLateCancellationCannotHideCommittedOutput(t *testing.T) {
	m := New()
	defer m.Close()
	ready, release := make(chan struct{}), make(chan struct{})
	j, _ := m.Start("export", "开始", func(_ context.Context, _ func(float64, string)) (string, error) {
		close(ready)
		<-release
		return "committed.mp4", nil
	})
	<-ready
	_, _ = m.Cancel(j.ID)
	close(release)
	result := terminal(t, m, j.ID)
	if result.Status != "succeeded" || result.OutputPath != "committed.mp4" {
		t.Fatalf("已经提交的输出被取消状态隐藏：%+v", result)
	}
}

func TestCloseWaitsForCancellationCleanup(t *testing.T) {
	m := New()
	started, cleaned := make(chan struct{}), make(chan struct{})
	_, _ = m.Start("export", "开始", func(ctx context.Context, _ func(float64, string)) (string, error) {
		close(started)
		<-ctx.Done()
		close(cleaned)
		return "", ctx.Err()
	})
	<-started
	m.Close()
	select {
	case <-cleaned:
	default:
		t.Fatal("关闭在任务清理前返回")
	}
	if _, err := m.Start("export", "开始", nil); err == nil {
		t.Fatal("关闭后仍允许启动新任务")
	}
}
