//go:build windows

package platform

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPublishWorkspace_WindowsTempHardeningRetainsCleanupCauses(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishDir(t, d)
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishWorkspace(t, w)
	originalHarden, originalDelete, originalClose := publishWindowsHardenTempFn, publishWindowsDeleteCreatedFn, publishWindowsCleanupCloseFn
	t.Cleanup(func() {
		publishWindowsHardenTempFn, publishWindowsDeleteCreatedFn, publishWindowsCleanupCloseFn = originalHarden, originalDelete, originalClose
	})
	hardenErr, deleteErr, closeErr := errors.New("hardening failed"), errors.New("deletion failed"), errors.New("close failed")
	publishWindowsHardenTempFn = func(windows.Handle, string) error { return hardenErr }
	publishWindowsDeleteCreatedFn = func(h windows.Handle) error { return errors.Join(originalDelete(h), deleteErr) }
	publishWindowsCleanupCloseFn = func(h windows.Handle) error { return errors.Join(originalClose(h), closeErr) }
	_, _, err = w.CreateTemp("hardening-*")
	if !errors.Is(err, hardenErr) || !errors.Is(err, deleteErr) || !errors.Is(err, closeErr) {
		t.Fatalf("temp hardening lost primary/cleanup causes: %v", err)
	}
}

func TestPublishWorkspace_WindowsRemoveRetainsCloseFailure(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishDir(t, d)
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishWorkspace(t, w)
	f, name, err := w.CreateTemp("remove-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	originalClose := publishWindowsCleanupCloseFn
	t.Cleanup(func() { publishWindowsCleanupCloseFn = originalClose })
	closeErr := errors.New("close failed")
	publishWindowsCleanupCloseFn = func(h windows.Handle) error { return errors.Join(originalClose(h), closeErr) }
	if err := w.Remove(name); !errors.Is(err, closeErr) {
		t.Fatalf("remove hid close failure: %v", err)
	}
}
