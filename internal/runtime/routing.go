package runtime

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/state"
)

// RoutingStatus returns saved routing intent and live kernel observation.
func (m *Manager) RoutingStatus(ctx context.Context) (protocol.RoutingStatus, error) {
	if err := m.lockMaintenance(ctx); err != nil {
		return protocol.RoutingStatus{}, err
	}
	defer m.unlock()
	status := m.savedRoutingStatus()
	if m.routingCoreStopped() {
		status.State = "pending"
		return status, nil
	}
	mode, err := m.observeRoutingMode(ctx)
	if err != nil {
		status.Message = "Routing state is unavailable"
		return status, nil
	}
	status.LiveMode = mode
	if mode == status.DesiredMode && !m.mutationDegraded.Load() {
		status.State = "applied"
	}
	group, err := m.observeGlobal(ctx)
	if err == nil {
		status.LiveGlobalSelection = group.Now
	}
	if status.DesiredMode == "global" || status.GlobalSelection != "" {
		if err != nil || group.Now != status.GlobalSelection {
			status.State = "unknown"
		}
	}
	return status, nil
}

// RoutingProxyCatalog binds complete display metadata and duplicate names to
// the candidate list's mutation revision and subscription.
func (m *Manager) RoutingProxyCatalog(ctx context.Context) (mihomo.Proxies, []string, uint64, string, error) {
	if err := m.lockMaintenance(ctx); err != nil {
		return mihomo.Proxies{}, nil, 0, "", err
	}
	defer m.unlock()
	proxies, duplicates, err := m.ProxyCatalog(ctx)
	return proxies, duplicates, m.store.Load().Revision, m.routingSubscriptionID(), err
}

// UpdateRouting persists and applies a routing mode without closing connections.
func (m *Manager) UpdateRouting(ctx context.Context, op Operation, mode string) (protocol.RoutingStatus, error) {
	if !protocol.ValidRoutingMode(mode) {
		return protocol.RoutingStatus{}, routingArgument("invalid routing mode")
	}
	result, err := m.doOperation(ctx, "routing:"+op.ID, func(ctx context.Context) (any, error) {
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkIfRevision(op.IfRevision); err != nil {
			return nil, err
		}
		status, changed, err := m.changeRoutingLocked(ctx, mode, "", false, m.routingCoreStopped())
		if err != nil {
			m.markRoutingDegraded(ctx, err)
			return nil, err
		}
		if changed {
			if _, err := m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{ID: op.ID, Source: op.Source}, func(s state.Snapshot) (state.Snapshot, error) { return s, nil }); err != nil {
				return nil, err
			}
		}
		status.Revision = m.store.Load().Revision
		return status, nil
	})
	if err != nil {
		return protocol.RoutingStatus{}, err
	}
	return result.(protocol.RoutingStatus), nil
}

// RestoreRouting reconciles persisted intent before the supervisor reports a
// healthy core. The supervisor owns its context and retries; no UI is required.
func (m *Manager) RestoreRouting(ctx context.Context) error {
	_, err := m.doOperation(ctx, "routing-restore:", func(ctx context.Context) (any, error) {
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		_, changed, err := m.changeRoutingLocked(ctx, m.settingsSnapshot().RoutingMode(), "", true, false)
		if err != nil {
			m.markRoutingDegraded(ctx, err)
			return nil, err
		}
		if changed {
			_, err = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{Source: "routing-restore"}, func(s state.Snapshot) (state.Snapshot, error) { return s, nil })
		}
		return nil, err
	})
	return err
}

func (m *Manager) routingSubscriptionID() string {
	if m.subscriptions != nil {
		return m.subscriptions.Snapshot().ActiveID
	}
	return m.store.Load().ActiveSubscription
}

func (m *Manager) routingCoreStopped() bool {
	core := m.store.Load().Core
	return core.PID == 0 && (core.Status == "stopped" || core.Status == "missing")
}

