package runtime

import (
	"context"

	"github.com/mihari-proxy/mihari/internal/core"
)

// LocalCore returns the last confirmed local executable observation. It never
// executes the core or authorizes execution. External file changes are checked
// by the existing execution boundary when a lifecycle operation needs the file.
func (m *Manager) LocalCore(ctx context.Context) (core.LocalCoreInfo, error) {
	if err := ctx.Err(); err != nil {
		return core.LocalCoreInfo{}, err
	}
	if observed := m.localCore.Load(); observed != nil {
		return *observed, nil
	}
	return core.LocalCoreInfo{}, nil
}

// publishLocalCore is called during construction or by the serialized core
// lifecycle owner. No asynchronous probe can overwrite a later installation.
func (m *Manager) publishLocalCore(version string) {
	m.localCore.Store(&core.LocalCoreInfo{Ready: version != "", Version: version})
}
