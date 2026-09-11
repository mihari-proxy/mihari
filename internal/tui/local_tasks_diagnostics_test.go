package tui

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func TestLocalTaskDiagnostics_InstallationOwnersBindDistinctOperations(t *testing.T) {
	var got []logging.OperationMetadata
	capture := func(ctx context.Context) { op, _ := logging.OperationFromContext(ctx); got = append(got, op) }
	w, actions := newInstallationWorker(InstallationActions{
		Inspect: func(ctx context.Context) (protocol.InstallationStatus, error) {
			capture(ctx)
			return protocol.InstallationStatus{}, nil
		},
		Plan: func(ctx context.Context, _ app.InstallationPlanRequest) (app.InstallationPlan, error) {
			capture(ctx)
			return app.InstallationPlan{}, nil
		},
	})
	defer w.shutdown()
	_, _ = actions.Inspect(context.Background())
	_, _ = actions.Plan(context.Background(), app.InstallationPlanRequest{})
	if len(got) != 2 || got[0].ID == "" || got[1].ID == "" || got[0].ID == got[1].ID || got[0].Name != "installation.inspect" || got[1].Name != "installation.plan" {
		t.Fatalf("local task metadata missing or shared: %+v", got)
	}
}

type diagnosticLocalLogging struct {
	operations chan logging.OperationMetadata
}

func (l diagnosticLocalLogging) Apply(ctx context.Context, _ logging.Config) {
	op, _ := logging.OperationFromContext(ctx)
	l.operations <- op
}
func TestLocalTaskDiagnostics_ApplierBindsActualExecution(t *testing.T) {
	local := diagnosticLocalLogging{make(chan logging.OperationMetadata, 2)}
	a := newLoggingApplier(context.Background(), local)
	defer a.CloseAndWait()
	a.Submit(logging.BootstrapConfig())
	first := <-local.operations
	a.Submit(logging.DefaultConfig())
	second := <-local.operations
	if first.ID == "" || second.ID == "" || first.ID == second.ID || first.Name != "logging.apply" || second.Name != "logging.apply" {
		t.Fatalf("apply metadata missing or shared: %+v %+v", first, second)
	}
}

func localTaskJSON(out io.Writer) diagnostics.Reporter {
	redactor := logging.NewRedactor()
	level := &slog.LevelVar{}
	level.Set(slog.LevelDebug)
	return logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(out, level, "tui", redactor)), redactor)
}

func TestLocalTaskDiagnostics_InstallationFailureOnceAndCancellation(t *testing.T) {
	for _, mode := range []string{"failure", "reported", "cancel", "upstream-timeout", "nil-reporter", "unavailable-logger"} {
		t.Run(mode, func(t *testing.T) {
			var out bytes.Buffer
			cause := error(&os.PathError{Op: "open", Path: "/private/export-token", Err: os.ErrPermission})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "reported" {
				cause = diagnostics.MarkReported(cause)
			}
			if mode == "cancel" {
				cancel()
				cause = context.Canceled
			}
			if mode == "upstream-timeout" {
				cause = context.DeadlineExceeded
			}
			calls := 0
			w, actions := newInstallationWorker(InstallationActions{Inspect: func(ctx context.Context) (protocol.InstallationStatus, error) {
				calls++
				return protocol.InstallationStatus{}, cause
			}})
			w.diagnostics.Reporter = localTaskJSON(&out)
			if mode == "nil-reporter" {
				w.diagnostics.Reporter = nil
			}
			if mode == "unavailable-logger" {
				w.diagnostics.Reporter = localTaskJSON(failedDiagnosticWriter{})
			}
			_, err := actions.Inspect(ctx)
			w.shutdown()
			if calls != 1 || !errors.Is(err, cause) {
				t.Fatalf("execution/result changed: calls=%d err=%v", calls, err)
			}
			want := mode == "failure" || mode == "upstream-timeout"
			if !want {
				if out.Len() != 0 {
					t.Fatalf("unexpected diagnostic: %s", out.String())
				}
				return
			}
			var record map[string]any
			decoder := json.NewDecoder(&out)
			if err := decoder.Decode(&record); err != nil {
				t.Fatalf("missing failure JSON: %v", err)
			}
			if record["level"] != "ERROR" || record["operation"] != "installation.inspect" || record["operation_id"] == nil || record["msg"] != "installation.inspect.failed" {
				t.Fatalf("wrong diagnostic: %+v", record)
			}
			if decoder.Decode(&record) != io.EOF {
				t.Fatal("duplicate final failure")
			}
			if !diagnostics.AlreadyReported(err) {
				t.Fatal("recorded failure lost owner marker")
			}
		})
	}
}

type failedDiagnosticWriter struct{}

