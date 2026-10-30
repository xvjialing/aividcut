//go:build windows

package playback

import (
	"golang.org/x/sys/windows"
	"os"
)

func workerEnvironment(path string) ([]string, error) {
	directory, e := windows.GetWindowsDirectory()
	if e != nil {
		return nil, e
	}
	return windowsWorkerEnvironment(path, directory, os.TempDir()), nil
}
