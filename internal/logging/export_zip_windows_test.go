//go:build windows

package logging

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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
