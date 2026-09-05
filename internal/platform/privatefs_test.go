package platform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPrivateFS_RejectsRelativeDataRoot(t *testing.T) {
	if _, err := NewPrivateFS("relative-data"); err == nil {
		t.Fatal("expected relative data root to be rejected")
	}
	if _, err := NewPrivateFS(filepath.Join(".", "data")); err == nil {
		t.Fatal("expected dotted relative data root to be rejected")
	}
}

func TestPrivateFS_CreatesMissingDataRoot(t *testing.T) {
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", filepath.Join(t.TempDir(), "outside.token"))
	root := filepath.Join(t.TempDir(), "data")
	fs, err := NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("data root is not a directory: %s", root)
	}
}

func TestPrivateFS_ControlCredentialOutsideRootIgnored(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "control.token")
	if err := os.WriteFile(outside, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", outside)
	root := filepath.Join(t.TempDir(), "data")
	fs, err := NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside credential was touched: %v", err)
	}
}

func TestPrivateFS_AcceptsAbsoluteAndBasenamePaths(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	for _, dir := range []string{paths.LogDir, "logs", paths.LogExportDir, "logs-export"} {
		if err := fs.EnsureDir(dir); err != nil {
			t.Fatalf("EnsureDir(%q): %v", dir, err)
		}
	}
	f, err := fs.OpenAppend(paths.DaemonLog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{paths.LogDir, "logs"} {
		entries, err := fs.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir(%q): %v", dir, err)
		}
		if !containsFile(entries, filepath.Base(paths.DaemonLog)) {
			t.Fatalf("ReadDir(%q) missing %s: %+v", dir, filepath.Base(paths.DaemonLog), namesOf(entries))
		}
	}
	tmp, err := fs.CreateTemp(paths.LogExportDir, "export-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	id, err := fs.OpenDirIdentity("logs")
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Close(); err != nil {
		t.Fatal(err)
	}
	if err := id.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateFS_RejectsPathsOutsideDataRoot(t *testing.T) {
	fs, _ := openTestPrivateFS(t)
	outside := filepath.Join(t.TempDir(), "outside.log")
	if err := fs.EnsureDir(outside); err == nil {
		t.Fatal("expected outside EnsureDir to fail")
	}
	if _, err := fs.OpenAppend(outside); err == nil {
		t.Fatal("expected outside OpenAppend to fail")
	}
}

func TestPrivateFS_RejectsDotDotEscape(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	escaped := filepath.Join(paths.LogDir, "..", "outside")
	if err := fs.EnsureDir(escaped); err == nil {
		t.Fatal("expected logs/../outside EnsureDir to fail")
	}
	if _, err := fs.OpenAppend(escaped); err == nil {
		t.Fatal("expected logs/../outside OpenAppend to fail")
	}
	if _, err := fs.OpenAppend(filepath.Join(paths.Root, "outside")); err == nil {
		t.Fatal("expected data-root sibling OpenAppend to fail")
	}
	if _, err := fs.CreateTemp(paths.Root, "tmp-*"); err == nil {
		t.Fatal("expected CreateTemp on data root to fail")
	}
}

func TestPrivateFS_RejectsDotAndDotDotNames(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".", ".."} {
		if err := fs.EnsureDir(name); err == nil {
			t.Fatalf("expected EnsureDir(%q) to fail", name)
		}
		if _, err := fs.OpenAppend(filepath.Join(paths.LogDir, name)); err == nil {
			t.Fatalf("expected OpenAppend(%q) to fail", name)
		}
	}
}

func TestPrivateFS_FileOpsRejectDataRootSiblings(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.OpenAppend(paths.Settings); err == nil {
		t.Fatal("expected OpenAppend of mihari.yaml to fail")
	}
	if _, err := fs.OpenAppend("mihari.yaml"); err == nil {
		t.Fatal("expected OpenAppend of data-root basename to fail")
	}
}

func TestPrivateFS_OpenReadCheckedIdentityMismatch(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "first\n"); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	ident := identityOf(t, entries, filepath.Base(paths.DaemonLog))
	// Keep the old inode allocated under another directory entry. Some Unix
	// filesystems immediately reuse an unlinked inode, which would make this
	// identity-mismatch fixture nondeterministic.
	if err := fs.Rename(paths.DaemonLog, paths.TUILog); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "second\n"); err != nil {
		t.Fatal(err)
	}
	f, err := fs.OpenReadChecked(paths.DaemonLog, ident)
	if err == nil {
		_ = f.Close()
		t.Fatal("expected identity mismatch")
	}
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("err=%v want ErrIdentityMismatch", err)
	}
	if err := fs.RepairAccessChecked(paths.DaemonLog, ident); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("repair must reject replaced identity: %v", err)
	}
}

