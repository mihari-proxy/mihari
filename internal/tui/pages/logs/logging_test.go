package logs

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"testing"
	"time"
)

func TestLogs_WarningAliasesMatchSameFilter(t *testing.T) {
	m := New(10)
	for _, level := range []string{"warn", "warning", "error"} {
		m.Observe(protocol.LogEntry{Level: level, Message: "fixture"}, time.Time{})
	}
	for _, filter := range []string{"warn", "warning"} {
		m.SetFilter(filter, "")
		if got := len(m.visibleEntries()); got != 2 {
			t.Fatalf("filter=%s count=%d", filter, got)
		}
	}
}
