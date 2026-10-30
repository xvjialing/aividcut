package playback

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsWorkerEnvironmentUsesOnlyExplicitDirectories(t *testing.T) {
	root := t.TempDir()
	worker := filepath.Join(root, "bundle/bin/worker.exe")
	windowsDir := filepath.Join(root, "Windows")
	temp := filepath.Join(root, "temp")
	env := windowsWorkerEnvironment(worker, windowsDir, temp)
	all := strings.Join(env, "\n")
	for _, value := range []string{filepath.Dir(worker), "SystemRoot=" + windowsDir, "WINDIR=" + windowsDir, "TEMP=" + temp} {
		if !strings.Contains(all, value) {
			t.Fatal(env)
		}
	}
	if strings.Contains(all, "/usr/bin:/bin") || len(env) != 5 {
		t.Fatal("不能把Unix PATH或任意父进程环境给Windows worker", env)
	}
}
