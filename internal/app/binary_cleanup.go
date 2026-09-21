package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/platform"
)

// CleanupApplicationBinary maintains only the executable location supplied by process assembly.
func CleanupApplicationBinary(ctx context.Context, executable string) error {
	return platform.CleanupBinaryStashes(ctx, executable)
}
