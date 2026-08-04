package tui

import (
	"strings"
	"testing"

	"github.com/LeeShunEE/mihari/internal/tui/ui"
)

func TestConfirmationPolicy(t *testing.T) {
	want := map[Action]bool{
		DeleteSubscription: true, CloseAllConnections: true, UpdateAllProviders: true, RefreshAllSubscriptions: true,
		RollbackPanel: true, RestartCore: true, UpdateCore: true, ApplyEndpointChange: true,
		SelectProxy: false, CloseConnection: false, RefreshSubscription: false, UpdateProvider: false,
	}
	for action, required := range want {
		if got := RequiresConfirmation(action); got != required {
			t.Fatalf("%v=%v want=%v", action, got, required)
		}
	}
	if RequiresConfirmation(Action("unknown")) {
		t.Fatal("unknown action unexpectedly requires confirmation instead of being rejected")
	}
}

func TestRevisionConflictPolicyIsPageLocalPathA(t *testing.T) {
	// Path A: pages show a local conflict toast and reload; Root does not need
	// to force globalState=StateRevisionConflict for correct UX.
	if ui.GlobalStateLabel(ui.StateRevisionConflict) != ui.GlobalStateConflictLabel {
		t.Fatalf("shared conflict label missing for future global use")
	}
	for _, message := range []string{
		ui.SubscriptionChangedMessage,
		ui.SetupChangedMessage,
		ui.SystemChangedMessage,
	} {
		if strings.TrimSpace(message) == "" {
			t.Fatalf("page-local conflict message empty")
		}
		lower := strings.ToLower(message)
		if !strings.Contains(lower, "reload") && !strings.Contains(lower, "changed") {
			t.Fatalf("page-local conflict message unclear: %q", message)
		}
	}
	// Fresh root shell starts without a revision-conflict global banner.
	model := NewModel()
	if model.globalState == ui.StateRevisionConflict {
		t.Fatal("root should not default to revision-conflict global state")
	}
	if strings.Contains(model.View().Content, ui.GlobalStateConflictLabel) {
		t.Fatalf("root footer unexpectedly shows global conflict label: %s", model.View().Content)
	}
}
