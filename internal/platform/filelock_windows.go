//go:build windows

package platform

import (
	"errors"
	"runtime"

	"golang.org/x/sys/windows"
)

type lockPlatform struct {
	mode       LockMode
	overlapped windows.Overlapped
}

func (l *advisoryLock) tryLock() (busy bool, err error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if l.plat.mode != LockShared {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err = windows.LockFileEx(windows.Handle(l.file.Fd()), flags, 0, 1, 0, &l.plat.overlapped)
	runtime.KeepAlive(l.file)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return true, nil
	}
	return false, err
}

func (l *advisoryLock) unlock() error {
	// UnlockFileEx requires the same Overlapped offset used by LockFileEx.
	err := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.plat.overlapped)
	runtime.KeepAlive(l.file)
	return err
}
