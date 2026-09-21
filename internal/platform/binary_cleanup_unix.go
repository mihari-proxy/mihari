//go:build !windows

package platform

import (
	"context"
	"io"
)

type noBinaryMaintenance struct{}

func (noBinaryMaintenance) Close() error { return nil }

// LockBinaryMaintenance is unnecessary on Unix; app replacement retains its existing install lease.
func LockBinaryMaintenance(context.Context, string) (io.Closer, error) {
	return noBinaryMaintenance{}, nil
}

// CleanupBinaryStashes is a no-op on Unix, where replacement does not create Windows stashes.
func CleanupBinaryStashes(context.Context, string) error { return nil }
