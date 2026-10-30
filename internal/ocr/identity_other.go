//go:build !windows

package ocr

import "aividcut/internal/managedprocess"

func packageIdentityAvailable() bool              { return false }
func configureWorker(command *managedprocess.Cmd) {}
