package logging

import "context"

type operationContextKey struct{}

// OperationMetadata identifies a logical operation in diagnostic logs.
type OperationMetadata struct {
	ID   string
	Name string
}

// WithOperation attaches a copy of operation to a non-nil context.
func WithOperation(ctx context.Context, operation OperationMetadata) context.Context {
	return context.WithValue(ctx, operationContextKey{}, operation)
}

// OperationFromContext returns operation metadata explicitly bound to ctx.
func OperationFromContext(ctx context.Context) (OperationMetadata, bool) {
	if ctx == nil {
		return OperationMetadata{}, false
	}
	operation, ok := ctx.Value(operationContextKey{}).(OperationMetadata)
	return operation, ok
}
