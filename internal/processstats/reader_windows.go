//go:build windows

package processstats

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"unsafe"
)

type platformReader struct{}

var memoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

type memoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

func (platformReader) Enumerate(ctx context.Context) ([]Identity, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, errors.New("Windows进程快照不可用")
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	err = windows.Process32First(snapshot, &entry)
	out := []Identity{}
	for err == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.ProcessID > 0 {
			out = append(out, Identity{PID: int(entry.ProcessID), PPID: int(entry.ParentProcessID), Name: windows.UTF16ToString(entry.ExeFile[:])})
		}
		if len(out) > MaxEnumerated {
			return nil, errors.New("Windows进程快照超过当前上限")
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		err = windows.Process32Next(snapshot, &entry)
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return nil, errors.New("Windows进程快照读取未完成")
	}
	return out, nil
}
func (platformReader) Read(ctx context.Context, node Identity) (Counters, error) {
	if err := ctx.Err(); err != nil {
		return Counters{}, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(node.PID))
	if err != nil {
		handle, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(node.PID))
	}
	if err != nil {
		return Counters{}, errors.New("进程已退出或无权读取身份")
	}
	defer windows.CloseHandle(handle)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(handle, &created, &exited, &kernel, &user) != nil {
		return Counters{}, errors.New("无法核对Windows进程出生身份")
	}
	node.StartedNS = created.Nanoseconds()
	total := ((uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)) + (uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime))) * 100
	out := Counters{Identity: node, CPUTimeNS: &total, PrivateKind: "privateCommit"}
	memory := memoryCounters{CB: uint32(unsafe.Sizeof(memoryCounters{}))}
	ok, _, _ := memoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&memory)), uintptr(memory.CB))
	if ok == 0 {
		out.Issue = fmt.Sprintf("PID %d内存读取受系统限制", node.PID)
	} else {
		rss, private := uint64(memory.WorkingSetSize), uint64(memory.PrivateUsage)
		out.RSSBytes = &rss
		out.PrivateBytes = &private
	}
	return out, nil
}
