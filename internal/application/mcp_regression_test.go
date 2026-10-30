package application

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"aividcut/internal/editor"
)

type mcpLockRegressionContext struct {
	context.Context
	firstCheck chan struct{}
	once       sync.Once
}

func (ctx *mcpLockRegressionContext) Err() error {
	// 先记录本次Err结果，再通知测试线程取消，保证首个前置检查确实看到了nil。
	err := ctx.Context.Err()
	ctx.once.Do(func() { close(ctx.firstCheck) })
	return err
}

func TestMCPCancelledWhileWaitingForEngineLockCannotCommit(t *testing.T) {
	service := newMCPApplication(t)
	before := service.GetBootstrap().Project
	if _, err := service.StartMCP(MCPStartInput{ProjectID: before.ID, AllowEdits: true}); err != nil {
		t.Fatal(err)
	}
	var handler func(context.Context, json.RawMessage) (any, error)
	for _, tool := range service.projectMCPTools(service.mcpScope) {
		if tool.Name == "aividcut_apply_edit" {
			handler = tool.Handler
		}
	}
	if handler == nil {
		t.Fatal("授权编辑工具不存在")
	}
	request := editor.EditRequest{BaseRevision: before.Revision, IdempotencyKey: "cancelled-engine-lock-edit", Commands: []editor.Command{{Type: "update_project", Name: "不能提交的取消编辑"}}}
	input, err := json.Marshal(map[string]any{"request": request})
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &mcpLockRegressionContext{Context: base, firstCheck: make(chan struct{})}
	finished := make(chan error, 1)
	service.mu.Lock()
	locked := true
	defer func() {
		if locked {
			service.mu.Unlock()
		}
	}()
	go func() { _, err := handler(ctx, input); finished <- err }()
	select {
	case <-ctx.firstCheck:
	case <-time.After(time.Second):
		t.Fatal("工具未进入锁前上下文检查")
	}
	cancel()
	service.mu.Unlock()
	locked = false
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("等待工程锁期间取消须保留取消状态：%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("锁释放后取消回调未结束")
	}
	after := service.GetBootstrap()
	if after.Project.Name != before.Name || after.Project.Revision != before.Revision || after.CanUndo {
		t.Fatal("取消的等待操作修改了真实工程或撤销栈")
	}
}
