package platform

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenSnapshot_AllowsAppendRenameAndDeleteThroughOtherHandles(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "first\n"); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := OpenSnapshot(fs, paths.DaemonLog, identityOf(t, entries, filepath.Base(paths.DaemonLog)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := snapshot.Close(); err != nil {
			t.Errorf("close snapshot: %v", err)
		}
	})

	appendHandle, err := fs.OpenAppend(paths.DaemonLog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appendHandle.Write([]byte("second\n")); err != nil {
		t.Fatal(err)
	}
	if err := appendHandle.Close(); err != nil {
		t.Fatal(err)
	}
	archive := paths.DaemonLog + ".1"
	if err := fs.Rename(paths.DaemonLog, archive); err != nil {
		t.Fatal(err)
	}
	if err := fs.Remove(archive); err != nil {
		t.Fatal(err)
	}

	got, err := io.ReadAll(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first\nsecond\n" {
		t.Fatalf("snapshot=%q", got)
	}
}

func TestOpenSnapshot_RejectsChangedIdentityAndSymlink(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "original"); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	expected := identityOf(t, entries, filepath.Base(paths.DaemonLog))
	if err := fs.Rename(paths.DaemonLog, paths.TUILog); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "replacement"); err != nil {
		t.Fatal(err)
	}
	if f, err := OpenSnapshot(fs, paths.DaemonLog, expected); !errors.Is(err, ErrIdentityMismatch) {
		if f != nil {
			_ = f.Close()
		}
		t.Fatalf("identity error=%v want ErrIdentityMismatch", err)
	}
	if err := fs.Remove(paths.DaemonLog); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(paths.TUILog, paths.DaemonLog); err != nil {
		if runtime.GOOS == "windows" || errors.Is(err, os.ErrPermission) {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if f, err := OpenSnapshot(fs, paths.DaemonLog, expected); err == nil {
		_ = f.Close()
		t.Fatal("expected symlink snapshot to fail closed")
	}
}
