//go:build !darwin && !linux && !windows

package media

import "os"

func platformRenameNoReplace(_, _ string) error { return errNoReplaceUnsupported }

func platformRenameReplace(from, to string) error { return os.Rename(from, to) }
