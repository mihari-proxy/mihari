package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/elevate"
)

func TestInstallationStatus_UnknownIsReadOnlyResult(t *testing.T) {
	deps := Dependencies{
		InstallationInspect: func(context.Context) (app.InstallationStatus, error) {
			return app.InstallationStatus{Schema: app.InstallationStatusSchema, Kind: "unknown", ServiceState: "unknown", Reason: "record_invalid"}, nil
		},
		PrepareLocalRoot: func() error { t.Fatal("installation inspection created business root"); return nil },
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "install-status", "--json"}, stdout, stderr, deps)
	var got app.InstallationStatus
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("status JSON: %v; exit=%d", err, exit)
	}
	if exit != ExitOK || got.Kind != "unknown" || got.StartFailed || stderr.Len() != 0 {
		t.Fatalf("exit=%d kind=%s", exit, got.Kind)
	}
}

func installationCommandFixture(t *testing.T) (Dependencies, *int, string) {
	t.Helper()
	old := elevate.Check
	elevate.Check = func() bool { return true }
	t.Cleanup(func() { elevate.Check = old })
	called := new(int)
	digest := strings.Repeat("a", 64)
	deps := Dependencies{
		InstallationPlan: func(_ context.Context, request app.InstallationPlanRequest) (app.InstallationPlan, error) {
			return app.InstallationPlan{Schema: app.InstallationPlanSchema, Mode: request.Mode, PlanSHA256: digest, Preserve: []app.InstallationEntry{}, Delete: []app.InstallationEntry{}}, nil
		},
		InstallationExecute: func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
			*called++
			return app.InstallationOutcome{Schema: app.InstallationOutcomeSchema, InstallationComplete: true, ServiceState: "stopped"}, nil
		},
		PrepareLocalRoot: func() error { t.Fatal("installer touched business root before authorization"); return nil },
	}
	return deps, called, digest
}

func TestInstallationFresh_RequiresCurrentDigestAndExplicitConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  int
		calls int
	}{
		{"neither", nil, ExitUsage, 0},
		{"yes only", []string{"--yes"}, ExitUsage, 0},
		{"digest only", []string{"--plan-sha256", strings.Repeat("a", 64)}, ExitUsage, 0},
		{"stale digest", []string{"--yes", "--plan-sha256", strings.Repeat("b", 64)}, ExitInvalidState, 0},
		{"matching", []string{"--yes", "--plan-sha256", strings.Repeat("a", 64)}, ExitOK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, called, _ := installationCommandFixture(t)
			args := append([]string{"service", "fresh", "--json"}, tc.flags...)
			exit := Execute(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, deps)
			if exit != tc.want || *called != tc.calls {
				t.Fatalf("exit=%d executions=%d", exit, *called)
			}
		})
	}
}

func TestInstallationPlan_ForwardsBinaryAndExplicitServicePolicy(t *testing.T) {
	deps, _, _ := installationCommandFixture(t)
	binary := filepath.Join(t.TempDir(), "candidate")
	var got app.InstallationPlanRequest
	deps.InstallationPlan = func(_ context.Context, r app.InstallationPlanRequest) (app.InstallationPlan, error) {
		got = r
		return app.InstallationPlan{Schema: app.InstallationPlanSchema, Mode: r.Mode}, nil
	}
	exit := Execute(context.Background(), []string{"service", "install-plan", "--mode", "fresh", "--binary", binary, "--start=false", "--enable=true", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, deps)
	if exit != ExitOK || got.Binary != binary || got.Mode != "fresh" || got.Start == nil || *got.Start || got.Enable == nil || !*got.Enable {
		t.Fatal("installation plan lost explicit binary or service policy")
	}
}

func TestInstallationRepair_RequiresElevationBeforePlanning(t *testing.T) {
	deps, called, _ := installationCommandFixture(t)
	elevate.Check = func() bool { return false }
	deps.InstallationPlan = func(context.Context, app.InstallationPlanRequest) (app.InstallationPlan, error) {
		t.Fatal("unprivileged repair planned protected installation")
		return app.InstallationPlan{}, nil
	}
	exit := Execute(context.Background(), []string{"service", "repair", "--json"}, &bytes.Buffer{}, &bytes.Buffer{}, deps)
	if exit != ExitPermission || *called != 0 {
		t.Fatalf("exit=%d calls=%d", exit, *called)
	}
}

func TestInstallationRepair_StartupFailureDoesNotPrintSuccess(t *testing.T) {
	deps, _, _ := installationCommandFixture(t)
	deps.InstallationExecute = func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
		return app.InstallationOutcome{InstallationComplete: true, StartFailed: true}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "service start failed", Details: map[string]any{"installation_complete": true, "reason": "service_start_failed"}}
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "repair", "--json"}, stdout, stderr, deps)
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(stderr.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if exit != ExitInvalidState || stdout.Len() != 0 || envelope.Error.Details["installation_complete"] != true {
		t.Fatalf("exit=%d stdout=%q", exit, stdout.String())
	}
}

