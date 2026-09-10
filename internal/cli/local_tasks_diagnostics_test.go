package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalTaskDiagnostics_ServiceActionBindsOperation(t *testing.T) {
	var operation logging.OperationMetadata
	cmd := newServiceActionCommand("stop", "", Dependencies{NewOperationID: func() string { return "service-local" }, ServiceAction: func(ctx context.Context, _ string) error {
		operation, _ = logging.OperationFromContext(ctx)
		return nil
	}}, &runOptions{}, false, nil)
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if operation != (logging.OperationMetadata{ID: "service-local", Name: "service.stop"}) {
		t.Fatalf("missing service operation: %+v", operation)
	}
}

func TestLocalTaskDiagnostics_ServiceBorrowsReporterWithoutChangingOutput(t *testing.T) {
	for _, mode := range []string{"failure", "cancel", "id-failure", "nil-reporter"} {
		t.Run(mode, func(t *testing.T) {
			var logs, out bytes.Buffer
			redactor := logging.NewRedactor()
			reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&logs, &slog.LevelVar{}, "cli", redactor)), redactor)
			if mode == "nil-reporter" {
				reporter = nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := error(&os.PathError{Op: "open", Path: "/private/service-token", Err: os.ErrPermission})
			if mode == "cancel" {
				cancel()
				cause = context.Canceled
			}
			if mode == "id-failure" {
				cause = nil
			}
			calls := 0
			cmd := newServiceActionCommand("stop", "", Dependencies{DiagnosticReporter: reporter, NewOperationID: func() string {
				if mode == "id-failure" {
					return ""
				}
				return "cli-own"
			}, ServiceAction: func(ctx context.Context, _ string) error { calls++; return cause }}, &runOptions{json: true}, false, nil)
			cmd.SetOut(&out)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			err := cmd.ExecuteContext(ctx)
			if calls != 1 || (cause == nil && err != nil) || (cause != nil && err == nil) {
				t.Fatalf("changed execution: calls=%d err=%v", calls, err)
			}
			if strings.Contains(logs.String(), "/private/") {
				t.Fatal("path leaked")
			}
			if mode == "cancel" || mode == "nil-reporter" {
				if logs.Len() != 0 {
					t.Fatalf("unexpected diagnostic: %s", logs.String())
				}
				return
			}
			var record map[string]any
			decoder := json.NewDecoder(&logs)
			if err := decoder.Decode(&record); err != nil {
				t.Fatalf("missing CLI diagnostic: %v", err)
			}
			if record["operation"] != "service.stop" {
				t.Fatalf("wrong operation: %+v", record)
			}
			if mode == "id-failure" {
				if record["operation_id"] != nil || record["msg"] != "local_task.id_generation_failed" {
					t.Fatalf("wrong ID failure: %+v", record)
				}
				var result map[string]any
				if err := json.Unmarshal(out.Bytes(), &result); err != nil || result["ok"] != true {
					t.Fatalf("JSON success changed: %s %v", out.String(), err)
				}
			}
			if decoder.Decode(&record) != io.EOF {
				t.Fatal("duplicate failure")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancel mapping changed")
			}
		})
	}
}

type localDiagnosticsUpdater struct{ prepare, apply func(context.Context) error }

func (u localDiagnosticsUpdater) Prepare(ctx context.Context, _, _, _ string) (update.PreparedUpdate, error) {
	return update.PreparedUpdate{}, u.prepare(ctx)
}
func (u localDiagnosticsUpdater) ApplyPrepared(ctx context.Context, _ update.PreparedUpdate) (update.Result, error) {
	return update.Result{}, u.apply(ctx)
}