func (m *Manager) savedRoutingStatus() protocol.RoutingStatus {
	s := m.settingsSnapshot()
	id := m.routingSubscriptionID()
	return protocol.RoutingStatus{Schema: "mihari/v1", Revision: m.store.Load().Revision, DesiredMode: s.RoutingMode(), State: "unknown", SubscriptionID: id, GlobalSelection: s.GlobalSelection(id), Message: m.routingMessage}
}

func (m *Manager) observeRoutingMode(ctx context.Context) (string, error) {
	if m.controller == nil {
		return "", routingUnavailable("mihomo controller is unavailable", nil)
	}
	document, err := m.controller.Configs(ctx)
	if err != nil {
		return "", err
	}
	mode, _ := document["mode"].(string)
	if !protocol.ValidRoutingMode(mode) {
		return "", routingUnavailable("mihomo returned an invalid routing mode", nil)
	}
	return mode, nil
}

func (m *Manager) observeGlobal(ctx context.Context) (mihomo.Proxy, error) {
	if m.controller == nil {
		return mihomo.Proxy{}, routingUnavailable("mihomo controller is unavailable", nil)
	}
	proxies, err := m.controller.Proxies(ctx)
	if err != nil {
		return mihomo.Proxy{}, err
	}
	group, ok := proxies.Proxies["GLOBAL"]
	if !ok {
		return group, routingUnavailable("GLOBAL group is unavailable", nil)
	}
	return group, nil
}

// changeRoutingLocked holds mutation ownership, but no settings/store mutex
// across IO. Settings are the restart authority: live changes are confirmed
// first, then a single atomic settings commit publishes intent. On a failed
// save or apply the old live values are restored and confirmed. After a crash,
// startup generation and the supervisor readiness hook reapply committed intent.
// A reload caller already owns its revision transaction and must not re-enter it.
func (m *Manager) changeRoutingLocked(ctx context.Context, mode, explicitExit string, restore, stopped bool) (protocol.RoutingStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return protocol.RoutingStatus{}, false, err
	}
	before := m.settingsSnapshot()
	after := before.Clone()
	if mode != before.RoutingMode() {
		after.SetRoutingMode(mode)
	}
	id := m.routingSubscriptionID()
	if m.subscriptions != nil && after.Routing != nil {
		catalog := m.subscriptions.Snapshot()
		for savedID := range after.Routing.GlobalSelections {
			if catalog.Index(savedID) < 0 {
				delete(after.Routing.GlobalSelections, savedID)
			}
		}
	}
	if stopped {
		if err := after.Validate(); err != nil {
			return protocol.RoutingStatus{}, false, err
		}
		if explicitExit != "" {
			return protocol.RoutingStatus{}, false, routingUnavailable("GLOBAL candidates are unavailable while the core is stopped", nil)
		}
		candidate := settingsCandidate{before: before, after: after, changed: !reflect.DeepEqual(before, after)}
		if _, err := m.saveSettingsCandidate(ctx, candidate); err != nil {
			return protocol.RoutingStatus{}, false, err
		}
		m.publishSettings(candidate)
		m.routingMessage = "Saved; waiting for the core to start"
		status := m.savedRoutingStatus()
		status.State = "pending"
		return status, candidate.changed, nil
	}
	oldMode, err := m.observeRoutingMode(ctx)
	if err != nil {
		return protocol.RoutingStatus{}, false, routingUnavailable("read routing state before apply", err)
	}
	var oldGlobal mihomo.Proxy
	exit := before.GlobalSelection(id)
	needGlobal := mode == "global" || exit != "" || explicitExit != ""
	message := ""
	if needGlobal {
		oldGlobal, err = m.observeGlobal(ctx)
		if err != nil {
			return protocol.RoutingStatus{}, false, routingUnavailable("read GLOBAL candidates before apply", err)
		}
		selectable := strings.EqualFold(oldGlobal.Type, "selector")
		if explicitExit != "" {
			if !selectable || !slices.Contains(oldGlobal.All, explicitExit) {
				return protocol.RoutingStatus{}, false, routingArgument("GLOBAL candidate is unavailable or not selectable")
			}
			exit = explicitExit
		} else if !selectable || !slices.Contains(oldGlobal.All, exit) {
			if selectable && slices.Contains(oldGlobal.All, "DIRECT") {
				exit = "DIRECT"
				message = "GLOBAL uses DIRECT; the saved exit was unavailable"
			} else {
				exit = ""
				if mode == "global" {
					mode = "rule"
					after.SetRoutingMode(mode)
				}
				message = "GLOBAL exit is unavailable; choose an exit before using Global"
			}
		}
		if exit != before.GlobalSelection(id) {
			after.SetGlobalSelection(id, exit)
		}
	}
	if err := after.Validate(); err != nil {
		return protocol.RoutingStatus{}, false, err
	}
	liveChanged := oldMode != mode || (exit != "" && exit != oldGlobal.Now)
	if liveChanged {
		if err := m.applyRoutingLive(ctx, mode, exit); err != nil {
			return protocol.RoutingStatus{}, false, m.compensateRouting(ctx, oldMode, oldGlobal.Now, err)
		}
	}
	candidate := settingsCandidate{before: before, after: after, changed: !reflect.DeepEqual(before, after)}
	if _, err := m.saveSettingsCandidate(ctx, candidate); err != nil {
		if liveChanged {
			err = m.compensateRouting(ctx, oldMode, oldGlobal.Now, err)
		}
		return protocol.RoutingStatus{}, false, err
	}
	m.publishSettings(candidate)
	if !restore || candidate.changed || liveChanged {
		m.routingMessage = message
	}
	status := m.savedRoutingStatus()
	status.LiveMode, status.State = mode, "applied"
	if needGlobal {
		status.LiveGlobalSelection = oldGlobal.Now
		if exit != "" {
			status.LiveGlobalSelection = exit
		}
	}
	return status, candidate.changed || liveChanged, nil
}

