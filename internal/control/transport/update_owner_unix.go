//go:build !windows

package transport

import (
	"context"
	"errors"
)

// UpdatePreparationAvailable limits the new maintenance protocol to Windows.
const UpdatePreparationAvailable = false

// OpenUpdateOwner refuses unsupported transports instead of accepting a supplied PID.
func OpenUpdateOwner(context.Context) (UpdateOwner, error) {
	return nil, errors.New("windows update preparation is unavailable")
}
