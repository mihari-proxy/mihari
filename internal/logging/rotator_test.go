package logging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestRotatingWriter_DefaultAndBootstrapConfig(t *testing.T) {
	got := DefaultConfig()
	if got.Level != slog.LevelInfo || got.MaxSizeBytes != 10<<20 || got.MaxFiles != 3 {
		t.Fatalf("DefaultConfig() = %+v", got)
	}
	got = BootstrapConfig()
	if got.Level != slog.LevelDebug || got.MaxSizeBytes != 100<<20 || got.MaxFiles != 10 {
		t.Fatalf("BootstrapConfig() = %+v", got)
	}
}

func TestRotatingWriter_RotatesWholeRecordBeforeExceedingLimit(t *testing.T) {
	w, fs, paths := openTestRotator(t, Config{Level: slog.LevelInfo, MaxSizeBytes: 20, MaxFiles: 3})
	rec1 := []byte(`{"msg":"one"}\n`)
	rec2 := []byte(`{"msg":"two"}\n`)
	if int64(len(rec1)) > 20 || int64(len(rec1)+len(rec2)) <= 20 {
		t.Fatalf("fixture sizes rec1=%d rec2=%d", len(rec1), len(rec2))
	}
	if _, err := w.Write(rec1); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(rec2); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	base := readLogFile(t, paths.TUILog)
	if string(base) != string(rec2) {
		t.Fatalf("active file = %q, want %q", base, rec2)
	}
	archived := readLogFile(t, paths.TUILog+".1")
	if string(archived) != string(rec1) {
		t.Fatalf(".1 = %q, want %q", archived, rec1)
	}
	assertLogNames(t, fs, paths.LogDir, filepath.Base(paths.TUILog), filepath.Base(paths.TUILog)+".1", filepath.Base(paths.TUILog)+".lock")
}

func TestRotatingWriter_OpenConvergesArchivesWithoutRewritingActive(t *testing.T) {
	fs, paths := openTestLogFS(t)
	active := []byte("ACTIVE-CONTENT\n")
	mustWriteFile(t, fs, paths.TUILog, active)
	for i := 1; i <= 9; i++ {
		mustWriteFile(t, fs, fmt.Sprintf("%s.%d", paths.TUILog, i), []byte(fmt.Sprintf("old-%d\n", i)))
	}

	w := openRotatorAt(t, fs, paths.TUILog, Config{Level: slog.LevelInfo, MaxSizeBytes: int64(len(active)), MaxFiles: 3})
	assertLogNames(t, fs, paths.LogDir,
		filepath.Base(paths.TUILog),
		filepath.Base(paths.TUILog)+".1",
		filepath.Base(paths.TUILog)+".2",
		filepath.Base(paths.TUILog)+".lock",
	)
	if got := readLogFile(t, paths.TUILog); !bytes.Equal(got, active) {
		t.Fatalf("Open rewrote active file: %q", got)
	}
	if got := readLogFile(t, paths.TUILog+".1"); string(got) != "old-1\n" {
		t.Fatalf(".1 rewritten: %q", got)
	}

	rec := []byte(`{"msg":"next"}\n`)
	if _, err := w.Write(rec); err != nil {
		t.Fatal(err)
	}
	assertLogNames(t, fs, paths.LogDir,
		filepath.Base(paths.TUILog),
		filepath.Base(paths.TUILog)+".1",
		filepath.Base(paths.TUILog)+".2",
		filepath.Base(paths.TUILog)+".lock",
	)
	if got := readLogFile(t, paths.TUILog); !bytes.Equal(got, rec) {
		t.Fatalf("base after rotate = %q", got)
	}
	if got := readLogFile(t, paths.TUILog+".1"); !bytes.Equal(got, active) {
		t.Fatalf(".1 after rotate = %q, want active snapshot", got)
	}
	if got := readLogFile(t, paths.TUILog+".2"); string(got) != "old-1\n" {
		t.Fatalf(".2 after rotate = %q", got)
	}
}

