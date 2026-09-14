package diagnostics

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxHTTPBodyBytes bounds retained upstream failure bodies, before JSON encoding.
const MaxHTTPBodyBytes = 256 << 10

// HandshakeError captures the failed websocket dial response. The websocket
// adapter returns a closed-network-body copy capped at 1024 bytes; mark that
// upstream library limit rather than claiming a complete error payload.
func HandshakeError(operation string, response *http.Response, cause error) *HTTPError {
	detail := &HTTPError{Operation: operation, Phase: "handshake", Cause: cause}
	if response == nil {
		return detail
	}
	detail.Status = response.StatusCode
	if response.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(response.Body, MaxHTTPBodyBytes+1))
		closeErr := response.Body.Close()
		detail.Body = HTTPBody(raw)
		if response.ContentLength > int64(len(raw)) || len(raw) == 1024 {
			detail.Body += " [handshake body truncated]"
		}
		detail.Cause = errors.Join(cause, err, closeErr)
	}
	return detail
}

// HTTPError retains upstream diagnostics outside the public API error. Only the
// logging boundary may render DiagnosticText; Error deliberately stays safe.
type HTTPError struct {
	Operation  string
	URL        string
	Phase      string
	Status     int
	Body       string
	Cause      error
	RetryDelay time.Duration
}

func (e *HTTPError) Error() string { return "mihomo HTTP operation failed" }
func (e *HTTPError) Unwrap() error { return e.Cause }

// RetryAfter exposes a parsed upstream retry delay to the provider read policy.
func (e *HTTPError) RetryAfter() time.Duration { return e.RetryDelay }

// DiagnosticText returns original HTTP details for the file diagnostic boundary.
func (e *HTTPError) DiagnosticText() string {
	text := e.Operation + " phase=" + e.Phase
	if e.URL != "" {
		text += " URL=" + e.URL
	}
	if e.Status != 0 {
		text += fmt.Sprintf(" HTTP %d", e.Status)
	}
	if e.Body != "" {
		text += " response=" + e.Body
	}
	if e.Cause != nil {
		text += " cause=" + e.Cause.Error()
	}
	return text
}

// HTTPBody bounds retained error payloads without retaining response objects.
func HTTPBody(raw []byte) string {
	const marker = " [truncated]"
	truncated := len(raw) > MaxHTTPBodyBytes
	if len(raw) > MaxHTTPBodyBytes+utf8.UTFMax {
		raw = raw[:MaxHTTPBodyBytes+utf8.UTFMax]
	}
	if truncated {
		start := len(raw) - 1
		for start > 0 && !utf8.RuneStart(raw[start]) {
			start--
		}
		if !utf8.FullRune(raw[start:]) {
			raw = raw[:start]
		}
	}
	text := string(raw)
	if !utf8.Valid(raw) {
		text = strings.ToValidUTF8(text, "�") + " [invalid UTF-8]"
	}
	if truncated && len(text) <= MaxHTTPBodyBytes {
		text += marker
	}
	if len(text) <= MaxHTTPBodyBytes {
		return text
	}
	end := MaxHTTPBodyBytes - len(marker)
	for !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + marker
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