func (m *Manager) applyRoutingLive(ctx context.Context, mode, exit string) error {
	var applyErr error
	if exit != "" {
		group, err := m.observeGlobal(ctx)
		if err != nil {
			return err
		}
		if group.Now != exit {
			applyErr = m.controller.SelectProxy(ctx, "GLOBAL", exit)
		}
	}
	if applyErr == nil {
		current, err := m.observeRoutingMode(ctx)
		if err != nil {
			applyErr = err
		} else if current != mode {
			applyErr = m.controller.PatchConfigs(ctx, map[string]any{"mode": mode})
		}
	}
	// Even a timed-out write may have committed. Re-read using a bounded owner
	// recovery context, never the canceled request context, before deciding.
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	observed, err := m.observeRoutingMode(check)
	confirmed := err == nil && observed == mode
	if confirmed && exit != "" {
		group, groupErr := m.observeGlobal(check)
		err = errors.Join(err, groupErr)
		confirmed = groupErr == nil && group.Now == exit
	}
	if confirmed {
		return nil
	}
	return routingUnavailable("routing change could not be confirmed", errors.Join(applyErr, err))
}

func (m *Manager) compensateRouting(ctx context.Context, mode, exit string, cause error) error {
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := m.applyRoutingLive(recovery, mode, exit); err != nil {
		m.mutationDegraded.Store(true)
		m.routingMessage = "Routing recovery could not be confirmed; restart required"
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: m.routingMessage, Details: map[string]any{"degraded": true}}, errors.Join(cause, err))
	}
	return cause
}

func routingArgument(message string) error {
	return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: message}
}

// markRoutingDegraded publishes recovery failure outside any coordinator callback.
func (m *Manager) markRoutingDegraded(ctx context.Context, err error) {
	var api protocol.APIError
	if !errors.As(err, &api) || api.Details["degraded"] != true {
		return
	}
	_, _ = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{Source: "routing"}, func(s state.Snapshot) (state.Snapshot, error) {
		s.Health = "degraded"
		s.LastError = "Routing recovery could not be confirmed; restart required"
		return s, nil
	}) // Best-effort health publication; the mutation fence is already active.
}

func routingConflict() error {
	return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "configuration inputs changed during routing preparation"}
}
func routingUnavailable(message string, cause error) error {
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: message}, cause)
}
