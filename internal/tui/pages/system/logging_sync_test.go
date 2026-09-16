package system

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"strings"
	"testing"
)

func TestLoggingRows_ShowsUnsavedCoreAndPassiveSilent(t *testing.T) {
	m := &Model{loggingAvailable: true, logging: protocol.LoggingStatus{Level: "info", CoreLevel: "debug", SyncState: "unsaved", SyncMessage: "Saving will be retried"}}
	row := m.loggingRows()[0]
	for _, want := range []string{"info", "debug", "unsaved"} {
		if !strings.Contains(row.value+row.detail, want) {
			t.Fatalf("missing %s in %+v", want, row)
		}
	}
	m.logging = protocol.LoggingStatus{Level: "silent", CoreLevel: "silent", SyncState: "applied"}
	row = m.loggingRows()[0]
	if !strings.Contains(row.value+row.detail, "core") {
		t.Fatalf("silent has no source explanation: %+v", row)
	}
}

func TestLoggingRows_ExplainsGlobalScope(t *testing.T) {
	m := &Model{loggingAvailable: true, logging: protocol.LoggingStatus{Level: "info", SyncState: "applied"}}
	row := m.loggingRows()[0]
	if !strings.Contains(row.detail, "Mihari") || !strings.Contains(row.detail, "mihomo") {
		t.Fatalf("missing scope explanation: %+v", row)
	}
}
