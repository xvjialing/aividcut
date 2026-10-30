package statefilelock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExclusiveAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", ".lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if other, err := Acquire(path); !errors.Is(err, ErrInUse) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("第二实例必须拒绝: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProcessLockHelper(t *testing.T) {
	path := os.Getenv("AIVIDCUT_TEST_LOCK_PATH")
	if path == "" {
		return
	}
	lock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestKernelReleasesLockWhenProcessExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), ".lock")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProcessLockHelper$")
	cmd.Env = append(os.Environ(), "AIVIDCUT_TEST_LOCK_PATH="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "locked" {
		t.Fatal("子进程未取得锁", scanner.Err())
	}
	if lock, err := Acquire(path); !errors.Is(err, ErrInUse) {
		if lock != nil {
			lock.Close()
		}
		t.Fatal("不同进程同时取得锁", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	lock, err := Acquire(path)
	if err != nil {
		t.Fatal("进程退出后未释放锁", err)
	}
	defer lock.Close()
}
