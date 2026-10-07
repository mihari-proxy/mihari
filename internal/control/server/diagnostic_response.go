package server

import (
	"encoding/json"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// diagnosticResponse borrows fields from a private DTO copy. Response budgeting
// cannot mutate a cached business result or its captured diagnostic pointers.
type diagnosticResponse struct {
	value    any
	warnings *protocol.WarningOutcome
	failure  **protocol.Diagnostic
}

func copyDiagnosticResponse(value any) diagnosticResponse {
	switch result := value.(type) {
	case protocol.EgressStatus:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.LoggingStatus:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.MutationResult:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.CoreInstallResult:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.GeoIPUpdateResult:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.OnboardingStatus:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.TUIPreferences:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.RoutingStatus:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.SystemProxyStatus:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.TunStatus:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.SubscriptionResult:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.SubscriptionList:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.WebGUIStatus:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.PanelList:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome}
	case protocol.ErrorEnvelope:
		return diagnosticResponse{value: &result, warnings: &result.WarningOutcome, failure: &result.Error.Diagnostic}
	}
	return diagnosticResponse{value: value}
}

// encodeDiagnosticResponse keeps diagnostic additions within the ordinary body
// limit when the business result itself fits. Excess details use the existing
// history; absent history is explicit. Metadata uses a separate bounded header
// only if even body references would overflow, preserving committed results.
func encodeDiagnosticResponse(value any, added protocol.WarningOutcome, history *diagnostics.History) ([]byte, string, error) {
	response := copyDiagnosticResponse(value)
	budget := diagnostics.MaxBytes
	if response.failure != nil && *response.failure != nil {
		budget -= len((*response.failure).Detail)
	}
	if response.warnings != nil {
		*response.warnings = budgetWarnings(*response.warnings, added, budget, history)
	}
	body, err := json.Marshal(response.value)
	if err != nil || len(body)+1 <= 4<<20 {
		return body, "", err
	}
	if response.failure != nil && *response.failure != nil {
		reference := responseDiagnosticReference(**response.failure, history)
		*response.failure = &reference
	}
	if response.warnings != nil {
		*response.warnings = budgetWarnings(*response.warnings, protocol.WarningOutcome{}, 0, history)
	}
	body, err = json.Marshal(response.value)
	if err != nil || len(body)+1 <= 4<<20 {
		return body, "", err
	}
	supplement := protocol.DiagnosticReferences{Schema: "mihari.diagnostic-references/v1"}
	if response.failure != nil {
		supplement.Diagnostic = *response.failure
	}
	if response.warnings != nil {
		supplement.WarningOutcome = *response.warnings
	}
	if supplement.Diagnostic == nil && len(supplement.Warnings) == 0 && supplement.WarningsOmitted == 0 {
		return body, "", nil
	}
	header, err := protocol.EncodeDiagnosticReferences(supplement)
	if err != nil {
		// Invalid diagnostic metadata must not turn a committed mutation into a
		// failed or unreadable business response. State the delivery loss explicitly;
		// do not edit the owner's captured text or replay the operation.
		fallback := unavailableDiagnosticReferences(supplement, "Diagnostic response metadata could not be transmitted: "+err.Error())
		header, err = protocol.EncodeDiagnosticReferences(fallback)
		if err != nil {
			return body, "", err
		}
	}
	if response.failure != nil {
		*response.failure = nil
	}
	if response.warnings != nil {
		*response.warnings = protocol.WarningOutcome{}
	}
	body, err = json.Marshal(response.value)
	return body, header, err
}

// Already captured bodies remain untouched; only their transmission form changes.
func budgetWarnings(existing, added protocol.WarningOutcome, bytes int, history *diagnostics.History) protocol.WarningOutcome {
	bytes = max(0, bytes)
	outcome := existing.Clone()
	outcome.Append(added)
	for i := range outcome.Warnings {
		detail := outcome.Warnings[i].Diagnostic
		if detail == nil {
			continue
		}
		if len(detail.Detail) > bytes {
			reference := responseDiagnosticReference(*detail, history)
			outcome.Warnings[i].Diagnostic = &reference
		} else {
			bytes -= len(detail.Detail)
		}
	}
	return outcome
}

// responseDiagnosticReference preserves anonymous collected text in the existing
// bounded history before removing it from the transmission copy. If history is
// unavailable, report that delivery limit explicitly without changing the source.
func responseDiagnosticReference(snapshot protocol.Diagnostic, history *diagnostics.History) protocol.Diagnostic {
	if snapshot.ID == "" && history != nil && snapshot.State == protocol.DiagnosticAvailable {
		snapshot = history.Add(snapshot)
	}
	if snapshot.ID != "" {
		return snapshot.Reference()
	}
	return unavailableResponseDiagnostic(snapshot, "Diagnostic detail exceeds the response budget and no queryable history is available")
}

func unavailableResponseDiagnostic(snapshot protocol.Diagnostic, reason string) protocol.Diagnostic {
	snapshot.ID, snapshot.InstanceID, snapshot.Sequence = "", "", 0
	snapshot.Detail, snapshot.State, snapshot.RetrievalError = "", protocol.DiagnosticUnavailable, reason
	if _, err := protocol.EncodeDiagnosticReferences(protocol.DiagnosticReferences{Schema: "mihari.diagnostic-references/v1", Diagnostic: &snapshot}); err == nil {
		return snapshot
	}
	// Malformed metadata must not prevent other valid warning summaries from
	// reaching the client. Preserve each representable classification field.
	fallback := protocol.Diagnostic{State: protocol.DiagnosticUnavailable, Severity: "warning", Summary: "Diagnostic response metadata is unavailable", RetrievalError: reason}
	if len(snapshot.Severity) <= 16 {
		fallback.Severity = snapshot.Severity
	}
	if len(snapshot.Summary) <= 4096 {
		fallback.Summary = snapshot.Summary
	}
	if len(snapshot.Code) <= 256 {
		fallback.Code = snapshot.Code
	}
	return fallback
}

func unavailableDiagnosticReferences(value protocol.DiagnosticReferences, reason string) protocol.DiagnosticReferences {
	result := protocol.DiagnosticReferences{Schema: value.Schema, WarningOutcome: value.Clone()}
	if value.Diagnostic != nil {
		if _, err := protocol.EncodeDiagnosticReferences(protocol.DiagnosticReferences{Schema: value.Schema, Diagnostic: value.Diagnostic}); err == nil {
			result.Diagnostic = value.Diagnostic
		} else {
			snapshot := unavailableResponseDiagnostic(*value.Diagnostic, reason)
			result.Diagnostic = &snapshot
		}
	}
	for i, warning := range result.Warnings {
		if _, err := protocol.EncodeDiagnosticReferences(protocol.DiagnosticReferences{Schema: value.Schema, WarningOutcome: protocol.WarningOutcome{Warnings: []protocol.Warning{warning}}}); err == nil {
			continue
		}
		var snapshot protocol.Diagnostic
		if warning.Diagnostic != nil {
			snapshot = *warning.Diagnostic
		}
		snapshot = unavailableResponseDiagnostic(snapshot, reason)
		warning.Diagnostic = &snapshot
		if len(warning.Message) > 4096 || len(warning.Code) > 256 {
			warning.Message, warning.Code = "Diagnostic warning metadata is unavailable", ""
			result.WarningsOmitted++
		}
		result.Warnings[i] = warning
	}
	return result
}
