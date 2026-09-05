//go:build unix

package platform

import (
	"errors"
	"runtime"

	"golang.org/x/sys/unix"
)

type lockPlatform struct {
	mode LockMode
}

func (l *advisoryLock) tryLock() (busy bool, err error) {
	how := unix.LOCK_NB
	switch l.plat.mode {
	case LockShared:
		how |= unix.LOCK_SH
	default:
		how |= unix.LOCK_EX
	}
	err = unix.Flock(int(l.file.Fd()), how)
	runtime.KeepAlive(l.file)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return true, nil
	}
	return false, err
}

func (l *advisoryLock) unlock() error {
	err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	runtime.KeepAlive(l.file)
	return err
}
