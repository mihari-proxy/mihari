package logging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// testAfterExclusiveLock is invoked after the exclusive advisory lock is held
// and before directory enumeration. Tests use it for deterministic pauses.
var testAfterExclusiveLock func()

// testBeforeRemove is invoked after the exclusive lock is held and immediately
// before archive Remove. Tests use it to prove maintenance ignores cancel.
var testBeforeRemove func()

// RotatorOptions opens a process-safe rotating JSONL writer.
type RotatorOptions struct {
	BasePath  string
	Config    Config
	PrivateFS *platform.PrivateFS
	OpenLock  func(*platform.PrivateFS, string) (platform.AdvisoryLock, error)
	WriteWait time.Duration
	Reporter  FailureReporter
}

// RotatingWriter appends full JSONL records with overflow-safe rotation.
type RotatingWriter struct {
	mu        sync.Mutex
	cfg       atomic.Pointer[Config]
	dropped   atomic.Uint64
	fs        *platform.PrivateFS
	basePath  string
	lock      platform.AdvisoryLock
	file      *os.File
	writeWait time.Duration
	reporter  FailureReporter
	closed    bool
}

// OpenRotatingWriter creates the lock, converges archives, and creates the base file.
// The initial lock wait uses the earlier of the caller deadline and the hard
// write-wait bound. On failure, acquired resources are closed.
func OpenRotatingWriter(ctx context.Context, opts RotatorOptions) (*RotatingWriter, error) {
	if opts.PrivateFS == nil {
		return nil, fmt.Errorf("rotator private fs is required")
	}
	if opts.BasePath == "" {
		return nil, fmt.Errorf("rotator base path is required")
	}
	if opts.OpenLock == nil {
		opts.OpenLock = platform.OpenAdvisoryLock
	}
	if opts.WriteWait <= 0 {
		opts.WriteWait = writeWaitBound
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := opts.PrivateFS.EnsureDir(filepath.Dir(opts.BasePath)); err != nil {
		return nil, err
	}
	lock, err := opts.OpenLock(opts.PrivateFS, opts.BasePath+".lock")
	if err != nil {
		return nil, err
	}
	w := &RotatingWriter{
		fs:        opts.PrivateFS,
		basePath:  opts.BasePath,
		lock:      lock,
		writeWait: opts.WriteWait,
		reporter:  opts.Reporter,
	}
	cfg := opts.Config
	w.cfg.Store(&cfg)
	lockCtx, cancelLock := context.WithTimeout(ctx, writeWaitBound)
	err = w.lock.Lock(lockCtx, platform.LockExclusive)
	cancelLock()
	if err != nil {
		_ = lock.Close()
		w.lock = nil
		return nil, err
	}
	if hook := testAfterExclusiveLock; hook != nil {
		hook()
	}
	err = w.convergeLocked()
	if err == nil {
		var f *os.File
		f, err = w.fs.OpenAppend(w.basePath)
		if err == nil {
			err = f.Close()
		}
	}
	unlockErr := w.lock.Unlock()
	if err != nil || unlockErr != nil {
		_ = lock.Close()
		w.lock = nil
		return nil, errors.Join(err, unlockErr)
	}
	return w, nil
}

// Write appends one complete JSONL record. It takes the process mutex, then an
// exclusive advisory lock (WriteWait, default 250ms). After the lock it opens
// and Stats the base file. A lock wait failure drops the whole record.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	ctx, cancel := context.WithTimeout(context.Background(), w.writeWait)
	defer cancel()
	if err := w.lock.Lock(ctx, platform.LockExclusive); err != nil {
		w.dropped.Add(1)
		w.report(FailureDropped, errors.New("lock wait exceeded"))
		return 0, err
	}
	defer w.lock.Unlock()
	if hook := testAfterExclusiveLock; hook != nil {
		hook()
	}
	if err := w.openBaseLocked(); err != nil {
		w.report(FailureWrite, err)
		return 0, err
	}
	st, err := w.file.Stat()
	if err != nil {
		_ = w.closeFile()
		w.report(FailureWrite, err)
		return 0, err
	}
	cfg := w.config()
	incoming := int64(len(p))
	if needRotate(st.Size(), incoming, cfg.MaxSizeBytes) {
		if err := w.rotateLocked(); err != nil {
			w.report(FailureRotate, err)
			if w.file == nil {
				if openErr := w.openBaseLocked(); openErr != nil {
					w.report(FailureWrite, openErr)
					return 0, openErr
				}
			}
		}
	}
	n, err := w.file.Write(p)
	closeErr := w.closeFile()
	if err != nil {
		w.report(FailureWrite, err)
		return n, err
	}
	return n, closeErr
}

// Apply atomically stores cfg, then waits min(ctx deadline, WriteWait) for the
// exclusive lock and converges archives. Lock timeout or ctx cancel skip
// maintenance; the swapped config is kept. After the lock is held, ReadDir and
// Remove are not canceled.
func (w *RotatingWriter) Apply(ctx context.Context, cfg Config) {
	w.swapConfig(cfg)
	w.convergeArchives(ctx)
}

func (w *RotatingWriter) swapConfig(cfg Config) {
	copied := cfg
	w.cfg.Store(&copied)
}

func (w *RotatingWriter) convergeArchives(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.lock == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lockCtx, cancel := context.WithTimeout(ctx, w.writeWait)
	defer cancel()
	if err := w.lock.Lock(lockCtx, platform.LockExclusive); err != nil {
		w.report(FailureCleanup, errors.New("lock wait exceeded"))
		return
	}
	defer w.lock.Unlock()
	if hook := testAfterExclusiveLock; hook != nil {
		hook()
	}
	if err := w.convergeLocked(); err != nil {
		w.report(FailureCleanup, err)
	}
}

