package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAdvisoryLock_ExclusiveMutex(t *testing.T) {
	fs, path, a := openTestAdvisoryLock(t)
	b := openAdvisoryLockAt(t, fs, path)

	if err := a.Lock(context.Background(), LockExclusive); err != nil {
		t.Fatal(err)
	}

	waitForWaiter, tick := installLockTicks(t)
	acquired := make(chan error, 1)
	go func() {
		acquired <- b.Lock(context.Background(), LockExclusive)
	}()
	waitForWaiter(t, acquired)

	if err := a.Unlock(); err != nil {
		t.Fatal(err)
	}
	tick()
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
}

func TestAdvisoryLock_SharedCoexist(t *testing.T) {
	fs, path, a := openTestAdvisoryLock(t)
	b := openAdvisoryLockAt(t, fs, path)

	if err := a.Lock(context.Background(), LockShared); err != nil {
		t.Fatal(err)
	}
	if err := b.Lock(context.Background(), LockShared); err != nil {
		t.Fatal(err)
	}

	c := openAdvisoryLockAt(t, fs, path)
	waitForWaiter, tick := installLockTicks(t)
	acquired := make(chan error, 1)
	go func() {
		acquired <- c.Lock(context.Background(), LockExclusive)
	}()
	waitForWaiter(t, acquired)

	if err := a.Unlock(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-acquired:
		t.Fatalf("exclusive lock acquired with remaining shared holder: %v", err)
	default:
	}
	if err := b.Unlock(); err != nil {
		t.Fatal(err)
	}
	tick()
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
}

func TestAdvisoryLock_ContextCanceled(t *testing.T) {
	fs, path, a := openTestAdvisoryLock(t)
	b := openAdvisoryLockAt(t, fs, path)

	if err := a.Lock(context.Background(), LockExclusive); err != nil {
		t.Fatal(err)
	}

	waitForWaiter, _ := installLockTicks(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- b.Lock(ctx, LockExclusive)
	}()
	waitForWaiter(t, done)
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Lock cancel: %v", err)
	}
}

func TestAdvisoryLock_ContextDeadlineExceeded(t *testing.T) {
	_, _, lock := openTestAdvisoryLock(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := lock.Lock(ctx, LockExclusive)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Lock deadline: %v", err)
	}
}

func TestAdvisoryLock_UnlockThenReacquire(t *testing.T) {
	fs, path, lock := openTestAdvisoryLock(t)
	if err := lock.Lock(context.Background(), LockExclusive); err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	assertLockFileExists(t, fs, path)
	if err := lock.Lock(context.Background(), LockExclusive); err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	assertLockFileExists(t, fs, path)
}

func TestAdvisoryLock_CloseReleases(t *testing.T) {
	fs, path, a := openTestAdvisoryLock(t)
	if err := a.Lock(context.Background(), LockExclusive); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Lock(context.Background(), LockExclusive); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Lock after Close: %v", err)
	}
	if err := a.Unlock(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Unlock after Close: %v", err)
	}
	assertLockFileExists(t, fs, path)

	b := openAdvisoryLockAt(t, fs, path)
	if err := b.Lock(context.Background(), LockExclusive); err != nil {
		t.Fatal(err)
	}
}

func TestAdvisoryLock_ExistingFileIsNotHeld(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(paths.LogDir, "mihari-tui.log.lock")
	f, err := fs.OpenAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	assertLockFileExists(t, fs, path)

	lock := openAdvisoryLockAt(t, fs, path)
	if err := lock.Lock(context.Background(), LockExclusive); err != nil {
		t.Fatal(err)
	}
}

func openTestAdvisoryLock(t *testing.T) (*PrivateFS, string, AdvisoryLock) {
	t.Helper()
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(paths.LogDir, "mihari-tui.log.lock")
	return fs, path, openAdvisoryLockAt(t, fs, path)
}

func openAdvisoryLockAt(t *testing.T, fs *PrivateFS, path string) AdvisoryLock {
	t.Helper()
	lock, err := OpenAdvisoryLock(fs, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	return lock
}

func assertLockFileExists(t *testing.T, fs *PrivateFS, path string) {
	t.Helper()
	entries, err := fs.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if !containsFile(entries, filepath.Base(path)) {
		t.Fatalf("lock file missing after release: %s", filepath.Base(path))
	}
}

type chanLockTimer struct {
	c <-chan time.Time
}

func (t chanLockTimer) C() <-chan time.Time { return t.c }
func (t chanLockTimer) Stop() bool          { return true }

func installLockTicks(t *testing.T) (waitForWaiter func(*testing.T, <-chan error), tick func()) {
	t.Helper()
	waiting := make(chan struct{}, 16)
	ticks := make(chan time.Time)
	prev := newLockTimer
	t.Cleanup(func() { newLockTimer = prev })
	newLockTimer = func(time.Duration) lockTimer {
		select {
		case waiting <- struct{}{}:
		default:
		}
		return chanLockTimer{c: ticks}
	}
	waitForWaiter = func(t *testing.T, acquired <-chan error) {
		t.Helper()
		select {
		case <-waiting:
		case err := <-acquired:
			t.Fatalf("lock acquired while it should block: %v", err)
		}
	}
	return waitForWaiter, func() { ticks <- time.Time{} }
}
