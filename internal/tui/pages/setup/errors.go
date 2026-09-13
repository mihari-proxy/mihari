package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"unicode"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func (m *Model) safeText(text string) string {
	var secrets []string
	if len(m.subscriptionInputs) > 1 {
		secrets = append(secrets, m.subscriptionInputs[1].Value())
	}
	text = logging.NewRedactor(secrets...).String(text)
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
	runes := []rune(text)
	if len(runes) > 4096 {
		return string(runes[:4095]) + "…"
	}
	return text
}

func (m *Model) fail(prefix string, err error) {
	cause, advice := "The cause could not be confirmed.", "Open details and use the operation ID to locate daemon logs."
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
	m.errorDetail = fmt.Sprintf("%s\n\n%s\n\n%s\n\nCode: %s", prefix, cause, advice, m.safeText(string(code)))
	if m.operationID != "" {
		m.errorDetail += "\nOperation: " + m.safeText(m.operationID)
	}
	if status, ok := api.Details["status"].(float64); ok && status >= 100 && status <= 599 {
		m.errorDetail += fmt.Sprintf("\nHTTP status: %.0f", status)
	}
}

func (m *Model) clearFailure() { m.lastError, m.errorAdvice, m.errorDetail = "", "", "" }

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
