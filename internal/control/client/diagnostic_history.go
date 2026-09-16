package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type diagnosticQueryContextKey struct{}

// Diagnostics reads a bounded metadata page without recursively reporting query failures.
func (c *Client) Diagnostics(ctx context.Context, instance string, after uint64, limit int) (protocol.DiagnosticList, error) {
	var result protocol.DiagnosticList
	query := url.Values{"instance_id": {instance}, "after": {strconv.FormatUint(after, 10)}}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	outcome := c.doRuntimeOutcome(context.WithValue(ctx, diagnosticQueryContextKey{}, true), http.MethodGet, "/v1/diagnostics?"+query.Encode(), nil, &result, maxControlResponseSize)
	if diagnosticUnsupported(outcome) {
		return protocol.DiagnosticList{Schema: "mihari.diagnostics/v1", State: protocol.DiagnosticUnsupported, Records: []protocol.Diagnostic{}}, nil
	}
	if outcome.err == nil && !validDiagnosticPage(result, instance, after, limit) {
		return protocol.DiagnosticList{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid diagnostic history response"}
	}
	return result, outcome.err
}

// Diagnostic retrieves one occurrence; missing history never retries a business operation.
func (c *Client) Diagnostic(ctx context.Context, id string) (protocol.DiagnosticResult, error) {
	var result protocol.DiagnosticResult
	if id == "" || len(id) > 256 {
		return result, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid diagnostic ID"}
	}
	outcome := c.doRuntimeOutcome(context.WithValue(ctx, diagnosticQueryContextKey{}, true), http.MethodGet, "/v1/diagnostics/"+url.PathEscape(id), nil, &result, maxControlResponseSize)
	if diagnosticUnsupported(outcome) {
		return protocol.DiagnosticResult{Schema: "mihari.diagnostic/v1", State: protocol.DiagnosticUnsupported}, nil
	}
	if outcome.err == nil {
		valid := result.Schema == "mihari.diagnostic/v1"
		switch result.State {
		case protocol.DiagnosticAvailable:
			valid = valid && result.Diagnostic != nil && result.Diagnostic.ID == id &&
				result.Diagnostic.State == protocol.DiagnosticAvailable &&
				len(result.Diagnostic.InstanceID) <= 128 && len(result.Diagnostic.Detail) <= diagnostics.MaxBytes &&
				result.Diagnostic.RetrievalError == "" && validDiagnosticMetadata(*result.Diagnostic)
		case protocol.DiagnosticExpired, protocol.DiagnosticUnknown, protocol.DiagnosticRestarted, protocol.DiagnosticUnsupported, protocol.DiagnosticUnavailable:
			valid = valid && result.Diagnostic == nil
		default:
			valid = false
		}
		if !valid {
			return protocol.DiagnosticResult{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid diagnostic detail response"}
		}
	}
	return result, outcome.err
}

func diagnosticUnsupported(outcome runtimeOutcome) bool {
	return outcome.httpStatus == http.StatusNotFound || outcome.httpStatus == http.StatusMethodNotAllowed
}

// ResolveDiagnostic preserves reference identity when detail cannot be retrieved.
func (c *Client) ResolveDiagnostic(ctx context.Context, snapshot protocol.Diagnostic) protocol.Diagnostic {
	if snapshot.State != protocol.DiagnosticReference {
		return snapshot
	}
	result, err := c.Diagnostic(ctx, snapshot.ID)
	if err != nil {
		snapshot.State = protocol.DiagnosticUnavailable
		snapshot.RetrievalError = diagnostics.Capture(err).Text
		return snapshot
	}
	if result.State == protocol.DiagnosticAvailable {
		return *result.Diagnostic
	}
	snapshot.State = result.State
	return snapshot
}

func (c *Client) resolveErrorDiagnostic(ctx context.Context, err error) error {
	api, ok := diagnostics.Classification(err)
	if !ok {
		return err
	}
	snapshot := protocol.Diagnostic{Code: api.Code, Summary: api.Message, State: protocol.DiagnosticUnsupported}
	if api.Diagnostic != nil {
		snapshot = c.ResolveDiagnostic(ctx, *api.Diagnostic)
	}
	api.Diagnostic = &snapshot
	c.resolveWarnings(ctx, &api.WarningOutcome)
	return api
}

func (c *Client) resolveWarnings(ctx context.Context, outcome *protocol.WarningOutcome) {
	if outcome == nil {
		return
	}
	if len(outcome.Warnings) > protocol.MaxWarnings {
		outcome.WarningsOmitted += uint64(len(outcome.Warnings) - protocol.MaxWarnings)
		outcome.Warnings = outcome.Warnings[:protocol.MaxWarnings]
	}
	for i := range outcome.Warnings {
		warning := &outcome.Warnings[i]
		if warning.Diagnostic == nil {
			warning.Diagnostic = &protocol.Diagnostic{Code: warning.Code, Summary: warning.Message, State: protocol.DiagnosticUnsupported}
			continue
		}
		snapshot := c.ResolveDiagnostic(ctx, *warning.Diagnostic)
		warning.Diagnostic = &snapshot
	}
}

// validDiagnosticPage prevents malformed metadata from advancing a session's
// cursor or entering its bounded history. It never publishes query failures.
func validDiagnosticPage(page protocol.DiagnosticList, instance string, after uint64, limit int) bool {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if page.Schema != "mihari.diagnostics/v1" || len(page.Records) > limit {
		return false
	}
	switch page.State {
	case protocol.DiagnosticUnsupported:
		return len(page.Records) == 0 && !page.HasMore
	case protocol.DiagnosticRestarted:
		if instance == "" || instance == page.InstanceID {
			return false
		}
		after = 0
	case protocol.DiagnosticAvailable:
		if instance != "" && instance != page.InstanceID {
			return false
		}
	default:
		return false
	}
	if page.InstanceID == "" || len(page.InstanceID) > 128 || page.OldestSequence == 0 {
		return false
	}
	if page.OldestSequence > page.LatestSequence && page.OldestSequence-page.LatestSequence != 1 {
		return false
	}
	if page.LostBefore != (after < page.OldestSequence-1) {
		return false
	}
	previous := after
	for _, record := range page.Records {
		if record.InstanceID != page.InstanceID || record.Sequence <= previous || record.Sequence < page.OldestSequence || record.Sequence > page.LatestSequence {
			return false
		}
		if record.ID != page.InstanceID+":"+strconv.FormatUint(record.Sequence, 10) || len(record.ID) > 256 || record.State != protocol.DiagnosticReference {
			return false
		}
		if record.Detail != "" || record.RetrievalError != "" || !validDiagnosticMetadata(record) {
			return false
		}
		previous = record.Sequence
	}
	if page.NextSequence != previous {
		return false
	}
	if page.HasMore {
		return len(page.Records) > 0 && previous < page.LatestSequence
	}
	return len(page.Records) == 0 || previous == page.LatestSequence
}

func validDiagnosticMetadata(record protocol.Diagnostic) bool {
	if len(record.Summary) > 4096 || len(record.Object) > 1024 {
		return false
	}
	for _, value := range []string{record.Component, record.Event, record.OperationID, record.Operation, string(record.Code), record.TruncationReason} {
		if len(value) > 256 {
			return false
		}
	}
	switch record.Severity {
	case "", "debug", "info", "warning", "error":
		return true
	default:
		return false
	}
}

// readDiagnosticReferences restores transport-only metadata before rendering or
// resolving details. Corrupt metadata never changes the business outcome.
func readDiagnosticReferences(header http.Header, failure **protocol.Diagnostic, warnings *protocol.WarningOutcome) {
	encoded := header.Get(protocol.DiagnosticReferencesHeader)
	if encoded == "" {
		return
	}
	supplement, err := protocol.DecodeDiagnosticReferences(encoded)
	if err != nil {
		snapshot := diagnostics.Describe(context.Background(), diagnostics.Record{Summary: "Diagnostic response metadata could not be read", Err: err})
		snapshot.State = protocol.DiagnosticUnavailable
		snapshot.Severity = "warning"
		warnings.Append(protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: snapshot.Summary, Diagnostic: &snapshot}}})
		return
	}
	if failure != nil && *failure == nil {
		*failure = supplement.Diagnostic
	}
	warnings.Append(supplement.WarningOutcome)
}
