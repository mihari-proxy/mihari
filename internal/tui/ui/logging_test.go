package ui

import (
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestLoggingMessagesCarryEpochAndCompleteRevisionZeroStatus(t *testing.T) {
	status := protocol.LoggingStatus{
		Schema: "mihari/v1", Revision: 0, Level: "debug", MaxSizeMB: 100, MaxFiles: 10, Dir: `C:\logs`,
	}
	sync := LoggingSyncMsg{Epoch: 1, Status: status, Available: true}
	observed := LoggingObservedMsg{Epoch: sync.Epoch, Status: sync.Status}
	if observed.Epoch != 1 || observed.Status != status || !sync.Available {
		t.Fatalf("sync=%+v observed=%+v", sync, observed)
	}
}
