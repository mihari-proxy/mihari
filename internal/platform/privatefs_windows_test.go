//go:build windows

package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPrivateFS_WindowsProtectedDACL(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "x"); err != nil {
		t.Fatal(err)
	}
	assertOwnerSystemDACL(t, paths.Root, true)
	assertOwnerSystemDACL(t, paths.LogDir, true)
	assertOwnerSystemDACL(t, paths.DaemonLog, false)
}

func TestPrivateFS_WindowsChildFileDACL(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.TUILog, "child"); err != nil {
		t.Fatal(err)
	}
	assertOwnerSystemDACL(t, paths.TUILog, false)
}

func TestPrivateFS_WindowsTightenWideDACL(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0o777); err != nil {
		t.Fatal(err)
	}
	setWideDACL(t, root)
	fs, err := NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	assertOwnerSystemDACL(t, root, true)
	logs := filepath.Join(root, "logs")
	if err := os.Mkdir(logs, 0o777); err != nil {
		t.Fatal(err)
	}
	setWideDACL(t, logs)
	if err := fs.EnsureDir(logs); err != nil {
		t.Fatal(err)
	}
	assertOwnerSystemDACL(t, logs, true)
}

func TestPrivateFS_WindowsRefuseCreateRootAsLocalSystem(t *testing.T) {
	orig := processIsLocalSystem
	t.Cleanup(func() { processIsLocalSystem = orig })
	processIsLocalSystem = func() (bool, error) { return true, nil }
	root := filepath.Join(t.TempDir(), "missing")
	if _, err := NewPrivateFS(root); err == nil {
		t.Fatal("expected LocalSystem missing-data-root to fail closed")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("root created as LocalSystem: %v", err)
	}
}

