package tui

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// Package-level shell smoke: no real terminal or IPC. Closes the Phase 4 audit
// residual that asked for a tiny TUI integration path without a heavy harness.
func TestTUI_SetupRequiredRoutesAndStaleDisablesMutations(t *testing.T) {
	events := make(chan session.Event, 4)
	model := NewModelWithEvents(events)
	if command := model.Init(); command == nil {
		t.Fatal("expected session wait command")
	}

	model.applySessionEvent(session.Event{
		Kind:   session.EventStatus,
		Status: protocol.Status{SetupRequired: true, Capabilities: []string{protocol.CapabilityCore}},
	})
	if model.Route() != ui.PageSetup {
		t.Fatalf("setup-required route=%v", model.Route())
	}
	if model.SetupComplete() {
		t.Fatal("setup should not report complete while required")
	}

	// Leave standalone setup so the standard root footer (with global state) is used.
	model.applySessionEvent(session.Event{
		Kind:   session.EventStatus,
		Status: protocol.Status{SetupRequired: false, Capabilities: []string{protocol.CapabilityCore}},
	})
	if model.Route() != ui.PageOverview {
		t.Fatalf("post-setup route=%v", model.Route())
	}

	model.applySessionEvent(session.Event{Kind: session.EventConnected})
	if !model.mutationsEnabled || model.stale {
		t.Fatalf("connected mutations=%v stale=%v", model.mutationsEnabled, model.stale)
	}
	model.applySessionEvent(session.Event{Kind: session.EventReconnecting})
	if model.mutationsEnabled || !model.stale || model.globalState != ui.StateStale {
		t.Fatalf("reconnecting mutations=%v stale=%v state=%q", model.mutationsEnabled, model.stale, model.globalState)
	}
	content := model.View().Content
	if !strings.Contains(content, ui.GlobalStateStaleLabel) {
		t.Fatalf("stale footer missing in:\n%s", content)
	}
}
