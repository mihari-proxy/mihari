package diagnostics

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HandshakeError captures the failed websocket dial response. The websocket
// adapter returns a closed-network-body copy capped at 1024 bytes; mark that
// upstream library limit rather than claiming a complete error payload.
func HandshakeError(operation string, response *http.Response, cause error, secret string) *HTTPError {
	detail := &HTTPError{Operation: operation, Phase: "handshake", Cause: cause}
	if response == nil {
		return detail.HideSecret(secret)
	}
	detail.Status = response.StatusCode
	if response.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		closeErr := response.Body.Close()
		detail.Body = HTTPBody(raw, secret)
		if response.ContentLength > int64(len(raw)) || len(raw) == 1024 {
			detail.Body += " [handshake body truncated]"
		}
		detail.Cause = errors.Join(cause, err, closeErr)
	}
	return detail.HideSecret(secret)
}

// HTTPError retains upstream diagnostics outside the public API error. Only the
// logging boundary may render DiagnosticText; Error deliberately stays safe.
type HTTPError struct {
	Operation  string
	Phase      string
	Status     int
	Body       string
	Cause      error
	CauseText  string
	RetryDelay time.Duration
}

func (e *HTTPError) Error() string { return "mihomo HTTP operation failed" }
func (e *HTTPError) Unwrap() error { return e.Cause }

// RetryAfter exposes a parsed upstream retry delay to the provider read policy.
func (e *HTTPError) RetryAfter() time.Duration { return e.RetryDelay }

// DiagnosticText returns original error text for a redacting, bounded logger.
func (e *HTTPError) DiagnosticText() string {
	text := e.Operation + " phase=" + e.Phase
	if e.Status != 0 {
		text += fmt.Sprintf(" HTTP %d", e.Status)
	}
	if e.Body != "" {
		text += " response=" + e.Body
	}
	if e.Cause != nil {
		cause := e.CauseText
		if cause == "" {
			cause = e.Cause.Error()
		}
		text += " cause=" + cause
	}
	return text
}

// HideSecret removes a known controller credential from the rendered copy while
// retaining the original cause for errors.Is/As. Call after adding all causes.
func (e *HTTPError) HideSecret(secret string) *HTTPError {
	e.Body = HTTPBody([]byte(e.Body), secret)
	if e.Cause != nil {
		e.CauseText = HTTPBody([]byte(e.Cause.Error()), secret)
	}
	return e
}

// HTTPBody bounds retained error payloads without retaining response objects.
func HTTPBody(raw []byte, secret string) string {
	text := string(raw)
	if secret != "" {
		text = strings.ReplaceAll(text, secret, "***")
	}
	const limit = 64 << 10
	if len(text) > limit {
		text = text[:limit] + " [truncated]"
	}
	return text
}

// HTTPOperation removes user-controlled path segments and query parameters.
func HTTPOperation(method, path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	operation := "controller"
	if len(parts) > 0 {
		switch parts[0] {
		case "proxies":
			operation = "proxies"
			if len(parts) > 1 {
				operation += ".node"
			}
			if len(parts) > 2 && parts[len(parts)-1] == "delay" {
				operation += ".delay"
			}
		case "group":
			operation = "group.delay"
		case "providers":
			operation = "providers"
			if len(parts) > 1 && (parts[1] == "rules" || parts[1] == "proxies") {
				operation += "." + parts[1]
			}
			if len(parts) > 2 {
				operation += ".item"
			}
			if len(parts) > 3 {
				operation += ".healthcheck"
			}
		case "version", "configs", "connections", "rules", "restart", "traffic", "memory", "logs":
			operation = parts[0]
		}
	}
	switch method {
	case http.MethodGet, http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodHead:
	default:
		method = "HTTP"
	}
	return "mihomo " + method + " " + operation
}
