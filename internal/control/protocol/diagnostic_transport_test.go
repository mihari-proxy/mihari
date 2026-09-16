package protocol

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticReferences_WorstCaseMetadataFitsHeaderBudget(t *testing.T) {
	// Every bounded text field uses the worst JSON control-character expansion.
	text := func(n int) string { return strings.Repeat("\x00", n) }
	record := Diagnostic{ID: strings.Repeat("i", 256), InstanceID: strings.Repeat("i", 128), State: DiagnosticReference, Severity: "warning", Summary: text(4096), Object: text(1024), Component: text(256), Event: text(256), OperationID: text(256), Operation: text(256), Code: ErrorCode(text(256)), TruncationReason: text(256)}
	value := DiagnosticReferences{Schema: "mihari.diagnostic-references/v1", Diagnostic: &record}
	for range MaxWarnings {
		value.Warnings = append(value.Warnings, Warning{Message: text(4096), Code: ErrorCode(text(256)), Diagnostic: &record})
	}
	encoded, err := EncodeDiagnosticReferences(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > MaxDiagnosticReferencesHeader {
		t.Fatal("bounded valid metadata exceeds transport budget")
	}
	decoded, err := DecodeDiagnosticReferences(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Warnings) != MaxWarnings || decoded.Warnings[63].Diagnostic.Summary != record.Summary || decoded.Diagnostic.OperationID != record.OperationID {
		t.Fatal("encoded metadata changed")
	}
}

func TestDiagnosticReferences_RejectsBodiesAndInvalidReferences(t *testing.T) {
	for _, snapshot := range []Diagnostic{{ID: "fixture:1", State: DiagnosticReference, Detail: "token=fixture"}, {State: DiagnosticReference}, {ID: "fixture:1", State: DiagnosticAvailable}, {ID: strings.Repeat("x", 257), State: DiagnosticReference}} {
		_, err := EncodeDiagnosticReferences(DiagnosticReferences{Schema: "mihari.diagnostic-references/v1", Diagnostic: &snapshot})
		if err == nil {
			t.Fatal("invalid header reference accepted")
		}
	}
	if _, err := DecodeDiagnosticReferences(strings.Repeat("x", MaxDiagnosticReferencesHeader+1)); err == nil {
		t.Fatal("unbounded response header accepted")
	}
}

func TestDiagnosticReferences_RejectMalformedMetadataOnDecode(t *testing.T) {
	for name, change := range map[string]func(*DiagnosticReferences){
		"schema":           func(v *DiagnosticReferences) { v.Schema = "invalid" },
		"warnings":         func(v *DiagnosticReferences) { v.Warnings = make([]Warning, MaxWarnings+1) },
		"summary":          func(v *DiagnosticReferences) { v.Diagnostic.Summary = strings.Repeat("x", 4097) },
		"object":           func(v *DiagnosticReferences) { v.Diagnostic.Object = strings.Repeat("x", 1025) },
		"operation":        func(v *DiagnosticReferences) { v.Diagnostic.OperationID = strings.Repeat("x", 257) },
		"warning metadata": func(v *DiagnosticReferences) { v.Warnings = []Warning{{Message: strings.Repeat("x", 4097)}} },
		"warning body": func(v *DiagnosticReferences) {
			v.Warnings = []Warning{{Diagnostic: &Diagnostic{ID: "daemon:2", State: DiagnosticReference, Detail: "raw body"}}}
		},
		"unavailable identity": func(v *DiagnosticReferences) {
			v.Diagnostic.State = DiagnosticUnavailable
			v.Diagnostic.RetrievalError = "unavailable"
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := DiagnosticReferences{Schema: "mihari.diagnostic-references/v1", Diagnostic: &Diagnostic{ID: "daemon:1", State: DiagnosticReference}}
			change(&value)
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeDiagnosticReferences(base64.RawStdEncoding.EncodeToString(raw))
			if err == nil || got.Diagnostic != nil || len(got.Warnings) != 0 {
				t.Fatal("malformed metadata entered the client result")
			}
		})
	}
	for _, raw := range []string{"bad:base64", base64.RawStdEncoding.EncodeToString([]byte("not-json"))} {
		if _, err := DecodeDiagnosticReferences(raw); err == nil {
			t.Fatal("malformed encoding accepted")
		}
	}
}
func TestDiagnosticReferences_EncodingFailureReturnsNoPartialHeader(t *testing.T) {
	value := DiagnosticReferences{Schema: "mihari.diagnostic-references/v1", Diagnostic: &Diagnostic{ID: "daemon:1", State: DiagnosticReference, Time: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}}
	if header, err := EncodeDiagnosticReferences(value); err == nil || header != "" {
		t.Fatal("failed encoding produced a partial header")
	}
}
func TestDiagnosticReferences_UnavailableFallbackRetainsReason(t *testing.T) {
	value := DiagnosticReferences{Schema: "mihari.diagnostic-references/v1", Diagnostic: &Diagnostic{State: DiagnosticUnavailable, RetrievalError: "metadata transport unavailable"}}
	header, err := EncodeDiagnosticReferences(value)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeDiagnosticReferences(header)
	if err != nil {
		t.Fatal(err)
	}
	if got.Diagnostic == nil || *got.Diagnostic != *value.Diagnostic {
		t.Fatal("unavailable fallback lost reason or invented identity")
	}
}
