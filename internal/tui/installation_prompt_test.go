package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func installationKey(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestInstallationPrompt_CtrlCUsesGlobalQuit(t *testing.T) {
	model := NewModel()
	model.setInstallationActions(InstallationActions{})
	model.updateInstallation(installationStatusMsg{status: protocol.InstallationStatus{Schema: "mihari.install-status/v1", Kind: "interrupted", ServiceState: "unknown"}})
	if !model.installation.visible {
		t.Fatal("installation prompt did not open")
	}

	updated, command := model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	got := updated.(Model)
	if command == nil || command() != tea.Quit() {
		t.Fatal("Ctrl+C did not use the global quit command")
	}
	if !got.installation.visible {
		t.Fatal("global Ctrl+C dismissed the installation prompt before quitting")
	}
}

func TestInstallationPrompt_OnlyConfirmedInterruptedAndOnce(t *testing.T) {
	for _, kind := range []string{"interrupted", "in_progress", "unknown", "permission_required", "installed"} {
		t.Run(kind, func(t *testing.T) {
			model := Model{}
			model.setInstallationActions(InstallationActions{})
			message := installationStatusMsg{status: protocol.InstallationStatus{Schema: "mihari.install-status/v1", Kind: kind, ServiceState: "unknown"}}
			model.updateInstallation(message)
			if model.installation.visible != (kind == "interrupted") {
				t.Fatalf("kind %s visible %v", kind, model.installation.visible)
			}
			if kind != "interrupted" {
				return
			}
			model.updateInstallation(installationKey(tea.KeyEscape))
			model.updateInstallation(message)
			if model.installation.visible {
				t.Fatal("dismissed prompt reopened")
			}
		})
	}
	model := Model{}
	model.setInstallationActions(InstallationActions{})
	model.updateInstallation(installationStatusMsg{status: protocol.InstallationStatus{Kind: "interrupted"}, err: errors.New("connection refused")})
	if model.installation.visible {
		t.Fatal("connection failure claimed interruption")
	}
}

func TestInstallationPrompt_CancelDoesNotPlanOrExecute(t *testing.T) {
	calls := 0
	model := Model{}
	model.setInstallationActions(InstallationActions{Plan: func(context.Context, app.InstallationPlanRequest) (app.InstallationPlan, error) {
		calls++
		return app.InstallationPlan{}, nil
	}})
	model.updateInstallation(installationStatusMsg{status: protocol.InstallationStatus{Schema: "mihari.install-status/v1", Kind: "interrupted", ServiceState: "unknown"}})
	model.updateInstallation(installationKey(tea.KeyEscape))
	if calls != 0 || model.preparedInstallation != nil || model.installation.visible {
		t.Fatal("cancel retained installation work")
	}
}

func TestInstallationPrompt_UnprivilegedChoiceDoesNotPlan(t *testing.T) {
	calls := 0
	model := Model{}
	model.setInstallationActions(InstallationActions{Elevated: func() bool { return false }, Plan: func(context.Context, app.InstallationPlanRequest) (app.InstallationPlan, error) {
		calls++
		return app.InstallationPlan{}, nil
	}})
	model.updateInstallation(installationStatusMsg{status: protocol.InstallationStatus{Schema: "mihari.install-status/v1", Kind: "interrupted", ServiceState: "unknown"}})
	model.updateInstallation(installationKey(tea.KeyEnter))
	if calls != 0 || model.modal == nil {
		t.Fatal("unprivileged choice must explain administrator entry without planning")
	}
}

func TestInstallationPrepared_CleanupBeforeExecuteAndFailurePreventsWrite(t *testing.T) {
	for _, failed := range []bool{false, true} {
		var order []string
		cleanupErr := errors.New("cleanup failed")
		model := Model{preparedInstallation: &app.InstallationExecuteRequest{}}
		err := finishInstallationRun(context.Background(), model, nil, io.Discard, func(tea.Model) error {
			order = append(order, "cleanup")
			if failed {
				return cleanupErr
			}
			return nil
		}, func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
			order = append(order, "execute")
			return app.InstallationOutcome{Schema: app.InstallationOutcomeSchema, InstallationComplete: true, ServiceState: app.InstallServiceStopped}, nil
		})
		want := []string{"cleanup", "execute"}
		if failed {
			want = []string{"cleanup"}
		}
		if !reflect.DeepEqual(order, want) || (failed && !errors.Is(err, cleanupErr)) || (!failed && err != nil) {
			t.Fatalf("order=%v err=%v", order, err)
		}
	}
}

