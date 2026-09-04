//go:build windows

package singleinstance

import (
	"syscall"
	"unsafe"
)

var (
	modKernel32             = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW        = modKernel32.NewProc("CreateMutexW")
	procCreateEventW        = modKernel32.NewProc("CreateEventW")
	procOpenEventW          = modKernel32.NewProc("OpenEventW")
	procSetEvent            = modKernel32.NewProc("SetEvent")
	procWaitForSingleObject = modKernel32.NewProc("WaitForSingleObject")
	procCloseHandle         = modKernel32.NewProc("CloseHandle")
)

const (
	mutexName        = "Local\\dsh-desktop-single-instance-mutex"
	eventName        = "Local\\dsh-desktop-show-window-event"
	errAlreadyExists = syscall.Errno(183)
	eventAllAccess   = 0x1F0003
	waitObject0      = 0
	infinite         = 0xFFFFFFFF
)

func Acquire() bool {
	namePtr, _ := syscall.UTF16PtrFromString(mutexName)
	handle, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if handle == 0 {
		return true
	}
	if errno, ok := lastErr.(syscall.Errno); ok && errno == errAlreadyExists {
		procCloseHandle.Call(handle)
		signalShowWindow()
		return false
	}
	return true
}

func signalShowWindow() {
	namePtr, _ := syscall.UTF16PtrFromString(eventName)
	evHandle, _, _ := procOpenEventW.Call(eventAllAccess, 0, uintptr(unsafe.Pointer(namePtr)))
	if evHandle == 0 {
		return
	}
	procSetEvent.Call(evHandle)
	procCloseHandle.Call(evHandle)
}

func ListenShowWindow(callback func()) {
	namePtr, _ := syscall.UTF16PtrFromString(eventName)
	handle, _, _ := procCreateEventW.Call(0, 0, 0, uintptr(unsafe.Pointer(namePtr)))
	if handle == 0 {
		return
	}
	go func() {
		for {
			ret, _, _ := procWaitForSingleObject.Call(handle, infinite)
			if ret == waitObject0 {
				callback()
			}
		}
	}()
}
