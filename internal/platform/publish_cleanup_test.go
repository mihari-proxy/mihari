package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishWorkspace_CloseCleansHeldContents(t *testing.T) {
	d, err := OpenPublishDir(privatePublishParent(t))
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishDir(t, d)
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := w.CreateTemp("sensitive-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("private log payload"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d.Path(), w.name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace remains: %v", err)
	}
}

func TestPublishWorkspace_ContentRemovalFailureStillCleansOtherFiles(t *testing.T) {
	d, err := OpenPublishDir(privatePublishParent(t))
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishDir(t, d)
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(d.Path(), w.name)
	// Unexpected directories are never recursively removed. One failed entry
	// must not prevent cleanup of a later regular temp or closing the capability.
	if err := os.Mkdir(filepath.Join(path, "a-unsupported-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	f, name, err := w.CreateTemp("z-payload-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err == nil {
		t.Fatal("missing incomplete-cleanup warning")
	}
	if _, err := os.Stat(filepath.Join(path, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp not cleaned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "a-unsupported-directory")); err != nil {
		t.Fatalf("unexpected directory touched: %v", err)
	}
	if err := w.Remove(name); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("capability not closed: %v", err)
	}
}
