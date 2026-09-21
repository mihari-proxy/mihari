//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenanceWindowsTrust_ServiceUsesExistingPrivatePrincipal(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, extra string
		want        bool
	}{
		{"inherited user", "", true},
		{"broad writable", "(A;;FA;;;WD)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, e := windows.SecurityDescriptorFromString(fmt.Sprintf("O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;ID;FA;;;%s)%s", user.String(), tc.extra))
			if e != nil {
				t.Fatal(e)
			}
			if got := maintenanceWindowsTrust(sd, system); got != tc.want {
				t.Fatalf("trusted=%v want %v", got, tc.want)
			}
		})
	}
}

func cleanupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	h, err := openNTPath(dir, windows.WRITE_DAC|windows.READ_CONTROL|windows.SYNCHRONIZE, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = hardenHandle(h, principalSDDL(user, true))
	closeErr := windows.CloseHandle(h)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return dir
}

// TestCleanupBinaryStashes_RetriesAfterMappedImageExit verifies the original Windows failure and next-start recovery.
func TestCleanupBinaryStashes_RetriesAfterMappedImageExit(t *testing.T) {
	dir := cleanupTestDir(t)
	target := filepath.Join(dir, "mihari.exe")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = issue282CopyExecutable(executable, target); err != nil {
		t.Fatal(err)
	}
	child := issue282StartCopiedChild(t, target, "stubborn")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = child.waitReady(ctx); err != nil {
		t.Fatal(err)
	}
	stash := target + ".old-1"
	if err = os.Rename(target, stash); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(target, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = CleanupBinaryStashes(ctx, target); err == nil {
		t.Fatal("mapped image deletion was reported successful")
	}
	if _, err = os.Stat(stash); err != nil {
		t.Fatal(err)
	}
	if err = child.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.wait() // Forced fixture exit is intentional; the OS handle is now reaped.
	if err = CleanupBinaryStashes(ctx, target); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(stash); !os.IsNotExist(err) {
		t.Fatalf("old image remains after exit: %v", err)
	}
}

// TestCleanupBinaryStashes_RespectsUpdateLock protects the stash while replacement can still roll back.
func TestCleanupBinaryStashes_RespectsUpdateLock(t *testing.T) {
	dir := cleanupTestDir(t)
	target := filepath.Join(dir, "mihari.exe")
	stash := target + ".old-1"
	if err := os.WriteFile(stash, []byte("rollback"), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := LockBinaryMaintenance(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err = CleanupBinaryStashes(ctx, target); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup ignored update lock: %v", err)
	}
	if _, err = os.Stat(stash); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(dir, dir+"-moved"); err == nil {
		t.Fatal("retained directory was movable")
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = CleanupBinaryStashes(context.Background(), target); err != nil {
		t.Fatal(err)
	}
}

// TestCleanupBinaryStashes_RejectsSymlinks keeps link targets outside the cleanup namespace untouched.
func TestCleanupBinaryStashes_RejectsSymlinks(t *testing.T) {
	dir := cleanupTestDir(t)
	target := filepath.Join(dir, "mihari.exe")
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target+".old-1"); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := CleanupBinaryStashes(context.Background(), target); err == nil {
		t.Fatal("symlink accepted")
	}
	if body, err := os.ReadFile(outside); err != nil || string(body) != "keep" {
		t.Fatalf("outside file changed: %q %v", body, err)
	}
}

// TestCleanupBinaryStashes_ExactNames protects unrelated files while collecting multiple generations.
func TestCleanupBinaryStashes_ExactNames(t *testing.T) {
	dir := cleanupTestDir(t)
	target := filepath.Join(dir, "mihari.exe")
	keep := []string{"mihari.exe", "mihari.exe.old-0", "mihari.exe.old-01", "mihari.exe.old--1", "mihari.exe.old-1.bak", "other.exe.old-1", "mihari.exe.old-9223372036854775808"}
	remove := []string{"mihari.exe.old-1", "mihari.exe.old-123456789", "MIHARI.EXE.old-123"}
	for _, name := range append(append([]string{}, keep...), remove...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := CleanupBinaryStashes(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("preserve %s: %v", name, err)
		}
	}
	for _, name := range remove {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("obsolete %s remains: %v", name, err)
		}
	}
}

// TestCleanupBinaryStashes_RejectsHardlinks prevents treating a second name as disposable residue.
func TestCleanupBinaryStashes_RejectsHardlinks(t *testing.T) {
	dir := cleanupTestDir(t)
	target := filepath.Join(dir, "mihari.exe")
	if err := os.WriteFile(target, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	stash := target + ".old-1"
	if err := os.Link(target, stash); err != nil {
		t.Fatal(err)
	}
	if err := CleanupBinaryStashes(context.Background(), target); err == nil {
		t.Fatal("hardlink accepted")
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "current" {
		t.Fatalf("current modified: %q %v", body, err)
	}
	if _, err := os.Stat(stash); err != nil {
		t.Fatal(err)
	}
}
