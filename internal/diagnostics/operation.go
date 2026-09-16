package diagnostics

import "context"

type operationContextKey struct{}

// OperationMetadata identifies a logical operation across diagnostic outlets.
type OperationMetadata struct {
	ID   string
	Name string
}

// WithOperation attaches copied operation metadata to a non-nil context.
func WithOperation(ctx context.Context, operation OperationMetadata) context.Context {
	return context.WithValue(ctx, operationContextKey{}, operation)
}

// OperationFromContext retrieves an explicitly bound operation identity.
func OperationFromContext(ctx context.Context) (OperationMetadata, bool) {
	if ctx == nil {
		return OperationMetadata{}, false
	}
	operation, ok := ctx.Value(operationContextKey{}).(OperationMetadata)
	return operation, ok
}
