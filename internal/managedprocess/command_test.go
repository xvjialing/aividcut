package managedprocess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"testing"
	"time"
)

type recordingProtection struct {
	events                           []string
	prepareErr, startedErr, closeErr error
}

func (p *recordingProtection) prepare(*exec.Cmd) error {
	p.events = append(p.events, "prepare")
	return p.prepareErr
}
func (p *recordingProtection) started(*exec.Cmd) error {
	p.events = append(p.events, "started")
	return p.startedErr
}
func (p *recordingProtection) cancel(c *exec.Cmd) error {
	p.events = append(p.events, "cancel")
	if c.Process != nil {
		return c.Process.Kill()
	}
	return os.ErrProcessDone
}
func (p *recordingProtection) close() error { p.events = append(p.events, "close"); return p.closeErr }

func TestManagedProcessHelper(t *testing.T) {
	mode := os.Getenv("AIVIDCUT_MANAGED_HELPER")
	if mode == "" {
		return
	}
	if mode == "leaf" {
		if err := os.WriteFile(os.Getenv("AIVIDCUT_MANAGED_PID_FILE"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(9)
		}
		if os.Getenv("AIVIDCUT_MANAGED_HEARTBEAT") == "1" {
			deadline := time.Now().Add(time.Minute)
			for time.Now().Before(deadline) {
				os.WriteFile(os.Getenv("AIVIDCUT_MANAGED_PID_FILE")+".heartbeat", []byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0600)
				time.Sleep(10 * time.Millisecond)
			}
		} else {
			time.Sleep(time.Minute)
		}
		os.Exit(0)
	}
	if mode == "branch" || mode == "branch-exit" {
		child := exec.Command(os.Args[0], "-test.run=^TestManagedProcessHelper$")
		child.Env = append(os.Environ(), "AIVIDCUT_MANAGED_HELPER=leaf")
		if err := child.Start(); err != nil {
			os.Exit(10)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(os.Getenv("AIVIDCUT_MANAGED_PID_FILE")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if mode == "branch-exit" {
			os.Exit(0)
		}
		child.Wait()
		os.Exit(0)
	}
	if mode == "wait" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	fmt.Fprint(os.Stdout, "output")
	fmt.Fprint(os.Stderr, "error-output")
	if mode == "fail" {
		os.Exit(7)
	}
	os.Exit(0)
}
func helper(ctx context.Context, mode string) *Cmd {
	c := CommandContext(ctx, os.Args[0], "-test.run=^TestManagedProcessHelper$")
	c.Env = append(os.Environ(), "AIVIDCUT_MANAGED_HELPER="+mode)
	return c
}
func TestPreparationFailureNeverStartsChild(t *testing.T) {
	c := helper(context.Background(), "print")
	p := &recordingProtection{prepareErr: errors.New("job配置失败")}
	c.protection = p
	if err := c.Run(); err == nil || c.Process != nil || !reflect.DeepEqual(p.events, []string{"prepare", "close"}) {
		t.Fatal("保护失败仍启动", err, p.events)
	}
}
func TestAttachmentFailureReapsStartedProcess(t *testing.T) {
	c := helper(context.Background(), "wait")
	p := &recordingProtection{startedErr: errors.New("加入失败")}
	c.protection = p
	if err := c.Run(); err == nil || c.ProcessState == nil || !reflect.DeepEqual(p.events, []string{"prepare", "started", "cancel", "close"}) {
		t.Fatal("附加失败未收尾", err, p.events)
	}
	if err := c.Start(); err == nil {
		t.Fatal("失败命令被重复启动")
	}
}
func TestRunAndOutputCannotBypassLifecycle(t *testing.T) {
	for _, combined := range []bool{false, true} {
		c := helper(context.Background(), "print")
		p := &recordingProtection{}
		c.protection = p
		var output []byte
		var err error
		if combined {
			output, err = c.CombinedOutput()
		} else {
			output, err = c.Output()
		}
		want := "output"
		if combined {
			want += "error-output"
		}
		if err != nil || string(output) != want || !reflect.DeepEqual(p.events, []string{"prepare", "started", "close"}) {
			t.Fatal(string(output), err, p.events)
		}
	}
}
func TestWaitKeepsExitErrorAndCleanupFailure(t *testing.T) {
	c := helper(context.Background(), "fail")
	cleanup := errors.New("关闭失败")
	c.protection = &recordingProtection{closeErr: cleanup}
	err := c.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || !errors.Is(err, cleanup) {
		t.Fatal("丢失退出或清理原因", err)
	}
}
func TestActualCommandCancellationIsBounded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := helper(ctx, "wait")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	cancel()
	finished := make(chan error, 1)
	go func() { finished <- c.Wait() }()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("取消被当成功")
		}
	case <-time.After(5 * time.Second):
		c.Process.Kill()
		t.Fatal("取消未在预算内退出")
	}
}
func TestCanceledContextDoesNotStartCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := helper(ctx, "print")
	if err := c.Run(); !errors.Is(err, context.Canceled) || c.Process != nil {
		t.Fatal("已取消上下文仍执行", err)
	}
}
