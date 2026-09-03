//go:build unix

package platform

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

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
	t.Setenv("HOME", "/root")
	t.Setenv("SUDO_USER", "attacker")
	effectiveUID = func() int { return 0 }

	root := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	var rootFDs []int
	fstatFn = func(fd int, st *unix.Stat_t) error {
		if err := unix.Fstat(fd, st); err != nil {
			return err
		}
		rootFDs = append(rootFDs, fd)
		st.Uid = 42
		st.Gid = 43
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
		if path == "/root" || filepath.Base(path) == "root" {
			t.Fatalf("fchownat used path %q", path)
		}
		chowns = append(chowns, path)
		return nil
	}

	fs, err := NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	if len(rootFDs) == 0 {
		t.Fatal("expected fstat of data root fd")
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