func TestRotatingWriter_MaxFilesOneRotateReplacesInode(t *testing.T) {
	fs, paths := openTestLogFS(t)
	oldBody := []byte("OLD-INODE\n")
	mustWriteFile(t, fs, paths.TUILog, oldBody)
	mustWriteFile(t, fs, paths.TUILog+".1", []byte("archive-1\n"))
	mustWriteFile(t, fs, paths.TUILog+".2", []byte("archive-2\n"))

	id := identityOf(t, fs, paths.LogDir, filepath.Base(paths.TUILog))
	old, err := fs.OpenReadChecked(paths.TUILog, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })

	w := openRotatorAt(t, fs, paths.TUILog, Config{Level: slog.LevelInfo, MaxSizeBytes: int64(len(oldBody)), MaxFiles: 1})
	if _, err := os.Stat(paths.TUILog + ".1"); !os.IsNotExist(err) {
		t.Fatalf("Open with max-files=1 left archive: %v", err)
	}
	if got := readLogFile(t, paths.TUILog); !bytes.Equal(got, oldBody) {
		t.Fatalf("Open replaced active inode: %q", got)
	}

	rec := []byte(`{"msg":"fresh"}\n`)
	if _, err := w.Write(rec); err != nil {
		t.Fatal(err)
	}
	gotOld, err := io.ReadAll(old)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotOld, oldBody) {
		t.Fatalf("old handle contents = %q, want %q", gotOld, oldBody)
	}
	if got := readLogFile(t, paths.TUILog); !bytes.Equal(got, rec) {
		t.Fatalf("new base = %q, want %q", got, rec)
	}
	for _, name := range []string{paths.TUILog + ".1", paths.TUILog + ".2"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("archive %s still present: %v", name, err)
		}
	}
	newID := identityOf(t, fs, paths.LogDir, filepath.Base(paths.TUILog))
	if newID == id {
		t.Fatal("max-files=1 rotate did not replace the active inode")
	}
}

func TestRotatingWriter_ApplyShrinkDeletesArchivesOnly(t *testing.T) {
	fs, paths := openTestLogFS(t)
	active := []byte("keep-active\n")
	mustWriteFile(t, fs, paths.TUILog, active)
	mustWriteFile(t, fs, paths.TUILog+".1", []byte("a1\n"))
	mustWriteFile(t, fs, paths.TUILog+".2", []byte("a2\n"))
	id := identityOf(t, fs, paths.LogDir, filepath.Base(paths.TUILog))

	w := openRotatorAt(t, fs, paths.TUILog, Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3})
	w.Apply(context.Background(), Config{Level: slog.LevelWarn, MaxSizeBytes: 512, MaxFiles: 1})

	if got := readLogFile(t, paths.TUILog); !bytes.Equal(got, active) {
		t.Fatalf("Apply emptied or rewrote active file: %q", got)
	}
	if identityOf(t, fs, paths.LogDir, filepath.Base(paths.TUILog)) != id {
		t.Fatal("Apply shrink replaced the active inode")
	}
	for _, name := range []string{paths.TUILog + ".1", paths.TUILog + ".2"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("archive %s survived Apply shrink: %v", name, err)
		}
	}
}

