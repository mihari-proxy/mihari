package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"

	"github.com/mihari-proxy/mihari/internal/config"
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
		return m.mutateSystemProxy(ctx, op, true, force)
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
		return m.mutateSystemProxy(ctx, op, false, false)
	})
	if err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	return result.(protocol.SystemProxyStatus), nil
}

func (m *Manager) mutateSystemProxy(ctx context.Context, op Operation, enable, force bool) (protocol.SystemProxyStatus, error) {
	if m.sysProxy == nil {
		return protocol.SystemProxyStatus{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "system proxy backend is unavailable"}
	}
	if err := m.lockMutation(ctx); err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	defer m.unlock()
	if err := ctx.Err(); err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	if err := m.checkIfRevision(op.IfRevision); err != nil {
		return protocol.SystemProxyStatus{}, err
	}

	settings := m.settingsSnapshot()
	target, host, port, err := resolveSystemProxyTarget(settings.MixedAddr)
	if err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	observed, err := m.sysProxy.Get()
	if err != nil {
		return protocol.SystemProxyStatus{}, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "read system proxy state"}
	}
	if enable && sysproxy.IsForeign(observed, target) && !force {
		return protocol.SystemProxyStatus{}, protocol.APIError{
			Code:    protocol.CodeSystemProxyConflict,
			Message: "system proxy is managed by another application",
			Details: map[string]any{"current_server": observed.Server, "target_server": target},
		}
	}
	if !enable && sysproxy.IsForeign(observed, target) {
		return protocol.SystemProxyStatus{}, protocol.APIError{
			Code:    protocol.CodeSystemProxyNotOwned,
			Message: "system proxy is managed by another application; Mihari will not clear it",
			Details: map[string]any{"current_server": observed.Server, "target_server": target},
		}
	}
	if err := ctx.Err(); err != nil {
		return protocol.SystemProxyStatus{}, err
	}

	candidate, err := m.updateSettings(func(next *config.Settings) error {
		next.SystemProxyDesired = enable
		return nil
	})
	if err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return protocol.SystemProxyStatus{}, m.compensateSystemProxy(ctx, op, candidate, observed, err, false)
	}

	var applyErr error
	if enable {
		if err := m.sysProxy.Enable(host, port); err != nil {
			applyErr = protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "enable system proxy"}
		}
	} else if sysproxy.IsOwned(observed, target) {
		if err := m.sysProxy.Disable(); err != nil {
			applyErr = protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "disable system proxy"}
		}
	}
	if applyErr != nil {
		return protocol.SystemProxyStatus{}, m.compensateSystemProxy(ctx, op, candidate, observed, applyErr, true)
	}
	if err := ctx.Err(); err != nil {
		return protocol.SystemProxyStatus{}, m.compensateSystemProxy(ctx, op, candidate, observed, err, true)
	}
	confirmed, err := m.sysProxy.Get()
	if err != nil {
		applyErr = protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "read system proxy state"}
	} else if enable && !sysproxy.IsOwned(confirmed, target) {
		applyErr = protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "enable system proxy"}
	} else if !enable && confirmed.Enabled {
		applyErr = protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "disable system proxy"}
	}
	if applyErr != nil {
		return protocol.SystemProxyStatus{}, m.compensateSystemProxy(ctx, op, candidate, observed, applyErr, true)
	}

	_, err = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{
		ID: op.ID, Source: op.Source, IfRevision: op.IfRevision,
	}, func(snapshot state.Snapshot) (state.Snapshot, error) {
		return snapshot, nil
	})
	if err != nil {
		return protocol.SystemProxyStatus{}, err
	}
	return m.buildSystemProxyStatus(enable, target, confirmed, ""), nil
}

func (m *Manager) compensateSystemProxy(ctx context.Context, op Operation, candidate settingsCandidate, observed sysproxy.State, cause error, restoreLive bool) error {
	_, rollbackErr := m.restoreSettings(candidate.before)
	var liveRestoreErr error
	if restoreLive {
		liveRestoreErr = m.restoreSystemProxy(observed)
	}
	if rollbackErr == nil && liveRestoreErr == nil {
		return cause
	}
	_, err := m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{
		ID: op.ID, Source: op.Source, IfRevision: op.IfRevision,
	}, func(snapshot state.Snapshot) (state.Snapshot, error) {
		degradedErr := m.enterMutationDegraded(&snapshot)
		return snapshot, degradedErr
	})
	return err
}

func (m *Manager) restoreSystemProxy(observed sysproxy.State) error {
	if !observed.Enabled {
		return m.sysProxy.Disable()
	}
	host, portText, err := net.SplitHostPort(observed.Server)
	if err != nil || host == "" || portText == "" {
		return errors.New("restore system proxy state")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("restore system proxy state")
	}
	return m.sysProxy.Enable(host, port)
}

// ApplyDesiredSystemProxy best-effort enables the OS proxy when settings desire it.
// Intended for daemon startup; failures are returned but callers may ignore them.
func (m *Manager) ApplyDesiredSystemProxy(ctx context.Context) error {
	if m.sysProxy == nil {
		return nil
	}
	if err := m.lockMaintenance(ctx); err != nil {
		return err
	}
	defer m.unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	desired, mixed := m.systemProxySettings()
	target, host, port, err := resolveSystemProxyTarget(mixed)
	if err != nil {
		return err
	}
	if desired {
		if err := m.sysProxy.Enable(host, port); err != nil {
			return fmt.Errorf("apply desired system proxy: %w", err)
		}
		return nil
	}
	observed, err := m.sysProxy.Get()
	if err != nil {
		return fmt.Errorf("apply desired system proxy: %w", err)
	}
	if !sysproxy.IsOwned(observed, target) {
		return nil
	}
	if err := m.sysProxy.Disable(); err != nil {
		return fmt.Errorf("apply desired system proxy: %w", err)
	}
	return nil
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