func (failedDiagnosticWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestLocalTaskDiagnostics_InstallationIDFailureStillExecutes(t *testing.T) {
	var out bytes.Buffer
	var op logging.OperationMetadata
	w, actions := newInstallationWorker(InstallationActions{Inspect: func(ctx context.Context) (protocol.InstallationStatus, error) {
		op, _ = logging.OperationFromContext(ctx)
		return protocol.InstallationStatus{Kind: "complete"}, nil
	}})
	w.diagnostics = ui.LocalTaskDiagnostics{Reporter: localTaskJSON(&out), NewID: func() (string, error) { return "", errors.New("secret-random-source") }}
	result, err := actions.Inspect(context.Background())
	w.shutdown()
	if err != nil || result.Kind != "complete" || op.ID != "" || op.Name != "installation.inspect" {
		t.Fatalf("diagnostic failure changed operation: %+v %+v %v", result, op, err)
	}
	if !strings.Contains(out.String(), "local_task.id_generation_failed") || strings.Contains(out.String(), "secret-random-source") || strings.Contains(out.String(), "operation_id") {
		t.Fatalf("missing or unsafe ID failure: %s", out.String())
	}
}

func TestLocalTaskDiagnostics_InstallationConcurrentFailureIDs(t *testing.T) {
	var out bytes.Buffer
	started := make(chan string, 2)
	release := map[string]chan struct{}{"first": make(chan struct{}), "second": make(chan struct{})}
	w, actions := newInstallationWorker(InstallationActions{Inspect: func(ctx context.Context) (protocol.InstallationStatus, error) {
		op, _ := logging.OperationFromContext(ctx)
		started <- op.ID
		<-release[op.ID]
		return protocol.InstallationStatus{}, io.ErrUnexpectedEOF
	}})
	w.diagnostics.Reporter = localTaskJSON(&out)
	done := make(chan string, 2)
	for _, id := range []string{"first", "second"} {
		go func() {
			_, _ = actions.Inspect(logging.WithOperation(context.Background(), logging.OperationMetadata{ID: id, Name: "installation.inspect"}))
			done <- id
		}()
	}
	<-started
	<-started
	close(release["second"])
	if <-done != "second" {
		t.Fatal("wrong completion order")
	}
	close(release["first"])
	<-done
	w.shutdown()
	decoder := json.NewDecoder(&out)
	for _, id := range []string{"second", "first"} {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil || record["operation_id"] != id {
			t.Fatalf("completion lost own ID: %+v %v", record, err)
		}
	}
}

type diagnosticsPreparedUpdater struct {
	blockingPreparedUpdater
	prepare func(context.Context) (update.PreparedUpdate, error)
}

func (u diagnosticsPreparedUpdater) Prepare(ctx context.Context, _, _, _ string) (update.PreparedUpdate, error) {
	return u.prepare(ctx)
}
func TestLocalTaskDiagnostics_PreparedUpdateFailure(t *testing.T) {
	var out bytes.Buffer
	var operation logging.OperationMetadata
	w := newRunPreparedUpdater(&diagnosticsPreparedUpdater{prepare: func(ctx context.Context) (update.PreparedUpdate, error) {
		operation, _ = logging.OperationFromContext(ctx)
		return update.PreparedUpdate{}, io.ErrUnexpectedEOF
	}})
	w.diagnostics.Reporter = localTaskJSON(&out)
	_, err := w.Prepare(context.Background(), "/private/binary", "v1", "main")
	if closeErr := w.close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) || operation.ID == "" || operation.Name != "self.prepare" || !strings.Contains(out.String(), "self.prepare.failed") || strings.Contains(out.String(), "/private/binary") {
		t.Fatalf("missing safe prepare failure: operation=%+v err=%v logs=%s", operation, err, out.String())
	}
}

func TestLocalTaskDiagnostics_InstallationAsyncResultsCarryOwnValues(t *testing.T) {
	seq := 0
	w, actions := newInstallationWorker(InstallationActions{
		Diagnostics: ui.LocalTaskDiagnostics{NewID: func() (string, error) { seq++; return fmt.Sprintf("inspect-%d", seq), nil }},
		Inspect: func(ctx context.Context) (protocol.InstallationStatus, error) {
			op, _ := logging.OperationFromContext(ctx)
			return protocol.InstallationStatus{ID: op.ID}, nil
		},
	})
	defer w.shutdown()
	model := NewModel()
	model.setInstallationActions(actions)
	first, second := model.inspectInstallation(), model.inspectInstallation()
	b, a := second().(installationStatusMsg), first().(installationStatusMsg)
	if a.operation.ID != "inspect-1" || b.operation.ID != "inspect-2" || a.status.ID != a.operation.ID || b.status.ID != b.operation.ID {
		t.Fatalf("async operation values mixed: %+v %+v", a, b)
	}
}

