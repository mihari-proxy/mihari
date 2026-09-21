//go:build windows

package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func cleanupBinaryUpdates(ctx context.Context, binary string) error {
	return platform.CleanupReplacedBinary(ctx, binary)
}