func TestPrivateFS_WindowsDataRootJunctionFollowed(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(base, "link")
	createJunction(t, linkRoot, realRoot)
	fs, err := NewPrivateFS(linkRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	paths := NewPaths(linkRoot)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "via-junction"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(realRoot, "logs", filepath.Base(paths.DaemonLog)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "via-junction" {
		t.Fatalf("got %q", got)
	}
	id, err := fs.OpenDirIdentity(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = id.Close() })
	held, err := id.identity()
	if err != nil {
		t.Fatal(err)
	}
	want := fileIdentityOfPath(t, filepath.Join(realRoot, "logs"))
	if held != want {
		t.Fatalf("held identity %+v want %+v", held, want)
	}
}

func TestPrivateFS_WindowsLogDirJunctionFailClosed(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outside, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideDACL := daclString(t, outside)
	createJunction(t, paths.LogDir, outside)
	if err := fs.EnsureDir(paths.LogDir); err == nil {
		t.Fatal("expected EnsureDir on junction logs to fail")
	}
	if _, err := fs.OpenAppend(paths.DaemonLog); err == nil {
		t.Fatal("expected OpenAppend through junction logs to fail")
	}
	if _, err := fs.ReadDir(paths.LogDir); err == nil {
		t.Fatal("expected ReadDir through junction logs to fail")
	}
	if err := fs.Rename(paths.DaemonLog, paths.DaemonLog+".1"); err == nil {
		t.Fatal("expected Rename through junction logs to fail")
	}
	if err := fs.Remove(paths.DaemonLog); err == nil {
		t.Fatal("expected Remove through junction logs to fail")
	}
	if _, err := fs.OpenReadChecked(paths.DaemonLog, FileIdentity{}); err == nil {
		t.Fatal("expected OpenReadChecked through junction logs to fail")
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("outside marker mutated: %q", got)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "marker" {
		t.Fatalf("outside dir mutated: %v", entries)
	}
	if got := daclString(t, outside); got != outsideDACL {
		t.Fatalf("outside DACL changed:\n%s\n%s", outsideDACL, got)
	}
}

func TestPrivateFS_WindowsFileReparseDoesNotAppendYAML(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Settings, []byte("keep-yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(paths.Settings, paths.DaemonLog); err != nil {
		t.Skipf("file symlink unavailable: %v", err)
	}
	if _, err := fs.OpenAppend(paths.DaemonLog); err == nil {
		t.Fatal("expected OpenAppend of reparse to fail")
	}
	got, err := os.ReadFile(paths.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep-yaml" {
		t.Fatalf("yaml mutated: %q", got)
	}
}

func TestPrivateFS_WindowsCloseReleasesHandles(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	root := fs.plat.root
	logs := fs.plat.dirs[privateLogDirName]
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(root, &info); err == nil {
		t.Fatal("root handle still open after Close")
	}
	if err := windows.GetFileInformationByHandle(logs, &info); err == nil {
		t.Fatal("logs handle still open after Close")
	}
}

func TestPublishWorkspace_WindowsProtectedDACL(t *testing.T) {
	parent := t.TempDir()
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	workspacePath := filepath.Join(parent, w.name)
	assertOwnerSystemDACL(t, workspacePath, true)
	f, name, err := w.CreateTemp("private-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	assertOwnerSystemDACL(t, filepath.Join(workspacePath, name), false)
	if err := w.Remove(name); err != nil {
		t.Fatal(err)
	}
}

func TestPublishDir_WindowsPostPublishFailureIsWarning(t *testing.T) {
	parent := t.TempDir()
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	f, name, err := w.CreateTemp("warn-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("published")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	original := publishWindowsPostRenameFlushFn
	t.Cleanup(func() { publishWindowsPostRenameFlushFn = original })
	publishWindowsPostRenameFlushFn = func(windows.Handle) error { return windows.ERROR_WRITE_FAULT }
	var warnings []error
	err = d.PublishNoReplace(w, name, "result.zip", func(err error) { warnings = append(warnings, err) })
	publishWindowsPostRenameFlushFn = original
	if err != nil {
		t.Fatalf("published target reported failure: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected post-publish warning")
	}
	if got, err := os.ReadFile(filepath.Join(parent, "result.zip")); err != nil || string(got) != "published" {
		t.Fatalf("target=%q err=%v", got, err)
	}
}

func TestPublishWorkspace_WindowsReparseInspectionFailureCleansCreatedDirectory(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	inspectionErr := errors.New("injected attribute query failure")
	originalInspect := publishWindowsCreatedHandleAttributesFn
	t.Cleanup(func() { publishWindowsCreatedHandleAttributesFn = originalInspect })
	publishWindowsCreatedHandleAttributesFn = func(windows.Handle) (uint32, error) { return 0, inspectionErr }
	if w, err := d.CreateWorkspace(); !errors.Is(err, inspectionErr) {
		if w != nil {
			_ = w.Close()
		}
		t.Fatalf("CreateWorkspace error=%v want inspection error", err)
	}
	entries, err := os.ReadDir(d.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("created workspace was not cleaned: %v", entries)
	}
}

func TestPublishWorkspace_WindowsReparseInspectionFailureCleansCreatedTemp(t *testing.T) {
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
	inspectionErr := errors.New("injected attribute query failure")
	originalInspect := publishWindowsCreatedHandleAttributesFn
	t.Cleanup(func() { publishWindowsCreatedHandleAttributesFn = originalInspect })
	publishWindowsCreatedHandleAttributesFn = func(windows.Handle) (uint32, error) { return 0, inspectionErr }
	if f, _, err := w.CreateTemp("inspect-*"); !errors.Is(err, inspectionErr) {
		if f != nil {
			_ = f.Close()
		}
		t.Fatalf("CreateTemp error=%v want inspection error", err)
	}
	empty, err := windowsDirectoryEmpty(w.plat.handle)
	if err != nil {
		t.Fatal(err)
	}
	if !empty {
		t.Fatal("created temp was not cleaned")
	}
}

func TestPublishWorkspace_WindowsReparseInspectionFailureJoinsCleanupError(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	inspectionErr := errors.New("injected attribute query failure")
	cleanupErr := errors.New("injected cleanup failure")
	originalInspect := publishWindowsCreatedHandleAttributesFn
	originalDelete := publishWindowsDeleteCreatedFn
	t.Cleanup(func() {
		publishWindowsCreatedHandleAttributesFn = originalInspect
		publishWindowsDeleteCreatedFn = originalDelete
	})
	publishWindowsCreatedHandleAttributesFn = func(windows.Handle) (uint32, error) { return 0, inspectionErr }
	publishWindowsDeleteCreatedFn = func(windows.Handle) error { return cleanupErr }
	w, err := d.CreateWorkspace()
	if w != nil {
		_ = w.Close()
	}
	if !errors.Is(err, inspectionErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("CreateWorkspace error=%v want joined inspection and cleanup errors", err)
	}
}

func makeDirectoryLink(t *testing.T, link, target string) {
	t.Helper()
	createJunction(t, link, target)
}

func moveWorkspaceOutside(t *testing.T, w *PublishWorkspace, _, outside string) string {
	t.Helper()
	d, err := OpenPublishDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	const movedName = "moved-workspace"
	if err := renameHandle(w.plat.handle, d.plat.handle, movedName, windows.FILE_RENAME_POSIX_SEMANTICS); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(outside, movedName)
}

func replaceWorkspaceEntry(t *testing.T, w *PublishWorkspace, parent, moved string) {
	t.Helper()
	if err := renameHandle(w.plat.handle, w.owner.plat.handle, filepath.Base(moved), windows.FILE_RENAME_POSIX_SEMANTICS); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(parent, w.name), 0o700); err != nil {
		t.Fatal(err)
	}
}

func equalFoldPath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

func assertOwnerSystemDACL(t *testing.T, path string, directory bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		t.Fatalf("owner: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatal("empty DACL is fully permissive")
	}
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatal(err)
	}
	anon, err := windows.CreateWellKnownSid(windows.WinAnonymousSid)
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	var sawOwner, sawSystem bool
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(world) || sid.Equals(users) || sid.Equals(anon) {
			t.Fatalf("%s DACL grants %s", path, sid)
		}
		if sid.Equals(owner) {
			sawOwner = true
		}
		if sid.Equals(system) {
			sawSystem = true
		}
		if !sid.Equals(owner) && !sid.Equals(system) {
			t.Fatalf("%s unexpected SID %s", path, sid)
		}
	}
	if owner.Equals(system) {
		if !sawSystem {
			t.Fatalf("%s missing SYSTEM ACE", path)
		}
		return
	}
	if !sawOwner || !sawSystem {
		t.Fatalf("%s DACL owner=%v system=%v dir=%v", path, sawOwner, sawSystem, directory)
	}
}

func setWideDACL(t *testing.T, path string) {
	t.Helper()
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(world),
		},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func createJunction(t *testing.T, link, target string) {
	t.Helper()
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J %s %s: %v\n%s", link, target, err, out)
	}
}

func daclString(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return sd.String()
}

func fileIdentityOfPath(t *testing.T, path string) FileIdentity {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	var info fileIDInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatal(err)
	}
	return FileIdentity{plat: fileIdentity{volume: info.VolumeSerialNumber, fileID: info.FileID}}
}
