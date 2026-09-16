package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiagnostic_RoundTripPreservesOriginalContent(t *testing.T) {
	const original = `{"schema":"mihari.error/v1","error":{"code":"data_failure","message":"load credential","details":{"field":"credential"},"diagnostic":{"id":"instance:1","detail":"open /private/config: token=fixture-token\nhttps://user:password@example.test/sub?token=fixture-token","truncated":false}}}`
	var envelope ErrorEnvelope
	if err := json.Unmarshal([]byte(original), &envelope); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"schema":"mihari.error/v1"`, `"code":"data_failure"`, `"field":"credential"`, `"diagnostic":`, "fixture-token", "user:password", "/private/config"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("diagnostic round trip lost %q: %s", want, raw)
		}
	}
}

func TestDiagnostic_OptionalFieldsPreserveLegacyEnvelope(t *testing.T) {
	raw, err := json.Marshal(NewError(CodeDataFailure, "load credential", nil))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"schema":"mihari.error/v1","error":{"code":"data_failure","message":"load credential"}}`
	if string(raw) != want {
		t.Fatalf("legacy envelope = %s, want %s", raw, want)
	}
}

func TestWarningOutcome_AppendBoundsPreexistingWarnings(t *testing.T) {
	outcome := WarningOutcome{Warnings: make([]Warning, MaxWarnings+3), WarningsOmitted: 2}
	outcome.Append(WarningOutcome{Warnings: []Warning{{Message: "next"}}, WarningsOmitted: 4})
	if len(outcome.Warnings) != MaxWarnings || outcome.WarningsOmitted != 10 {
		t.Fatalf("warning count=%d omitted=%d", len(outcome.Warnings), outcome.WarningsOmitted)
	}
}

func TestWarningOutcome_ReplayCopiesCannotMutateOriginal(t *testing.T) {
	original := WarningOutcome{Warnings: []Warning{{Message: "saved", Diagnostic: &Diagnostic{ID: "daemon:1", State: DiagnosticAvailable, Detail: "token=fixture-original"}}, {Message: "legacy warning"}}, WarningsOmitted: 3}
	clone := original.Clone()
	clone.Warnings[0].Message = "changed"
	clone.Warnings[0].Diagnostic.Detail = "changed"
	clone.Warnings[1].Message = "changed"
	clone.WarningsOmitted = 4
	if original.Warnings[0].Message != "saved" || original.Warnings[0].Diagnostic.Detail != "token=fixture-original" || original.Warnings[1].Message != "legacy warning" || original.WarningsOmitted != 3 {
		t.Fatal("replay copy mutated cached warning")
	}
	var zero WarningOutcome
	if zero.Clone().Warnings != nil {
		t.Fatal("zero warning clone changed JSON omission")
	}
}
func TestWarningOutcome_ReferencesKeepUnqueryableDetails(t *testing.T) {
	original := WarningOutcome{Warnings: []Warning{
		{Diagnostic: &Diagnostic{ID: "daemon:1", State: DiagnosticAvailable, Detail: "remote cause"}},
		{Diagnostic: &Diagnostic{State: DiagnosticAvailable, Detail: "local token=fixture"}},
		{Message: "legacy"},
	}}
	refs := original.References()
	if refs.Warnings[0].Diagnostic.State != DiagnosticReference || refs.Warnings[0].Diagnostic.Detail != "" || refs.Warnings[0].Diagnostic.ID != "daemon:1" {
		t.Fatal("cached queryable warning did not retain its reference")
	}
	if refs.Warnings[1].Diagnostic.Detail != "local token=fixture" || refs.Warnings[2].Diagnostic != nil || original.Warnings[0].Diagnostic.Detail != "remote cause" {
		t.Fatal("reference conversion discarded local-only cause or mutated original")
	}
}
func TestWarningOutcome_AppendOwnsSnapshotsAndReportsOmissions(t *testing.T) {
	input := WarningOutcome{Warnings: []Warning{{Message: "original", Diagnostic: &Diagnostic{Detail: "token=fixture"}}}, WarningsOmitted: 2}
	var result WarningOutcome
	result.DiagnosticWarnings().Append(input)
	input.Warnings[0].Diagnostic.Detail = "mutated"
	input.Warnings[0].Message = "mutated"
	if result.Warnings[0].Diagnostic.Detail != "token=fixture" || result.Warnings[0].Message != "original" {
		t.Fatal("append borrowed caller memory")
	}
	for range MaxWarnings {
		result.Append(WarningOutcome{Warnings: []Warning{{Message: "next"}}})
	}
	if len(result.Warnings) != MaxWarnings || result.WarningsOmitted != 3 {
		t.Fatal("append did not bound collection or preserve omission count")
	}
}
