//go:build unix

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPrivateFS_UnixRejectsFilesystemRootBeforeHardening(t *testing.T) {
	fs, err := NewPrivateFS(string(filepath.Separator))
	if fs != nil {
		_ = fs.Close()
	}
	if err == nil {
		t.Fatal("expected filesystem root to be rejected")
	}
	if !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("error=%q want filesystem-root validation", err)
	}
}

func TestPrivateFS_UnixPermissions(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "x"); err != nil {
		t.Fatal(err)
	}
	assertMode(t, paths.LogDir, os.ModeDir|0o700)
	assertMode(t, paths.DaemonLog, 0o600)
}

func TestPrivateFS_UnixUmaskStill0600(t *testing.T) {
	old := unix.Umask(0o022)
	t.Cleanup(func() { unix.Umask(old) })
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "x"); err != nil {
		t.Fatal(err)
	}
	assertMode(t, paths.DaemonLog, 0o600)
	assertMode(t, paths.LogDir, os.ModeDir|0o700)
}

func TestPrivateFS_UnixTightenWidePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	fs, err := NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	assertMode(t, root, os.ModeDir|0o700)
	logs := filepath.Join(root, "logs")
	if err := os.Mkdir(logs, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(logs, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := fs.EnsureDir(logs); err != nil {
		t.Fatal(err)
	}
	assertMode(t, logs, os.ModeDir|0o700)
}

func TestPrivateFS_UnixRefuseCreateRootAsRoot(t *testing.T) {
	orig := effectiveUID
	t.Cleanup(func() { effectiveUID = orig })
	effectiveUID = func() int { return 0 }
	root := filepath.Join(t.TempDir(), "missing")
	if _, err := NewPrivateFS(root); err == nil {
		t.Fatal("expected root missing-data-root to fail closed")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("root created as privileged user: %v", err)
	}
}

func TestPrivateFS_UnixRootOwnerFromDataRootFD(t *testing.T) {
	origUID, origFstat, origFchownat := effectiveUID, fstatFn, fchownatFn
	t.Cleanup(func() {
		effectiveUID, fstatFn, fchownatFn = origUID, origFstat, origFchownat
	})
	effectiveUID = func() int { return 0 }

	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	var fstatFDs []int
	fstatFn = func(fd int, st *unix.Stat_t) error {
		if err := unix.Fstat(fd, st); err != nil {
			return err
		}
		fstatFDs = append(fstatFDs, fd)
		if len(fstatFDs) == 1 {
			st.Uid = 42
			st.Gid = 43
		}
		return nil
	}
	var chowns []string
	fchownatFn = func(dirfd int, path string, uid, gid, flags int) error {
		if uid != 42 || gid != 43 {
			t.Fatalf("fchownat uid=%d gid=%d", uid, gid)
		}
		if flags&unix.AT_SYMLINK_NOFOLLOW == 0 {
			t.Fatal("expected AT_SYMLINK_NOFOLLOW")
		}
		chowns = append(chowns, path)
		return nil
	}

	fs, err := NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	if len(fstatFDs) != 1 || fstatFDs[0] != fs.plat.rootFD {
		t.Fatalf("fstat fds=%v want only root fd %d during construction", fstatFDs, fs.plat.rootFD)
	}
	if fs.plat.uid != 42 || fs.plat.gid != 43 {
		t.Fatalf("owner=%d:%d want 42:43", fs.plat.uid, fs.plat.gid)
	}
	paths := NewPaths(root)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "x"); err != nil {
		t.Fatal(err)
	}
	if len(chowns) == 0 {
		t.Fatal("expected fchownat from data-root owner")
	}
}

