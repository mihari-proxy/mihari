package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/platform"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalTaskDiagnostics_ExportBindsActualExecution(t *testing.T) {
	var operation logging.OperationMetadata
	r := newExportRunner(context.Background(), func(ctx context.Context, _ logging.ExportRequest) (logging.ExportResult, error) {
		operation, _ = logging.OperationFromContext(ctx)
		return logging.ExportResult{}, nil
	})
	results, ok := r.Start(4, logging.ExportRequest{})
	if !ok {
		t.Fatal("export refused")
	}
	<-results
	r.Wait()
	if operation.ID == "" || operation.Name != "logs.export" {
		t.Fatalf("missing export operation: %+v", operation)
	}
}

func exportDiagnosticReporter(out io.Writer) diagnostics.Reporter {
	redactor := logging.NewRedactor()
	level := &slog.LevelVar{}
	level.Set(slog.LevelDebug)
	return logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(out, level, "tui", redactor)), redactor)
}
func TestLocalTaskDiagnostics_ExportFailureWarningAndCancellation(t *testing.T) {
	for _, mode := range []string{"failure", "warning", "cancel", "deadline", "panic", "id-failure", "nil-reporter"} {
		t.Run(mode, func(t *testing.T) {
			var out bytes.Buffer
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := &os.PathError{Op: "open", Path: "/private/export-token", Err: os.ErrPermission}
			r := newExportRunner(ctx, func(ctx context.Context, req logging.ExportRequest) (logging.ExportResult, error) {
				switch mode {
				case "warning":
					req.OnWarning(cause)
					req.OnWarning(cause)
					return logging.ExportResult{Path: "/private/archive.zip"}, nil
				case "cancel":
					cancel()
					return logging.ExportResult{}, ctx.Err()
				case "deadline":
					return logging.ExportResult{}, context.DeadlineExceeded
				case "panic":
					panic("secret-panic")
				case "id-failure":
					return logging.ExportResult{Path: "/private/archive.zip"}, nil
				default:
					return logging.ExportResult{}, cause
				}
			})
			r.diagnostics = LocalTaskDiagnostics{Reporter: exportDiagnosticReporter(&out), NewID: func() (string, error) {
				if mode == "id-failure" {
					return "", errors.New("secret-id-error")
				}
				return "export-own", nil
			}}
			if mode == "nil-reporter" {
				r.diagnostics.Reporter = nil
			}
			results, ok := r.Start(8, logging.ExportRequest{})
			if !ok {
				t.Fatal("export refused")
			}
			result := <-results
			r.Wait()
			if result.Generation != 8 || (mode == "warning" && (!result.Warning || result.Err != nil)) || (mode == "id-failure" && result.Err != nil) {
				t.Fatalf("result semantics changed: %+v", result)
			}
			if strings.Contains(out.String(), "/private/") || strings.Contains(out.String(), "secret-") {
				t.Fatalf("unsafe diagnostic: %s", out.String())
			}
			if mode == "cancel" || mode == "nil-reporter" {
				if out.Len() != 0 {
					t.Fatalf("unexpected diagnostics: %s", out.String())
				}
				return
			}
			var record map[string]any
			decoder := json.NewDecoder(&out)
			if err := decoder.Decode(&record); err != nil {
				t.Fatalf("missing export diagnostic: %v", err)
			}
			if record["operation"] != "logs.export" {
				t.Fatalf("wrong export operation: %+v", record)
			}
			if mode == "id-failure" {
				if record["operation_id"] != nil || record["msg"] != "local_task.id_generation_failed" {
					t.Fatalf("wrong ID failure: %+v", record)
				}
			} else if record["operation_id"] != "export-own" {
				t.Fatalf("wrong correlation: %+v", record)
			}
			if mode == "warning" && record["level"] != "WARN" {
				t.Fatalf("lost warning severity: %+v", record)
			}
			if decoder.Decode(&record) != io.EOF {
				t.Fatal("duplicate export diagnostic")
			}
		})
	}
}

func TestLocalTaskDiagnostics_ExportExpectedOutcomesAreNotErrors(t *testing.T) {
	for _, cause := range []error{logging.ErrNoLogLines, logging.ErrExportTargetExists, logging.ErrInvalidExportRequest, logging.ErrExportTargetChanged} {
		t.Run(cause.Error(), func(t *testing.T) {
			var logs bytes.Buffer
			r := newExportRunner(context.Background(), func(context.Context, logging.ExportRequest) (logging.ExportResult, error) {
				return logging.ExportResult{}, cause
			})
			r.diagnostics.Reporter = exportDiagnosticReporter(&logs)
			results, _ := r.Start(1, logging.ExportRequest{})
			result := <-results
			r.Wait()
			if !errors.Is(result.Err, cause) {
				t.Fatal("expected outcome changed")
			}
			var record map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil || record["level"] != "DEBUG" {
				t.Fatalf("expected export outcome recorded as operational failure: %+v %v", record, err)
			}
		})
	}
}