func TestRotatingWriter_SkipsNonRegularAndIllegalSuffix(t *testing.T) {
	fs, paths := openTestLogFS(t)
	mustWriteFile(t, fs, paths.TUILog, []byte("base\n"))
	mustWriteFile(t, fs, paths.TUILog+".2", []byte("keep-regular-2\n"))
	mustWriteFile(t, fs, paths.TUILog+".7", []byte("delete-me\n"))
	mustWriteFile(t, fs, paths.TUILog+".bad", []byte("illegal\n"))
	mustWriteFile(t, fs, filepath.Join(paths.LogDir, "other.log.3"), []byte("other\n"))
	dirName := paths.TUILog + ".1"
	if err := os.Mkdir(dirName, 0o700); err != nil {
		t.Fatal(err)
	}
	linkName := paths.TUILog + ".4"
	linkOK := os.Symlink(paths.TUILog+".bad", linkName) == nil

	w := openRotatorAt(t, fs, paths.TUILog, Config{Level: slog.LevelInfo, MaxSizeBytes: 8, MaxFiles: 3})
	if _, err := os.Stat(paths.TUILog + ".7"); !os.IsNotExist(err) {
		t.Fatalf("regular suffix 7 should be deleted: %v", err)
	}
	if _, err := os.Stat(dirName); err != nil {
		t.Fatalf("directory suffix should be skipped: %v", err)
	}
	if _, err := os.Stat(paths.TUILog + ".bad"); err != nil {
		t.Fatalf("illegal suffix should be skipped: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.LogDir, "other.log.3")); err != nil {
		t.Fatalf("foreign basename should be skipped: %v", err)
	}
	if linkOK {
		if _, err := os.Lstat(linkName); err != nil {
			t.Fatalf("symlink suffix should be skipped: %v", err)
		}
	}
	if got := readLogFile(t, paths.TUILog); string(got) != "base\n" {
		t.Fatalf("active rewritten: %q", got)
	}

	if _, err := w.Write([]byte(`{"x":1}\n`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirName); err != nil {
		t.Fatalf("rotate followed directory: %v", err)
	}
	if linkOK {
		if _, err := os.Lstat(linkName); err != nil {
			t.Fatalf("rotate followed symlink: %v", err)
		}
	}
}

func TestRotatingWriter_OverflowSafeDecision(t *testing.T) {
	max := int64(math.MaxInt64)
	tests := []struct {
		current, incoming, maxSize int64
		want                       bool
	}{
		{0, 10, 10, false},
		{10, 1, 10, true},
		{5, 5, 10, false},
		{6, 5, 10, true},
		{0, 11, 10, true},
		{max, 1, max, true},
		{max, max, max, true},
		{0, max, max, false},
		{1, max, max, true},
		{max - 1, 2, max, true},
		{max - 1, 1, max, false},
		{max, 0, max, false},
	}
	for _, tc := range tests {
		got := needRotate(tc.current, tc.incoming, tc.maxSize)
		if got != tc.want {
			t.Fatalf("needRotate(%d,%d,%d)=%v want %v", tc.current, tc.incoming, tc.maxSize, got, tc.want)
		}
		if tc.incoming <= tc.maxSize {
			// The unsafe form current+incoming overflows for MaxInt64 cases.
			_ = tc.current + tc.incoming
		}
	}
}

func TestRotatingWriter_FailureStormRateLimitedRedacted(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	secret := "super-secret-token-value"
	redactor := NewRedactor(secret)
	reporter := NewFailureReporter(&buf, redactor, func() time.Time { return now })

	fullPath := `C:\Users\Kinema\Documents\mihari\logs\mihari-tui.log`
	err := fmt.Errorf("write %s token=%s", fullPath, secret)
	for i := 0; i < 50; i++ {
		reporter.Report(FailureWrite, err)
		reporter.Report(FailureRotate, err)
		reporter.Report(FailureDropped, err)
		reporter.Report(FailureCleanup, err)
	}
	first := buf.String()
	for _, class := range []FailureClass{FailureWrite, FailureRotate, FailureDropped, FailureCleanup} {
		token := "logging: " + string(class)
		if strings.Count(first, token) != 1 {
			t.Fatalf("class %s count=%d in %q", class, strings.Count(first, token), first)
		}
	}
	if strings.Contains(first, secret) {
		t.Fatalf("secret leaked: %q", first)
	}
	if strings.Contains(first, "***") == false {
		t.Fatalf("expected redacted secret in %q", first)
	}
	if strings.Contains(first, fullPath) || strings.Contains(first, `C:\Users`) {
		t.Fatalf("full path leaked: %q", first)
	}

	now = now.Add(2 * time.Second)
	reporter.Report(FailureWrite, err)
	if strings.Count(buf.String(), "logging: "+string(FailureWrite)) != 2 {
		t.Fatalf("rate limit did not lift: %q", buf.String())
	}
}

func TestRotatingWriter_WriteDropsWhenLockTimesOut(t *testing.T) {
	w, _, _ := openTestRotator(t, Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3})
	w.writeWait = 20 * time.Millisecond
	if err := w.lock.Close(); err != nil {
		t.Fatal(err)
	}
	w.lock = neverLock{}
	n, err := w.Write([]byte(`{"msg":"drop-me"}\n`))
	if n != 0 {
		t.Fatalf("n=%d, want 0", n)
	}
	if err == nil {
		t.Fatal("expected drop error")
	}
	if w.Dropped() != 1 {
		t.Fatalf("Dropped()=%d, want 1", w.Dropped())
	}
}

func TestRotatingWriter_WriteReturnsUnlockFailure(t *testing.T) {
	w, _, _ := openTestRotator(t, Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3})
	if err := w.lock.Close(); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("release test lock")
	w.lock = &unlockErrorLock{err: wantErr}
	record := []byte("{\"msg\":\"written\"}\n")

	n, err := w.Write(record)
	if n != len(record) {
		t.Fatalf("Write n=%d, want %d", n, len(record))
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Write error=%v, want unlock error", err)
	}
}

