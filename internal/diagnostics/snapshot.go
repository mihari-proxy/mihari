package diagnostics

import (
	"context"
	"log/slog"
	"reflect"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// Classification finds the outer API classification with bounded graph traversal.
func Classification(err error) (protocol.APIError, bool) {
	var result protocol.APIError
	remaining := MaxNodes
	var visit func(error, int) bool
	visit = func(current error, depth int) bool {
		if current == nil || depth > MaxDepth || remaining == 0 || nilErrorValue(reflect.ValueOf(current)) {
			return false
		}
		remaining--
		switch current := current.(type) {
		case protocol.APIError:
			result = current
			return true
		case *protocol.APIError:
			result = *current
			return true
		case interface{ Unwrap() []error }:
			for _, child := range current.Unwrap() {
				if visit(child, depth+1) {
					return true
				}
				if remaining == 0 {
					break
				}
			}
		case interface{ Unwrap() error }:
			return visit(current.Unwrap(), depth+1)
		}
		return false
	}
	found := visit(err, 1)
	return result, found
}

// Snapshot returns an occurrence attached to this failure, not a joined child.
func Snapshot(err error) (protocol.Diagnostic, bool) {
	for depth := 0; depth < MaxDepth && err != nil; depth++ {
		if nilErrorValue(reflect.ValueOf(err)) {
			break
		}
		switch current := err.(type) {
		case interface{ DiagnosticSnapshot() protocol.Diagnostic }:
			return current.DiagnosticSnapshot(), true
		case protocol.APIError:
			if current.Diagnostic != nil {
				return *current.Diagnostic, true
			}
			return protocol.Diagnostic{}, false
		case *protocol.APIError:
			if current.Diagnostic != nil {
				return *current.Diagnostic, true
			}
			return protocol.Diagnostic{}, false
		case failure:
			if current.api.Diagnostic != nil {
				return *current.api.Diagnostic, true
			}
			err = current.cause
		case interface{ Unwrap() error }:
			err = current.Unwrap()
		default:
			return protocol.Diagnostic{}, false
		}
	}
	return protocol.Diagnostic{}, false
}

type capturedFailure struct {
	cause    error
	snapshot protocol.Diagnostic
}

func (e capturedFailure) Error() string                           { return e.cause.Error() }
func (e capturedFailure) Unwrap() error                           { return e.cause }
func (e capturedFailure) DiagnosticSnapshot() protocol.Diagnostic { return e.snapshot }
func (e capturedFailure) As(target any) bool {
	api, ok := target.(*protocol.APIError)
	if !ok {
		return false
	}
	classification, found := Classification(e.cause)
	if !found {
		return false
	}
	snapshot := e.snapshot
	classification.Diagnostic = &snapshot
	*api = classification
	return true
}

// WithSnapshot retains cause traversal while attaching an immutable occurrence.
func WithSnapshot(err error, snapshot protocol.Diagnostic) error {
	if err == nil {
		return nil
	}
	return capturedFailure{cause: err, snapshot: snapshot}
}

// Describe captures one occurrence without publishing it or consulting a logger.
func Describe(ctx context.Context, record Record) protocol.Diagnostic {
	captured := Capture(record.Err)
	api, _ := Classification(record.Err)
	summary := api.Message
	if record.Summary != "" {
		summary = record.Summary
	}
	if summary == "" {
		summary, _, _ = strings.Cut(captured.Text, "\n")
	}
	severity := "info"
	if record.Level >= slog.LevelError {
		severity = "error"
	} else if record.Level >= slog.LevelWarn {
		severity = "warning"
	}
	operation, _ := OperationFromContext(ctx)
	return protocol.Diagnostic{
		Code: api.Code, Summary: truncateDiagnostic(summary, 4096),
		Component: truncateDiagnostic(record.Component, 256), Event: truncateDiagnostic(record.Event, 256),
		OperationID: truncateDiagnostic(operation.ID, 256), Operation: truncateDiagnostic(operation.Name, 256), Object: truncateDiagnostic(record.Object, 1024),
		Detail: captured.Text, Truncated: captured.Truncated, TruncationReason: captured.Reason,
		State: protocol.DiagnosticAvailable, Severity: severity,
	}
}

// ReportError synchronously obtains the occurrence published by an injected owner.
// A missing reporter still preserves details for CLI errors before logger setup.
func ReportError(ctx context.Context, reporter Reporter, record Record) error {
	if record.Err == nil {
		return nil
	}
	if snapshot, ok := Snapshot(record.Err); ok && snapshot.ID != "" {
		return record.Err
	}
	var snapshot protocol.Diagnostic
	record.receipt = &snapshot
	if reporter != nil {
		reporter(ctx, record)
	}
	if snapshot.State == "" {
		snapshot = Describe(ctx, record)
	}
	return WithSnapshot(record.Err, snapshot)
}
