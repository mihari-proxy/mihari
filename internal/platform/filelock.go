package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

const lockRetryTick = 5 * time.Millisecond

// LockMode selects a shared or exclusive advisory lock.
type LockMode uint8

const (
	// LockShared allows concurrent shared holders.
	LockShared LockMode = iota
	// LockExclusive denies concurrent holders.
	LockExclusive
)

// AdvisoryLock is a closeable OS advisory lock on a lock file.
type AdvisoryLock interface {
	Lock(context.Context, LockMode) error
	Unlock() error
	Close() error
}

type lockTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type stdLockTimer struct {
	t *time.Timer
}

func (t stdLockTimer) C() <-chan time.Time { return t.t.C }
func (t stdLockTimer) Stop() bool          { return t.t.Stop() }

var newLockTimer = func(d time.Duration) lockTimer {
	return stdLockTimer{t: time.NewTimer(d)}
}

type advisoryLock struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
	held   bool
	plat   lockPlatform
}

// OpenAdvisoryLock opens path via fs.OpenAppend and returns an advisory lock.
func OpenAdvisoryLock(fs *PrivateFS, path string) (AdvisoryLock, error) {
	f, err := fs.OpenAppend(path)
	if err != nil {
		return nil, fmt.Errorf("open advisory lock: %w", err)
	}
	return &advisoryLock{file: f}, nil
}

func (l *advisoryLock) Lock(ctx context.Context, mode LockMode) error {
	return waitLock(ctx, func() (busy bool, err error) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.closed || l.file == nil {
			return false, fmt.Errorf("advisory lock: %w", os.ErrClosed)
		}
		l.plat.mode = mode
		busy, err = l.tryLock()
		if err != nil {
			return false, err
		}
		if !busy {
			l.held = true
		}
		return busy, nil
	})
}

func (l *advisoryLock) Unlock() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return fmt.Errorf("advisory lock: %w", os.ErrClosed)
	}
	if !l.held {
		return nil
	}
	if err := l.unlock(); err != nil {
		return fmt.Errorf("unlock advisory lock: %w", err)
	}
	l.held = false
	return nil
}

func (l *advisoryLock) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	var errs []error
	if l.held {
		if err := l.unlock(); err != nil {
			errs = append(errs, err)
		}
		l.held = false
	}
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			errs = append(errs, err)
		}
		l.file = nil
	}
	return errors.Join(errs...)
}

func waitLock(ctx context.Context, tryLock func() (busy bool, err error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		busy, err := tryLock()
		if err != nil {
			return err
		}
		if !busy {
			return nil
		}
		timer := newLockTimer(lockRetryTick)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C():
			timer.Stop()
		}
	}
}
