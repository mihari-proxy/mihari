package ui

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

// LoggingSyncMsg carries root-gated logging availability and state to pages.
type LoggingSyncMsg struct {
	Epoch     uint64
	Status    protocol.LoggingStatus
	Available bool
}

// LoggingObservedMsg carries an epoch-tagged GET/PATCH result to the root gate.
type LoggingObservedMsg struct {
	Epoch     uint64
	Status    protocol.LoggingStatus
	Operation logging.OperationMetadata
}
