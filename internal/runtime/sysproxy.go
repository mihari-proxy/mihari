package runtime

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

// SystemProxyStatus returns desired intent plus live OS observation.
func (m *Manager) SystemProxyStatus(ctx context.Context) (protocol.SystemProxyStatus, error) {
	if err := m.lockMaintenance(ctx); err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	defer m.unlock()
	if m.sysProxy == nil {
		return protocol.SystemProxyStatus{}, protocol.APIError{
			Code: protocol.CodeInvalidState, Message: "system proxy backend is unavailable",
		}
	}
	return m.systemProxyStatusLocked(ctx)
}

// EnableSystemProxy turns on the OS system proxy for the mixed endpoint.
// When a foreign proxy is active, force must be true to overwrite it.
func (m *Manager) EnableSystemProxy(ctx context.Context, op Operation, force bool) (protocol.SystemProxyStatus, error) {
	result, err := m.doOperation(ctx, "sysproxy-enable:"+op.ID, func() (any, error) {
		if m.sysProxy == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "system proxy backend is unavailable"}
		}
		_, mixed := m.systemProxySettings()
		target, host, port, err := resolveSystemProxyTarget(mixed)
		if err != nil {
			return nil, err
		}
		observed, err := m.sysProxy.Get()
		if err != nil {
			return nil, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "read system proxy state"}
		}
		if sysproxy.IsForeign(observed, target) && !force {
			return nil, protocol.APIError{
				Code:    protocol.CodeSystemProxyConflict,
				Message: "system proxy is managed by another application",
				Details: map[string]any{
					"current_server": observed.Server,
					"target_server":  target,
				},
			}
		}
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		// Check revision before OS write so stale clients do not mutate the desktop proxy.
		if op.IfRevision != nil {
			current := m.store.Load().Revision
			if *op.IfRevision != current {
				return nil, protocol.APIError{
					Code:    protocol.CodeRevisionConflict,
					Message: "state revision changed",
					Details: map[string]any{
						"expected_revision": *op.IfRevision,
						"current_revision":  current,
					},
				}
			}
		}
		if err := m.sysProxy.Enable(host, port); err != nil {
			return nil, protocol.APIError{
				Code:    protocol.CodeUpstreamFailure,
				Message: "enable system proxy",
			}
		}
		_, err = m.updateStateLocked(ctx, state.CommandMeta{
			ID: op.ID, Source: op.Source, IfRevision: op.IfRevision,
		}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			m.settingsMu.Lock()
			m.settings.SystemProxyDesired = true
			saveErr := m.persistSettings()
			m.settingsMu.Unlock()
			if saveErr != nil {
				return snapshot, saveErr
			}
			return snapshot, nil
		})
		if err != nil {
			return nil, err
		}
		return m.systemProxyStatusLocked(ctx)
	})
	if err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	return result.(protocol.SystemProxyStatus), nil
}

// DisableSystemProxy clears Mihari-owned system proxy (policy i).
// Foreign proxies are refused without OS write or desired-state change.
func (m *Manager) DisableSystemProxy(ctx context.Context, op Operation) (protocol.SystemProxyStatus, error) {
	result, err := m.doOperation(ctx, "sysproxy-disable:"+op.ID, func() (any, error) {
		if m.sysProxy == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "system proxy backend is unavailable"}
		}
		_, mixed := m.systemProxySettings()
		target, _, _, err := resolveSystemProxyTarget(mixed)
		if err != nil {
			return nil, err
		}
		observed, err := m.sysProxy.Get()
		if err != nil {
			return nil, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "read system proxy state"}
		}
		if sysproxy.IsForeign(observed, target) {
			return nil, protocol.APIError{
				Code:    protocol.CodeSystemProxyNotOwned,
				Message: "system proxy is managed by another application; Mihari will not clear it",
				Details: map[string]any{
					"current_server": observed.Server,
					"target_server":  target,
				},
			}
		}
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkOpen(); err != nil {
			return nil, err
		}
		if op.IfRevision != nil {
			current := m.store.Load().Revision
			if *op.IfRevision != current {
				return nil, protocol.APIError{
					Code:    protocol.CodeRevisionConflict,
					Message: "state revision changed",
					Details: map[string]any{
						"expected_revision": *op.IfRevision,
						"current_revision":  current,
					},
				}
			}
		}
		if sysproxy.IsOwned(observed, target) {
			if err := m.sysProxy.Disable(); err != nil {
				return nil, protocol.APIError{
					Code:    protocol.CodeUpstreamFailure,
					Message: "disable system proxy",
				}
			}
		}
		_, err = m.updateStateLocked(ctx, state.CommandMeta{
			ID: op.ID, Source: op.Source, IfRevision: op.IfRevision,
		}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			m.settingsMu.Lock()
			m.settings.SystemProxyDesired = false
			saveErr := m.persistSettings()
			m.settingsMu.Unlock()
			if saveErr != nil {
				return snapshot, saveErr
			}
			return snapshot, nil
		})
		if err != nil {
			return nil, err
		}
		return m.systemProxyStatusLocked(ctx)
	})
	if err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	return result.(protocol.SystemProxyStatus), nil
}

