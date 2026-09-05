//go:build windows

package platform

import (
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPrivateFS_CachedServicePrincipalCannotUndoMigration(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "before\n"); err != nil {
		t.Fatal(err)
	}
	user := fs.plat.owner
	// Hold repair authority so the intentionally broken pre-fix run also cleans up.
	h, err := openNTPath(paths.DaemonLog, windows.WRITE_DAC, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hardenHandle(h, "D:P(A;;FA;;;"+user.String()+")(A;;FA;;;SY)"); _ = windows.CloseHandle(h) })
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	fs.plat.owner = admins // service resolved BA before the interactive root repair
	if err := writeLog(fs, paths.DaemonLog, "after\n"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(paths.DaemonLog)
	if err != nil || string(got) != "before\nafter\n" {
		t.Fatalf("service restored unreadable ACL after root migration: %q %v", got, err)
	}
	assertPrincipalSystemDACL(t, paths.DaemonLog, user, false)
}

func TestPrivateFS_MigrationRestoresReadForNonAdminToken(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "existing log\n"); err != nil {
		t.Fatal(err)
	}
	h, err := openNTPath(paths.DaemonLog, windows.WRITE_DAC|windows.READ_CONTROL, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	user := fs.plat.owner
	t.Cleanup(func() { _ = hardenHandle(h, principalSDDL(user, false)); _ = windows.CloseHandle(h) })
	reader := nonAdminReaderToken(t)
	if err := hardenHandle(h, "D:P(A;;FA;;;BA)(A;;FA;;;SY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := readWithToken(reader, paths.DaemonLog); !os.IsPermission(err) {
		t.Fatalf("legacy ACL should deny ordinary reader, got %v", err)
	}
	if err := fs.hardenPrivateHandle(h, false); err != nil {
		t.Fatal(err)
	}
	if got, err := readWithToken(reader, paths.DaemonLog); err != nil || string(got) != "existing log\n" {
		t.Fatalf("ordinary read after migration=%q %v", got, err)
	}
	// A subsequent service-style write must preserve the same ordinary read.
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	fs.plat.owner = admins
	if err := writeLog(fs, paths.DaemonLog, "new log\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := readWithToken(reader, paths.DaemonLog); err != nil || string(got) != "existing log\nnew log\n" {
		t.Fatalf("ordinary read after subsequent write=%q %v", got, err)
	}
}

// Kept in this test package (and duplicated in integration) so native token
// helpers never become production platform APIs or introduce package cycles.
// This token cannot satisfy an Administrators allow ACE, even on elevated CI.
func nonAdminReaderToken(t *testing.T) windows.Token {
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
	ok, _, callErr := proc.Call(uintptr(source), 1, 1, uintptr(unsafe.Pointer(&disable)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	runtime.KeepAlive(disable)
	if ok == 0 {
		t.Fatalf("CreateRestrictedToken: %v", callErr)
	}
	t.Cleanup(func() { _ = restricted.Close() })
	var reader windows.Token
	if err := windows.DuplicateTokenEx(restricted, windows.TOKEN_IMPERSONATE|windows.TOKEN_QUERY, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &reader); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

func readWithToken(token windows.Token, path string) (data []byte, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.SetThreadToken(nil, token); err != nil {
		return nil, err
	}
	defer func() {
		if revertErr := windows.RevertToSelf(); revertErr != nil {
			panic(revertErr)
		}
	}()
	return os.ReadFile(path)
}

func TestPrivateFS_InFlightServiceACLRechecksAfterUserMigration(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "retained\n"); err != nil {
		t.Fatal(err)
	}
	user := fs.plat.owner
	h, err := openNTPath(paths.DaemonLog, windows.WRITE_DAC|windows.READ_CONTROL, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hardenHandle(h, principalSDDL(user, false)); _ = windows.CloseHandle(h) })
	oldQuery, oldHarden, oldSystem := privateRootSecurity, privateHardenHandle, processIsLocalSystem
	t.Cleanup(func() {
		privateRootSecurity, privateHardenHandle, processIsLocalSystem = oldQuery, oldHarden, oldSystem
	})
	processIsLocalSystem = func() (bool, error) { return true, nil }
	legacy, err := windows.SecurityDescriptorFromString("O:BAD:P(A;;FA;;;BA)(A;;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	migrated := false
	privateRootSecurity = func(handle windows.Handle) (*windows.SECURITY_DESCRIPTOR, error) {
		if !migrated {
			return legacy, nil
		}
		return oldQuery(handle)
	}
	applies := 0
	privateHardenHandle = func(handle windows.Handle, sddl string) error {
		applies++
		if err := oldHarden(handle, sddl); err != nil {
			return err
		}
		// The interactive process grants the root user while service applies BA.
		migrated = true
		return nil
	}
	if err := fs.hardenPrivateHandle(h, false); err != nil {
		t.Fatal(err)
	}
	if applies != 2 {
		t.Fatalf("ACL apply count=%d; migration was not rechecked", applies)
	}
	assertPrincipalSystemDACL(t, paths.DaemonLog, user, false)
	if got, err := os.ReadFile(paths.DaemonLog); err != nil || string(got) != "retained\n" {
		t.Fatal("migrated log not readable")
	}
}

func TestPrivateFS_UnresolvedSystemRootDoesNotEraseUserGrant(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	user := fs.plat.owner
	oldQuery, oldSystem := privateRootSecurity, processIsLocalSystem
	t.Cleanup(func() { privateRootSecurity, processIsLocalSystem = oldQuery, oldSystem })
	legacy, err := windows.SecurityDescriptorFromString("O:BAD:P(A;;FA;;;BA)(A;;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	privateRootSecurity = func(windows.Handle) (*windows.SECURITY_DESCRIPTOR, error) { return legacy, nil }
	processIsLocalSystem = func() (bool, error) { return true, nil }
	service, err := NewPrivateFS(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	assertPrincipalSystemDACL(t, paths.Root, user, true)
}