func TestPrivateFS_UnixDataRootSymlinkFollowed(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(base, "link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	fs, err := NewPrivateFS(linkRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	paths := NewPaths(linkRoot)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "via-link"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(realRoot, "logs", filepath.Base(paths.DaemonLog)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "via-link" {
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
	st, err := os.Stat(filepath.Join(realRoot, "logs"))
	if err != nil {
		t.Fatal(err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("missing Stat_t")
	}
	want := FileIdentity{plat: fileIdentity{dev: uint64(sys.Dev), ino: uint64(sys.Ino)}}
	if held != want {
		t.Fatalf("held identity %+v want %+v", held, want)
	}
}

func TestPrivateFS_UnixLogDirSymlinkFailClosed(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outside, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := fs.EnsureDir(paths.LogDir); err == nil {
		t.Fatal("expected EnsureDir on symlink logs to fail")
	}
	if _, err := fs.OpenAppend(paths.DaemonLog); err == nil {
		t.Fatal("expected OpenAppend through symlink logs to fail")
	}
	if _, err := fs.ReadDir(paths.LogDir); err == nil {
		t.Fatal("expected ReadDir through symlink logs to fail")
	}
	if err := fs.Rename(paths.DaemonLog, paths.DaemonLog+".1"); err == nil {
		t.Fatal("expected Rename through symlink logs to fail")
	}
	if err := fs.Remove(paths.DaemonLog); err == nil {
		t.Fatal("expected Remove through symlink logs to fail")
	}
	if _, err := fs.OpenReadChecked(paths.DaemonLog, FileIdentity{}); err == nil {
		t.Fatal("expected OpenReadChecked through symlink logs to fail")
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
}

func TestPrivateFS_UnixFileSymlinkDoesNotAppendYAML(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Settings, []byte("keep-yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(paths.Settings, paths.DaemonLog); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.OpenAppend(paths.DaemonLog); err == nil {
		t.Fatal("expected OpenAppend of symlink to fail")
	}
	got, err := os.ReadFile(paths.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep-yaml" {
		t.Fatalf("yaml mutated: %q", got)
	}
}

func TestPrivateFS_UnixOpenAppendRejectsFIFOWithoutBlockingOrHardening(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(paths.DaemonLog, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.DaemonLog, 0o640); err != nil {
		t.Fatal(err)
	}

	type result struct {
		file *os.File
		err  error
	}
	done := make(chan result, 1)
	go func() {
		f, err := fs.OpenAppend(paths.DaemonLog)
		done <- result{file: f, err: err}
	}()

	var got result
	select {
	case got = <-done:
	case <-time.After(250 * time.Millisecond):
		t.Error("OpenAppend blocked while opening a FIFO")
		reader, err := unix.Open(paths.DaemonLog, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatalf("open FIFO reader to release blocked OpenAppend: %v", err)
		}
		got = <-done
		if err := unix.Close(reader); err != nil {
			t.Fatal(err)
		}
	}
	if got.file != nil {
		_ = got.file.Close()
	}
	if got.err == nil {
		t.Fatal("expected FIFO append to be rejected")
	}
	info, err := os.Lstat(paths.DaemonLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("FIFO permissions=%v want=0640", got)
	}
}

func TestPrivateFS_UnixRenameDoesNotReplaceExistingDestination(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "source"); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.TUILog, "destination"); err != nil {
		t.Fatal(err)
	}

	err := fs.Rename(paths.DaemonLog, paths.TUILog)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("rename error=%v want os.ErrExist", err)
	}
	for path, want := range map[string]string{
		paths.DaemonLog: "source",
		paths.TUILog:    "destination",
	} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != want {
			t.Fatalf("%s=%q want=%q", filepath.Base(path), got, want)
		}
	}
}

func TestPrivateFS_UnixRenameUsesAtomicNoReplacePrimitive(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "source"); err != nil {
		t.Fatal(err)
	}

	original := renameatNoReplaceFn
	t.Cleanup(func() { renameatNoReplaceFn = original })
	called := false
	renameatNoReplaceFn = func(dirfd int, oldName, newName string) error {
		called = true
		if dirfd != fs.plat.dirs[privateLogDirName] || oldName != filepath.Base(paths.DaemonLog) || newName != filepath.Base(paths.TUILog) {
			t.Fatalf("rename args=(%d,%q,%q)", dirfd, oldName, newName)
		}
		return unix.EEXIST
	}

	err := fs.Rename(paths.DaemonLog, paths.TUILog)
	if !called {
		t.Fatal("atomic no-replace primitive was not called")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("rename error=%v want os.ErrExist", err)
	}
	if got, readErr := os.ReadFile(paths.DaemonLog); readErr != nil || string(got) != "source" {
		t.Fatalf("source after failed rename=%q err=%v", got, readErr)
	}
	if _, statErr := os.Lstat(paths.TUILog); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination after failed rename: %v", statErr)
	}
}

