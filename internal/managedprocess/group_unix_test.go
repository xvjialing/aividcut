//go:build darwin || linux

package managedprocess

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestActualCancellationStopsOwnedGrandchildHeartbeat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	file := filepath.Join(t.TempDir(), "owned.pid")
	c := helper(ctx, "branch")
	c.Env = append(c.Env, "AIVIDCUT_MANAGED_PID_FILE="+file, "AIVIDCUT_MANAGED_HEARTBEAT=1")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(file)
		pid, _ = strconv.Atoi(string(data))
		if pid > 0 {
			if _, err := os.Stat(file + ".heartbeat"); err == nil {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid <= 0 {
		cancel()
		c.Wait()
		t.Fatal("未启动实际后代")
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	cancel()
	if err := c.Wait(); err == nil {
		t.Fatal("取消被当作成功")
	}
	first, err := os.ReadFile(file + ".heartbeat")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	last, err := os.ReadFile(file + ".heartbeat")
	if err != nil || !bytes.Equal(first, last) {
		t.Fatal("取消后实际后代仍在运行", err)
	}
}
