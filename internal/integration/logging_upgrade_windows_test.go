//go:build windows

package integration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"golang.org/x/sys/windows"
)

// Windows CI creates the real legacy Administrators-owned fixture. No service,
// real user directory, installed binary or machine-wide permission is modified.
func TestLoggingUpgrade_LegacyACLToOrdinaryReadAndContinuedRotation(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("Windows upgrade fixture requires the elevated CI runner")
		}
		t.Skip("legacy ACL fixture requires elevation; exercised by Windows unit/race CI")
	}
	paths := platform.NewPaths(filepath.Join(t.TempDir(), "data"))
	serviceFS, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serviceFS.Close() })
	cfg := logging.DefaultConfig()
	cfg.MaxSizeBytes = 48
	var writers []*logging.RotatingWriter
	for _, base := range []string{paths.DaemonLog, paths.MihomoLog} {
		w, e := logging.OpenRotatingWriter(context.Background(), logging.RotatorOptions{BasePath: base, PrivateFS: serviceFS, Config: cfg})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = w.Close() })
		writers = append(writers, w)
		if _, e := w.Write([]byte("before-upgrade\n")); e != nil {
			t.Fatal(e)
		}
	}
	if err := os.WriteFile(paths.TUILog, []byte("old-tui\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{paths.DaemonLog, paths.TUILog, paths.MihomoLog} {
		if err := os.WriteFile(base+".1", []byte("retained-history\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(paths.Root, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, admins, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		setUpgradeACL(t, filepath.Join(paths.LogDir, entry.Name()), false)
	}
	setUpgradeACL(t, paths.LogDir, true)
	setUpgradeACL(t, paths.Root, true)
	reader := upgradeReaderToken(t)
	if _, err := upgradeRead(reader, paths.DaemonLog); !os.IsPermission(err) {
		t.Fatalf("legacy ACL did not deny ordinary read: %v", err)
	}
	// Updated TUI starts after the service, using a separate root capability.
	tuiFS, err := platform.NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tuiFS.Close() })
	tuiWriter, err := logging.OpenRotatingWriter(context.Background(), logging.RotatorOptions{BasePath: paths.TUILog, PrivateFS: tuiFS, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tuiWriter.Close() })
	for _, entry := range entries {
		if _, err := upgradeRead(reader, filepath.Join(paths.LogDir, entry.Name())); err != nil {
			t.Fatalf("startup missed %s: %v", entry.Name(), err)
		}
	}
	for _, w := range append(writers, tuiWriter) {
		if _, err := w.Write([]byte(strings.Repeat("after-upgrade", 5) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	// Continued service writes rotated the pre-upgrade active logs. Both active
	// files, retained history and lock files remain readable without admin rights.
	entries, err = os.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, err := upgradeRead(reader, filepath.Join(paths.LogDir, entry.Name())); err != nil {
			t.Fatalf("post-rotation read %s: %v", entry.Name(), err)
		}
	}
	for _, base := range []string{paths.DaemonLog, paths.MihomoLog} {
		got, err := upgradeRead(reader, base+".1")
		if err != nil || string(got) != "before-upgrade\n" {
			t.Fatalf("lost pre-upgrade content: %q %v", got, err)
		}
	}
}

func setUpgradeACL(t *testing.T, path string, dir bool) {
	t.Helper()
	inherit := ""
	if dir {
		inherit = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;BA)(A;" + inherit + ";FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func upgradeReaderToken(t *testing.T) windows.Token {
	t.Helper()
	var source windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY, &source); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	disable := windows.SIDAndAttributes{Sid: admins}
	var restricted windows.Token
	proc := windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")
	ok, _, err := proc.Call(uintptr(source), 1, 1, uintptr(unsafe.Pointer(&disable)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	runtime.KeepAlive(disable)
	if ok == 0 {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restricted.Close() })
	var reader windows.Token
	if err := windows.DuplicateTokenEx(restricted, windows.TOKEN_IMPERSONATE|windows.TOKEN_QUERY, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &reader); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

func upgradeRead(token windows.Token, path string) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.SetThreadToken(nil, token); err != nil {
		return nil, err
	}
	defer func() {
		if err := windows.RevertToSelf(); err != nil {
			panic(err)
		}
	}()
	return os.ReadFile(path)
}
