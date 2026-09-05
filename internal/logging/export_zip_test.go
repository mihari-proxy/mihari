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
	"reflect"
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
	writeExportFixture(t, fs, paths.TUILog, `{"time":"2026-09-02T09:00:00Z","msg":"outside"}`)
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
	var order []string
	for _, f := range r.File {
		order = append(order, f.Name)
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
	if strings.Join(order, ",") != "manifest.json,daemon/mihari-daemon.log,mihomo/mihomo.log" {
		t.Fatalf("entry order=%v", order)
	}
	if _, ok := got[exportTUIEntry]; ok {
		t.Fatal("empty TUI source must be omitted")
	}
	if strings.Contains(got[exportDaemonEntry], "secret") || !strings.HasSuffix(got[exportDaemonEntry], "\n") {
		t.Fatalf("daemon=%q", got[exportDaemonEntry])
	}
	if got[exportDaemonEntry] != `{"seq":1,"time":"2026-09-02T10:30:00Z","token":"***"}`+"\n" {
		t.Fatalf("daemon JSONL=%q", got[exportDaemonEntry])
	}
	if got[exportMihomoEntry] != `{"msg":"ok","time":"2026-09-02T11:00:00Z"}`+"\n" {
		t.Fatalf("mihomo JSONL=%q", got[exportMihomoEntry])
	}
	var manifest map[string]any
	decoder := json.NewDecoder(strings.NewReader(got[exportManifestEntry]))
	decoder.UseNumber()
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{
		"schema": "mihari-logs-export/v1", "exported_at": "2026-09-02T23:41:08+08:00", "timezone": "+08:00",
		"range": map[string]any{"kind": "between", "from": "2026-09-02T10:00:00Z", "to": "2026-09-02T12:00:00Z"},
		"files": []any{
			map[string]any{"name": exportDaemonEntry, "lines": json.Number("1"), "skipped_invalid": json.Number("1"), "redacted": json.Number("1"), "sources": []any{"mihari-daemon.log"}},
			map[string]any{"name": exportMihomoEntry, "lines": json.Number("1"), "skipped_invalid": json.Number("0"), "redacted": json.Number("0"), "sources": []any{"mihomo.log"}},
		},
		"notes": []any{exportReviewNote},
	}
	if !reflect.DeepEqual(manifest, expected) {
		t.Fatalf("manifest=%#v want %#v", manifest, expected)
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

func TestExportWithOps_PublicErrorsAreStableAndClassified(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	sensitive := errors.New(`open C:\Users\secret\token-value.log: api-key=very-secret`)
	out := filepath.Join(t.TempDir(), "failed.zip")
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}, exportOps{Checkpoint: func(stage exportStage) error {
		if stage == stageBeforePublish {
			return sensitive
		}
		return nil
	}})
	if !errors.Is(err, sensitive) {
		t.Fatalf("classification lost: %v", err)
	}
	if err.Error() != "log export failed" {
		t.Fatalf("public error leaked cause: %q", err)
	}
}

func TestExportWithOps_CancellationBeforePublishCleansResources(t *testing.T) {
	for _, stage := range []exportStage{stageEnumerate, stageReadBatch, stageDecodeLine, stageWriteSpool, stageWriteZip, stageBeforeZipClose, stageBeforeSync, stageBeforePublish} {
		t.Run(fmt.Sprint(stage), func(t *testing.T) {
			fs, paths := openExportTestFS(t)
			writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
			parent := filepath.Join(t.TempDir(), "publish")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(parent, "cancelled.zip")
			ctx, cancel := context.WithCancel(context.Background())
			var observedTarget *exportTarget
			var observedWorkspace *platform.PublishWorkspace
			var snapshots []*os.File
			logDirCloses := 0
			_, err := exportWithOps(ctx, ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}, exportOps{Checkpoint: func(got exportStage) error {
				if got == stage {
					cancel()
				}
				return nil
			}, Observe: func(target *exportTarget, workspace *platform.PublishWorkspace) {
				observedTarget, observedWorkspace = target, workspace
			}, Snapshots: func(handles []snapshotHandle) {
				for _, handle := range handles {
					snapshots = append(snapshots, handle.file)
				}
			}, CloseLogDir: func(dir *platform.DirectoryIdentity) error { logDirCloses++; return dir.Close() }})
			if !errors.Is(err, context.Canceled) || err.Error() != "log export cancelled" {
				t.Fatalf("error=%q", err)
			}
			entries, readErr := os.ReadDir(parent)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("resources remain: %v", entries)
			}
			if err := os.Remove(parent); err != nil {
				t.Fatalf("publish directory still held: %v", err)
			}
			if observedTarget == nil || observedWorkspace == nil || logDirCloses != 1 {
				t.Fatalf("ownership observations target=%p workspace=%p log closes=%d", observedTarget, observedWorkspace, logDirCloses)
			}
			if _, _, closeErr := observedWorkspace.CreateTemp("closed-*"); !errors.Is(closeErr, os.ErrClosed) {
				t.Fatalf("workspace remains open: %v", closeErr)
			}
			if _, closeErr := observedTarget.Dir.Exists("closed.zip"); !errors.Is(closeErr, os.ErrClosed) {
				t.Fatalf("PublishDir remains open: %v", closeErr)
			}
			for _, snapshot := range snapshots {
				if _, statErr := snapshot.Stat(); statErr == nil {
					t.Fatalf("snapshot remains open: %v", statErr)
				}
			}
		})
	}
}

