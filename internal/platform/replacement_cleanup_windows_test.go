//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCleanupReplacedBinary_RetriesLockedFileAndPreservesOtherEntries(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "mihari.exe")
	old := binary + ".old-1234567890"
	for _, path := range []string{binary, old, binary + ".old-1234567891", binary + ".old-not-a-timestamp", binary + ".old-01", binary + ".old-+1", binary + ".old-0", binary + ".old--1", filepath.Join(dir, "other.exe.old-1234567890")} {
		if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	directory := binary + ".old-1234567892"
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(old)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
	}()
	if err := CleanupReplacedBinary(t.Context(), binary); err == nil {
		t.Fatal("locked old binary did not report a cleanup warning")
	}
	if _, err := os.Stat(binary + ".old-1234567891"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unlocked sibling retained: %v", err)
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	handle = 0
	if err := CleanupReplacedBinary(t.Context(), binary); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retry retained old binary: %v", err)
	}
	for _, path := range []string{binary, binary + ".old-not-a-timestamp", binary + ".old-01", binary + ".old-+1", binary + ".old-0", binary + ".old--1", filepath.Join(dir, "other.exe.old-1234567890"), directory} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unrelated entry removed: %s: %v", path, err)
		}
	}
}

func TestCleanupReplacedBinary_PreservesLinkedOldFiles(t *testing.T) {
	for _, symbolic := range []bool{false, true} {
		t.Run(map[bool]string{false: "hardlink", true: "symlink"}[symbolic], func(t *testing.T) {
			root := t.TempDir()
			binary, outside := filepath.Join(root, "mihari.exe"), filepath.Join(t.TempDir(), "important")
			for _, path := range []string{binary, outside} {
				if err := os.WriteFile(path, []byte("keep"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			old := binary + ".old-123"
			link := os.Link
			if symbolic {
				link = os.Symlink
			}
			if err := link(outside, old); err != nil {
				if symbolic {
					t.Skipf("symlink unavailable: %v", err)
				}
				t.Fatal(err)
			}
			_ = CleanupReplacedBinary(t.Context(), binary)
			for _, path := range []string{binary, outside, old} {
				if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
					t.Fatalf("changed %s: %q %v", path, data, err)
				}
			}
		})
	}
}

func TestCleanupReplacedBinary_MissingTargetPreservesRollback(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mihari.exe")
	old := binary + ".old-1234567890"
	if err := os.WriteFile(old, []byte("rollback"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := CleanupReplacedBinary(t.Context(), binary); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("rollback lost: %v", err)
	}
}
