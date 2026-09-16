package protocol

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

// DiagnosticReferencesHeader carries bounded diagnostic references when the
// existing business JSON already fills its body budget. It never carries causes.
const DiagnosticReferencesHeader = "X-Mihari-Diagnostic-References"

// MaxDiagnosticReferencesHeader bounds encoded metadata, including worst-case
// JSON escapes and base64 for 64 bounded warnings. Ordinary JSON stays at 4 MiB.
const MaxDiagnosticReferencesHeader = 8 << 20

// DiagnosticReferences is an optional transport supplement, merged into the
// typed result by the client before rendering CLI JSON or TUI diagnostics.
type DiagnosticReferences struct {
	WarningOutcome
	Schema     string      `json:"schema"`
	Diagnostic *Diagnostic `json:"diagnostic,omitempty"`
}

// EncodeDiagnosticReferences encodes only queryable references and warning
// metadata. Collected original text remains in the authenticated history.
func EncodeDiagnosticReferences(value DiagnosticReferences) (string, error) {
	if !validDiagnosticReferences(value) {
		return "", errors.New("invalid diagnostic response references")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if base64.RawStdEncoding.EncodedLen(len(raw)) > MaxDiagnosticReferencesHeader {
		return "", errors.New("diagnostic response references exceed metadata limit")
	}
	return base64.RawStdEncoding.EncodeToString(raw), nil
}

// DecodeDiagnosticReferences validates a bounded header before any detail query.
func DecodeDiagnosticReferences(encoded string) (DiagnosticReferences, error) {
	var value DiagnosticReferences
	if len(encoded) > MaxDiagnosticReferencesHeader {
		return value, errors.New("diagnostic response references exceed metadata limit")
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return DiagnosticReferences{}, err
	}
	if !validDiagnosticReferences(value) {
		return DiagnosticReferences{}, errors.New("invalid diagnostic response references")
	}
	return value, nil
}

func validDiagnosticReferences(value DiagnosticReferences) bool {
	if value.Schema != "mihari.diagnostic-references/v1" || len(value.Warnings) > MaxWarnings {
		return false
	}
	if value.Diagnostic != nil && !validTransportReference(*value.Diagnostic) {
		return false
	}
	for _, warning := range value.Warnings {
		if len(warning.Message) > 4096 || len(warning.Code) > 256 {
			return false
		}
		if warning.Diagnostic != nil && !validTransportReference(*warning.Diagnostic) {
			return false
		}
	}
	return true
}

func validTransportReference(d Diagnostic) bool {
	// A transport fallback describes unavailable diagnostic delivery without a
	// queryable identity. It must never smuggle an inline cause into this header.
	if d.State == DiagnosticUnavailable {
		if d.ID != "" || d.InstanceID != "" || d.Detail != "" || d.RetrievalError == "" || len(d.RetrievalError) > 4096 {
			return false
		}
		d.State, d.ID = DiagnosticReference, "unavailable"
		d.RetrievalError = ""
	}
	if d.ID == "" || len(d.ID) > 256 || len(d.InstanceID) > 128 || d.State != DiagnosticReference || d.Detail != "" || d.RetrievalError != "" {
		return false
	}
	if len(d.Summary) > 4096 || len(d.Object) > 1024 || len(d.Severity) > 16 {
		return false
	}
	for _, value := range []string{d.Component, d.Event, d.OperationID, d.Operation, string(d.Code), d.TruncationReason} {
		if len(value) > 256 {
			return false
		}
	}
	return true
}
