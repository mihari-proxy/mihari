package logging

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestRotatorOriginal_WriteCloseFailureUsesIndependentReporter(t *testing.T) {
	w, _, _ := openTestRotator(t, DefaultConfig())
	cause := errors.New("fixture close output")
	var reports []error
	w.reporter = scanFailureReporter(func(_ FailureClass, err error) { reports = append(reports, err) })
	w.closeOutput = func(f *os.File) error { return errors.Join(f.Close(), cause) }
	n, err := w.Write([]byte("fixture\n"))
	if n != len("fixture\n") || !errors.Is(err, cause) || len(reports) != 1 || !errors.Is(reports[0], cause) {
		t.Fatalf("close failure dropped: bytes=%d error=%v reports=%d", n, err, len(reports))
	}
	w.closeOutput = nil
}

type scanFailureReporter func(FailureClass, error)

func (f scanFailureReporter) Report(class FailureClass, err error) { f(class, err) }

type scanShortWriter struct{}

func (scanShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

type scanReadWithData struct{ err error }

func (r scanReadWithData) Read(p []byte) (int, error) { return copy(p, []byte("fixture")), r.err }

func TestExportOriginal_CopyKeepsReadFailureAlongsideWriteOrCancel(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "write"
		if cancelled {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			cause := errors.New("fixture source read failure")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var destination io.Writer = scanShortWriter{}
			if cancelled {
				destination = scanCancelWriter{cancel: cancel}
			}
			err := copySpool(ctx, scanReadWithData{cause}, destination, exportOps{Checkpoint: func(exportStage) error { return nil }})
			if !errors.Is(err, cause) {
				t.Fatal("source read failure overwritten")
			}
			other := error(io.ErrShortWrite)
			if cancelled {
				other = context.Canceled
			}
			if !errors.Is(err, other) {
				t.Fatal("write/cancellation cause lost")
			}
		})
	}
}

type scanCancelWriter struct{ cancel context.CancelFunc }

func (w scanCancelWriter) Write(p []byte) (int, error) { w.cancel(); return len(p), nil }

func TestExportOriginal_ShortWriteIsFailure(t *testing.T) {
	for _, kind := range []string{"json", "zip-copy", "source-copy"} {
		t.Run(kind, func(t *testing.T) {
			var err error
			switch kind {
			case "json":
				_, err = exportJSON(t.Context(), strings.NewReader("{\"time\":\"2026-09-02T10:00:00Z\"}\n"), scanShortWriter{}, ExportRange{Kind: RangeAll}, nil)
			case "zip-copy":
				err = copySpool(t.Context(), strings.NewReader("fixture"), scanShortWriter{}, exportOps{Checkpoint: func(exportStage) error { return nil }})
			case "source-copy":
				src := &memorySnapshotSource{records: []memoryRecord{{payload: []byte("{}")}}}
				reader, _ := src.Open(t.Context(), SnapshotWindow{})
				var total int64
				err = copySourceReader(t.Context(), exportOps{Checkpoint: func(exportStage) error { return nil }}, reader, scanShortWriter{}, &total)
			}
			if !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("short write hidden: %v", err)
			}
		})
	}
}

