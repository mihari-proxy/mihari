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

// encodeDiagnosticResponse preserves the ordinary body limit without changing a
// committed result. Metadata uses a separate bounded header only if even body
// references would overflow; all bodies remain available through readonly IPC.
func encodeDiagnosticResponse(value any, added protocol.WarningOutcome) ([]byte, string, error) {
	response := copyDiagnosticResponse(value)
	budget := diagnostics.MaxBytes
	if response.failure != nil && *response.failure != nil {
		budget -= len((*response.failure).Detail)
	}
	if response.warnings != nil {
		*response.warnings = budgetWarnings(*response.warnings, added, budget)
	}
	body, err := json.Marshal(response.value)
	if err != nil || len(body)+1 <= 4<<20 {
		return body, "", err
	}
	if response.failure != nil && *response.failure != nil && (*response.failure).ID != "" {
		reference := (*response.failure).Reference()
		*response.failure = &reference
	}
	if response.warnings != nil {
		*response.warnings = budgetWarnings(*response.warnings, protocol.WarningOutcome{}, 0)
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
		unavailable := protocol.Diagnostic{
			State: protocol.DiagnosticUnavailable, Severity: "warning",
			Summary:        "Diagnostic response details are unavailable",
			RetrievalError: "Diagnostic response metadata could not be transmitted: " + err.Error(),
		}
		fallback := protocol.DiagnosticReferences{Schema: "mihari.diagnostic-references/v1"}
		if supplement.Diagnostic != nil {
			fallback.Diagnostic = &unavailable
		}
		if len(supplement.Warnings) > 0 || supplement.WarningsOmitted > 0 {
			fallback.Warnings = []protocol.Warning{{Message: unavailable.Summary, Diagnostic: &unavailable}}
			fallback.WarningsOmitted = supplement.WarningsOmitted + uint64(len(supplement.Warnings))
		}
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
func budgetWarnings(existing, added protocol.WarningOutcome, bytes int) protocol.WarningOutcome {
	outcome := existing.Clone()
	outcome.Append(added)
	for i := range outcome.Warnings {
		detail := outcome.Warnings[i].Diagnostic
		if detail == nil {
			continue
		}
		if len(detail.Detail) > bytes && detail.ID != "" {
			reference := detail.Reference()
			outcome.Warnings[i].Diagnostic = &reference
		} else {
			bytes -= len(detail.Detail)
		}
	}
	return outcome
}
