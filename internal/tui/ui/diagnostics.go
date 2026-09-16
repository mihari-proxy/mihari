package ui

import "github.com/mihari-proxy/mihari/internal/control/protocol"

// DiagnosticMsg reports an occurrence to the global history without changing focus.
type DiagnosticMsg struct {
	Page       PageID
	Err        error
	Diagnostic *protocol.Diagnostic
}
