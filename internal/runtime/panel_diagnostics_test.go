package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestPanelDiagnostic_OperationsKeepOwnerCauseAndReplayJSON(t *testing.T) {
	for _, action := range []string{"install", "update", "reinstall", "activate", "rollback", "uninstall", "install_prepare"} {
		t.Run(action, func(t *testing.T) {
			cause := &os.PathError{Op: "open", Path: "/private/business-secret", Err: os.ErrPermission}
			failure := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "panel operation failed"}, cause)
			calls := 0
			fail := func() error { calls++; return failure }
			panels := &fakePanels{installCommit: fail, updateCommit: fail, reinstallCommit: fail, activate: func(context.Context, string) error { return fail() }, rollback: func(context.Context, string) error { return fail() }, uninstall: func(context.Context, string) error { return fail() }}
			if action == "install_prepare" {
				panels.install = func(context.Context, string, string) error { return fail() }
			}
			candidate := &fakePreparedPanelMutation{commit: fail}
			if action == "install" {
				panels.installCandidate = candidate
			}
			var output bytes.Buffer
			m := newTestManager(Options{Panels: panels, DiagnosticReporter: businessJSONReporter(&output)})
			op := Operation{ID: "panel-" + action, Source: "test"}
			invoke := func() error {
				switch action {
				case "install", "install_prepare":
					return m.InstallPanel(context.Background(), op, "fixture", "")
				case "update":
					return m.UpdatePanel(context.Background(), op, "fixture")
				case "reinstall":
					return m.ReinstallPanel(context.Background(), op, "fixture")
				case "activate":
					return m.ActivatePanel(context.Background(), op, "fixture")
				case "rollback":
					return m.RollbackPanel(context.Background(), op, "fixture")
				default:
					return m.UninstallPanel(context.Background(), op, "fixture")
				}
			}
			for range 2 {
				if err := invoke(); !errors.Is(err, cause) || !diagnostics.AlreadyReported(err) {
					t.Fatalf("cause/marker lost: %v", err)
				}
			}
			name := action
			if action == "install_prepare" {
				name = "install"
			}
			assertBusinessFailureJSON(t, output.String(), op.ID, "panel."+name, "network_failure")
			if calls != 1 || m.Snapshot().Revision != 0 {
				t.Fatal("replay/revision changed")
			}
			if action == "install" && !candidate.cleaned {
				t.Fatal("prepared candidate not cleaned")
			}
		})
	}
}
