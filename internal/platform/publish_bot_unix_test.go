//go:build unix

package platform

import (
	"errors"
	"os"
	"path/filepath"
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

func TestPublishDir_VerificationOpenPreservesNonMissingCause(t *testing.T) {
	parent := t.TempDir()
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
