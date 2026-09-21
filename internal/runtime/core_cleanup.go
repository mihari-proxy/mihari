package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path/filepath"
	"time"
)

// CleanupOldCore removes historical Windows stashes under the daemon's core mutation owner.
// Transaction journals and backups use different paths and are never cleanup candidates.
func (m *Manager) CleanupOldCore(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if m.installRequest.BinaryPath == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Dir(m.installRequest.BinaryPath)); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := m.lockMutation(ctx); err != nil {
		return err
	}
	defer m.unlock()
	return platform.CleanupBinaryStashes(ctx, m.installRequest.BinaryPath)
}
