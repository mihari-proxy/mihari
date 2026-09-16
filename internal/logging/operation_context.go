package logging

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// OperationMetadata identifies a logical operation in all diagnostic outlets.
type OperationMetadata = diagnostics.OperationMetadata

// WithOperation attaches a copy of operation to a non-nil context.
func WithOperation(ctx context.Context, operation OperationMetadata) context.Context {
	return diagnostics.WithOperation(ctx, operation)
}

// OperationFromContext returns operation metadata explicitly bound to ctx.
func OperationFromContext(ctx context.Context) (OperationMetadata, bool) {
	return diagnostics.OperationFromContext(ctx)
}