func TestLocalTaskDiagnostics_ExportResultsHaveIndependentValues(t *testing.T) {
	parent := logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "lifecycle", Name: "logs.export"})
	r := newExportRunner(parent, func(context.Context, logging.ExportRequest) (logging.ExportResult, error) {
		return logging.ExportResult{}, nil
	})
	first, _ := r.Start(10, logging.ExportRequest{})
	a := <-first
	r.Wait()
	second, _ := r.Start(11, logging.ExportRequest{})
	b := <-second
	r.Wait()
	if a.Operation.ID == "" || a.Operation.ID == "lifecycle" || a.Operation.ID == b.Operation.ID || a.Generation != 10 || b.Generation != 11 {
		t.Fatalf("result identity mixed: %+v %+v", a, b)
	}
}

func TestLocalTaskDiagnostics_NativeExportRejectionsRemainDebug(t *testing.T) {
	for _, kind := range []string{"invalid-range", "existing-target", "no-lines"} {
		t.Run(kind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "data")
			paths := platform.NewPaths(root)
			fs, err := platform.NewPrivateFS(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := fs.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := fs.EnsureDir(paths.LogDir); err != nil {
				t.Fatal(err)
			}
			request := logging.ExportRequest{Now: exportTestNow(), Range: logging.ExportRange{Kind: logging.RangeAll}, AutoNumber: true, PrivateFS: fs, Paths: logging.ExportPaths{LogDir: paths.LogDir, ExportDir: paths.LogExportDir, DaemonLog: paths.DaemonLog, TUILog: paths.TUILog, MihomoLog: paths.MihomoLog}}
			wantCause := logging.ErrNoLogLines
			switch kind {
			case "invalid-range":
				request.Range.Kind = "unsupported"
				wantCause = logging.ErrInvalidExportRequest
			case "existing-target":
				request.AutoNumber = false
				request.OutputPath = filepath.Join(t.TempDir(), "existing.zip")
				if err := os.WriteFile(request.OutputPath, []byte("existing archive"), 0600); err != nil {
					t.Fatal(err)
				}
				wantCause = logging.ErrExportTargetExists
			}
			var logs bytes.Buffer
			r := newExportRunner(context.Background(), logging.Export)
			r.diagnostics = LocalTaskDiagnostics{Reporter: exportDiagnosticReporter(&logs), NewID: func() (string, error) { return "native-export", nil }}
			results, ok := r.Start(7, request)
			if !ok {
				t.Fatal("export refused")
			}
			result := <-results
			r.Wait()
			if !errors.Is(result.Err, wantCause) || result.Result.Path != "" || result.Warning {
				t.Fatalf("unexpected native export result: %+v", result)
			}
			assertExportDiagnosticLevel(t, &logs, "DEBUG", "logs.export.rejected")
			if kind == "existing-target" {
				raw, err := os.ReadFile(request.OutputPath)
				if err != nil || string(raw) != "existing archive" {
					t.Fatal("existing archive changed")
				}
			}
		})
	}
}