func TestExportWithOps_CleanupFailureContinuesAndClosesCapabilities(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	parent := filepath.Join(t.TempDir(), "publish")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := errors.New("primary C:\\secret\\token.log")
	cleanup := errors.New("remove C:\\secret\\spool.tmp")
	var workspace *platform.PublishWorkspace
	var target *exportTarget
	var order []string
	var warnings []error
	removeCalls := 0
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(parent, "failed.zip"), Paths: paths, PrivateFS: fs, OnWarning: func(err error) { warnings = append(warnings, err) }}, exportOps{
		Checkpoint: func(stage exportStage) error {
			if stage == stageBeforePublish {
				return primary
			}
			return nil
		},
		Observe: func(gotTarget *exportTarget, gotWorkspace *platform.PublishWorkspace) {
			target, workspace = gotTarget, gotWorkspace
		},
		Remove: func(w *platform.PublishWorkspace, name string) error {
			removeCalls++
			order = append(order, "remove")
			if removeCalls == 1 {
				return cleanup
			}
			return w.Remove(name)
		},
		CloseWorkspace:  func(w *platform.PublishWorkspace) error { order = append(order, "workspace"); return w.Close() },
		ClosePublishDir: func(d *platform.PublishDir) error { order = append(order, "publish-dir"); return d.Close() },
		CloseLogDir:     func(d *platform.DirectoryIdentity) error { order = append(order, "log-dir"); return d.Close() },
	})
	if !errors.Is(err, primary) || !errors.Is(err, cleanup) || err.Error() != "log export failed" {
		t.Fatalf("error=%q primary=%v cleanup=%v", err, errors.Is(err, primary), errors.Is(err, cleanup))
	}
	if strings.Join(order[len(order)-3:], ",") != "workspace,publish-dir,log-dir" || removeCalls < 2 {
		t.Fatalf("cleanup order=%v removeCalls=%d", order, removeCalls)
	}
	if len(warnings) != 1 || warnings[0].Error() != "log export cleanup incomplete" {
		t.Fatalf("warnings=%v", warnings)
	}
	if _, _, closeErr := workspace.CreateTemp("closed-*"); !errors.Is(closeErr, os.ErrClosed) {
		t.Fatalf("workspace open: %v", closeErr)
	}
	if _, closeErr := target.Dir.Exists("closed.zip"); !errors.Is(closeErr, os.ErrClosed) {
		t.Fatalf("publish dir open: %v", closeErr)
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("residue=%v error=%v", entries, readErr)
	}
}