func TestRotatingWriter_OpenCancelClosesResources(t *testing.T) {
	fs, paths := openTestLogFS(t)
	var spy *closeSpyLock
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := OpenRotatingWriter(ctx, RotatorOptions{
		BasePath:  paths.TUILog,
		Config:    Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3},
		PrivateFS: fs,
		OpenLock: func(fs *platform.PrivateFS, path string) (platform.AdvisoryLock, error) {
			inner, err := platform.OpenAdvisoryLock(fs, path)
			if err != nil {
				return nil, err
			}
			spy = &closeSpyLock{AdvisoryLock: inner}
			return spy, nil
		},
		WriteWait: 20 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected Open to fail on canceled context")
	}
	if spy == nil {
		t.Fatal("OpenLock was not called")
	}
	if !spy.closed.Load() {
		t.Fatal("canceled Open left lock open")
	}
}

func TestRotatingWriter_OpenBackgroundContextHasHardLockCap(t *testing.T) {
	fs, paths := openTestLogFS(t)
	held, err := platform.OpenAdvisoryLock(fs, paths.TUILog+".lock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if err := held.Lock(context.Background(), platform.LockExclusive); err != nil {
		t.Fatal(err)
	}

	type openResult struct {
		writer  *RotatingWriter
		err     error
		elapsed time.Duration
	}
	result := make(chan openResult, 1)
	started := time.Now()
	go func() {
		writer, openErr := OpenRotatingWriter(context.Background(), RotatorOptions{
			BasePath:  paths.TUILog,
			Config:    Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3},
			PrivateFS: fs,
			WriteWait: 5 * time.Second,
		})
		result <- openResult{writer: writer, err: openErr, elapsed: time.Since(started)}
	}()

	select {
	case got := <-result:
		if got.writer != nil {
			_ = got.writer.Close()
		}
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("Open error=%v, want deadline exceeded", got.err)
		}
		if got.elapsed < 150*time.Millisecond || got.elapsed > 1500*time.Millisecond {
			t.Fatalf("Open elapsed=%v, want approximately the 250ms hard cap", got.elapsed)
		}
	case <-time.After(2 * time.Second):
		if err := held.Unlock(); err != nil {
			t.Fatal(err)
		}
		got := <-result
		if got.writer != nil {
			_ = got.writer.Close()
		}
		t.Fatalf("Open remained blocked past the 250ms hard cap; returned after %v with error %v", got.elapsed, got.err)
	}
}

func TestRotatingWriter_OpenCancelsLockContextBeforeMaintenance(t *testing.T) {
	fs, paths := openTestLogFS(t)
	lock := &contextCaptureLock{}
	lockContextCanceled := false
	maintenanceStarted := false
	t.Cleanup(func() { testAfterExclusiveLock = nil })
	testAfterExclusiveLock = func() {
		maintenanceStarted = true
		select {
		case <-lock.lockContext.Done():
			lockContextCanceled = true
		default:
		}
	}

	w, err := OpenRotatingWriter(context.Background(), RotatorOptions{
		BasePath:  paths.TUILog,
		Config:    Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3},
		PrivateFS: fs,
		OpenLock: func(*platform.PrivateFS, string) (platform.AdvisoryLock, error) {
			return lock, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if !maintenanceStarted {
		t.Fatal("Open did not start post-lock maintenance")
	}
	if !lockContextCanceled {
		t.Fatal("Open kept the lock-acquisition context alive during local maintenance")
	}
	if _, err := os.Stat(paths.TUILog); err != nil {
		t.Fatalf("post-lock maintenance did not create base log: %v", err)
	}
}

func TestRotatingWriter_ApplySwapsConfigBeforeLockWait(t *testing.T) {
	w, _, paths := openTestRotator(t, Config{Level: slog.LevelInfo, MaxSizeBytes: 1000, MaxFiles: 3})
	rec := bytes.Repeat([]byte("a"), 40)
	rec = append(rec, '\n')
	if _, err := w.Write(rec); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Apply(ctx, Config{Level: slog.LevelError, MaxSizeBytes: 20, MaxFiles: 3})
	if _, err := w.Write(rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.TUILog + ".1"); err != nil {
		t.Fatalf("expected rotate under swapped max-size: %v", err)
	}
}

func TestRotatingWriter_ApplyCleanupIgnoresCancelAfterLock(t *testing.T) {
	fs, paths := openTestLogFS(t)
	mustWriteFile(t, fs, paths.TUILog, []byte("active\n"))
	mustWriteFile(t, fs, paths.TUILog+".1", []byte("archive\n"))
	w := openRotatorAt(t, fs, paths.TUILog, Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { testAfterExclusiveLock = nil })
	testAfterExclusiveLock = func() { cancel() }
	w.Apply(ctx, Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 1})
	if _, err := os.Stat(paths.TUILog + ".1"); !os.IsNotExist(err) {
		t.Fatalf("cleanup should finish after cancel: %v", err)
	}
	if got := readLogFile(t, paths.TUILog); string(got) != "active\n" {
		t.Fatalf("active rewritten during Apply: %q", got)
	}
}

func TestRotatingWriter_ApplyReportsUnlockFailure(t *testing.T) {
	w, _, _ := openTestRotator(t, Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3})
	if err := w.lock.Close(); err != nil {
		t.Fatal(err)
	}
	w.lock = &unlockErrorLock{err: errors.New("release test lock")}
	var output bytes.Buffer
	w.reporter = NewFailureReporter(&output, nil, func() time.Time {
		return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	})

	w.Apply(context.Background(), Config{Level: slog.LevelInfo, MaxSizeBytes: 1024, MaxFiles: 3})
	if got := output.String(); !strings.Contains(got, "logging: cleanup: release test lock") {
		t.Fatalf("failure report=%q, want unlock failure", got)
	}
}

