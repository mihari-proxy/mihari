//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPublishWorkspace_WindowsGuardExistsDuringCreationInspection(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	original := publishWindowsCreatedHandleAttributesFn
	defer func() { publishWindowsCreatedHandleAttributesFn = original }()
	publishWindowsCreatedHandleAttributesFn = func(h windows.Handle) (uint32, error) {
		path, err := finalPathFromHandle(h)
		if err != nil {
			t.Fatal(err)
		}
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		external, err := windows.CreateFile(p, windows.DELETE, privateShare, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err == nil {
			if err := windows.CloseHandle(external); err != nil {
				t.Error(err)
			}
			t.Error("external DELETE handle accepted during creation inspection")
		}
		if err != nil && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			t.Errorf("external DELETE open: %v", err)
		}
		return original(h)
	}
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPublishWorkspace_WindowsExternalRenameDeleteBlocked(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	path := filepath.Join(d.Path(), w.name)
	if err := os.Rename(path, path+"-moved"); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Errorf("rename error=%v", err)
	}
	if err := os.Remove(path); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Errorf("delete error=%v", err)
	}
	f, name, err := w.CreateTemp("payload-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("payload"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.PublishNoReplace(w, name, "result.zip", func(err error) { t.Error(err) }); err != nil {
		t.Fatal(err)
	}
}

func TestPublishWorkspace_WindowsMovedBeforeValidationNeverSetsDisposition(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	// The trusted held handle can rename; it is not an external adversary.
	moved := moveWorkspaceOutside(t, w, d.Path(), t.TempDir())
	original := publishWindowsCleanupDispositionFn
	defer func() { publishWindowsCleanupDispositionFn = original }()
	publishWindowsCleanupDispositionFn = func(windows.Handle, bool) error {
		t.Error("validation failure reached disposition")
		return windows.ERROR_WRITE_FAULT
	}
	if err := w.Close(); err == nil {
		t.Fatal("expected moved-out warning")
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("moved directory removed: %v", err)
	}
}

func TestPublishWorkspace_WindowsReadAndCloseFailuresPreserved(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	h := w.plat.handle
	originalRead, originalClose := publishWindowsCleanupReadFn, publishWindowsCleanupCloseFn
	defer func() { publishWindowsCleanupReadFn, publishWindowsCleanupCloseFn = originalRead, originalClose }()
	readErr, closeErr := errors.New("injected read failure"), errors.New("injected close failure")
	publishWindowsCleanupReadFn = func(windows.Handle) ([]windowsDirent, error) { return nil, readErr }
	publishWindowsCleanupCloseFn = func(handle windows.Handle) error { return errors.Join(originalClose(handle), closeErr) }
	err = w.Close()
	if !errors.Is(err, readErr) || !errors.Is(err, closeErr) {
		t.Fatalf("cleanup errors lost: %v", err)
	}
	if _, err := identityFromHandle(h); err == nil {
		t.Fatal("workspace handle still open")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
