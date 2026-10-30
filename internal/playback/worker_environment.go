package playback

import "path/filepath"

// windowsWorkerEnvironment 仅传运行所需目录，不继承CLI账号或任意全局PATH。
func windowsWorkerEnvironment(worker, windowsDir, temp string) []string {
	return []string{"PATH=" + filepath.Dir(worker) + ";" + filepath.Join(windowsDir, "System32") + ";" + windowsDir, "SystemRoot=" + windowsDir, "WINDIR=" + windowsDir, "TEMP=" + temp, "TMP=" + temp}
}