func TestLocalTaskDiagnostics_CLILocalOwnersPreserveJSONAndExit(t *testing.T) {
	for _, owner := range []string{"inspect", "plan", "execute", "self-prepare", "self-apply"} {
		t.Run(owner, func(t *testing.T) {
			deps, _, _ := installationCommandFixture(t)
			deps.NewOperationID = func() string { return "cli-local" }
			var seen logging.OperationMetadata
			failure := func(ctx context.Context) error {
				seen, _ = logging.OperationFromContext(ctx)
				return &os.PathError{Op: "open", Path: "/private/task-secret", Err: os.ErrPermission}
			}
			var args []string
			var want string
			switch owner {
			case "inspect":
				deps.InstallationInspect = func(ctx context.Context) (app.InstallationStatus, error) {
					return app.InstallationStatus{}, failure(ctx)
				}
				args = []string{"service", "install-status", "--json"}
				want = "installation.inspect"
			case "plan":
				deps.InstallationPlan = func(ctx context.Context, _ app.InstallationPlanRequest) (app.InstallationPlan, error) {
					return app.InstallationPlan{}, failure(ctx)
				}
				args = []string{"service", "install-plan", "--mode", "repair", "--json"}
				want = "installation.plan"
			case "execute":
				deps.InstallationExecute = func(ctx context.Context, _ app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
					return app.InstallationOutcome{}, failure(ctx)
				}
				args = []string{"service", "repair", "--json"}
				want = "installation.execute"
			case "self-prepare", "self-apply":
				deps.SelfUpdateChannel = func(context.Context) (string, error) { return "main", nil }
				updater := localDiagnosticsUpdater{prepare: func(context.Context) error { return nil }, apply: func(context.Context) error { return nil }}
				if owner == "self-prepare" {
					updater.prepare = failure
				} else {
					updater.apply = failure
				}
				deps.SelfUpdater = updater
				args = []string{"self", "update", "--json"}
				want = "self.update"
			}
			var baselineOut, baselineErr bytes.Buffer
			baselineCode := Execute(context.Background(), args, &baselineOut, &baselineErr, deps)
			var out, stderr, logs bytes.Buffer
			redactor := logging.NewRedactor()
			deps.DiagnosticReporter = logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&logs, &slog.LevelVar{}, "cli", redactor)), redactor)
			code := Execute(context.Background(), args, &out, &stderr, deps)
			if code != baselineCode || out.String() != baselineOut.String() || stderr.String() != baselineErr.String() {
				t.Fatal("diagnostic injection changed CLI contract")
			}
			if seen != (logging.OperationMetadata{ID: "cli-local", Name: want}) {
				t.Fatalf("local owner did not bind: %+v want %s", seen, want)
			}
			var record map[string]any
			decoder := json.NewDecoder(&logs)
			if err := decoder.Decode(&record); err != nil || record["operation"] != want || record["operation_id"] != "cli-local" {
				t.Fatalf("missing local owner diagnostic: %+v err=%v", record, err)
			}
			if decoder.Decode(&record) != io.EOF {
				t.Fatal("duplicate local failure")
			}
		})
	}
}

func TestLocalTaskDiagnostics_ServiceApplyBindsAndReports(t *testing.T) {
	request := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(request, []byte(`{"schema":"mihari.install-request/v1","operation":"recover"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	redactor := logging.NewRedactor()
	var operation logging.OperationMetadata
	deps := Dependencies{NewOperationID: func() string { return "apply-own" }, DiagnosticReporter: logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&logs, &slog.LevelVar{}, "cli", redactor)), redactor), ServiceApply: func(ctx context.Context, _ app.InstallRequest, _ update.ReplacementConsent) (app.InstallResult, error) {
		operation, _ = logging.OperationFromContext(ctx)
		return app.InstallResult{}, io.ErrUnexpectedEOF
	}}
	cmd := newServiceApplyCommand(deps, &runOptions{json: true}, func() int { return 0 })
	cmd.SetArgs([]string{"--request", request})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("failure swallowed")
	}
	if operation != (logging.OperationMetadata{ID: "apply-own", Name: "service.apply"}) || !strings.Contains(logs.String(), "service.apply.failed") {
		t.Fatalf("missing service apply diagnostic: %+v %s", operation, logs.String())
	}
}

type failingLocalService struct{ fakeService }

func (*failingLocalService) Stop() error                         { return io.ErrUnexpectedEOF }
func (*failingLocalService) Status() (service.StatusKind, error) { return "", io.ErrUnexpectedEOF }
func TestLocalTaskDiagnostics_LegacyServiceOwnersReport(t *testing.T) {
	for _, kind := range []string{"stop", "status"} {
		t.Run(kind, func(t *testing.T) {
			var logs bytes.Buffer
			redactor := logging.NewRedactor()
			deps := Dependencies{ServiceController: &failingLocalService{}, NewOperationID: func() string { return "legacy-service" }, DiagnosticReporter: logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&logs, &slog.LevelVar{}, "cli", redactor)), redactor)}
			cmd := newServiceActionCommand("stop", "", deps, &runOptions{}, false, func(c ServiceController) error { return c.Stop() })
			if kind == "status" {
				cmd = newServiceStatusCommand(deps, &runOptions{})
			}
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			if err := cmd.ExecuteContext(context.Background()); err == nil {
				t.Fatal("service failure swallowed")
			}
			if !strings.Contains(logs.String(), "service."+kind+".failed") || !strings.Contains(logs.String(), "legacy-service") {
				t.Fatalf("missing borrowed service diagnostic: %s", logs.String())
			}
		})
	}
}
