//go:build windows

package logging

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
	"golang.org/x/sys/windows"
)

func TestExportWithOps_WindowsWorkspaceGuardBlocksExternalRenameAndDelete(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	parent := filepath.Join(t.TempDir(), "publish")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := exportWithOps(ctx, ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(parent, "result.zip"), Paths: paths, PrivateFS: fs}, exportOps{Checkpoint: func(stage exportStage) error {
		if stage != stageBeforePublish {
			return nil
		}
		entries, readErr := os.ReadDir(parent)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var workspace string
		for _, entry := range entries {
			if entry.IsDir() {
				workspace = filepath.Join(parent, entry.Name())
				break
			}
		}
		if workspace == "" {
			t.Fatal("workspace not found")
		}
		if renameErr := os.Rename(workspace, workspace+"-moved"); !errors.Is(renameErr, windows.ERROR_SHARING_VIOLATION) {
			t.Fatalf("rename error=%v", renameErr)
		}
		if removeErr := os.Remove(workspace); !errors.Is(removeErr, windows.ERROR_SHARING_VIOLATION) {
			t.Fatalf("remove error=%v", removeErr)
		}
		cancel()
		return nil
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("resources=%v error=%v", entries, readErr)
	}
}

func TestExportWithOps_WindowsVisibleParentJunctionCannotRedirectPublish(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	base := t.TempDir()
	visible := filepath.Join(base, "publish")
	held := filepath.Join(base, "held")
	external := filepath.Join(base, "external")
	if err := os.Mkdir(visible, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(visible, "result.zip"), Paths: paths, PrivateFS: fs}, exportOps{Checkpoint: func(stage exportStage) error {
		if stage != stageBeforePublish {
			return nil
		}
		if renameErr := os.Rename(visible, held); renameErr != nil {
			t.Skipf("mandatory workspace guard prevents ancestor replacement: %v", renameErr)
		}
		if output, linkErr := exec.Command("cmd", "/c", "mklink", "/J", visible, external).CombinedOutput(); linkErr != nil {
			t.Fatalf("create junction: %v: %s", linkErr, output)
		}
		return nil
	}})
	if !errors.Is(err, platform.ErrPublishDirectoryChanged) {
		t.Fatalf("error=%v", err)
	}
	entries, readErr := os.ReadDir(external)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("external entries=%v error=%v", entries, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(held, "result.zip")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("held target: %v", statErr)
	}
}
