package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestDiagnosticResponse_InlineBudgetReferencesExcessWarnings(t *testing.T) {
	ctx, result := diagnostics.WithResult(context.Background())
	first := protocol.Diagnostic{ID: "instance:1", State: protocol.DiagnosticAvailable, Detail: strings.Repeat("\x00", diagnostics.MaxBytes)}
	second := protocol.Diagnostic{ID: "instance:2", State: protocol.DiagnosticAvailable, Detail: strings.Repeat("x", diagnostics.MaxBytes)}
	diagnostics.ReturnWarnings(ctx, protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: "first", Diagnostic: &first}, {Message: "second", Diagnostic: &second}}})
	recorder := httptest.NewRecorder()
	writer := &responseWriteObserver{ResponseWriter: recorder, diagnosticResult: result}
	writeJSON(writer, http.StatusOK, protocol.MutationResult{Schema: "mihari/v1", OperationID: "operation"})
	var got protocol.MutationResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || got.OperationID != "operation" || len(got.Warnings) != 2 {
		t.Fatalf("business result changed: %+v", got)
	}
	if got.Warnings[0].Diagnostic.Detail != first.Detail {
		t.Fatal("JSON escaping changed original detail")
	}
	if got.Warnings[1].Diagnostic.State != protocol.DiagnosticReference || got.Warnings[1].Diagnostic.Detail != "" || got.Warnings[1].Diagnostic.ID != second.ID {
		t.Fatal("excess detail should be referenced")
	}
	if result.Warnings().Warnings[1].Diagnostic.Detail != second.Detail {
		t.Fatal("response budgeting mutated the captured result")
	}
	if recorder.Body.Len() >= 4<<20 {
		t.Fatal("response exceeds existing client limit")
	}
}

func TestDiagnosticResponse_EncodedBudgetKeepsBusinessAndReferencesDetail(t *testing.T) {
	ctx, receipt := diagnostics.WithResult(context.Background())
	_, history := diagnosticServer(t, 10)
	snapshot := history.Add(protocol.Diagnostic{Summary: "saved with warning", Detail: strings.Repeat("\x00", diagnostics.MaxBytes)})
	diagnostics.ReturnWarnings(ctx, protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: snapshot.Summary, Diagnostic: &snapshot}}})
	// The business result fits the existing limit; JSON escaping of a bounded
	// diagnostic must not make the already committed operation unreadable.
	business := protocol.SubscriptionResult{Schema: "mihari/v1", Revision: 42, Subscription: protocol.Subscription{Name: strings.Repeat("b", 3<<20)}}
	recorder := httptest.NewRecorder()
	writer := &responseWriteObserver{ResponseWriter: recorder, diagnosticResult: receipt}
	writeJSON(writer, http.StatusOK, business)
	if recorder.Body.Len() > 4<<20 {
		t.Fatalf("encoded response exceeds client limit: %d bytes", recorder.Body.Len())
	}
	var got protocol.SubscriptionResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || got.Revision != 42 || got.Subscription.Name != business.Subscription.Name {
		t.Fatal("committed business result changed")
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Diagnostic == nil {
		t.Fatal("warning was lost")
	}
	ref := got.Warnings[0].Diagnostic
	if ref.State != protocol.DiagnosticReference || ref.ID != snapshot.ID || ref.Detail != "" {
		t.Fatal("oversized inline diagnostic was not replaced with its reference")
	}
	if stored := history.Get(ref.ID); stored.Diagnostic == nil || stored.Diagnostic.Detail != snapshot.Detail {
		t.Fatal("captured original detail changed")
	}
	if receipt.Warnings().Warnings[0].Diagnostic.Detail != snapshot.Detail {
		t.Fatal("budgeting modified the operation result")
	}
}

func TestDiagnosticResponse_ExactErrorBudgetPreservesClassification(t *testing.T) {
	server, history := diagnosticServer(t, 10)
	snapshot := history.Add(protocol.Diagnostic{Summary: "fixture failure", Detail: "token=fixture-original", State: protocol.DiagnosticAvailable})
	envelope := protocol.NewError(protocol.CodeDataFailure, "fixture failure", map[string]any{"fixture": ""})
	base, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", (4<<20)-len(base)-1)
	envelope.Error.Details["fixture"] = payload
	envelope.Error.Diagnostic = &snapshot
	recorder := httptest.NewRecorder()
	server.writeControlError(context.Background(), recorder, diagnostics.MarkReported(envelope.Error))
	if recorder.Body.Len() != 4<<20 {
		t.Fatalf("error body exceeds original business budget: %d", recorder.Body.Len())
	}
	var got protocol.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusUnprocessableEntity || got.Error.Code != envelope.Error.Code || got.Error.Message != envelope.Error.Message || got.Error.Details["fixture"] != payload {
		t.Fatal("diagnostic delivery changed public error classification or data")
	}
	if recorder.Header().Get("X-Mihari-Diagnostic-References") == "" {
		t.Fatal("diagnostic reference disappeared")
	}
}

func TestDiagnosticResponse_InvalidSupplementDoesNotInvalidateCommit(t *testing.T) {
	for _, kind := range []string{"missing_identity", "oversized_metadata"} {
		t.Run(kind, func(t *testing.T) {
			business := protocol.SubscriptionResult{Schema: "mihari/v1", Revision: 42}
			base, err := json.Marshal(business)
			if err != nil {
				t.Fatal(err)
			}
			business.Subscription.Name = strings.Repeat("x", (4<<20)-len(base)-1)
			snapshot := protocol.Diagnostic{State: protocol.DiagnosticAvailable, Detail: "token=fixture-captured"}
			message := "fixture warning"
			if kind == "oversized_metadata" {
				snapshot.ID = "fixture:1"
				message = strings.Repeat("x", 4097)
			}
			business.WarningOutcome = protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: message, Diagnostic: &snapshot}}}
			recorder := httptest.NewRecorder()
			writer := &responseWriteObserver{ResponseWriter: recorder}
			writeJSON(writer, http.StatusOK, business)
			if writer.err != nil {
				t.Fatalf("diagnostic supplement invalidated committed result: %v", writer.err)
			}
			var got protocol.SubscriptionResult
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || got.Revision != 42 || got.Subscription.Name != business.Subscription.Name || recorder.Body.Len() > 4<<20 {
				t.Fatal("committed business outcome changed")
			}
			supplement, err := protocol.DecodeDiagnosticReferences(recorder.Header().Get(protocol.DiagnosticReferencesHeader))
			if err != nil {
				t.Fatal(err)
			}
			if len(supplement.Warnings) != 1 || supplement.Warnings[0].Diagnostic == nil || supplement.Warnings[0].Diagnostic.State != protocol.DiagnosticUnavailable || supplement.Warnings[0].Diagnostic.RetrievalError == "" || supplement.WarningsOmitted != 1 {
				t.Fatalf("missing explicit diagnostic delivery failure: %#v", supplement)
			}
			if business.Warnings[0].Diagnostic.Detail != "token=fixture-captured" {
				t.Fatal("captured original text was changed")
			}
		})
	}
}
