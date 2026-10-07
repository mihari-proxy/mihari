package app

import (
	"context"
	"fmt"
	"os"
)

// CleanupCurrentBinaryUpdates retries cleanup beside this process's executable.
// It never accesses daemon business files or discovers other installations.
func CleanupCurrentBinaryUpdates(ctx context.Context) error {
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable for startup cleanup: %w", err)
	}
	if err := cleanupBinaryUpdates(ctx, binary); err != nil {
		return fmt.Errorf("cleanup updates beside %s: %w", binary, err)
	}
	return nil
}
