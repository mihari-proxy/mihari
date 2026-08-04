package tui

import "testing"

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
