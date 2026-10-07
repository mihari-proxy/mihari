package runtime

import (
	"context"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// StartupNetworkStatus observes initial network application without waiting for
// mutation ownership. Later restarts and explicit actions never restart it.
func (m *Manager) StartupNetworkStatus() *protocol.StartupNetworkStatus {
	if m.closing.Load() {
		return nil
	}
	status := protocol.StartupNetworkStatus{
		SystemProxyApplying: m.startupSystemProxyApplying.Load(),
		TunApplying:         m.startupTunApplying.Load(),
	}
	if !status.SystemProxyApplying && !status.TunApplying {
		return nil
	}
	return &status
}

func (m *Manager) applyStartupSystemProxy(ctx context.Context) error {
	if m.sysProxy == nil || ctx.Err() != nil {
		return ctx.Err()
	}
	m.startupSystemProxyApplying.Store(true)
	defer m.startupSystemProxyApplying.Store(false)
	return m.ApplyDesiredSystemProxy(ctx)
}
