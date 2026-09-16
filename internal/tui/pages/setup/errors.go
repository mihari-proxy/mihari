package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// safeText escapes terminal controls without redacting the underlying text.
func (m *Model) safeText(text string) string { return diagnostics.EscapeTerminal(text) }

// fail retains original diagnostic details alongside the classified step summary.
func (m *Model) fail(prefix string, err error) {
	m.settlementNotice = ""
	cause, advice := "The cause could not be confirmed.", "Press F2 to inspect the original error details."
	snapshot, ok := diagnostics.Snapshot(err)
	if !ok {
		snapshot = diagnostics.Describe(m.ctx, diagnostics.Record{Err: err})
	}
	if snapshot.Summary != "" {
		cause = m.safeText(snapshot.Summary)
	}
	code := protocol.CodeInternal
	var api protocol.APIError
	if errors.As(err, &api) {
		code = api.Code
		if api.Message != "" {
			cause = m.safeText(api.Message)
		}
		switch api.Code {
		case protocol.CodeNetworkFailure, protocol.CodeUpstreamFailure:
			advice = "Check connectivity and the download source, then retry."
		case protocol.CodePermissionDenied:
			advice = "Check the required permissions, then retry."
		case protocol.CodeInvalidArgument:
			advice = "Correct the highlighted step's input, then retry."
		case protocol.CodeDaemonUnavailable:
			advice = "Reconnect to the daemon and check the saved result before retrying."
		case protocol.CodeRevisionConflict:
			advice = "State changed. Review the refreshed state before retrying."
		}
	} else if errors.Is(err, context.Canceled) {
		cause, advice = "Cancellation requested.", "Check the saved result before retrying."
	} else if errors.Is(err, context.DeadlineExceeded) {
		cause, advice = "The request timed out.", "Check the saved result before retrying."
	}
	m.lastError = prefix + ": " + strings.Join(strings.Fields(cause), " ")
	m.errorAdvice = advice
	if snapshot.Code == "" {
		snapshot.Code = code
	}
	if snapshot.OperationID == "" {
		snapshot.OperationID = m.operationID
	}
	m.errorDetail = prefix + "\n\n" + diagnostics.TerminalText("Error", snapshot) + "\n" + advice
	if status, ok := api.Details["status"].(float64); ok && status >= 100 && status <= 599 {
		m.errorDetail += fmt.Sprintf("\nHTTP status: %.0f", status)
	}
}

// clearFailure removes obsolete error and settlement messages before another action.
func (m *Model) clearFailure() {
	m.lastError, m.errorAdvice, m.errorDetail, m.settlementNotice = "", "", "", ""
}

// uncertainOutcome identifies response failures that require readback before retrying a mutation.
func uncertainOutcome(err error) bool {
	var api protocol.APIError
	var network net.Error
	var syntax *json.SyntaxError
	var shape *json.UnmarshalTypeError
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &network) || errors.As(err, &syntax) || errors.As(err, &shape) {
		return true
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &api) && api.Code == protocol.CodeDaemonUnavailable)
}
