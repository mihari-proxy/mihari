package platform

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishDir_RejectsInvalidBasenames(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })

	invalid := []string{"", ".", "..", "nested/file", `nested\file`, filepath.Join(t.TempDir(), "absolute")}
	for _, name := range invalid {
		if _, err := d.Exists(name); err == nil {
			t.Errorf("Exists(%q) accepted invalid basename", name)
		}
		if err := w.Remove(name); err == nil {
			t.Errorf("Remove(%q) accepted invalid basename", name)
		}
		if err := d.PublishNoReplace(w, name, "target.zip", nil); err == nil {
			t.Errorf("PublishNoReplace temp %q accepted invalid basename", name)
		}
		if err := d.PublishNoReplace(w, "temp.zip", name, nil); err == nil {
			t.Errorf("PublishNoReplace target %q accepted invalid basename", name)
		}
	}
	for _, pattern := range []string{".", "..", "nested/*", `nested\*`} {
		if f, _, err := w.CreateTemp(pattern); err == nil {
			_ = f.Close()
			t.Errorf("CreateTemp(%q) accepted invalid pattern", pattern)
		}
	}
}

func TestPublishDir_CloseIsIdempotentAndPathRemainsReadable(t *testing.T) {
	root := t.TempDir()
	d, err := OpenPublishDir(root)
	if err != nil {
		t.Fatal(err)
	}
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Path(); !sameCanonicalPath(got, canonical) {
		t.Fatalf("Path()=%q want %q", got, canonical)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.CreateTemp("late-*"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("CreateTemp after Close: %v", err)
	}
	if err := w.Remove("late"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Remove after Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if got := d.Path(); !sameCanonicalPath(got, canonical) {
		t.Fatalf("Path() after Close=%q want %q", got, canonical)
	}
	if _, err := d.Exists("late"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Exists after Close: %v", err)
	}
	if _, err := d.CreateWorkspace(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("CreateWorkspace after Close: %v", err)
	}
	if err := d.PublishNoReplace(w, "late", "target", nil); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("PublishNoReplace after Close: %v", err)
	}
}

func TestPublishCapabilities_ClosedErrorPrecedesInvalidArguments(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	workspaceOps := []struct {
		name string
		call func() error
	}{
		{name: "CreateTemp invalid", call: func() error { _, _, err := w.CreateTemp("../bad"); return err }},
		{name: "Remove invalid", call: func() error { return w.Remove("../bad") }},
		{name: "Publish with closed workspace", call: func() error { return d.PublishNoReplace(w, "../bad", "../bad", nil) }},
	}
	for _, op := range workspaceOps {
		t.Run(op.name, func(t *testing.T) {
			if err := op.call(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("error=%v want os.ErrClosed", err)
			}
		})
	}

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	dirOps := []struct {
		name string
		call func() error
	}{
		{name: "Exists invalid", call: func() error { _, err := d.Exists("../bad"); return err }},
		{name: "IsWithin nil", call: func() error { _, err := d.IsWithin(nil); return err }},
		{name: "CreateWorkspace", call: func() error { _, err := d.CreateWorkspace(); return err }},
		{name: "Publish nil invalid", call: func() error { return d.PublishNoReplace(nil, "../bad", "../bad", nil) }},
	}
	for _, op := range dirOps {
		t.Run(op.name, func(t *testing.T) {
			if err := op.call(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("error=%v want os.ErrClosed", err)
			}
		})
	}
}

func TestPublishWorkspace_CloseMovedOutsideParentReturnsWarning(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "parent")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	moved := moveWorkspaceOutside(t, w, parent, outside)
	if err := w.Close(); err == nil {
		t.Fatal("expected cleanup warning for workspace moved outside parent")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close=%v", err)
	}
	if info, err := os.Stat(moved); err != nil || !info.IsDir() {
		t.Fatalf("moved workspace was touched: info=%v err=%v", info, err)
	}
}

func TestPublishWorkspace_CloseFailsClosedWhenEntryChangesAfterIdentityCheck(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(parent, w.name)
	movedPath := filepath.Join(parent, w.name+"-moved-during-close")
	originalCheckpoint := publishWorkspaceCleanupCheckpoint
	t.Cleanup(func() { publishWorkspaceCleanupCheckpoint = originalCheckpoint })
	publishWorkspaceCleanupCheckpoint = func() {
		replaceWorkspaceEntry(t, w, parent, movedPath)
	}

	if err := w.Close(); err == nil {
		t.Fatal("expected fail-closed cleanup warning")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close=%v", err)
	}
	for _, path := range []string{originalPath, movedPath} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("cleanup touched %s: info=%v err=%v", path, info, err)
		}
	}
}

func TestPublishWorkspace_CloseFailsClosedWhenMovedOutsideAfterIdentityCheck(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "parent")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	var movedPath string
	originalCheckpoint := publishWorkspaceCleanupCheckpoint
	t.Cleanup(func() { publishWorkspaceCleanupCheckpoint = originalCheckpoint })
	publishWorkspaceCleanupCheckpoint = func() {
		movedPath = moveWorkspaceOutside(t, w, parent, outside)
	}

	if err := w.Close(); err == nil {
		t.Fatal("expected fail-closed cleanup warning")
	}
	if movedPath == "" {
		t.Fatal("cleanup checkpoint did not run")
	}
	if info, err := os.Stat(movedPath); err != nil || !info.IsDir() {
		t.Fatalf("moved workspace was touched: info=%v err=%v", info, err)
	}
}

