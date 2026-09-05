//go:build windows

package logging

import (
	"archive/zip"
	"context"
	"testing"
	"time"
)

func TestExport_WindowsSuccessfulArchiveHasNoWarnings(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:30:00Z","msg":"ok"}`)
	var warnings []error
	result, err := Export(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, AutoNumber: true, Paths: paths, PrivateFS: fs, Redactor: NewRedactor(), OnWarning: func(err error) { warnings = append(warnings, err) }})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if len(archive.File) != 2 {
		t.Fatalf("entries=%d", len(archive.File))
	}
	if len(warnings) != 0 {
		t.Fatalf("normal successful export warnings: %v", warnings)
	}
}
