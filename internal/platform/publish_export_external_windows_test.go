//go:build windows

package platform_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestExport_WindowsRecoveredFactoryCleanupStillWarns(t *testing.T) {
	for _, prefix := range []string{"spool-", "archive-"} {
		for _, cleanupFails := range []bool{false, true} {
			t.Run(prefix+map[bool]string{false: "clean", true: "recovered"}[cleanupFails], func(t *testing.T) {
				paths := platform.NewPaths(t.TempDir())
				fs, err := platform.NewPrivateFS(paths.Root)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := fs.Close(); err != nil {
						t.Errorf("close filesystem: %v", err)
					}
				})
				if err := fs.EnsureDir(paths.LogDir); err != nil {
					t.Fatal(err)
				}
				file, err := fs.OpenAppend(paths.DaemonLog)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.WriteString(`{"time":"2026-09-02T12:00:00Z","msg":"safe"}` + "\n")
				if err := errors.Join(writeErr, file.Close()); err != nil {
					t.Fatal(err)
				}
				hardenErr := errors.New("private hardening failure")
				var cleanupErr error
				if cleanupFails {
					cleanupErr = errors.New("private first deletion failure")
				}
				t.Cleanup(platform.InjectPublishTempFailureForTest(prefix, hardenErr, cleanupErr))
				parent := t.TempDir()
				output := filepath.Join(parent, "result.zip")
				warnings := 0
				_, err = logging.Export(context.Background(), logging.ExportRequest{
					Now: time.Now(), Range: logging.ExportRange{Kind: logging.RangeAll}, OutputPath: output, PrivateFS: fs,
					Paths: logging.ExportPaths{LogDir: paths.LogDir, ExportDir: paths.LogExportDir, DaemonLog: paths.DaemonLog, TUILog: paths.TUILog, MihomoLog: paths.MihomoLog},
					OnWarning: func(warning error) {
						warnings++
						if warning.Error() != "log export cleanup incomplete" {
							t.Error("raw cleanup warning escaped")
						}
					},
				})
				if !errors.Is(err, hardenErr) || (cleanupFails && !errors.Is(err, cleanupErr)) || err.Error() != "log export failed" {
					t.Fatalf("lost causes or stable primary outcome: %v", err)
				}
				if errors.Is(err, platform.ErrPublishCleanupIncomplete) != cleanupFails {
					t.Error("cleanup classification does not match initial cleanup result")
				}
				wantWarnings := 0
				if cleanupFails {
					wantWarnings = 1
				}
				if warnings != wantWarnings {
					t.Errorf("warnings=%d want %d after factory cleanup", warnings, wantWarnings)
				}
				if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("failed export published target: %v", err)
				}
				entries, err := os.ReadDir(parent)
				if err != nil || len(entries) != 0 {
					t.Fatalf("final held-workspace cleanup did not recover: entries=%d err=%v", len(entries), err)
				}
			})
		}
	}
}