func TestExportWithOps_CancellationDuringMultiChunkZipCopyReturnsPromptly(t *testing.T) {
	fs, paths := openExportTestFS(t)
	line := `{"time":"2026-09-02T10:00:00Z","msg":"` + strings.Repeat("x", 4096) + `"}`
	writeExportFixture(t, fs, paths.DaemonLog, strings.Repeat(line+"\n", 40))
	parent := filepath.Join(t.TempDir(), "publish")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	chunks := 0
	start := time.Now()
	_, err := exportWithOps(ctx, ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(parent, "cancelled.zip"), Paths: paths, PrivateFS: fs}, exportOps{Checkpoint: func(stage exportStage) error {
		if stage == stageWriteZip {
			chunks++
			if chunks == 2 {
				cancel()
			}
		}
		return nil
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation took %v", time.Since(start))
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("resources remain: %v", entries)
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
		competitor := []byte("competitor-must-survive")
		_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: out, Paths: paths, PrivateFS: fs}, exportOps{Publish: func(d *platform.PublishDir, w *platform.PublishWorkspace, temp, target string, warning func(error)) error {
			if err := os.WriteFile(out, competitor, 0o600); err != nil {
				return err
			}
			return d.PublishNoReplace(w, temp, target, warning)
		}})
		if !errors.Is(err, ErrExportTargetExists) {
			t.Fatalf("error=%v", err)
		}
		got, readErr := os.ReadFile(out)
		if readErr != nil || string(got) != string(competitor) {
			t.Fatalf("competitor=%q error=%v", got, readErr)
		}
	})
	t.Run("automatic retries same archive", func(t *testing.T) {
		calls := 0
		var names []string
		var firstDir *platform.PublishDir
		var firstWorkspace *platform.PublishWorkspace
		var firstTemp string
		ops := exportOps{Publish: func(d *platform.PublishDir, w *platform.PublishWorkspace, temp, target string, warning func(error)) error {
			calls++
			names = append(names, target)
			if calls == 1 {
				firstDir, firstWorkspace, firstTemp = d, w, temp
			} else if d != firstDir || w != firstWorkspace || temp != firstTemp {
				t.Error("collision retry replaced held capabilities or archive temp")
			}
			if calls < 3 {
				if err := os.WriteFile(filepath.Join(d.Path(), target), []byte(fmt.Sprintf("competitor-%d", calls)), 0o600); err != nil {
					return err
				}
				return d.PublishNoReplace(w, temp, target, warning)
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
		for i := 0; i < 2; i++ {
			got, err := os.ReadFile(filepath.Join(paths.ExportDir, names[i]))
			if err != nil || string(got) != fmt.Sprintf("competitor-%d", i+1) {
				t.Fatalf("competitor %d=%q error=%v", i, got, err)
			}
		}
	})
}

func TestExportWithOps_VisibleParentReplacementDoesNotRedirectPublish(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	base := t.TempDir()
	parent, held := filepath.Join(base, "publish"), filepath.Join(base, "held")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(parent, "result.zip"), Paths: paths, PrivateFS: fs}, exportOps{Checkpoint: func(stage exportStage) error {
		if stage != stageBeforePublish {
			return nil
		}
		if err := os.Rename(parent, held); err != nil {
			t.Skipf("platform guard prevented parent replacement: %v", err)
		}
		return os.Mkdir(parent, 0o700)
	}})
	if !errors.Is(err, platform.ErrPublishDirectoryChanged) {
		t.Fatalf("error=%v", err)
	}
	for _, path := range []string{filepath.Join(parent, "result.zip"), filepath.Join(held, "result.zip")} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("unexpected target %s: %v", path, statErr)
		}
	}
	entries, readErr := os.ReadDir(held)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("held resources=%v error=%v", entries, readErr)
	}
}

func TestExportWithOps_FreshContainmentCheckBlocksMovedPublishDir(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	parent := filepath.Join(t.TempDir(), "publish")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(paths.LogDir, "moved-publish")
	publishCalled := false
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(parent, "result.zip"), Paths: paths, PrivateFS: fs}, exportOps{
		Checkpoint: func(stage exportStage) error {
			if stage == stageBeforePublish {
				if renameErr := os.Rename(parent, moved); renameErr != nil {
					t.Skipf("platform guard prevented containment move: %v", renameErr)
				}
			}
			return nil
		},
		Publish: func(*platform.PublishDir, *platform.PublishWorkspace, string, string, func(error)) error {
			publishCalled = true
			return nil
		},
	})
	if !errors.Is(err, ErrExportTargetChanged) || publishCalled {
		t.Fatalf("error=%v publishCalled=%v", err, publishCalled)
	}
	entries, readErr := os.ReadDir(moved)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("moved resources=%v error=%v", entries, readErr)
	}
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

func TestExportWithOps_SecondaryZipCloseErrorIsClassifiedButSanitized(t *testing.T) {
	fs, paths := openExportTestFS(t)
	writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z"}`)
	primary := errors.New(`create header C:\private\token.json`)
	secondary := errors.New(`close C:\private\secret.zip`)
	warnings := []error{}
	_, err := exportWithOps(context.Background(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(t.TempDir(), "failed.zip"), Paths: paths, PrivateFS: fs, OnWarning: func(err error) { warnings = append(warnings, err) }}, exportOps{NewZipWriter: func(io.Writer) zipWriter {
		return failingCreateZipWriter{primary: primary, secondary: secondary}
	}})
	if !errors.Is(err, primary) || !errors.Is(err, secondary) {
		t.Fatalf("secondary classification lost: %v", err)
	}
	if err.Error() != "log export failed" {
		t.Fatalf("error leaked sensitive cause: %q", err)
	}
	if len(warnings) != 1 || warnings[0].Error() != "log export cleanup incomplete" {
		t.Fatalf("warnings=%v", warnings)
	}
}

type failingCreateZipWriter struct{ primary, secondary error }

func (w failingCreateZipWriter) CreateHeader(*zip.FileHeader) (io.Writer, error) {
	return nil, w.primary
}
func (w failingCreateZipWriter) Close() error { return w.secondary }

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