func TestLocalTaskDiagnostics_ExportWrapperClassificationPreservesRealCauses(t *testing.T) {
	_, native := logging.Export(context.Background(), logging.ExportRequest{Range: logging.ExportRange{Kind: "unsupported"}})
	if !errors.Is(native, logging.ErrInvalidExportRequest) || native == logging.ErrInvalidExportRequest {
		t.Fatalf("fixture did not obtain actual export wrapper: %T", native)
	}
	wrapped := fmt.Errorf("outer export: %w", native)
	deep := wrapped
	for i := 0; i < 64; i++ {
		deep = fmt.Errorf("wrapped: %w", deep)
	}
	for _, test := range []struct {
		name         string
		cause        error
		level, event string
	}{
		{"transparent wrapper", wrapped, "DEBUG", "logs.export.rejected"},
		{"joined IO", fmt.Errorf("wrapped join: %w", errors.Join(native, io.ErrUnexpectedEOF)), "ERROR", "logs.export.failed"},
		{"joined unknown", errors.Join(native, errors.New("private-extra-cause")), "ERROR", "logs.export.failed"},
		{"over-depth", deep, "ERROR", "logs.export.failed"},
		{"cycle", &exportDiagnosticCycle{}, "ERROR", "logs.export.failed"},
		{"already reported", diagnostics.MarkReported(native), "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			r := newExportRunner(context.Background(), func(context.Context, logging.ExportRequest) (logging.ExportResult, error) {
				return logging.ExportResult{}, test.cause
			})
			r.diagnostics.Reporter = exportDiagnosticReporter(&logs)
			results, _ := r.Start(2, logging.ExportRequest{})
			result := <-results
			r.Wait()
			if result.Err == nil {
				t.Fatal("classification swallowed failure")
			}
			if test.name == "joined IO" && !errors.Is(result.Err, io.ErrUnexpectedEOF) {
				t.Fatal("classification discarded joined IO cause")
			}
			if test.level == "" {
				if logs.Len() != 0 {
					t.Fatal("already-reported rejection duplicated")
				}
				return
			}
			assertExportDiagnosticLevel(t, &logs, test.level, test.event)
		})
	}
}

type exportDiagnosticCycle struct{}

func (*exportDiagnosticCycle) Error() string   { return "cycle" }
func (e *exportDiagnosticCycle) Unwrap() error { return e }

func assertExportDiagnosticLevel(t *testing.T, logs *bytes.Buffer, level, event string) {
	t.Helper()
	var record map[string]any
	decoder := json.NewDecoder(logs)
	if err := decoder.Decode(&record); err != nil || record["level"] != level || record["msg"] != event {
		t.Fatalf("wrong export diagnostic: %+v err=%v want %s %s", record, err, level, event)
	}
	if decoder.Decode(&record) != io.EOF {
		t.Fatal("duplicate export diagnostic")
	}
}

func TestLocalTaskDiagnostics_ExportWithoutReporterDoesNotInspectError(t *testing.T) {
	cause := &opaqueExportDiagnosticError{}
	released := make(chan struct{})
	var canceled <-chan struct{}
	r := newExportRunner(context.Background(), func(ctx context.Context, request logging.ExportRequest) (logging.ExportResult, error) {
		canceled = ctx.Done()
		defer close(released)
		request.OnWarning(io.ErrUnexpectedEOF)
		return logging.ExportResult{Path: "original-result"}, cause
	})
	results, ok := r.Start(13, logging.ExportRequest{})
	if !ok {
		t.Fatal("export refused")
	}
	result := <-results
	r.Wait()
	// Error identity is compared directly: the no-reporter contract must not
	// invoke the opaque error's methods, including from this test assertion.
	if result.Err != cause || result.Result.Path != "original-result" || result.Generation != 13 || !result.Warning {
		t.Fatal("disabled diagnostics changed the export result")
	}
	if cause.unwrapCalls != 0 {
		t.Fatalf("disabled diagnostics inspected opaque error %d times", cause.unwrapCalls)
	}
	select {
	case <-released:
	default:
		t.Fatal("export resources not released")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("worker context not canceled before join")
	}
	if _, open := <-results; open {
		t.Fatal("result channel not closed")
	}
	r.mu.Lock()
	running := r.running
	r.mu.Unlock()
	if running {
		t.Fatal("joined worker still running")
	}
}

type opaqueExportDiagnosticError struct{ unwrapCalls int }

func (*opaqueExportDiagnosticError) Error() string   { return "opaque export error" }
func (e *opaqueExportDiagnosticError) Unwrap() error { e.unwrapCalls++; return nil }

func TestLocalTaskDiagnostics_ExportWithoutReporterPreservesTypedNilError(t *testing.T) {
	var cause *nilExportDiagnosticError
	r := newExportRunner(context.Background(), func(context.Context, logging.ExportRequest) (logging.ExportResult, error) {
		return logging.ExportResult{Path: "original-result"}, cause
	})
	results, ok := r.Start(14, logging.ExportRequest{})
	if !ok {
		t.Fatal("export refused")
	}
	result := <-results
	r.Wait()
	if result.Err != cause || result.Result.Path != "original-result" || result.Generation != 14 {
		t.Fatal("disabled diagnostics changed typed-nil result identity")
	}
	if _, open := <-results; open {
		t.Fatal("result channel not closed")
	}
}

type nilExportDiagnosticError struct{ cause error }

func (*nilExportDiagnosticError) Error() string   { return "opaque nil export error" }
func (e *nilExportDiagnosticError) Unwrap() error { return e.cause }
