package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type coreDiagnosticRecord struct {
	operation logging.OperationMetadata
	record    diagnostics.Record
}

type coreDiagnosticRecorder struct {
	mu      sync.Mutex
	records []coreDiagnosticRecord
}

func (r *coreDiagnosticRecorder) report(ctx context.Context, record diagnostics.Record) {
	operation, _ := logging.OperationFromContext(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, coreDiagnosticRecord{operation: operation, record: record})
}

func (r *coreDiagnosticRecorder) snapshot() []coreDiagnosticRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]coreDiagnosticRecord(nil), r.records...)
}

type coreMaintenanceSupervisor struct {
	*fakeSupervisor
	maintain func(context.Context, func() error) error
}

func (s *coreMaintenanceSupervisor) Maintain(ctx context.Context, work func() error) error {
	return s.maintain(ctx, work)
}

func TestCoreDiagnostic_InstallFailureMatrixKeepsCauseAndSingleOwner(t *testing.T) {
	tests := []struct {
		name    string
		build   func(error) (*Manager, *fakeCandidate)
		execute func(*Manager) error
		wantOp  string
	}{
		{
			name: "prepare",
			build: func(failure error) (*Manager, *fakeCandidate) {
				installer := &fakeInstaller{prepare: func(context.Context, core.InstallRequest) (PreparedCore, error) {
					return nil, failure
				}}
				return newTestManager(Options{Installer: installer}), nil
			},
			execute: func(manager *Manager) error {
				_, err := manager.Install(context.Background(), Operation{ID: "prepare-failure", Source: "test"})
				return err
			},
			wantOp: "core.install",
		},
		{
			name: "commit",
			build: func(failure error) (*Manager, *fakeCandidate) {
				candidate := &fakeCandidate{version: "v1.19.0", commit: func() (core.InstallResult, error) {
					return core.InstallResult{}, failure
				}}
				return newTestManager(Options{Installer: &fakeInstaller{candidate: candidate}}), candidate
			},
			execute: func(manager *Manager) error {
				_, err := manager.Install(context.Background(), Operation{ID: "commit-failure", Source: "test"})
				return err
			},
			wantOp: "core.install",
		},
		{
			name: "restart_after_commit",
			build: func(failure error) (*Manager, *fakeCandidate) {
				candidate := &fakeCandidate{version: "v1.19.0"}
				manager := newTestManager(Options{
					Installer:  &fakeInstaller{candidate: candidate},
					Supervisor: &fakeSupervisor{restart: func(context.Context) error { return failure }},
				})
				manager.running.Store(true)
				return manager, candidate
			},
			execute: func(manager *Manager) error {
				_, err := manager.Install(context.Background(), Operation{ID: "install-restart-failure", Source: "test"})
				return err
			},
			wantOp: "core.install",
		},
		{
			name: "maintenance",
			build: func(failure error) (*Manager, *fakeCandidate) {
				candidate := &fakeCandidate{version: "v1.19.0"}
				supervisor := &coreMaintenanceSupervisor{
					fakeSupervisor: &fakeSupervisor{},
					maintain:       func(context.Context, func() error) error { return failure },
				}
				return newTestManager(Options{
					TrustedCore: &core.TrustedExecution{}, Installer: &fakeInstaller{candidate: candidate}, Supervisor: supervisor,
				}), candidate
			},
			execute: func(manager *Manager) error {
				_, err := manager.Install(context.Background(), Operation{ID: "maintenance-failure", Source: "test"})
				return err
			},
			wantOp: "core.install",
		},
		{
			name: "explicit_restart",
			build: func(failure error) (*Manager, *fakeCandidate) {
				return newTestManager(Options{Supervisor: &fakeSupervisor{restart: func(context.Context) error { return failure }}}), nil
			},
			execute: func(manager *Manager) error {
				return manager.Restart(context.Background(), Operation{ID: "restart-failure", Source: "test"})
			},
			wantOp: "core.restart",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cause := &os.PathError{Op: "open", Path: "/private/core-secret", Err: os.ErrPermission}
			failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: test.name + " failed"}, cause)
			manager, candidate := test.build(failure)
			recorder := &coreDiagnosticRecorder{}
			manager.diagnosticReporter = recorder.report

			err := test.execute(manager)

			var apiError protocol.APIError
			if !errors.Is(err, cause) || !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
				t.Fatalf("failure lost cause or classification: %v", err)
			}
			if !diagnostics.AlreadyReported(err) || strings.Contains(err.Error(), "core-secret") {
				t.Fatalf("owner marker or safe error lost: %v", err)
			}
			records := recorder.snapshot()
			if len(records) != 1 || records[0].operation.Name != test.wantOp || records[0].record.Event != "operation.failed" || records[0].record.Level != slog.LevelError || !errors.Is(records[0].record.Err, cause) {
				t.Fatalf("records=%#v", records)
			}
			if candidate != nil && !candidate.cleaned.Load() {
				t.Fatal("candidate was not cleaned")
			}
		})
	}
}

