//go:build !darwin && !linux && !windows

package managedprocess

import (
	"errors"
	"os/exec"
)

type unsupportedProtection struct{}

func newProtection() protection { return &unsupportedProtection{} }
func (*unsupportedProtection) prepare(*exec.Cmd) error {
	return errors.New("当前平台尚未实现受控进程启动")
}
func (*unsupportedProtection) started(*exec.Cmd) error { return nil }
func (*unsupportedProtection) cancel(*exec.Cmd) error  { return nil }
func (*unsupportedProtection) close() error            { return nil }
