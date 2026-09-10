package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func TestManagerBackgroundDiagnostic_ReporterOwnsSchedulerFailure(t *testing.T) {
	for _, test := range []struct {
		name             string
		err              error
		canceled, silent bool
	}{
		{name: "failure", err: &os.PathError{Op: "open", Path: "/private/background-token", Err: os.ErrPermission}},
		{name: "upstream deadline", err: context.DeadlineExceeded},
		{name: "upstream canceled", err: context.Canceled},
		{name: "shutdown", err: context.Canceled, canceled: true, silent: true},
		{name: "shutdown deadline", err: context.DeadlineExceeded, canceled: true, silent: true},
		{name: "mixed shutdown failure", err: errors.Join(context.Canceled, os.ErrPermission), canceled: true},
		{name: "mixed deadline failure", err: errors.Join(context.DeadlineExceeded, os.ErrPermission), canceled: true},
		{name: "reported", err: fmt.Errorf("owner: %w", diagnostics.MarkReported(os.ErrPermission)), silent: true},
		{name: "reported sibling and new failure", err: errors.Join(diagnostics.MarkReported(context.Canceled), os.ErrPermission)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			redactor := logging.NewRedactor("background-token")
			reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "daemon", redactor)), redactor)
			legacy := 0
			ctx, cancel := context.WithTimeout(logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "background-owner", Name: "scheduler.run"}), 3*time.Second)
			defer cancel()
			manager := newTestManager(Options{DiagnosticReporter: reporter, OnBackgroundError: func(string, error) { legacy++; cancel() }, RunScheduler: func(context.Context) error {
				if test.canceled {
					cancel()
				}
				return test.err
			}})
			// Wait for the owner to finish reporting before ending the manager lifetime.
			manager.diagnosticReporter = func(ctx context.Context, record diagnostics.Record) { reporter(ctx, record); cancel() }
			if test.silent {
				manager.runScheduler = func(context.Context) error { cancel(); return test.err }
			}
			if err := manager.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if legacy != 0 {
				t.Fatalf("legacy calls=%d", legacy)
			}
			if test.silent {
				if output.Len() != 0 {
					t.Fatalf("unexpected diagnostic: %s", output.String())
				}
				return
			}
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatalf("missing single JSON diagnostic: %v", err)
			}
			if record["component"] != "scheduler" || record["msg"] != "background.failed" || record["level"] != "ERROR" || record["operation_id"] != "background-owner" || record["operation"] != "scheduler.run" {
				t.Fatalf("record=%v", record)
			}
			if strings.Contains(output.String(), "background-token") || strings.Contains(output.String(), "/private/") {
				t.Fatal("private diagnostic data leaked")
			}
			if test.name == "failure" && record["cause"] != "path operation open: permission denied" {
				t.Fatalf("cause=%v", record["cause"])
			}
		})
	}
}

func TestManagerBackgroundDiagnostic_LegacyAlternative(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name string
		ctx  context.Context
		err  error
		want int
	}{
		{"ordinary failure", context.Background(), os.ErrPermission, 1},
		{"upstream deadline", context.Background(), context.DeadlineExceeded, 1},
		{"upstream canceled", context.Background(), context.Canceled, 1},
		{"normal cancellation", canceled, context.Canceled, 0},
		{"normal deadline", canceled, context.DeadlineExceeded, 0},
		{"mixed canceled", canceled, errors.Join(context.Canceled, os.ErrPermission), 1},
		{"mixed deadline", canceled, errors.Join(context.DeadlineExceeded, os.ErrPermission), 1},
		{"reported", context.Background(), diagnostics.MarkReported(os.ErrPermission), 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			manager := newTestManager(Options{OnBackgroundError: func(component string, err error) {
				calls++
				if component != "scheduler" || err != test.err {
					t.Fatal("legacy result changed")
				}
			}})
			manager.reportBackgroundContext(test.ctx, "scheduler", test.err)
			if calls != test.want {
				t.Fatalf("calls=%d want=%d", calls, test.want)
			}
		})
	}
}

func TestManagerBackgroundDiagnostic_RealMutationMarkerPreventsSecondReport(t *testing.T) {
	var output bytes.Buffer
	redactor := logging.NewRedactor()
	reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "daemon", redactor)), redactor)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := newTestManager(Options{GeoIP: &fakeGeoIPService{}, PrepareGeoIP: func(context.Context) (GeoIPCandidate, error) { return nil, os.ErrPermission }, DiagnosticReporter: reporter})
	manager.runScheduler = func(ctx context.Context) error {
		defer cancel()
		_, err := manager.UpdateGeoIP(ctx, Operation{ID: "scheduled-geoip-failure", Source: "scheduler"})
		if !diagnostics.AlreadyReported(err) {
			t.Error("actual mutation failure not marked")
		}
		return err
	}
	if err := manager.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("expected one mutation diagnostic: %v", err)
	}
	if record["component"] != "runtime" || record["msg"] != "operation.failed" || record["operation_id"] != "scheduled-geoip-failure" || record["cause"] != "permission denied" {
		t.Fatalf("record=%v", record)
	}
}

func TestManagerBackgroundDiagnostic_CoreRecoveryHasNoInventedOperation(t *testing.T) {
	var output bytes.Buffer
	redactor := logging.NewRedactor()
	reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "daemon", redactor)), redactor)
	manager := newTestManager(Options{DiagnosticReporter: reporter, Supervisor: &coreMaintenanceSupervisor{fakeSupervisor: &fakeSupervisor{}, maintain: func(ctx context.Context, _ func() error) error {
		if _, ok := logging.OperationFromContext(ctx); ok {
			t.Error("maintenance inherited unrelated operation")
		}
		return os.ErrPermission
	}}})
	if err := manager.lockMutation(logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "unrelated-operation", Name: "settings.update"})); err != nil {
		t.Fatal(err)
	}
	manager.stopCoreOnUnlock.Store(true)
	manager.unlock()
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["component"] != "core-recovery" || record["cause"] != "permission denied" {
		t.Fatalf("record=%v", record)
	}
	if _, ok := record["operation_id"]; ok {
		t.Fatal("invented recovery operation")
	}
}
