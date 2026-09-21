package transport

import (
	"context"
	"net"
)

// UpdateOwner retains a verified elevated local updater process across HTTP requests.
type UpdateOwner interface {
	Key() string
	Exited(context.Context) (bool, error)
	Close() error
}

type updateConnectionKey struct{}

// UpdateConnContext binds the accepted local connection without trusting request fields.
func UpdateConnContext(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, updateConnectionKey{}, conn)
}
