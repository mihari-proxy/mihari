package diagnostics

import (
	"context"
	"sync"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type resultContextKey struct{}

// Result is a call-scoped diagnostic result, separate from persisted business data.
// A control adapter supplies it to receive warnings from use cases that return
// either a value or only an error. It never owns or stores a context.
type Result struct {
	mu       sync.Mutex
	warnings protocol.WarningOutcome
}

// WithResult creates a result receiver for this call and its child operations.
func WithResult(ctx context.Context) (context.Context, *Result) {
	result := &Result{}
	return context.WithValue(ctx, resultContextKey{}, result), result
}

// Warnings returns a copy of the diagnostic result without transferring ownership.
func (r *Result) Warnings() protocol.WarningOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.warnings.Clone()
}

// ReturnWarnings returns already published occurrences to the invoking adapter.
// Replaying an operation returns its original warnings without publishing again.
func ReturnWarnings(ctx context.Context, warnings protocol.WarningOutcome) {
	result, ok := ctx.Value(resultContextKey{}).(*Result)
	if !ok || result == nil {
		return
	}
	result.mu.Lock()
	defer result.mu.Unlock()
	result.warnings.Append(warnings)
}