func TestLocalTaskDiagnostics_InstallationPlanResultCarriesOperation(t *testing.T) {
	w, actions := newInstallationWorker(InstallationActions{Diagnostics: ui.LocalTaskDiagnostics{NewID: func() (string, error) { return "plan-result", nil }}, Elevated: func() bool { return true }, Execute: func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
		t.Fatal("unexpected installation")
		return app.InstallationOutcome{}, nil
	}, Plan: func(ctx context.Context, _ app.InstallationPlanRequest) (app.InstallationPlan, error) {
		return app.InstallationPlan{}, io.ErrUnexpectedEOF
	}})
	defer w.shutdown()
	model := NewModel()
	model.setInstallationActions(actions)
	model.installation.visible = true
	cmd, ok := model.updateInstallation(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !ok || cmd == nil {
		t.Fatal("plan not started")
	}
	result := cmd().(installationPlanMsg)
	if result.operation != (logging.OperationMetadata{ID: "plan-result", Name: "installation.plan"}) || !errors.Is(result.err, io.ErrUnexpectedEOF) {
		t.Fatalf("plan result lost correlation: %+v", result)
	}
}

func TestLocalTaskDiagnostics_ApplierDoesNotReuseLifecycleOperation(t *testing.T) {
	local := diagnosticLocalLogging{make(chan logging.OperationMetadata, 2)}
	parent := logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "lifecycle", Name: "logging.apply"})
	a := newLoggingApplier(parent, local)
	defer a.CloseAndWait()
	a.Submit(logging.BootstrapConfig())
	first := <-local.operations
	a.Submit(logging.DefaultConfig())
	second := <-local.operations
	if first.ID == "lifecycle" || second.ID == "lifecycle" || first.ID == second.ID {
		t.Fatalf("actual applications shared lifecycle identity: %+v %+v", first, second)
	}
}

func TestLocalTaskDiagnostics_ApplierIDFailureAndVoidVisibility(t *testing.T) {
	var logs bytes.Buffer
	local := diagnosticLocalLogging{make(chan logging.OperationMetadata, 1)}
	a := newLoggingApplierWithDiagnostics(context.Background(), local, ui.LocalTaskDiagnostics{Reporter: localTaskJSON(&logs), NewID: func() (string, error) { return "", io.ErrUnexpectedEOF }})
	a.Submit(logging.BootstrapConfig())
	operation := <-local.operations
	a.CloseAndWait()
	if operation.ID != "" || operation.Name != "logging.apply" {
		t.Fatalf("invalid fallback operation: %+v", operation)
	}
	var record map[string]any
	decoder := json.NewDecoder(&logs)
	if err := decoder.Decode(&record); err != nil || record["msg"] != "local_task.id_generation_failed" {
		t.Fatalf("missing diagnostic-only ID failure: %+v %v", record, err)
	}
	if decoder.Decode(&record) != io.EOF {
		t.Fatal("void Apply invented a result diagnostic")
	}
}

func TestLocalTaskDiagnostics_LateTasksBindAfterCleanup(t *testing.T) {
	for _, kind := range []string{"self.apply", "installation.execute"} {
		t.Run(kind, func(t *testing.T) {
			model := NewModel()
			closed := false
			cleanup := func(tea.Model) error { closed = true; return nil }
			check := func(ctx context.Context) {
				operation, _ := logging.OperationFromContext(ctx)
				if !closed || operation.ID == "" || operation.Name != kind {
					t.Errorf("late task lost cleanup order/metadata: closed=%v op=%+v", closed, operation)
				}
			}
			var err error
			if kind == "self.apply" {
				model.preparedUpdate = &update.PreparedUpdate{Available: true}
				err = finishPreparedRun(context.Background(), model, nil, io.Discard, nil, cleanup, func(ctx context.Context, _ update.PreparedUpdate) (update.Result, error) {
					check(ctx)
					return update.Result{}, io.ErrUnexpectedEOF
				})
			} else {
				model.preparedInstallation = &app.InstallationExecuteRequest{}
				err = finishInstallationRun(context.Background(), model, nil, io.Discard, cleanup, func(ctx context.Context, _ app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
					check(ctx)
					return app.InstallationOutcome{}, io.ErrUnexpectedEOF
				})
			}
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal("late operation return changed")
			}
		})
	}
}

func TestLocalTaskDiagnostics_InstallationJoinIncludesReporterOutsideLock(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	w, actions := newInstallationWorker(InstallationActions{Inspect: func(context.Context) (protocol.InstallationStatus, error) {
		return protocol.InstallationStatus{}, io.ErrUnexpectedEOF
	}})
	w.diagnostics.Reporter = func(context.Context, diagnostics.Record) {
		if !w.mu.TryLock() {
			t.Error("reporter called under worker lock")
		} else {
			w.mu.Unlock()
		}
		close(entered)
		<-release
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = actions.Inspect(context.Background()) }()
	<-entered
	joined := make(chan struct{})
	go func() { w.shutdown(); close(joined) }()
	select {
	case <-joined:
		t.Error("shutdown returned while reporter still owned")
	default:
	}
	close(release)
	<-done
	<-joined
}
