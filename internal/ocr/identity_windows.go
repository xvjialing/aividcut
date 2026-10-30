package ocr

import (
	"aividcut/internal/managedprocess"
	"golang.org/x/sys/windows"
	"syscall"
	"unsafe"
)

func packageIdentityAvailable() bool {
	call := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentPackageFullName")
	if call.Find() != nil {
		return false
	}
	var length uint32
	result, _, _ := call.Call(uintptr(unsafe.Pointer(&length)), 0)
	return result == 122 && length > 0 && length <= 4096
}

func configureWorker(command *managedprocess.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