func TestPrivateFS_ReplaceEmptyKeepsOldHandleReadable(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "keep-me"); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	ident := identityOf(t, entries, filepath.Base(paths.DaemonLog))
	old, err := fs.OpenReadChecked(paths.DaemonLog, ident)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })
	if err := fs.ReplaceEmpty(paths.DaemonLog); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(old)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep-me" {
		t.Fatalf("old handle got %q", got)
	}
	fresh, err := fs.OpenAppend(paths.DaemonLog)
	if err != nil {
		t.Fatal(err)
	}
	info, err := fresh.Stat()
	if err != nil {
		t.Fatal(err)
	}
	_ = fresh.Close()
	if info.Size() != 0 {
		t.Fatalf("replaced file size=%d", info.Size())
	}
	entries, err = fs.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	if identityOf(t, entries, filepath.Base(paths.DaemonLog)) == ident {
		t.Fatal("ReplaceEmpty did not produce a new identity")
	}
}

func TestPrivateFS_CloseIdempotentAndBlocksOps(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "x"); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	ident := identityOf(t, entries, filepath.Base(paths.DaemonLog))
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	ops := []struct {
		name string
		fn   func() error
	}{
		{"EnsureDir", func() error { return fs.EnsureDir(paths.LogDir) }},
		{"RepairAccessChecked", func() error { return fs.RepairAccessChecked(paths.DaemonLog, ident) }},
		{"OpenAppend", func() error { _, err := fs.OpenAppend(paths.DaemonLog); return err }},
		{"OpenReadChecked", func() error { _, err := fs.OpenReadChecked(paths.DaemonLog, ident); return err }},
		{"CreateTemp", func() error { _, err := fs.CreateTemp(paths.LogDir, "tmp-*"); return err }},
		{"ReplaceEmpty", func() error { return fs.ReplaceEmpty(paths.DaemonLog) }},
		{"ReadDir", func() error { _, err := fs.ReadDir(paths.LogDir); return err }},
		{"OpenDirIdentity", func() error { _, err := fs.OpenDirIdentity(paths.LogDir); return err }},
		{"Rename", func() error {
			return fs.Rename(paths.DaemonLog, paths.DaemonLog+".1")
		}},
		{"Remove", func() error { return fs.Remove(paths.DaemonLog) }},
	}
	for _, op := range ops {
		if err := op.fn(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("%s after Close: err=%v want os.ErrClosed", op.name, err)
		}
	}
}

func TestPrivateFS_RenameRequiresSameDirectory(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := fs.EnsureDir(paths.LogExportDir); err != nil {
		t.Fatal(err)
	}
	if err := writeLog(fs, paths.DaemonLog, "x"); err != nil {
		t.Fatal(err)
	}
	exportFile := filepath.Join(paths.LogExportDir, "moved.log")
	if err := fs.Rename(paths.DaemonLog, exportFile); err == nil {
		t.Fatal("expected cross-directory rename to fail")
	}
	if err := fs.Rename(paths.DaemonLog, paths.DaemonLog+".1"); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	if containsFile(entries, filepath.Base(paths.DaemonLog)) {
		t.Fatal("original name still present")
	}
	if !containsFile(entries, filepath.Base(paths.DaemonLog)+".1") {
		t.Fatal("renamed file missing")
	}
}

func TestPrivateFS_ConcurrentReadDirSeesCompleteUniqueNames(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	const n = 64
	want := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("entry-%02d.log", i)
		want[name] = struct{}{}
		if err := writeLog(fs, filepath.Join(paths.LogDir, name), "x"); err != nil {
			t.Fatal(err)
		}
	}
	const workers = 8
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			entries, err := fs.ReadDir(paths.LogDir)
			if err != nil {
				errs <- err
				return
			}
			got := make(map[string]int, len(entries))
			for _, entry := range entries {
				got[entry.Name]++
			}
			if len(got) != n {
				errs <- fmt.Errorf("unique names=%d want=%d", len(got), n)
				return
			}
			for name := range want {
				if got[name] != 1 {
					errs <- fmt.Errorf("%s count=%d", name, got[name])
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestPrivateFS_OpenDirIdentityClosed(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	id, err := fs.OpenDirIdentity(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := id.identity(); err != nil {
		t.Fatal(err)
	}
	if err := id.Close(); err != nil {
		t.Fatal(err)
	}
	if err := id.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := id.identity(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("identity after Close: %v", err)
	}
}

func openTestPrivateFS(t *testing.T) (*PrivateFS, Paths) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	fs, err := NewPrivateFS(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs, NewPaths(root)
}

func writeLog(fs *PrivateFS, path, body string) error {
	f, err := fs.OpenAppend(path)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(body)); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func containsFile(entries []FileEntry, name string) bool {
	for _, entry := range entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}

func namesOf(entries []FileEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func identityOf(t *testing.T, entries []FileEntry, name string) FileIdentity {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry.Identity
		}
	}
	t.Fatalf("missing entry %q in %v", name, namesOf(entries))
	return FileIdentity{}
}
