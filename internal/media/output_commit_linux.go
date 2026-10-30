//go:build linux

package media

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func platformRenameNoReplace(from, to string) error {
	return normalizeCommitError(unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE))
}

func platformRenameReplace(from, to string) error {
	return normalizeCommitError(os.Rename(from, to))
}

func normalizeCommitError(err error) error {
	if errors.Is(err, syscall.EXDEV) {
		return errors.Join(err, errCommitCrossDevice)
	}
	if errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EINVAL) {
		return errors.Join(err, errNoReplaceUnsupported)
	}
	return err
}
