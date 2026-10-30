//go:build darwin || linux

package managedprocess

import (
	"os"
	"os/exec"
	"syscall"
)

type unixProtection struct{}

func newProtection() protection { return &unixProtection{} }
func (*unixProtection) prepare(command *exec.Cmd) error {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setpgid = true
	return nil
}
func (*unixProtection) started(*exec.Cmd) error { return nil }
func (*unixProtection) close() error            { return nil }
func (*unixProtection) cancel(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}