// ApplyDesiredSystemProxy best-effort enables the OS proxy when settings desire it.
// Intended for daemon startup; failures are returned but callers may ignore them.
func (m *Manager) ApplyDesiredSystemProxy(ctx context.Context) error {
	if m.sysProxy == nil {
		return nil
	}
	desired, mixed := m.systemProxySettings()
	if !desired {
		return nil
	}
	_, host, port, err := resolveSystemProxyTarget(mixed)
	if err != nil {
		return err
	}
	if err := m.sysProxy.Enable(host, port); err != nil {
		return fmt.Errorf("apply desired system proxy: %w", err)
	}
	return ctx.Err()
}

// ClearOwnedSystemProxy best-effort disables the OS proxy when Mihari owns it.
// Intended for graceful daemon shutdown; failures are returned but callers may ignore them.
func (m *Manager) ClearOwnedSystemProxy(ctx context.Context) error {
	if m.sysProxy == nil {
		return nil
	}
	_, mixed := m.systemProxySettings()
	target, _, _, err := resolveSystemProxyTarget(mixed)
	if err != nil {
		return err
	}
	observed, err := m.sysProxy.Get()
	if err != nil {
		return fmt.Errorf("clear owned system proxy: %w", err)
	}
	if !sysproxy.IsOwned(observed, target) {
		return nil
	}
	if err := m.sysProxy.Disable(); err != nil {
		return fmt.Errorf("clear owned system proxy: %w", err)
	}
	return ctx.Err()
}

func (m *Manager) systemProxySettings() (desired bool, mixed string) {
	settings := m.settingsSnapshot()
	return settings.SystemProxyDesired, settings.MixedAddr
}

func (m *Manager) systemProxyStatusLocked(ctx context.Context) (protocol.SystemProxyStatus, error) {
	if err := ctx.Err(); err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	desired, mixed := m.systemProxySettings()
	target, _, _, err := resolveSystemProxyTarget(mixed)
	if err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	observed, err := m.sysProxy.Get()
	if err != nil {
		return protocol.SystemProxyStatus{}, protocol.APIError{
			Code: protocol.CodeUpstreamFailure, Message: "read system proxy state",
		}
	}
	return m.buildSystemProxyStatus(desired, target, observed, ""), nil
}

func (m *Manager) buildSystemProxyStatus(desired bool, target string, observed sysproxy.State, lastError string) protocol.SystemProxyStatus {
	return protocol.SystemProxyStatus{
		Schema:   "mihari/v1",
		Revision: m.store.Load().Revision,
		Desired:  desired,
		Target:   target,
		Observed: protocol.SystemProxyObserved{
			Enabled: observed.Enabled,
			Server:  observed.Server,
			Owned:   sysproxy.IsOwned(observed, target),
			Foreign: sysproxy.IsForeign(observed, target),
		},
		LastError: lastError,
	}
}

func resolveSystemProxyTarget(mixedAddr string) (target, host string, port int, err error) {
	addrPort, parseErr := netip.ParseAddrPort(mixedAddr)
	if parseErr != nil {
		return "", "", 0, protocol.APIError{
			Code:    protocol.CodeInvalidArgument,
			Message: "invalid mixed-addr for system proxy",
		}
	}
	host = addrPort.Addr().String()
	port = int(addrPort.Port())
	target = sysproxy.NormalizeServer(host, port)
	return target, host, port, nil
}
