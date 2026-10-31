//go:build !windows

package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func acquireProjectLock(path string) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open project lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errProjectBusy
		}
		return nil, fmt.Errorf("acquire project lock: %w", err)
	}
	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			if closeErr != nil {
				return fmt.Errorf("unlock project lock: %v; close lock file: %w", unlockErr, closeErr)
			}
			return fmt.Errorf("unlock project lock: %w", unlockErr)
		}
		return closeErr
	}, nil
}
