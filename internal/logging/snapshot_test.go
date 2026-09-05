package logging

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestSnapshotSource_OrdersOnlyBaseAndSingleDigitArchives(t *testing.T) {
	fs, paths := openExportTestFS(t)
	base := paths.TUILog
	for _, name := range []string{
		filepath.Base(base), filepath.Base(base) + ".1", filepath.Base(base) + ".2", filepath.Base(base) + ".9",
		filepath.Base(base) + ".0", filepath.Base(base) + ".10", filepath.Base(base) + ".01", filepath.Base(base) + ".x",
		filepath.Base(base) + "-sibling",
	} {
		writeSnapshotFixture(t, fs, filepath.Join(paths.LogDir, name), name)
	}
	if err := os.Mkdir(filepath.Join(paths.LogDir, "unrelated"), 0o700); err != nil {
		t.Fatal(err)
	}

	entries, err := fs.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	wantIdentity := make(map[string]platform.FileIdentity)
	for _, entry := range entries {
		wantIdentity[entry.Name] = entry.Identity
	}
	var opened []string
	handles, err := snapshotSource(context.Background(), fs, base, func(string) func() { return func() {} }, platform.OpenAdvisoryLock,
		func(fs *platform.PrivateFS, path string, identity platform.FileIdentity) (*os.File, error) {
			name := filepath.Base(path)
			if identity != wantIdentity[name] {
				t.Fatalf("identity for %q was not preserved from ReadDir", name)
			}
			opened = append(opened, name)
			return platform.OpenSnapshot(fs, path, identity)
		})
	if err != nil {
		t.Fatalf("snapshotSource: %v", err)
	}
	defer func() { _ = closeSnapshots(handles) }()
	want := []string{filepath.Base(base) + ".9", filepath.Base(base) + ".2", filepath.Base(base) + ".1", filepath.Base(base)}
	if !reflect.DeepEqual(opened, want) {
		t.Fatalf("opened=%v want=%v", opened, want)
	}
	for i, handle := range handles {
		if handle.name != want[i] || handle.size != int64(len(want[i])) {
			t.Fatalf("handle[%d]=%+v want name=%q size=%d", i, handle, want[i], len(want[i]))
		}
		body, readErr := io.ReadAll(io.LimitReader(handle.file, handle.size))
		if readErr != nil || string(body) != want[i] {
			t.Fatalf("read %q: body=%q err=%v", handle.name, body, readErr)
		}
	}
}

func TestSnapshotSource_EmptySourceIsValid(t *testing.T) {
	fs, paths := openExportTestFS(t)
	handles, err := snapshotSource(context.Background(), fs, paths.DaemonLog, nil, nil, nil)
	if err != nil || len(handles) != 0 {
		t.Fatalf("handles=%v err=%v, want empty success", handles, err)
	}
}

