package jobs

import (
	"context"
	"testing"
	"time"
)

func TestTerminalObserverRunsOutsideQueueLock(t *testing.T) {
	finished := make(chan Job, 1)
	var manager *Manager
	manager = NewWithObserver(func(job Job) { _ = manager.List(); finished <- job })
	defer manager.Close()
	job, err := manager.Start("fixture", "等待", func(context.Context, func(float64, string)) (string, error) { return "fixture-output", nil })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case terminal := <-finished:
		if terminal.ID != job.ID || terminal.Status != "succeeded" {
			t.Fatal("终态观察不一致", terminal)
		}
	case <-time.After(time.Second):
		t.Fatal("观察者在队列锁内死锁")
	}
}

func TestObserverPanicDoesNotRepeatOrPreventDone(t *testing.T) {
	manager := NewWithObserver(func(Job) { panic("fixture observer") })
	if _, err := manager.Start("fixture", "等待", func(context.Context, func(float64, string)) (string, error) { return "", nil }); err != nil {
		t.Fatal(err)
	}
	manager.Close()
	select {
	case <-manager.Done():
	case <-time.After(time.Second):
		t.Fatal("观察者异常阻止工作组退出")
	}
}
