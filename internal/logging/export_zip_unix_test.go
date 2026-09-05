//go:build unix

package logging

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestExportWithOps_UnixWorkspaceReplacementCleansHeldOriginalOnly(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	parent := filepath.Join(t.TempDir(), "publish")
	mustMkdir(t, parent)
	var original, moved string
	injected := errors.New("stop before publish")
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(parent, "result.zip"), Paths: paths, PrivateFS: fs}, exportOps{Checkpoint: func(stage exportStage) error {
		if stage != stageBeforePublish {
			return nil
		}
		original = onlyWorkspace(t, parent)
		moved = original + "-held"
		if err := os.Rename(original, moved); err != nil {
			t.Fatal(err)
		}
		mustMkdir(t, original)
		if err := os.WriteFile(filepath.Join(original, "replacement-marker"), []byte("external"), 0o600); err != nil {
			t.Fatal(err)
		}
		return injected
	}})
	if !errors.Is(err, injected) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(moved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("held workspace remains: %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(original, "replacement-marker"))
	if readErr != nil || string(got) != "external" {
		t.Fatalf("replacement touched: %q %v", got, readErr)
	}
}

func TestExportWithOps_UnixVisibleParentSymlinkCannotRedirectPublish(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	base := t.TempDir()
	visible := filepath.Join(base, "publish")
	held := filepath.Join(base, "held")
	external := filepath.Join(base, "external")
	mustMkdir(t, visible)
	mustMkdir(t, external)
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(visible, "result.zip"), Paths: paths, PrivateFS: fs}, exportOps{Checkpoint: func(stage exportStage) error {
		if stage != stageBeforePublish {
			return nil
		}
		if err := os.Rename(visible, held); err != nil {
			t.Fatal(err)
		}
		return os.Symlink(external, visible)
	}})
	if !errors.Is(err, platform.ErrPublishDirectoryChanged) {
		t.Fatalf("error=%v", err)
	}
	for _, path := range []string{filepath.Join(external, "result.zip"), filepath.Join(held, "result.zip")} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("redirected write %s: %v", path, statErr)
		}
	}
	entries, readErr := os.ReadDir(external)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("external entries=%v error=%v", entries, readErr)
	}
	heldEntries, readErr := os.ReadDir(held)
	if readErr != nil || len(heldEntries) != 0 {
		t.Fatalf("held residue=%v error=%v", heldEntries, readErr)
	}
}

func TestExportWithOps_UnixMovedWorkspaceAggregatesCleanupAndSanitizes(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	parent := filepath.Join(t.TempDir(), "publish")
	outside := filepath.Join(t.TempDir(), "outside")
	mustMkdir(t, parent)
	mustMkdir(t, outside)
	primary := errors.New("failure /private/customer-token.log")
	var moved string
	var warnings []error
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(parent, "result.zip"), Paths: paths, PrivateFS: fs, OnWarning: func(err error) { warnings = append(warnings, err) }}, exportOps{Checkpoint: func(stage exportStage) error {
		if stage != stageBeforePublish {
			return nil
		}
		workspace := onlyWorkspace(t, parent)
		moved = filepath.Join(outside, filepath.Base(workspace))
		if err := os.Rename(workspace, moved); err != nil {
			t.Fatal(err)
		}
		return primary
	}})
	if !errors.Is(err, primary) || err.Error() != "log export failed" {
		t.Fatalf("error=%q classified=%v", err, errors.Is(err, primary))
	}
	if len(warnings) != 1 || warnings[0].Error() != "log export cleanup incomplete" {
		t.Fatalf("warnings=%v", warnings)
	}
	entries, readErr := os.ReadDir(moved)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("orphan contents=%v error=%v", entries, readErr)
	}
}

func TestExportWithOps_UnixPostCommitUntrustedCleanupWarnsButSucceeds(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	parent := filepath.Join(t.TempDir(), "publish")
	mustMkdir(t, parent)
	var warnings []error
	result, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(parent, "result.zip"), Paths: paths, PrivateFS: fs, OnWarning: func(err error) { warnings = append(warnings, err) }}, exportOps{Publish: func(d *platform.PublishDir, w *platform.PublishWorkspace, temp, target string, warning func(error)) error {
		if err := d.PublishNoReplace(w, temp, target, warning); err != nil {
			return err
		}
		return os.Chmod(parent, 0o777)
	}})
	t.Cleanup(func() {
		if err := os.Chmod(parent, 0o700); err != nil {
			t.Errorf("restore parent permissions: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	assertSameExportFile(t, result.Path, filepath.Join(parent, "result.zip"))
	if len(warnings) == 0 {
		t.Fatal("missing sanitized cleanup warning")
	}
	for _, warning := range warnings {
		if warning.Error() != "log export cleanup incomplete" || strings.Contains(warning.Error(), parent) {
			t.Fatalf("warning=%q", warning)
		}
	}
}

func onlyWorkspace(t *testing.T, parent string) string {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(parent, entry.Name())
		}
	}
	t.Fatal("workspace not found")
	return ""
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
