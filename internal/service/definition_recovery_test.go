package service

import (
	"context"
	"testing"
)

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
