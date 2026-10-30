//go:build darwin

package media

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func platformRenameNoReplace(from, to string) error {
	return normalizeCommitError(unix.RenamexNp(from, to, unix.RENAME_EXCL))
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