func TestPublishWorkspace_OperationsStayAnchoredAfterEntryReplacement(t *testing.T) {
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
	original := filepath.Join(parent, w.name)
	movedName := w.name + "-moved"
	moved := filepath.Join(parent, movedName)
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	f, name, err := w.CreateTemp("payload-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("held-workspace")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, name)); err != nil {
		t.Fatalf("temp not created through held workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(original, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement workspace received temp: %v", err)
	}
	if err := w.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("renamed original workspace still exists: %v", err)
	}
	if info, err := os.Stat(original); err != nil || !info.IsDir() {
		t.Fatalf("replacement workspace was touched: info=%v err=%v", info, err)
	}
}

func TestPublishDir_PublishNoReplace(t *testing.T) {
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
	create := func(body string) string {
		t.Helper()
		f, name, err := w.CreateTemp("export-*.zip")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, body); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		return name
	}

	first := create("complete payload")
	var warnings []error
	if err := d.PublishNoReplace(w, first, "result.zip", func(err error) { warnings = append(warnings, err) }); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("normal publish warnings=%v want none", warnings)
	}
	got, err := os.ReadFile(filepath.Join(parent, "result.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "complete payload" {
		t.Fatalf("target=%q", got)
	}
	if _, err := os.Stat(filepath.Join(parent, w.name, first)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published temp remains: %v", err)
	}

	second := create("must stay private")
	err = d.PublishNoReplace(w, second, "result.zip", nil)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing target error=%v want os.ErrExist", err)
	}
	private, err := os.ReadFile(filepath.Join(parent, w.name, second))
	if err != nil || string(private) != "must stay private" {
		t.Fatalf("source after collision=%q err=%v", private, err)
	}
	if err := w.Remove(second); err != nil {
		t.Fatal(err)
	}
}

func TestPublishDir_ExistsIncludesSymlinkOrJunction(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "parent")
	target := filepath.Join(base, "target")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenPublishDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	makeDirectoryLink(t, filepath.Join(parent, "occupied.zip"), target)
	exists, err := d.Exists("occupied.zip")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("reparse target was treated as absent")
	}
}

func TestPublishDir_RejectsWorkspaceFromAnotherDirectory(t *testing.T) {
	d1, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d1.Close()
	d2, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	w, err := d1.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := d2.PublishNoReplace(w, "temp.zip", "target.zip", nil); err == nil {
		t.Fatal("accepted workspace owned by another publish directory")
	}
}

func TestPublishDir_ChangedVisiblePathFailsClosed(t *testing.T) {
	base := t.TempDir()
	visible := filepath.Join(base, "publish")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(visible, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := OpenPublishDir(visible)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	heldPath := filepath.Join(base, "held")
	if err := os.Rename(visible, heldPath); err != nil {
		t.Fatal(err)
	}
	makeDirectoryLink(t, visible, outside)
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	f, tempName, err := w.CreateTemp("export-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("private")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	err = d.PublishNoReplace(w, tempName, "result.zip", nil)
	if !errors.Is(err, ErrPublishDirectoryChanged) {
		t.Fatalf("publish error=%v want ErrPublishDirectoryChanged", err)
	}
	for _, path := range []string{filepath.Join(outside, "result.zip"), filepath.Join(heldPath, "result.zip")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected published target %s: %v", path, err)
		}
	}
	if err := w.Remove(tempName); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryContainment_UsesHeldIdentities(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real-data")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(base, "linked-data")
	makeDirectoryLink(t, linkedRoot, realRoot)
	fs, err := NewPrivateFS(linkedRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	paths := NewPaths(linkedRoot)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if err := fs.EnsureDir(paths.LogExportDir); err != nil {
		t.Fatal(err)
	}
	logIdentity, err := fs.OpenDirIdentity(paths.LogDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logIdentity.Close() })
	realLogDir := filepath.Join(realRoot, "logs")
	insideChild := filepath.Join(realLogDir, "nested")
	if err := os.Mkdir(insideChild, 0o700); err != nil {
		t.Fatal(err)
	}
	insideLink := filepath.Join(base, "inside-link")
	makeDirectoryLink(t, insideLink, realLogDir)
	sibling := filepath.Join(realRoot, "logs-sibling")
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(base, "external")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		path   string
		within bool
	}{
		{name: "same directory", path: realLogDir, within: true},
		{name: "descendant", path: insideChild, within: true},
		{name: "symlink or junction into logs", path: insideLink, within: true},
		{name: "sibling prefix", path: sibling, within: false},
		{name: "external", path: external, within: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := OpenPublishDir(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			got, err := d.IsWithin(logIdentity)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.within {
				t.Fatalf("IsWithin=%v want %v", got, tt.within)
			}
		})
	}

	defaultDir, err := fs.OpenPublishDir(paths.LogExportDir)
	if err != nil {
		t.Fatal(err)
	}
	if within, err := defaultDir.IsWithin(logIdentity); err != nil || within {
		t.Fatalf("default export containment=%v err=%v", within, err)
	}
	if err := defaultDir.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := defaultDir.IsWithin(logIdentity); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("IsWithin after Close: %v", err)
	}
}

func TestPrivateFSOpenPublishDir_RejectsLogDirectory(t *testing.T) {
	fs, paths := openTestPrivateFS(t)
	if err := fs.EnsureDir(paths.LogDir); err != nil {
		t.Fatal(err)
	}
	if d, err := fs.OpenPublishDir(paths.LogDir); err == nil {
		_ = d.Close()
		t.Fatal("PrivateFS.OpenPublishDir accepted logs instead of only logs-export")
	}
}

func sameCanonicalPath(a, b string) bool {
	return equalFoldPath(filepath.Clean(a), filepath.Clean(b))
}
