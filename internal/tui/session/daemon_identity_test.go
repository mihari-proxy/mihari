package session

import (
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSession_DaemonReplacementInvalidatesRevisionCaches(t *testing.T) {
	for _, condition := range []string{"new PID", "reused PID", "same owner"} {
		t.Run(condition, func(t *testing.T) {
			s := &Session{loggingEpoch: 3}
			status := protocol.Status{PID: 42, StartedAt: time.Unix(100, 0), Capabilities: []string{protocol.CapabilityLogging, protocol.CapabilityRouting}}
			s.observeLoggingCapability(status)
			revision := uint64(6797)
			s.loggingRevision = &revision
			switch condition {
			case "new PID":
				status.PID++
			case "reused PID":
				status.StartedAt = time.Unix(101, 0)
			}
			s.observeLoggingCapability(status)
			if condition == "same owner" {
				if s.loggingEpoch != 3 || s.loggingRevision == nil {
					t.Fatal("unchanged owner invalidated a usable cache")
				}
			} else if s.loggingEpoch != 4 || s.loggingRevision != nil {
				t.Fatalf("new daemon retained old revision cache: epoch=%d revision=%v", s.loggingEpoch, s.loggingRevision)
			}
		})
	}
}
