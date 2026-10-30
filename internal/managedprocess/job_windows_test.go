//go:build windows

package managedprocess

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestNativeWindowsJobOwnsGrandchildOnCancelAndRootExit(t *testing.T) {
	for _, mode := range []string{"branch", "branch-exit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pidFile := filepath.Join(t.TempDir(), "owned-leaf.pid")
			command := helper(ctx, mode)
			command.Env = append(command.Env, "AIVIDCUT_MANAGED_PID_FILE="+pidFile)
			if err := command.Start(); err != nil {
				t.Fatal("原生Job启动失败", err)
			}
			defer command.Process.Kill()
			var pid int
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				data, err := os.ReadFile(pidFile)
				if err == nil {
					pid, _ = strconv.Atoi(string(data))
					if pid > 0 {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid <= 0 {
				cancel()
				command.Wait()
				t.Fatal("受控派生进程未就绪")
			}
			leaf, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
			if err != nil {
				cancel()
				command.Wait()
				t.Fatal(err)
			}
			defer windows.CloseHandle(leaf)
			if mode == "branch" {
				cancel()
			}
			err = command.Wait()
			if mode == "branch-exit" && err != nil {
				t.Fatal("正常退出被错误标失败", err)
			}
			if mode == "branch" && err == nil {
				t.Fatal("取消被标成功")
			}
			state, err := windows.WaitForSingleObject(leaf, 5000)
			if err != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatal("本次Job的派生进程仍运行", state, err)
			}
		})
	}
}