func TestInstallationPrompt_FreshRequiresPreviewAndExplicitSecondConfirmation(t *testing.T) {
	root := t.TempDir()
	plan, err := app.BindInstallationPlan(app.VerifiedInstallationPlanInput{
		Mode:            app.InstallationModeFresh,
		Instance:        app.InstallationPlanInstance{RecordID: strings.Repeat("a", 32), RecordSHA256: strings.Repeat("b", 64), DataRoot: root, DataIdentity: &app.InstallationIdentity{BootID: "boot", Key: "root", Marker: "marker"}},
		CandidateSHA256: strings.Repeat("c", 64),
		Preserve:        []app.InstallationEntry{{Path: filepath.Join(root, "control.token"), Category: app.InstallationCategoryCredential}},
		Delete:          []app.InstallationEntry{{Path: filepath.Join(root, "web"), Category: app.InstallationCategoryWeb}},
	})
	if err != nil {
		t.Fatal(err)
	}
	model := Model{}
	model.setInstallationActions(InstallationActions{Elevated: func() bool { return true }, Plan: func(_ context.Context, request app.InstallationPlanRequest) (app.InstallationPlan, error) {
		if request.Mode != app.InstallationModeFresh {
			t.Errorf("mode=%s", request.Mode)
		}
		return plan, nil
	}, Execute: func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
		t.Fatal("execution inside TUI")
		return app.InstallationOutcome{}, nil
	}})
	model.updateInstallation(installationStatusMsg{status: protocol.InstallationStatus{Schema: "mihari.install-status/v1", Kind: "interrupted", ServiceState: "unknown"}})
	model.updateInstallation(installationKey(tea.KeyDown))
	command, _ := model.updateInstallation(installationKey(tea.KeyEnter))
	if command == nil || model.preparedInstallation != nil {
		t.Fatal("first choice did not only prepare preview")
	}
	model.updateInstallation(command())
	if model.installation.plan == nil || model.installation.selected != 1 {
		t.Fatal("preview must default to cancel")
	}
	model.updateInstallation(installationKey(tea.KeyLeft))
	command, _ = model.updateInstallation(installationKey(tea.KeyEnter))
	if command == nil || model.preparedInstallation == nil || !model.preparedInstallation.ResetConfirmed || model.preparedInstallation.Plan.PlanSHA256 != plan.PlanSHA256 {
		t.Fatal("explicit confirmation not bound to shown plan")
	}
}

func TestInstallationWorker_ShutdownCancelsAndJoinsDiscardedPreparation(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	w, actions := newInstallationWorker(InstallationActions{Plan: func(ctx context.Context, _ app.InstallationPlanRequest) (app.InstallationPlan, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return app.InstallationPlan{}, ctx.Err()
	}})
	done := make(chan error, 1)
	go func() { _, err := actions.Plan(context.Background(), app.InstallationPlanRequest{}); done <- err }()
	<-entered
	shutdown := make(chan struct{})
	go func() { w.shutdown(); close(shutdown) }()
	<-canceled
	select {
	case <-shutdown:
		t.Fatal("shutdown did not join pending plan")
	default:
	}
	close(release)
	<-shutdown
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("plan error=%v", err)
	}
	if _, err := actions.Plan(context.Background(), app.InstallationPlanRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("new work accepted after shutdown: %v", err)
	}
}

func TestInstallationPrompt_CanceledPlanCannotReopenDialog(t *testing.T) {
	model := Model{}
	model.setInstallationActions(InstallationActions{})
	model.installation.visible, model.installation.loading = true, true
	model.installation.generation = 5
	model.updateInstallation(installationKey(tea.KeyEscape))
	model.updateInstallation(installationPlanMsg{generation: 5, err: errors.New("late result")})
	if model.installation.visible || model.modal != nil || model.preparedInstallation != nil {
		t.Fatal("late result reopened canceled prompt")
	}
}

func TestInstallationPrepared_StartFailureReportsRetryWithoutSuccess(t *testing.T) {
	var out bytes.Buffer
	startErr := protocol.APIError{Code: protocol.CodeInvalidState, Message: "start failed", Details: map[string]any{"installation_complete": true, "reason": app.InstallationReasonServiceStartFailed}}
	err := finishInstallationRun(context.Background(), Model{preparedInstallation: &app.InstallationExecuteRequest{}}, nil, &out, nil, func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
		return app.InstallationOutcome{}, startErr
	})
	if err == nil || !strings.Contains(out.String(), "mihari service start") || strings.Contains(out.String(), "Reinstallation completed.") {
		t.Fatalf("output=%q error=%v", out.String(), err)
	}
}

func TestInstallationPrepared_CandidateCloseFailurePreventsExecution(t *testing.T) {
	closeErr := errors.New("candidate close failed")
	var order []string
	cleanup := installationCleanup(func() error { order = append(order, "candidates"); return closeErr }, func(tea.Model) error { order = append(order, "resources"); return nil })
	err := finishInstallationRun(context.Background(), Model{preparedInstallation: &app.InstallationExecuteRequest{}}, nil, io.Discard, cleanup, func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
		order = append(order, "execute")
		return app.InstallationOutcome{InstallationComplete: true}, nil
	})
	if !errors.Is(err, closeErr) || !reflect.DeepEqual(order, []string{"candidates", "resources"}) {
		t.Fatalf("cleanup order=%v err=%v", order, err)
	}
}

func TestInstallationPrepared_InvalidOutcomeCannotReportSuccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome app.InstallationOutcome
	}{
		{name: "malformed schema", outcome: app.InstallationOutcome{Schema: "mihari.install-outcome/v2", InstallationComplete: true, ServiceState: app.InstallServiceStopped}},
		{name: "unknown service", outcome: app.InstallationOutcome{Schema: app.InstallationOutcomeSchema, InstallationComplete: true, ServiceState: app.InstallServiceUnknown}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			executeCalls := 0
			err := finishInstallationRun(context.Background(), Model{preparedInstallation: &app.InstallationExecuteRequest{}}, nil, &out, nil, func(context.Context, app.InstallationExecuteRequest) (app.InstallationOutcome, error) {
				executeCalls++
				return tc.outcome, nil
			})
			if err == nil || executeCalls != 1 || strings.Contains(out.String(), "Reinstallation completed.") {
				t.Fatalf("invalid outcome reported success: calls=%d output=%q error=%v", executeCalls, out.String(), err)
			}
		})
	}
}