// Dropped returns the number of records dropped because the write lock wait failed.
func (w *RotatingWriter) Dropped() uint64 {
	return w.dropped.Load()
}

// Close closes the current write handle and lock. It does not close PrivateFS.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	err := w.closeFile()
	if w.lock != nil {
		err = errors.Join(err, w.lock.Close())
		w.lock = nil
	}
	return err
}

func (w *RotatingWriter) config() Config {
	if p := w.cfg.Load(); p != nil {
		return *p
	}
	return DefaultConfig()
}

func (w *RotatingWriter) report(class FailureClass, err error) {
	if w.reporter == nil || err == nil {
		return
	}
	w.reporter.Report(class, err)
}

func (w *RotatingWriter) openBaseLocked() error {
	if err := w.closeFile(); err != nil {
		return err
	}
	f, err := w.fs.OpenAppend(w.basePath)
	if err != nil {
		return err
	}
	w.file = f
	return nil
}

func (w *RotatingWriter) closeFile() error {
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingWriter) rotateLocked() error {
	if err := w.closeFile(); err != nil {
		return err
	}
	if err := w.convergeLocked(); err != nil {
		return err
	}
	cfg := w.config()
	if cfg.MaxFiles <= 1 {
		if err := w.fs.ReplaceEmpty(w.basePath); err != nil {
			return err
		}
	} else if err := w.shiftLocked(cfg.MaxFiles); err != nil {
		return err
	}
	return w.openBaseLocked()
}

func (w *RotatingWriter) convergeLocked() error {
	dir := filepath.Dir(w.basePath)
	base := filepath.Base(w.basePath)
	entries, err := w.fs.ReadDir(dir)
	if err != nil {
		return err
	}
	maxFiles := w.config().MaxFiles
	if maxFiles < 1 {
		maxFiles = 1
	}
	for _, entry := range entries {
		n, ok := archiveSuffix(base, entry.Name)
		if !ok || n < maxFiles {
			continue
		}
		if !entry.Mode.IsRegular() {
			w.report(FailureCleanup, fmt.Errorf("skip non-regular archive suffix %d", n))
			continue
		}
		if hook := testBeforeRemove; hook != nil {
			hook()
		}
		if err := w.fs.Remove(w.child(entry.Name)); err != nil {
			w.report(FailureCleanup, err)
		}
	}
	return nil
}

func (w *RotatingWriter) shiftLocked(maxFiles int) error {
	listing, err := w.listLocked()
	if err != nil {
		return err
	}
	base := filepath.Base(w.basePath)
	oldest := maxFiles - 1
	if oldest >= 1 {
		if err := w.removeIfRegular(listing, archiveName(base, oldest)); err != nil {
			return err
		}
	}
	for i := oldest - 1; i >= 1; i-- {
		if err := w.renameIfRegular(listing, archiveName(base, i), archiveName(base, i+1)); err != nil {
			return err
		}
	}
	return w.renameIfRegular(listing, base, archiveName(base, 1))
}

func (w *RotatingWriter) listLocked() (map[string]platform.FileEntry, error) {
	entries, err := w.fs.ReadDir(filepath.Dir(w.basePath))
	if err != nil {
		return nil, err
	}
	out := make(map[string]platform.FileEntry, len(entries))
	for _, entry := range entries {
		out[entry.Name] = entry
	}
	return out, nil
}

func (w *RotatingWriter) removeIfRegular(listing map[string]platform.FileEntry, name string) error {
	entry, ok := listing[name]
	if !ok {
		return nil
	}
	if !entry.Mode.IsRegular() {
		w.report(FailureRotate, fmt.Errorf("skip non-regular %s", name))
		return nil
	}
	if err := w.fs.Remove(w.child(name)); err != nil {
		return err
	}
	delete(listing, name)
	return nil
}

func (w *RotatingWriter) renameIfRegular(listing map[string]platform.FileEntry, oldName, newName string) error {
	src, ok := listing[oldName]
	if !ok {
		return nil
	}
	if !src.Mode.IsRegular() {
		w.report(FailureRotate, fmt.Errorf("skip non-regular %s", oldName))
		return nil
	}
	if dst, exists := listing[newName]; exists {
		if !dst.Mode.IsRegular() {
			w.report(FailureRotate, fmt.Errorf("skip rename over non-regular %s", newName))
			return nil
		}
		if err := w.fs.Remove(w.child(newName)); err != nil {
			return err
		}
		delete(listing, newName)
	}
	if err := w.fs.Rename(w.child(oldName), w.child(newName)); err != nil {
		return err
	}
	delete(listing, oldName)
	src.Name = newName
	listing[newName] = src
	return nil
}

func (w *RotatingWriter) child(name string) string {
	return filepath.Join(filepath.Dir(w.basePath), name)
}

func archiveName(base string, n int) string {
	return base + "." + strconv.Itoa(n)
}

func archiveSuffix(base, name string) (int, bool) {
	prefix := base + "."
	if !strings.HasPrefix(name, prefix) {
		return 0, false
	}
	suffix := name[len(prefix):]
	if suffix == "" || suffix[0] < '1' || suffix[0] > '9' {
		return 0, false
	}
	n, err := strconv.Atoi(suffix)
	if err != nil || n < 1 || strconv.Itoa(n) != suffix {
		return 0, false
	}
	return n, true
}

func needRotate(currentSize, incoming, maxSize int64) bool {
	return incoming > maxSize || (incoming <= maxSize && currentSize > maxSize-incoming)
}
