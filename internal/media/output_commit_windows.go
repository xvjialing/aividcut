//go:build windows

package media

import (
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func platformRenameNoReplace(from, to string) error {
	return windowsMove(from, to, windows.MOVEFILE_WRITE_THROUGH)
}

func platformRenameReplace(from, to string) error {
	// 不带 COPY_ALLOWED，保持同卷替换；跨卷由公共层先复制到私有暂存。
	return windowsMove(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func windowsMove(from, to string, flags uint32) error {
	source, err := windowsPath(from)
	if err != nil {
		return err
	}
	target, err := windowsPath(to)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(source, target, flags)
	if errors.Is(err, windows.ERROR_NOT_SAME_DEVICE) {
		return errors.Join(err, errCommitCrossDevice)
	}
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return errors.Join(err, errNoReplaceUnsupported)
	}
	return err
}

func windowsPath(path string) (*uint16, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(absolute, `\\?\`) {
		if strings.HasPrefix(absolute, `\\`) {
			absolute = `\\?\UNC\` + absolute[2:]
		} else {
			absolute = `\\?\` + absolute
		}
	}
	return windows.UTF16PtrFromString(absolute)
}
