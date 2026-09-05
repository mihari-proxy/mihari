package ui

import "github.com/mihari-proxy/mihari/internal/control/protocol"

// LoggingSyncMsg carries root-gated logging availability and state to pages.
type LoggingSyncMsg struct {
	Epoch     uint64
	Status    protocol.LoggingStatus
	Available bool
}

// LoggingObservedMsg carries an epoch-tagged GET/PATCH result to the root gate.
type LoggingObservedMsg struct {
	Epoch  uint64
	Status protocol.LoggingStatus
}
