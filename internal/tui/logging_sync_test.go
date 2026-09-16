package tui

import (
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/tui/session"
)

func TestLoggingSync_TwoTUIInstancesApplySavedLevelThenPassiveSilent(t *testing.T) {
	for range 2 {
		model := NewModel()
		applier := &recordingLoggingApplier{}
		model.SetLoggingApplier(applier)
		model.applySessionEvent(session.Event{Kind: session.EventStatus, Epoch: 1, Status: protocol.Status{Revision: 1, Capabilities: []string{protocol.CapabilityLogging}}})
		model.applySessionEvent(session.Event{Kind: session.EventLogging, Epoch: 1, Logging: protocol.LoggingStatus{Revision: 1, Level: "info", CoreLevel: "debug", SyncState: "unsaved", MaxSizeMB: 10, MaxFiles: 3}})
		if got := applier.last(); got != logging.DefaultConfig() {
			t.Fatalf("TUI applied unsaved core level: %+v", got)
		}
		model.applySessionEvent(session.Event{Kind: session.EventLogging, Epoch: 1, Logging: protocol.LoggingStatus{Revision: 2, Level: "silent", CoreLevel: "silent", SyncState: "applied", MaxSizeMB: 10, MaxFiles: 3}})
		if applier.last().Level != logging.LevelSilent {
			t.Fatal("TUI rejected passive silent")
		}
	}
}