func openTestRotator(t *testing.T, cfg Config) (*RotatingWriter, *platform.PrivateFS, platform.Paths) {
	t.Helper()
	fs, paths := openTestLogFS(t)
	return openRotatorAt(t, fs, paths.TUILog, cfg), fs, paths
}

func openTestLogFS(t *testing.T) (*platform.PrivateFS, platform.Paths) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	fs, err := platform.NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	paths := platform.NewPaths(root)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	return fs, paths
}

func openRotatorAt(t *testing.T, fs *platform.PrivateFS, base string, cfg Config) *RotatingWriter {
	t.Helper()
	w, err := OpenRotatingWriter(context.Background(), RotatorOptions{
		BasePath:  base,
		Config:    cfg,
		PrivateFS: fs,
		WriteWait: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func mustWriteFile(t *testing.T, fs *platform.PrivateFS, path string, body []byte) {
	t.Helper()
	f, err := fs.OpenAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func readLogFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func assertLogNames(t *testing.T, fs *platform.PrivateFS, dir string, want ...string) {
	t.Helper()
	entries, err := fs.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		got[e.Name] = struct{}{}
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Fatalf("missing %s in %v", name, got)
		}
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, name := range want {
		wantSet[name] = struct{}{}
	}
	for name := range got {
		if _, ok := wantSet[name]; !ok {
			t.Fatalf("unexpected %s in %v", name, got)
		}
	}
}

func identityOf(t *testing.T, fs *platform.PrivateFS, dir, name string) platform.FileIdentity {
	t.Helper()
	entries, err := fs.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == name {
			return e.Identity
		}
	}
	t.Fatalf("missing %s", name)
	return platform.FileIdentity{}
}

type neverLock struct{}

func (neverLock) Lock(ctx context.Context, _ platform.LockMode) error {
	<-ctx.Done()
	return ctx.Err()
}
func (neverLock) Unlock() error { return nil }
func (neverLock) Close() error  { return nil }

type closeSpyLock struct {
	platform.AdvisoryLock
	closed atomic.Bool
}

type contextCaptureLock struct {
	lockContext context.Context
}

type unlockErrorLock struct {
	err error
}

func (*unlockErrorLock) Lock(context.Context, platform.LockMode) error { return nil }
func (l *unlockErrorLock) Unlock() error                               { return l.err }
func (*unlockErrorLock) Close() error                                  { return nil }

func (l *contextCaptureLock) Lock(ctx context.Context, _ platform.LockMode) error {
	l.lockContext = ctx
	return nil
}

func (*contextCaptureLock) Unlock() error { return nil }
func (*contextCaptureLock) Close() error  { return nil }

func (s *closeSpyLock) Close() error {
	s.closed.Store(true)
	if s.AdvisoryLock == nil {
		return nil
	}
	return s.AdvisoryLock.Close()
}