func TestCoreDiagnostic_NilReporterDoesNotMarkFailure(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: "/private/core-secret", Err: os.ErrPermission}
	failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "prepare failed"}, cause)
	manager := newTestManager(Options{Installer: &fakeInstaller{prepare: func(context.Context, core.InstallRequest) (PreparedCore, error) {
		return nil, failure
	}}})

	_, err := manager.Install(context.Background(), Operation{ID: "nil-reporter", Source: "test"})

	if !errors.Is(err, cause) || diagnostics.AlreadyReported(err) {
		t.Fatalf("nil reporter changed failure ownership: %v", err)
	}
}

func TestCoreDiagnostic_ReportRunsOutsideMutationLock(t *testing.T) {
	cause := &os.PathError{Op: "rename", Path: "/private/core-secret", Err: os.ErrPermission}
	failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "commit failed"}, cause)
	candidate := &fakeCandidate{version: "v1.19.0", commit: func() (core.InstallResult, error) {
		return core.InstallResult{}, failure
	}}
	var manager *Manager
	manager = newTestManager(Options{
		Installer: &fakeInstaller{candidate: candidate},
		DiagnosticReporter: func(context.Context, diagnostics.Record) {
			if err := manager.withMaintenance(context.Background(), func() error { return nil }); err != nil {
				t.Errorf("reporter could not re-enter runtime: %v", err)
			}
		},
	})

	if _, err := manager.Install(context.Background(), Operation{ID: "outside-lock", Source: "test"}); !errors.Is(err, cause) {
		t.Fatalf("install error=%v", err)
	}
}

func TestCoreDiagnostic_SuccessUsesExecutionOperationJSON(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation string
		id        string
		run       func(*Manager) error
		options   Options
	}{
		{
			name: "install", operation: "core.install", id: "install-success",
			options: Options{Installer: &fakeInstaller{candidate: &fakeCandidate{version: "v1.19.0"}}},
			run: func(manager *Manager) error {
				_, err := manager.Install(context.Background(), Operation{ID: "install-success", Source: "test"})
				return err
			},
		},
		{
			name: "restart", operation: "core.restart", id: "restart-success",
			options: Options{Supervisor: &fakeSupervisor{}},
			run: func(manager *Manager) error {
				return manager.Restart(context.Background(), Operation{ID: "restart-success", Source: "test"})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			level := new(slog.LevelVar)
			level.Set(slog.LevelDebug)
			redactor := logging.NewRedactor("core-secret")
			test.options.DiagnosticReporter = logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, level, "daemon", redactor)), redactor)
			manager := newTestManager(test.options)

			if err := test.run(manager); err != nil {
				t.Fatal(err)
			}

			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatalf("diagnostic JSON: %v; output=%s", err, output.String())
			}
			if record["msg"] != "operation.succeeded" || record["operation_id"] != test.id || record["operation"] != test.operation || strings.Contains(output.String(), "core-secret") {
				t.Fatalf("diagnostic record=%#v output=%s", record, output.String())
			}
		})
	}
}

func TestCoreDiagnostic_FailureJSONKeepsSafeCauseAndExecutionOperation(t *testing.T) {
	const secret = "core-secret"
	var output bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	redactor := logging.NewRedactor(secret)
	cause := &os.PathError{Op: "open", Path: "/private/" + secret, Err: os.ErrPermission}
	failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "prepare failed"}, cause)
	manager := newTestManager(Options{
		Installer: &fakeInstaller{prepare: func(context.Context, core.InstallRequest) (PreparedCore, error) {
			return nil, failure
		}},
		DiagnosticReporter: logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, level, "daemon", redactor)), redactor),
	})

	_, err := manager.Install(context.Background(), Operation{ID: "install-failure-json", Source: "test"})

	if !errors.Is(err, cause) || err.Error() != "prepare failed" || strings.Contains(err.Error(), secret) {
		t.Fatalf("install error=%v", err)
	}
	var record map[string]any
	if decodeErr := json.Unmarshal(output.Bytes(), &record); decodeErr != nil {
		t.Fatalf("diagnostic JSON: %v; output=%s", decodeErr, output.String())
	}
	causeText, _ := record["cause"].(string)
	if record["msg"] != "operation.failed" || record["operation_id"] != "install-failure-json" || record["operation"] != "core.install" {
		t.Fatalf("diagnostic record=%#v", record)
	}
	if !strings.Contains(causeText, "path operation open") || !strings.Contains(causeText, "permission denied") || strings.Contains(output.String(), secret) {
		t.Fatalf("diagnostic cause=%q output=%s", causeText, output.String())
	}
}
