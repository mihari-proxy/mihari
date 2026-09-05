//go:build unix

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPublishWorkspace_InitiallyUntrustedParentRemainsDegraded(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishDir(t, d)
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err == nil {
		t.Fatal("initially untrusted namespace was allowed directory-entry deletion")
	}
	entries, err := os.ReadDir(filepath.Join(parent, w.name))
	if err != nil || len(entries) != 0 {
		t.Fatalf("expected empty private orphan: entries=%d err=%v", len(entries), err)
	}
}

func TestPublishDir_VerificationFailurePreservesStatAndCloseCauses(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "stat failure", true: "identity mismatch"}[mismatch], func(t *testing.T) {
			d, err := OpenPublishDir(privatePublishParent(t))
			if err != nil {
				t.Fatal(err)
			}
			cleanupPublishDir(t, d)
			w, err := d.CreateWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			cleanupPublishWorkspace(t, w)
			file, name, err := w.CreateTemp("unpublished-*")
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			originalStat, originalClose := publishUnixVerifyStatFn, publishUnixVerifyCloseFn
			t.Cleanup(func() { publishUnixVerifyStatFn, publishUnixVerifyCloseFn = originalStat, originalClose })
			statErr, closeErr := errors.New("injected stat cause"), errors.New("injected close cause")
			verifiedFD := -1
			publishUnixVerifyStatFn = func(fd int, st *unix.Stat_t) error {
				verifiedFD = fd
				if err := originalStat(fd, st); err != nil {
					return err
				}
				if mismatch {
					st.Ino++
					return nil
				}
				return statErr
			}
			publishUnixVerifyCloseFn = func(fd int) error { return errors.Join(originalClose(fd), closeErr) }
			err = d.PublishNoReplace(w, name, "result.zip", nil)
			if !errors.Is(err, ErrPublishDirectoryChanged) || !errors.Is(err, closeErr) || (!mismatch && !errors.Is(err, statErr)) {
				t.Fatalf("directory verification lost classification or causes: %v", err)
			}
			if !strings.Contains(err.Error(), "close publish directory verification") || (!mismatch && !strings.Contains(err.Error(), "stat publish directory verification")) {
				t.Fatalf("verification lacks operation context: %v", err)
			}
			if err := unix.Fstat(verifiedFD, &unix.Stat_t{}); !errors.Is(err, unix.EBADF) {
				t.Fatalf("verification handle not closed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(d.Path(), "result.zip")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed verification published target: %v", err)
			}
		})
	}
}

func TestPublishDir_VerificationOpenPreservesNonMissingCause(t *testing.T) {
	parent := privatePublishParent(t)
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishDir(t, d)
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishWorkspace(t, w)
	// A file in place of a directory produces ENOTDIR without root-dependent
	// permission behavior. No source is opened or published on this path.
	d.path = filepath.Join(parent, "file")
	if err := os.WriteFile(d.path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err = d.PublishNoReplace(w, "unused", "result.zip", nil)
	if !errors.Is(err, unix.ENOTDIR) {
		t.Fatalf("verification lost underlying error: %v", err)
	}
}