func TestSnapshotSource_RejectsRecognizedNonRegularEntry(t *testing.T) {
	fs, paths := openExportTestFS(t)
	for _, name := range []string{filepath.Base(paths.TUILog), filepath.Base(paths.TUILog) + ".1"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(paths.LogDir, name)
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			_, err := snapshotSource(context.Background(), fs, paths.TUILog, nil, nil, nil)
			if err == nil {
				t.Fatal("expected non-regular matching entry to fail")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSnapshotSource_RejectsRecognizedSymlink(t *testing.T) {
	fs, paths := openExportTestFS(t)
	target := filepath.Join(t.TempDir(), "target.log")
	if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths.TUILog); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := snapshotSource(context.Background(), fs, paths.TUILog, nil, nil, nil); err == nil {
		t.Fatal("expected matching symlink to fail")
	}
}

func TestSnapshotSource_EntersMutexBeforeOpeningSharedLock(t *testing.T) {
	fs, paths := openExportTestFS(t)
	var events []string
	lock := &snapshotTestLock{events: &events}
	handles, err := snapshotSource(context.Background(), fs, paths.TUILog,
		func(path string) func() {
			if path != paths.TUILog {
				t.Fatalf("mutex path=%q want=%q", path, paths.TUILog)
			}
			events = append(events, "mutex-enter")
			return func() { events = append(events, "mutex-leave") }
		},
		func(_ *platform.PrivateFS, path string) (platform.AdvisoryLock, error) {
			if path != paths.TUILog+".lock" {
				t.Fatalf("lock path=%q", path)
			}
			events = append(events, "lock-open")
			return lock, nil
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := closeSnapshots(handles); err != nil {
		t.Fatal(err)
	}
	want := []string{"mutex-enter", "lock-open", "lock-shared", "lock-unlock", "lock-close", "mutex-leave"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
}

func TestSnapshotSource_ClosesEarlierHandlesWhenLaterIdentityChanges(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeSnapshotFixture(t, fs, paths.TUILog+".1", "archive")
	writeSnapshotFixture(t, fs, paths.TUILog, "base")
	var first *os.File
	openCount := 0
	_, err := snapshotSource(context.Background(), fs, paths.TUILog, nil, nil,
		func(fs *platform.PrivateFS, path string, identity platform.FileIdentity) (*os.File, error) {
			openCount++
			if openCount == 2 {
				if err := fs.Rename(path, paths.DaemonLog); err != nil {
					t.Fatal(err)
				}
				writeSnapshotFixture(t, fs, path, "replacement")
			}
			f, err := platform.OpenSnapshot(fs, path, identity)
			if openCount == 1 {
				first = f
			}
			return f, err
		})
	if !errors.Is(err, platform.ErrIdentityMismatch) {
		t.Fatalf("error=%v want identity mismatch", err)
	}
	if first == nil {
		t.Fatal("first handle was not opened")
	}
	if _, statErr := first.Stat(); statErr == nil {
		t.Fatal("earlier snapshot handle remained open")
	}
}

func TestSnapshotSource_BaseDisappearsAfterEnumeration(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeSnapshotFixture(t, fs, paths.TUILog, "base")
	_, err := snapshotSource(context.Background(), fs, paths.TUILog, nil, nil,
		func(fs *platform.PrivateFS, path string, identity platform.FileIdentity) (*os.File, error) {
			if removeErr := fs.Remove(path); removeErr != nil {
				t.Fatal(removeErr)
			}
			return platform.OpenSnapshot(fs, path, identity)
		})
	if err == nil {
		t.Fatal("expected vanished base to fail")
	}
}

func TestSnapshotSource_CancelWhileWaitingForSharedLock(t *testing.T) {
	fs, paths := openExportTestFS(t)
	held, err := platform.OpenAdvisoryLock(fs, paths.TUILog+".lock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if err := held.Lock(context.Background(), platform.LockExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = snapshotSource(ctx, fs, paths.TUILog, nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context canceled", err)
	}
}

func TestCloseSnapshots_ClosesEveryHandle(t *testing.T) {
	var handles []snapshotHandle
	for range 2 {
		f, err := os.CreateTemp(t.TempDir(), "snapshot-*")
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, snapshotHandle{file: f})
	}
	if err := closeSnapshots(handles); err != nil {
		t.Fatal(err)
	}
	for _, handle := range handles {
		if _, err := handle.file.Stat(); err == nil {
			t.Fatal("snapshot handle remained open")
		}
	}
}

func TestSnapshotSource_SizeBoundsReadsAfterPathReplacement(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeSnapshotFixture(t, fs, paths.TUILog, "{\"record\":1}\n")
	handles, err := snapshotSource(context.Background(), fs, paths.TUILog, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeSnapshots(handles) }()
	if err := fs.Rename(paths.TUILog, paths.TUILog+".1"); err != nil {
		t.Fatal(err)
	}
	writeSnapshotFixture(t, fs, paths.TUILog+".1", "{\"record\":2}\n")
	if err := fs.Remove(paths.TUILog + ".1"); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(io.LimitReader(handles[0].file, handles[0].size))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "{\"record\":1}\n" || int64(len(body)) != handles[0].size {
		t.Fatalf("body=%q len=%d fixed-size=%d", body, len(body), handles[0].size)
	}
}

func writeSnapshotFixture(t *testing.T, fs *platform.PrivateFS, path, body string) {
	t.Helper()
	f, err := fs.OpenAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

type snapshotTestLock struct {
	events *[]string
}

func (l *snapshotTestLock) Lock(_ context.Context, mode platform.LockMode) error {
	if mode != platform.LockShared {
		return errors.New("snapshot did not request shared lock")
	}
	*l.events = append(*l.events, "lock-shared")
	return nil
}

func (l *snapshotTestLock) Unlock() error {
	*l.events = append(*l.events, "lock-unlock")
	return nil
}

func (l *snapshotTestLock) Close() error {
	*l.events = append(*l.events, "lock-close")
	return nil
}
