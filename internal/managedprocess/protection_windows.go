//go:build windows

package managedprocess

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProtection struct {
	mu       sync.Mutex
	job      windows.Handle
	assigned bool
	stopping bool
}

func newProtection() protection { return &windowsProtection{} }
func (p *windowsProtection) prepare(command *exec.Cmd) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("无法创建进程Job：%w", err)
	}
	p.job = job
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fmt.Errorf("无法设置Job关闭清理：%w", err)
	}
	if err = windows.SetHandleInformation(job, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		return err
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_BREAKAWAY_FROM_JOB != 0 {
		return errors.New("受控进程不能请求离开Job")
	}
	command.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return nil
}

var processIDOfThread = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessIdOfThread")

// Go的StartProcess关闭初始线程句柄；在主线程尚未执行时确认唯一线程并重新打开。
func suspendedThread(pid uint32) (windows.Handle, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	err = windows.Thread32First(snapshot, &entry)
	var found uint32
	for count := 0; err == nil; count++ {
		if count >= 65536 {
			return 0, errors.New("线程枚举超出确认预算")
		}
		if entry.OwnerProcessID == pid {
			if found != 0 {
				return 0, errors.New("挂起进程线程数不唯一，未继续执行")
			}
			found = entry.ThreadID
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		err = windows.Thread32Next(snapshot, &entry)
	}
	if err != windows.ERROR_NO_MORE_FILES || found == 0 {
		return 0, errors.New("无法确认挂起的主线程")
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME|windows.THREAD_QUERY_LIMITED_INFORMATION, false, found)
	if err != nil {
		return 0, err
	}
	owner, _, _ := processIDOfThread.Call(uintptr(thread))
	if uint32(owner) != pid {
		windows.CloseHandle(thread)
		return 0, errors.New("线程身份已变化")
	}
	return thread, nil
}

func (p *windowsProtection) started(command *exec.Cmd) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping || p.job == 0 {
		return errors.New("启动已取消，未恢复主线程")
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(command.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(p.job, process); err != nil {
		return fmt.Errorf("无法将挂起进程加入Job：%w", err)
	}
	p.assigned = true
	thread, err := suspendedThread(uint32(command.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(thread)
	previous, err := windows.ResumeThread(thread)
	if err != nil {
		return fmt.Errorf("恢复主线程失败：%w", err)
	}
	if previous != 1 {
		return fmt.Errorf("主线程挂起状态未确认（%d）", previous)
	}
	return nil
}
func (p *windowsProtection) cancel(command *exec.Cmd) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopping = true
	if p.job != 0 && p.assigned {
		return windows.TerminateJobObject(p.job, 1)
	}
	if command.Process != nil {
		return command.Process.Kill()
	}
	return os.ErrProcessDone
}
func (p *windowsProtection) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopping = true
	if p.job == 0 {
		return nil
	}
	job := p.job
	p.job = 0
	return windows.CloseHandle(job)
}
