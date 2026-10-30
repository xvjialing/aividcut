// Package managedprocess 管理本应用明确启动的子进程；不是权限沙箱。
package managedprocess

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

type protection interface {
	prepare(*exec.Cmd) error
	started(*exec.Cmd) error
	cancel(*exec.Cmd) error
	close() error
}

// Cmd保留exec的流/环境配置，启动与等待统一经过平台进程树控制。
// 与exec.Cmd相同，除Cancel回调外，不应并发调用其生命周期方法。
type Cmd struct {
	*exec.Cmd
	protection protection
	started    bool
	closed     bool
}

func CommandContext(ctx context.Context, name string, args ...string) *Cmd {
	command := exec.CommandContext(ctx, name, args...)
	command.WaitDelay = 2 * time.Second
	result := &Cmd{Cmd: command, protection: newProtection()}
	command.Cancel = func() error { return result.protection.cancel(command) }
	return result
}

func (c *Cmd) close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	return c.protection.close()
}
func (c *Cmd) Start() error {
	if c.started || c.closed {
		return errors.New("受控进程不能重复启动")
	}
	if err := c.protection.prepare(c.Cmd); err != nil {
		c.close()
		return err
	}
	if err := c.Cmd.Start(); err != nil {
		c.close()
		return err
	}
	c.started = true
	if err := c.protection.started(c.Cmd); err != nil {
		// Windows此时主线程仍挂起；失败不允许退化为普通未受控启动。
		c.protection.cancel(c.Cmd)
		c.Cmd.Process.Kill()
		c.Cmd.Wait()
		c.close()
		return err
	}
	return nil
}
func (c *Cmd) Wait() error {
	err := c.Cmd.Wait()
	return errors.Join(err, c.close())
}
func (c *Cmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}
func (c *Cmd) Output() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("受控Output不能已有Stdout")
	}
	var output bytes.Buffer
	c.Stdout = &output
	err := c.Run()
	return output.Bytes(), err
}
func (c *Cmd) CombinedOutput() ([]byte, error) {
	if c.Stdout != nil || c.Stderr != nil {
		return nil, errors.New("受控CombinedOutput不能已有输出流")
	}
	var output bytes.Buffer
	c.Stdout, c.Stderr = &output, &output
	err := c.Run()
	return output.Bytes(), err
}