func TestExportOriginal_SuccessWarningsKeepCleanupCause(t *testing.T) {
	for _, assembled := range []bool{false, true} {
		for _, stage := range []string{"publish", "cleanup"} {
			t.Run(fmtScanCase(assembled, stage), func(t *testing.T) {
				fs, paths := openExportTestFS(t)
				writeExportFixture(t, fs, paths.DaemonLog, `{"time":"2026-09-02T10:00:00Z","msg":"fixture"}`)
				cause := errors.New("fixture /private/archive cleanup")
				var warnings []error
				request := ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(t.TempDir(), "export.zip"), Paths: paths, PrivateFS: fs, OnWarning: func(err error) { warnings = append(warnings, err) }}
				ops := exportOps{}
				if stage == "publish" {
					ops.Publish = func(d *platform.PublishDir, w *platform.PublishWorkspace, temp, target string, warning func(error)) error {
						if err := d.PublishNoReplace(w, temp, target, warning); err != nil {
							return err
						}
						warning(cause)
						return nil
					}
				} else {
					ops.CloseWorkspace = func(w *platform.PublishWorkspace) error { return errors.Join(w.Close(), cause) }
				}
				var result ExportResult
				var err error
				if assembled {
					source := NewFileSnapshotSource(DaemonSource, MachineSnapshotOptions{PrivateFS: fs, Paths: paths})
					result, err = assembleWithOps(t.Context(), request, ExportScopeMachineAndCurrentUser, []NamedSource{{ID: DaemonSource, Source: source}}, nil, ops)
				} else {
					result, err = exportWithOps(t.Context(), request, ops)
				}
				if err != nil || result.Path == "" {
					t.Fatalf("warning changed published result: %v", err)
				}
				if len(warnings) != 1 || !errors.Is(warnings[0], cause) || warnings[0].Error() != "log export cleanup incomplete" {
					t.Fatalf("original warning cause lost: warnings=%v", warnings)
				}
			})
		}
	}
}

func fmtScanCase(assembled bool, stage string) string {
	if assembled {
		return "assemble/" + stage
	}
	return "export/" + stage
}

type scanFailedSource struct {
	readErr, finishErr, closeErr error
	closes                       int
}

func (s *scanFailedSource) Open(context.Context, SnapshotWindow) (SourceReader, error) { return s, nil }
func (s *scanFailedSource) Next(context.Context) ([]byte, bool, error)                 { return nil, false, s.readErr }
func (s *scanFailedSource) Finish(context.Context) (SourceStats, error) {
	return SourceStats{}, s.finishErr
}
func (s *scanFailedSource) Close() error { s.closes++; return s.closeErr }

func TestAssembleOriginal_ReadFinishAndCloseCausesSurvive(t *testing.T) {
	fs, paths := openExportTestFS(t)
	source := &scanFailedSource{readErr: errors.New("fixture read"), finishErr: errors.New("fixture finish"), closeErr: errors.New("fixture close")}
	_, err := Assemble(t.Context(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(t.TempDir(), "failed.zip"), Paths: paths, PrivateFS: fs}, ExportScopeCurrentUserOnly, []NamedSource{{ID: TUISource, Source: source}}, nil)
	if !errors.Is(err, source.readErr) || !errors.Is(err, source.finishErr) || !errors.Is(err, source.closeErr) || source.closes != 1 {
		t.Fatalf("source cause lost: %v closes=%d", err, source.closes)
	}
}

func TestExportOriginal_TargetOpenCauseSurvives(t *testing.T) {
	fs, paths := openExportTestFS(t)
	_, err := Export(t.Context(), ExportRequest{Now: time.Now(), Range: ExportRange{Kind: RangeAll}, OutputPath: filepath.Join(t.TempDir(), "missing", "export.zip"), Paths: paths, PrivateFS: fs})
	if !errors.Is(err, ErrInvalidExportRequest) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target cause lost: %v", err)
	}
	if err.Error() != ErrInvalidExportRequest.Error() {
		t.Fatal("public classification changed")
	}
}

func TestRotatorOriginal_OpenFailureRetainsLockCleanup(t *testing.T) {
	for _, phase := range []string{"lock", "unlock"} {
		t.Run(phase, func(t *testing.T) {
			fs, paths := openExportTestFS(t)
			primary, cleanup := errors.New("fixture acquire"), errors.New("fixture lock close")
			lock := &diagnosticErrorLock{closeErr: cleanup}
			if phase == "lock" {
				lock.lockErr = primary
			} else {
				lock.unlockErr = primary
			}
			_, err := OpenRotatingWriter(t.Context(), RotatorOptions{PrivateFS: fs, BasePath: paths.DaemonLog, Config: DefaultConfig(), OpenLock: func(*platform.PrivateFS, string) (platform.AdvisoryLock, error) { return lock, nil }})
			if !errors.Is(err, primary) || !errors.Is(err, cleanup) || lock.closes != 1 {
				t.Fatalf("lock cleanup cause lost: %v closes=%d", err, lock.closes)
			}
		})
	}
}
