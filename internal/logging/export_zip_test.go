package logging

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestExport_ZipLayoutManifestAndRedaction(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, strings.Join([]string{
		`{"time":"2026-09-02T10:30:00Z","token":"secret","seq":1}`,
		`{"time":"2026-09-02T13:00:00Z","seq":2}`,
		`{broken}`,
	}, "\n"))
	writeExportFixture(t, fs, paths.MihomoLog, `{"time":"2026-09-02T11:00:00Z","msg":"ok"}`)
	now := time.Date(2026, 9, 2, 23, 41, 8, 0, time.FixedZone("UTC+8", 8*60*60))
	out := filepath.Join(t.TempDir(), "chosen.zip")
	result, err := Export(context.Background(), ExportRequest{
		Now: now, Range: ExportRange{Kind: RangeBetween, From: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)},
		OutputPath: out, Paths: paths, PrivateFS: fs, Redactor: NewRedactor(),
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if result.Path != out {
		t.Fatalf("Path=%q want %q", result.Path, out)
	}
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got := map[string]string{}
	for _, f := range r.File {
		if f.Method != zip.Deflate {
			t.Fatalf("%s method=%d want Deflate", f.Name, f.Method)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		got[f.Name] = string(b)
	}
	if len(got) != 3 || got[exportManifestEntry] == "" || got[exportDaemonEntry] == "" || got[exportMihomoEntry] == "" {
		t.Fatalf("entries=%v", got)
	}
	if _, ok := got[exportTUIEntry]; ok {
		t.Fatal("empty TUI source must be omitted")
	}
	if strings.Contains(got[exportDaemonEntry], "secret") || !strings.HasSuffix(got[exportDaemonEntry], "\n") {
		t.Fatalf("daemon=%q", got[exportDaemonEntry])
	}
	var manifest exportManifest
	if err := json.Unmarshal([]byte(got[exportManifestEntry]), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 2 || manifest.Files[0].Name != exportDaemonEntry || manifest.Files[0].Lines != 1 || manifest.Files[0].SkippedInvalid != 1 || manifest.Files[0].Redacted != 1 || manifest.Files[1].Name != exportMihomoEntry {
		t.Fatalf("manifest=%+v", manifest)
	}
	if strings.Contains(got[exportManifestEntry], paths.LogDir) || strings.Contains(got[exportManifestEntry], ".lock") {
		t.Fatalf("manifest leaked path: %s", got[exportManifestEntry])
	}
}

func TestExport_NoMatchingLinesDoesNotPublish(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T09:00:00Z"}`)
	out := filepath.Join(t.TempDir(), "none.zip")
	_, err := Export(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeBetween, From: time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)}, OutputPath: out, Paths: paths, PrivateFS: fs, Redactor: NewRedactor()})
	if !errors.Is(err, ErrNoLogLines) {
		t.Fatalf("error=%v want ErrNoLogLines", err)
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists/error=%v", err)
	}
}

func TestExportWithOps_FailureBeforePublishLeavesNoTarget(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	for _, stage := range []exportStage{stageEnumerate, stageReadBatch, stageDecodeLine, stageWriteSpool, stageWriteZip, stageBeforeZipClose, stageBeforeSync, stageBeforePublish} {
		t.Run(fmt.Sprint(stage), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "failed.zip")
			injected := errors.New("injected")
			_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs, Redactor: NewRedactor()}, exportOps{Checkpoint: func(s exportStage) error {
				if s == stage {
					return injected
				}
				return nil
			}})
			if err == nil {
				t.Fatal("expected failure")
			}
			if _, statErr := os.Lstat(out); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("target exists/error=%v", statErr)
			}
		})
	}
}

func TestExport_ManifestUsesFixedSourceNames(t *testing.T) {
	fs, paths := openExportTestFS(t)
	paths.DaemonLog = filepath.Join(paths.LogDir, "customer-secret-name.log")
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	out := filepath.Join(t.TempDir(), "fixed.zip")
	if _, err := Export(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != exportManifestEntry {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "customer-secret-name") {
			t.Fatalf("manifest leaked configured basename: %s", b)
		}
		if !strings.Contains(string(b), `"sources":["mihari-daemon.log"]`) {
			t.Fatalf("manifest source mapping=%s", b)
		}
	}
}

func TestExportWithOps_PostPublishCancellationReturnsSuccess(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	out := filepath.Join(t.TempDir(), "committed.zip")
	ctx, cancel := context.WithCancel(context.Background())
	result, err := exportWithOps(ctx, ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}, exportOps{Publish: func(d *platform.PublishDir, w *platform.PublishWorkspace, temp, target string, warning func(error)) error {
		err := d.PublishNoReplace(w, temp, target, warning)
		cancel()
		return err
	}})
	if err != nil || result.Path != out {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestExportWithOps_PublishCollisionPolicy(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	t.Run("custom", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "chosen.zip")
		_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}, exportOps{Publish: func(*platform.PublishDir, *platform.PublishWorkspace, string, string, func(error)) error {
			return os.ErrExist
		}})
		if !errors.Is(err, ErrExportTargetExists) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("automatic retries same archive", func(t *testing.T) {
		calls := 0
		var names []string
		ops := exportOps{Publish: func(d *platform.PublishDir, w *platform.PublishWorkspace, temp, target string, warning func(error)) error {
			calls++
			names = append(names, target)
			if calls < 3 {
				return os.ErrExist
			}
			return d.PublishNoReplace(w, temp, target, warning)
		}}
		now := time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC)
		result, err := exportWithOps(context.Background(), ExportRequest{Now: now, Range: ExportRange{Kind: RangeAll}, AutoNumber: true, Paths: paths, PrivateFS: fs}, ops)
		if err != nil {
			t.Fatal(err)
		}
		if calls != 3 || !strings.HasSuffix(result.Path, "-2.zip") || names[2] != filepath.Base(result.Path) {
			t.Fatalf("calls=%d names=%v result=%+v", calls, names, result)
		}
	})
}

func TestExportWithOps_CloseSyncAndPublishErrorsDoNotPublish(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	tests := []struct {
		name string
		ops  exportOps
	}{
		{"zip close", exportOps{NewZipWriter: func(w io.Writer) zipWriter { return &failingCloseZipWriter{Writer: zip.NewWriter(w)} }}},
		{"sync", exportOps{Sync: func(*os.File) error { return errors.New("sync") }}},
		{"publish", exportOps{Publish: func(*platform.PublishDir, *platform.PublishWorkspace, string, string, func(error)) error {
			return errors.New("publish")
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "failed.zip")
			if _, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}, tt.ops); err == nil {
				t.Fatal("expected error")
			}
			if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("target exists/error=%v", err)
			}
		})
	}
}

type failingCloseZipWriter struct{ *zip.Writer }

func (w *failingCloseZipWriter) Close() error { _ = w.Writer.Close(); return errors.New("close") }

func writeExportFixture(t *testing.T, fs interface {
	OpenAppend(string) (*os.File, error)
}, path, content string) {
	t.Helper()
	f, err := fs.OpenAppend(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