func TestInstallationRepair_LocalIOFailureIsNotDaemonUnavailable(t *testing.T) {
	deps, _, _ := installationCommandFixture(t)
	deps.InstallationPlan = func(context.Context, app.InstallationPlanRequest) (app.InstallationPlan, error) {
		return app.InstallationPlan{}, errors.New("private-path-and-secret")
	}
	stderr := &bytes.Buffer{}
	exit := Execute(context.Background(), []string{"service", "repair", "--json"}, &bytes.Buffer{}, stderr, deps)
	if exit != ExitData || strings.Contains(stderr.String(), "private-path-and-secret") {
		t.Fatalf("local error classification: exit=%d", exit)
	}
}

func TestInstallationFresh_InteractiveCancelPreservesData(t *testing.T) {
	deps, called, _ := installationCommandFixture(t)
	deps.Interactive = true
	cmd := newRoot(deps, &runOptions{})
	cmd.SetArgs([]string{"service", "fresh"})
	cmd.SetIn(strings.NewReader("cancel\n"))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.ExecuteContext(context.Background()); err == nil || *called != 0 {
		t.Fatalf("cancellation executed installation: err=%v calls=%d", err, *called)
	}
}

func TestInstallationFresh_InteractiveConfirmationFollowsPreview(t *testing.T) {
	deps, _, _ := installationCommandFixture(t)
	deps.Interactive = true
	stdout := &bytes.Buffer{}
	deps.InstallationPlan = func(context.Context, app.InstallationPlanRequest) (app.InstallationPlan, error) {
		return app.InstallationPlan{Mode: "fresh", Delete: []app.InstallationEntry{{Path: "data\nforged confirmation", Category: "logs"}}}, nil
	}
	deps.InstallationExecute = func(_ context.Context, request app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
		if !request.ResetConfirmed || !strings.Contains(stdout.String(), `"data\nforged confirmation"`) {
			t.Fatal("reset was not confirmed after an unambiguous preview")
		}
		return app.InstallationOutcome{Schema: app.InstallationOutcomeSchema, InstallationComplete: true, ServiceState: "stopped"}, nil
	}
	cmd := newRoot(deps, &runOptions{})
	cmd.SetArgs([]string{"service", "fresh"})
	cmd.SetIn(strings.NewReader("reinstall\n"))
	cmd.SetOut(stdout)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestInstallationRepair_InvalidOutcomeCannotReportSuccess(t *testing.T) {
	for _, outcome := range []app.InstallationOutcome{{InstallationComplete: false, StartFailed: true}, {InstallationComplete: true, StartFailed: true}, {Schema: app.InstallationOutcomeSchema, InstallationComplete: true, ServiceState: "unknown"}} {
		deps, _, _ := installationCommandFixture(t)
		deps.InstallationExecute = func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
			return outcome, nil
		}
		for _, jsonMode := range []bool{false, true} {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			args := []string{"service", "repair"}
			if jsonMode {
				args = append(args, "--json")
			}
			exit := Execute(context.Background(), args, stdout, stderr, deps)
			if exit == ExitOK || stdout.Len() != 0 {
				t.Fatalf("invalid outcome reported success: exit=%d output=%q", exit, stdout.String())
			}
		}
	}
}