func TestModeFromStatPreservesUnixFileTypes(t *testing.T) {
	tests := []struct {
		name string
		mode uint32
		want os.FileMode
	}{
		{name: "regular", mode: unix.S_IFREG | 0o640, want: 0},
		{name: "directory", mode: unix.S_IFDIR | 0o750, want: os.ModeDir},
		{name: "symlink", mode: unix.S_IFLNK | 0o777, want: os.ModeSymlink},
		{name: "fifo", mode: unix.S_IFIFO | 0o600, want: os.ModeNamedPipe},
		{name: "socket", mode: unix.S_IFSOCK | 0o600, want: os.ModeSocket},
		{name: "block device", mode: unix.S_IFBLK | 0o600, want: os.ModeDevice},
		{name: "character device", mode: unix.S_IFCHR | 0o600, want: os.ModeDevice | os.ModeCharDevice},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := modeFromUnixMode(tt.mode)
			if got.Type() != tt.want {
				t.Fatalf("type=%v want=%v", got.Type(), tt.want)
			}
		})
	}
}

func TestPrivateFS_UnixCloseReleasesFDs(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	rootFD := fs.plat.rootFD
	logsFD := fs.plat.dirs[privateLogDirName]
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fstat(rootFD, &unix.Stat_t{}); err == nil {
		t.Fatal("root fd still open after Close")
	}
	if err := unix.Fstat(logsFD, &unix.Stat_t{}); err == nil {
		t.Fatal("logs fd still open after Close")
	}
}

func TestPublishWorkspace_UnixPrivateModes(t *testing.T) {
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
	t.Cleanup(func() {
		if err := w.Close(); err == nil || !strings.Contains(err.Error(), "parent permits untrusted entry replacement") {
			t.Errorf("expected degraded mutable-parent cleanup: %v", err)
		}
	})
	assertMode(t, filepath.Join(parent, w.name), os.ModeDir|0o700)
	f, name, err := w.CreateTemp("private-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Join(parent, w.name, name), 0o600)
}

func TestPublishWorkspace_UnixCloseFailsClosedWhenParentIsMutableByOthers(t *testing.T) {
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
	workspacePath := filepath.Join(parent, w.name)

	if err := w.Close(); err == nil || !strings.Contains(err.Error(), "parent permits untrusted entry replacement") {
		t.Fatalf("Close error=%v want fail-closed mutable-parent warning", err)
	}
	if info, err := os.Stat(workspacePath); err != nil || !info.IsDir() {
		t.Fatalf("workspace was removed despite mutable parent: info=%v err=%v", info, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close error=%v", err)
	}
}

func TestPrivateFSPublishDir_UnixPropagatesDataRootOwner(t *testing.T) {
	originalUID := effectiveUID
	originalChown := publishUnixFchownFn
	t.Cleanup(func() {
		effectiveUID = originalUID
		publishUnixFchownFn = originalChown
	})
	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fs, err := NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fs.Close(); err != nil {
			t.Errorf("close private filesystem: %v", err)
		}
	})
	paths := NewPaths(root)
	if err := fs.EnsureDir(paths.LogExportDir); err != nil {
		t.Fatal(err)
	}
	effectiveUID = func() int { return 0 }
	fs.plat.uid = 4242
	fs.plat.gid = 4343
	type ownerCall struct{ uid, gid int }
	var calls []ownerCall
	publishUnixFchownFn = func(_ int, uid, gid int) error {
		calls = append(calls, ownerCall{uid: uid, gid: gid})
		return nil
	}

	d, err := fs.OpenPublishDir(paths.LogExportDir)
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishDir(t, d)
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	cleanupWorkspaceAfterSyntheticOwnerTest(t, w)
	f, name, err := w.CreateTemp("owner-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Join(paths.LogExportDir, w.name, name), 0o600)
	if err := w.Remove(name); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("fchown calls=%v want workspace and temp", calls)
	}
	for _, call := range calls {
		if call.uid != 4242 || call.gid != 4343 {
			t.Fatalf("fchown owner=%d:%d want 4242:4343", call.uid, call.gid)
		}
	}
	assertMode(t, filepath.Join(paths.LogExportDir, w.name), os.ModeDir|0o700)

	before := len(calls)
	externalDir, err := OpenPublishDir(privatePublishParent(t))
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishDir(t, externalDir)
	externalWorkspace, err := externalDir.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	cleanupWorkspaceAfterSyntheticOwnerTest(t, externalWorkspace)
	externalTemp, externalName, err := externalWorkspace.CreateTemp("external-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := externalTemp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := externalWorkspace.Remove(externalName); err != nil {
		t.Fatal(err)
	}
	if len(calls) != before {
		t.Fatalf("external publish inherited data-root owner: calls=%v", calls[before:])
	}
}

