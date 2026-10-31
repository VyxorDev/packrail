//go:build windows

package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	lockFileFailImmediately = 0x00000001
	lockFileExclusiveLock   = 0x00000002
	errorLockViolation      = syscall.Errno(33)
)

var (
	lockKernel32       = syscall.NewLazyDLL("kernel32.dll")
	lockFileExProc     = lockKernel32.NewProc("LockFileEx")
	unlockFileExProc   = lockKernel32.NewProc("UnlockFileEx")
)

func acquireProjectLock(path string) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open project lock: %w", err)
	}
	overlapped := new(syscall.Overlapped)
	flags := lockFileFailImmediately | lockFileExclusiveLock
	result, _, callErr := lockFileExProc.Call(
		file.Fd(),
		uintptr(flags),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(overlapped)),
	)
	if result == 0 {
		_ = file.Close()
		if errors.Is(callErr, errorLockViolation) {
			return nil, errProjectBusy
		}
		if callErr == syscall.Errno(0) {
			callErr = syscall.GetLastError()
		}
		return nil, fmt.Errorf("acquire project lock: %w", callErr)
	}
	return func() error {
		result, _, unlockErr := unlockFileExProc.Call(
			file.Fd(),
			0,
			1,
			0,
			uintptr(unsafe.Pointer(overlapped)),
		)
		closeErr := file.Close()
		if result == 0 {
			if unlockErr == syscall.Errno(0) {
				unlockErr = syscall.GetLastError()
			}
			if closeErr != nil {
				return fmt.Errorf("unlock project lock: %v; close lock file: %w", unlockErr, closeErr)
			}
			return fmt.Errorf("unlock project lock: %w", unlockErr)
		}
		return closeErr
	}, nil
}
