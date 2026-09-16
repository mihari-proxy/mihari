package diagnostics

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// Capture limits apply before protocol encoding and terminal rendering.
const (
	MaxDepth           = 32
	MaxNodes           = 64
	MaxBytes           = 256 << 10
	diagnosticMaxDepth = MaxDepth
	diagnosticMaxNodes = MaxNodes
	diagnosticMaxBytes = MaxBytes
)

// CapturedText holds original text and explicit collection limits.
type CapturedText struct {
	Text      string
	Truncated bool
	Reason    string
}

// Capture formats an error graph without redaction or additional stack capture.
func Capture(err error) CapturedText {
	if snapshot, ok := Snapshot(err); ok && (snapshot.State == protocol.DiagnosticAvailable || snapshot.State == "" && snapshot.Detail != "") {
		return CapturedText{Text: snapshot.Detail, Truncated: snapshot.Truncated, Reason: snapshot.TruncationReason}
	}
	formatter := diagnosticFormatter{seen: make(map[error]*diagnosticVisit)}
	text, _ := formatter.visit(err, 1)
	return CapturedText{Text: text, Truncated: formatter.reason != "", Reason: formatter.reason}
}

// BoundText bounds an individual original text value using the capture budget.
func BoundText(text string) string { return boundDiagnostic(text) }

type diagnosticVisit struct {
	active   bool
	text     string
	complete bool
}

type diagnosticFormatter struct {
	seen   map[error]*diagnosticVisit
	nodes  int
	reason string
}

// visit checks children before formatting their parent: a recursive Error or
// joined Error must not bypass a cycle or the traversal budget.
func (f *diagnosticFormatter) visit(err error, depth int) (text string, complete bool) {
	if f.nodes >= diagnosticMaxNodes || depth > diagnosticMaxDepth {
		f.mark("graph_limit")
		return "diagnostic graph truncated", false
	}
	f.nodes++
	if err == nil {
		return "", true
	}
	value := reflect.ValueOf(err)
	if nilDiagnosticErrorValue(value) {
		return typeSummary(err) + " (nil)", true
	}
	if value.Comparable() {
		if previous := f.seen[err]; previous != nil {
			if previous.active {
				f.mark("cycle")
				return "diagnostic error cycle", false
			}
			return previous.text, previous.complete
		}
		entry := &diagnosticVisit{active: true}
		f.seen[err] = entry
		defer func() { entry.active, entry.text, entry.complete = false, text, complete }()
	}

	var children []error
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children = wrapped.Unwrap()
	case interface{ Unwrap() error }:
		if child := wrapped.Unwrap(); child != nil {
			children = []error{child}
		}
	}
	parts := make([]string, 0, min(len(children), diagnosticMaxNodes))
	complete = true
	for _, child := range children {
		if f.nodes >= diagnosticMaxNodes {
			f.mark("graph_limit")
			parts = append(parts, "diagnostic graph truncated")
			complete = false
			break
		}
		childText, childComplete := f.visit(child, depth+1)
		parts = append(parts, childText)
		complete = complete && childComplete
	}
	if !complete {
		return f.joinDiagnostic("", parts), false
	}

	var own string
	switch detail := err.(type) {
	case *HTTPError:
		if detail.BodyTruncated {
			f.mark("upstream_limit")
		}
		own = detail.Operation + " phase=" + detail.Phase
		if detail.URL != "" {
			own += " URL=" + detail.URL
		}
		if detail.Status != 0 {
			own += fmt.Sprintf(" HTTP %d", detail.Status)
		}
		if detail.Body != "" {
			own += " response=" + f.bound(detail.Body)
		}
	case protocol.APIError:
		own = f.apiDiagnosticText(detail)
	case *protocol.APIError:
		own = f.apiDiagnosticText(*detail)
	case *exec.ExitError:
		own = "command exited"
		if detail.ProcessState != nil {
			own = detail.Error()
		}
		if len(detail.Stderr) != 0 {
			own += "\nstderr: " + f.bound(string(detail.Stderr))
		}
	default:
		// %+v retains a stack already carried by a formatter; it does not
		// capture a new stack at the final logging site.
		own = fmt.Sprintf("%+v", err)
	}
	return f.joinDiagnostic(f.bound(own), parts), true
}

func (f *diagnosticFormatter) apiDiagnosticText(api protocol.APIError) string {
	text := "api error (" + string(api.Code) + "): " + api.Message
	if len(api.Details) != 0 {
		details, err := json.Marshal(api.Details)
		if err != nil {
			// Invalid/cyclic DTO data is not traversed by an unbounded formatter.
			return f.bound(text + "\ndetails encoding failed: " + err.Error())
		}
		text += "\ndetails: " + string(details)
	}
	return f.bound(text)
}

func (f *diagnosticFormatter) joinDiagnostic(text string, children []string) string {
	parts := []string{}
	if text != "" {
		parts = append(parts, text)
	}
	total := len(text)
	for _, child := range children {
		if child == "" || strings.Contains(text, child) {
			continue
		}
		parts = append(parts, child)
		total += len(child)
	}
	const separator = "\ncaused by: "
	if len(parts) == 0 {
		return ""
	}
	if total+(len(parts)-1)*len(separator) <= diagnosticMaxBytes {
		return strings.Join(parts, separator)
	}
	f.mark("byte_limit")
	// At the budget boundary put already-formatted children first and reserve
	// space for every branch. A long outer context must not hide a leaf cause.
	if text != "" && len(parts) > 1 {
		parts = append(parts[1:], text)
	}
	remaining := diagnosticMaxBytes - (len(parts) - 1)
	for index, part := range parts {
		parts[index] = truncateDiagnostic(part, remaining/(len(parts)-index))
		remaining -= len(parts[index])
	}
	return strings.Join(parts, "\n")
}

func boundDiagnostic(text string) string {
	// Bound before UTF-8 repair too: an untrusted Error may hold a large
	// string and each invalid byte can expand to a replacement character.
	truncated := len(text) > diagnosticMaxBytes
	if len(text) > diagnosticMaxBytes+utf8.UTFMax {
		text = text[:diagnosticMaxBytes+utf8.UTFMax]
	}
	if truncated {
		start := len(text) - 1
		for start > 0 && !utf8.RuneStart(text[start]) {
			start--
		}
		if !utf8.FullRuneInString(text[start:]) {
			text = text[:start]
		}
	}
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "�") + " [invalid UTF-8]"
	}
	if truncated && len(text) <= diagnosticMaxBytes {
		text += " [truncated]"
	}
	return truncateDiagnostic(text, diagnosticMaxBytes)
}

func nilDiagnosticErrorValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func typeSummary(err error) string {
	if kind := reflect.TypeOf(err); kind != nil {
		return "error (" + kind.String() + ")"
	}
	return "error"
}

func truncateDiagnostic(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	const marker = " [truncated]"
	if limit < len(marker) {
		return marker[:limit]
	}
	text = text[:limit-len(marker)]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text + marker
}

func (f *diagnosticFormatter) mark(reason string) {
	for _, existing := range strings.Split(f.reason, ",") {
		if existing == reason {
			return
		}
	}
	if f.reason != "" {
		f.reason += ","
	}
	f.reason += reason
}

func (f *diagnosticFormatter) bound(text string) string {
	if len(text) > MaxBytes {
		f.mark("byte_limit")
	} else if !utf8.ValidString(text) && len(strings.ToValidUTF8(text, "�")+" [invalid UTF-8]") > MaxBytes {
		f.mark("byte_limit")
	}
	return boundDiagnostic(text)
}
