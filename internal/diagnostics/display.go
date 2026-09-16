package diagnostics

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// EscapeTerminal renders control characters as text while preserving line breaks
// and tabs. It never changes the captured snapshot used for JSON or copying.
func EscapeTerminal(value string) string {
	var out strings.Builder
	for _, r := range value {
		switch {
		case r == '\n' || r == '\t':
			out.WriteRune(r)
		case r == '\r':
			out.WriteString(`\r`)
		case unicode.IsControl(r) || unicode.In(r, unicode.Cf):
			if r < 256 {
				fmt.Fprintf(&out, `\x%02x`, r)
			} else {
				fmt.Fprintf(&out, `\u%04x`, r)
			}
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// AvailabilityText describes missing remote content without inventing a cause.
func AvailabilityText(state protocol.DiagnosticState) string {
	switch state {
	case protocol.DiagnosticUnsupported:
		return "This daemon version does not provide complete diagnostics."
	case protocol.DiagnosticExpired:
		return "This diagnostic has expired from daemon history."
	case protocol.DiagnosticRestarted:
		return "The daemon restarted; this diagnostic is no longer available."
	case protocol.DiagnosticUnknown:
		return "The daemon does not recognize this diagnostic ID."
	case protocol.DiagnosticReference:
		return "Diagnostic details have not been retrieved."
	case protocol.DiagnosticUnavailable:
		return "Diagnostic details are currently unavailable."
	default:
		return ""
	}
}

// TerminalText renders the shared summary and original-detail sections.
func TerminalText(label string, snapshot protocol.Diagnostic) string {
	var text strings.Builder
	fmt.Fprintf(&text, "%s: %s\n", label, EscapeTerminal(snapshot.Summary))
	if snapshot.Code != "" {
		fmt.Fprintf(&text, "Code: %s\n", EscapeTerminal(string(snapshot.Code)))
	}
	if snapshot.OperationID != "" {
		fmt.Fprintf(&text, "Operation: %s\n", EscapeTerminal(snapshot.OperationID))
	}
	text.WriteString("\nDetails:\n")
	if snapshot.Detail != "" {
		text.WriteString(EscapeTerminal(snapshot.Detail))
		text.WriteByte('\n')
	}
	if unavailable := AvailabilityText(snapshot.State); unavailable != "" {
		text.WriteString(unavailable)
		text.WriteByte('\n')
	}
	if snapshot.RetrievalError != "" {
		text.WriteString(EscapeTerminal(snapshot.RetrievalError))
		text.WriteByte('\n')
	}
	if snapshot.Truncated {
		fmt.Fprintf(&text, "[Diagnostic capture truncated: %s]\n", EscapeTerminal(snapshot.TruncationReason))
	}
	return text.String()
}