func cleanupWorkspaceAfterSyntheticOwnerTest(t *testing.T, w *PublishWorkspace) {
	t.Helper()
	t.Cleanup(func() {
		err := w.Close()
		// This test replaces fchown with a recorder, so the on-disk owner may
		// intentionally disagree with the synthetic trusted owner at cleanup.
		if err != nil && !strings.Contains(err.Error(), "parent permits untrusted entry replacement") {
			t.Errorf("close publish workspace after synthetic owner test: %v", err)
		}
	})
}

func TestPrivateFSPublishDir_UnixRootCreatesDataOwnerObjects(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to verify real fchown ownership")
	}
	const ownerUID, ownerGID = 4242, 4343
	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(root, ownerUID, ownerGID); err != nil {
		t.Fatal(err)
	}
	fs, err := NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fs.Close(); err != nil {
			t.Errorf("close private filesystem: %v", err)
		}
	})
	paths := NewPaths(root)
	if err := fs.EnsureDir(paths.LogExportDir); err != nil {
		t.Fatal(err)
	}
	d, err := fs.OpenPublishDir(paths.LogExportDir)
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishDir(t, d)
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishWorkspace(t, w)
	f, name, err := w.CreateTemp("owner-real-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	for path, wantMode := range map[string]os.FileMode{
		filepath.Join(paths.LogExportDir, w.name):       os.ModeDir | 0o700,
		filepath.Join(paths.LogExportDir, w.name, name): 0o600,
	} {
		var stat syscall.Stat_t
		if err := syscall.Stat(path, &stat); err != nil {
			t.Fatal(err)
		}
		if int(stat.Uid) != ownerUID || int(stat.Gid) != ownerGID {
			t.Fatalf("owner of %s=%d:%d want %d:%d", path, stat.Uid, stat.Gid, ownerUID, ownerGID)
		}
		assertMode(t, path, wantMode)
	}
	if err := w.Remove(name); err != nil {
		t.Fatal(err)
	}
}

func TestPublishDir_UnixPostPublishFailureIsWarning(t *testing.T) {
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
	original := publishUnixPostLinkUnlinkFn
	t.Cleanup(func() { publishUnixPostLinkUnlinkFn = original })
	publishUnixPostLinkUnlinkFn = func(int, string, int) error { return unix.EIO }
	var warnings []error
	err = d.PublishNoReplace(w, name, "result.zip", func(err error) { warnings = append(warnings, err) })
	publishUnixPostLinkUnlinkFn = original
	if err != nil {
		t.Fatalf("published target reported failure: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings=%v", warnings)
	}
	if got, err := os.ReadFile(filepath.Join(parent, "result.zip")); err != nil || string(got) != "published" {
		t.Fatalf("target=%q err=%v", got, err)
	}
	if err := w.Remove(name); err != nil {
		t.Fatal(err)
	}
}

func makeDirectoryLink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func moveWorkspaceOutside(t *testing.T, w *PublishWorkspace, parent, outside string) string {
	t.Helper()
	moved := filepath.Join(outside, "moved-workspace")
	if err := os.Rename(filepath.Join(parent, w.name), moved); err != nil {
		t.Fatal(err)
	}
	return moved
}

func replaceWorkspaceEntry(t *testing.T, w *PublishWorkspace, parent, moved string) {
	t.Helper()
	original := filepath.Join(parent, w.name)
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
}

func equalFoldPath(a, b string) bool { return a == b }

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	got := info.Mode().Perm()
	if info.IsDir() {
		got |= os.ModeDir
	}
	if got != want {
		t.Fatalf("%s mode=%v want=%v", path, got, want)
	}
}
