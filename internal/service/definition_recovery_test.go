package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestLaunchdInspect_RejectsFailedDisabledQuery(t *testing.T) {
	for _, stdout := range []string{
		"disabled services = {\n}\n",
		"{\n\t\"mihari\" => false\n}\n",
	} {
		t.Run(stdout, func(t *testing.T) {
			h := newLaunchdHarness(t, false, true, true)
			h.runner.handle(func(argv []string) bool { return containsArg(argv, "print-disabled") }, func([]string) (CommandResult, error) {
				return CommandResult{ExitCode: 7, Stdout: []byte(stdout), Stderr: []byte("query denied: fixture")}, nil
			})
			_, err := h.adapter.InspectDefinition(t.Context())
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
				t.Fatalf("failed query reported success or wrong classification: %v", err)
			}
			for _, want := range []string{"exit status 7", stdout, "query denied: fixture"} {
				if detail := diagnostics.Capture(err).Text; !strings.Contains(detail, want) {
					t.Fatalf("diagnostic missing %q: %s", want, detail)
				}
			}
		})
	}
}

func TestLaunchdObserveAction_RejectsFailedDisabledQuery(t *testing.T) {
	for _, kind := range []string{DefinitionActionDisabled, DefinitionActionEnable, DefinitionActionDisable} {
		t.Run(string(kind), func(t *testing.T) {
			h := newLaunchdHarness(t, false, true, true)
			h.runner.handle(func(argv []string) bool { return containsArg(argv, "print-disabled") }, func([]string) (CommandResult, error) {
				return CommandResult{ExitCode: 1, Stdout: []byte("{\n\t\"mihari\" => false\n}\n")}, nil
			})
			if state, err := h.adapter.ObserveAction(context.Background(), DefinitionAction{Kind: kind}); err == nil || state != "" {
				t.Fatalf("failed query reported state %q: %v", state, err)
			}
		})
	}
}

func TestDefinitionFileState_IncludesPermissionSnapshot(t *testing.T) {
	original := DefinitionFile{Bytes: []byte("same unit"), Owner: 0, Mode: 0600}
	target := original
	target.Mode = 0644
	if DefinitionFileState(original, "") == DefinitionFileState(target, "") {
		t.Fatal("different permission states collapsed into one content hash")
	}
}
