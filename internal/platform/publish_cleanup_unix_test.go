//go:build unix

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPublishWorkspace_UnixTrustedStickyParentCleans(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("trusted sticky cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, w.name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace remains: %v", err)
	}
}

func TestPublishWorkspace_UnixUntrustedOwnerStickyLeavesEmptyOrphan(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated chown")
	}
	parent := t.TempDir()
	if err := os.Chown(parent, 4242, 4242); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := w.CreateTemp("payload-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	fd := w.plat.fd
	if err := w.Close(); err == nil {
		t.Fatal("untrusted parent owner cleanup accepted")
	}
	entries, err := os.ReadDir(filepath.Join(parent, w.name))
	if err != nil || len(entries) != 0 {
		t.Fatalf("orphan contents=%v error=%v", entries, err)
	}
	if err := unix.Fstat(fd, &unix.Stat_t{}); !errors.Is(err, unix.EBADF) {
		t.Fatalf("workspace fd not closed: %v", err)
	}
}

func TestPublishWorkspace_UnixPermissionsRecheckedAtCleanup(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d.Path(), 0o770); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err == nil {
		t.Fatal("widened parent permissions accepted")
	}
	if _, err := os.Stat(filepath.Join(d.Path(), w.name)); err != nil {
		t.Fatal(err)
	}
}

func TestPublishWorkspace_UnixACLRecheckedBeforeDirectoryRemoval(t *testing.T) {
	for _, queryErr := range []error{nil, unix.EIO} {
		t.Run(fmt.Sprint(queryErr), func(t *testing.T) {
			d, err := OpenPublishDir(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			w, err := d.CreateWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			f, _, err := w.CreateTemp("payload-*")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			originalACL, originalUnlink := publishUnixACLBoundaryFn, publishUnixCleanupUnlinkFn
			defer func() { publishUnixACLBoundaryFn, publishUnixCleanupUnlinkFn = originalACL, originalUnlink }()
			publishUnixACLBoundaryFn = func(int) (bool, error) { return false, queryErr }
			publishUnixCleanupUnlinkFn = func(int, string, int) error { t.Error("unsafe namespace reached directory unlink"); return nil }
			if err := w.Close(); err == nil {
				t.Fatal("ACL change/query failure accepted")
			}
			entries, err := os.ReadDir(filepath.Join(d.Path(), w.name))
			if err != nil || len(entries) != 0 {
				t.Fatalf("orphan contents=%v error=%v", entries, err)
			}
		})
	}
}

func TestPublishWorkspace_UnixUntrustedParentReplacementNeverUnlinksDirectory(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d.Path(), 0o777); err != nil {
		t.Fatal(err)
	}
	originalCheckpoint, originalUnlink := publishWorkspaceCleanupCheckpoint, publishUnixCleanupUnlinkFn
	defer func() {
		publishWorkspaceCleanupCheckpoint, publishUnixCleanupUnlinkFn = originalCheckpoint, originalUnlink
	}()
	publishWorkspaceCleanupCheckpoint = func() { replaceWorkspaceEntry(t, w, d.Path(), filepath.Join(d.Path(), "moved")) }
	publishUnixCleanupUnlinkFn = func(int, string, int) error { t.Error("unsafe namespace reached directory unlink"); return nil }
	if err := w.Close(); err == nil {
		t.Fatal("expected cleanup warning")
	}
	for _, name := range []string{w.name, "moved"} {
		if _, err := os.Stat(filepath.Join(d.Path(), name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublishWorkspace_UnixReadAndCloseFailuresPreserved(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	fd := w.plat.fd
	originalRead, originalClose := publishUnixCleanupReadFn, publishUnixCleanupCloseFn
	defer func() { publishUnixCleanupReadFn, publishUnixCleanupCloseFn = originalRead, originalClose }()
	readErr, closeErr := errors.New("injected read failure"), errors.New("injected close failure")
	publishUnixCleanupReadFn = func(int) ([]string, error) { return nil, readErr }
	publishUnixCleanupCloseFn = func(fd int) error { return errors.Join(originalClose(fd), closeErr) }
	err = w.Close()
	if !errors.Is(err, readErr) || !errors.Is(err, closeErr) {
		t.Fatalf("cleanup errors lost: %v", err)
	}
	if err := unix.Fstat(fd, &unix.Stat_t{}); !errors.Is(err, unix.EBADF) {
		t.Fatalf("workspace fd still open: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestPublishWorkspace_UnixOwnerChangeRechecked(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated chown")
	}
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Fchown(d.plat.fd, 4242, 4242); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err == nil {
		t.Fatal("untrusted new owner accepted")
	}
	if _, err := os.Stat(filepath.Join(d.Path(), w.name)); err != nil {
		t.Fatal(err)
	}
}
