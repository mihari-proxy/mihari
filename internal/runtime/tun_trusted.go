package runtime

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

// Trusted TUN changes prepare source-derived bytes before mutation ownership.
// Settings/catalog/cache remain restart authority; no new graph WAL is created.
func (m *Manager) mutateTrustedTun(ctx context.Context, op Operation, enable, force bool) (protocol.TunStatus, error) {
	if err := m.lockMutation(ctx); err != nil {
		return protocol.TunStatus{}, err
	}
	err := m.checkIfRevision(op.IfRevision)
	m.unlock()
	if err != nil {
		return protocol.TunStatus{}, err
	}
	conflict := m.detectTunConflict(ctx)
	if err := ctx.Err(); err != nil {
		return protocol.TunStatus{}, err
	}
	if enable && !force && conflict != nil && len(conflict.OtherTunInterfaces) > 0 {
		return protocol.TunStatus{}, protocol.APIError{Code: protocol.CodeTunConflict, Message: "other TUN adapters detected; routing conflict or loop risk", Details: map[string]any{"other_tun_interfaces": conflict.OtherTunInterfaces, "other_mihomo_processes": conflict.OtherMihomoProcesses}}
	}
	candidate, err := m.prepareSettings(func(s *config.Settings) error { s.Tun = buildManagedTun(enable, s.Tun); return nil })
	if err != nil {
		return protocol.TunStatus{}, err
	}
	liveBefore, ok := m.captureTunLive(ctx)
	if !ok {
		return protocol.TunStatus{}, protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "TUN live state before apply is unavailable"}
	}
	if m.subscriptions == nil {
		return protocol.TunStatus{}, subscriptionsUnavailable()
	}
	catalog := m.subscriptions.Snapshot()
	var sourceHash [32]byte
	if catalog.ActiveID != "" {
		raw, _, err := m.subscriptions.ReadCache(catalog.ActiveID)
		if err != nil {
			return protocol.TunStatus{}, err
		}
		sourceHash = sha256.Sum256(raw)
	}
	generated, err := m.prepareTunConfigWithSettings(ctx, catalog, candidate.after, candidate.generation, liveBefore)
	if err != nil {
		return protocol.TunStatus{}, mapTunApplyError(err)
	}
	defer generated.cleanup()
	if err = m.lockMutation(ctx); err != nil {
		return protocol.TunStatus{}, err
	}
	defer m.unlock()
	if err = m.checkIfRevision(op.IfRevision); err != nil {
		return protocol.TunStatus{}, err
	}
	if candidate.generation != m.currentConfigGeneration() || !sameActiveSubscription(catalog, m.subscriptions.Snapshot()) {
		return protocol.TunStatus{}, protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "configuration inputs changed during TUN preparation"}
	}
	if catalog.ActiveID != "" {
		raw, _, err := m.subscriptions.ReadCache(catalog.ActiveID)
		if err != nil || sha256.Sum256(raw) != sourceHash {
			return protocol.TunStatus{}, protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "subscription cache changed during TUN preparation"}
		}
	}
	previous, err := m.trustedCore.PreviousConfig(ctx)
	if err != nil {
		return protocol.TunStatus{}, err
	}
	if _, err = m.saveSettingsCandidate(candidate); err != nil {
		return protocol.TunStatus{}, err
	}
	// Keep the captured generation until the validated config is committed;
	// publish settings in memory only after both reload and live confirmation.
	if err = m.commitRuntimeConfig(ctx, generated); err != nil {
		mapped := mapTunApplyError(err)
		m.setTunLastError(tunErrorMessage(mapped))
		rollback := m.rollbackTrustedTun(ctx, op, candidate, nil, mapped)
		m.markConfigDegraded(ctx, err)
		return protocol.TunStatus{}, rollback
	}
	live, confirmed := false, false
	if m.controller != nil {
		if configs, e := m.controller.Configs(ctx); e == nil {
			live, confirmed = liveTunEnable(configs)
		}
	}
	if !confirmed || live != enable {
		message := "TUN did not become live after apply"
		if !enable {
			message = "TUN did not become disabled after apply"
		}
		mapped := protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: message}
		m.setTunLastError(message)
		return protocol.TunStatus{}, m.rollbackTrustedTun(ctx, op, candidate, previous, mapped)
	}
	m.publishSettings(candidate)
	m.setTunLastError("")
	_, err = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{ID: op.ID, Source: op.Source, IfRevision: op.IfRevision}, func(s state.Snapshot) (state.Snapshot, error) { markConfigApplied(&s); return s, nil })
	if err != nil {
		return protocol.TunStatus{}, err
	}
	return buildTunStatusFromObservation(candidate.after, m.store.Load().Revision, conflict, &live, ""), nil
}
func sameActiveSubscription(a, b subscription.Catalog) bool {
	if a.ActiveID != b.ActiveID {
		return false
	}
	if a.ActiveID == "" {
		return true
	}
	i, j := a.Index(a.ActiveID), b.Index(b.ActiveID)
	return i >= 0 && j >= 0 && a.Profiles[i].Generation == b.Profiles[j].Generation && a.Profiles[i].Version == b.Profiles[j].Version
}

// rollbackTrustedTun restores settings and the startup-bound configuration,
// degrading mutations only when recovery cannot be confirmed.
func (m *Manager) rollbackTrustedTun(ctx context.Context, op Operation, candidate settingsCandidate, previous []byte, cause error) error {
	recovery := context.WithoutCancel(ctx)
	rollback := settingsCandidate{before: candidate.after, after: candidate.before, changed: candidate.changed}
	_, settingsErr := m.saveSettingsCandidate(rollback)
	var configErr error
	if previous != nil {
		cap, err := m.trustedCore.RestoreConfig(recovery, previous)
		configErr = err
		if err == nil {
			defer func() { _ = cap.Close() }() // Read-only capability; rollback ownership is already settled.
			_, err := cap.Path(recovery)
			configErr = err
			if err == nil {
				reloader, ok := m.controller.(configReloader)
				if !ok {
					configErr = errors.New("mihomo reload is unavailable")
				} else {
					// Reload the startup-bound config after verifying the restored
					// capability, just as commitTrustedRuntimeConfig does.
					configErr = reloader.Reload(recovery, "", true)
				}
			}
		}
		if configErr == nil {
			configs, err := m.controller.Configs(recovery)
			live, ok := liveTunEnable(configs)
			// Confirm the restored generated configuration, which may differ from a
			// previous ephemeral live PATCH. No validation or PATCH bypass on rollback.
			document, parseErr := subscription.ParseDocument(previous)
			want := false
			if tun, ok := document["tun"].(subscription.Document); ok {
				want, _ = tun["enable"].(bool)
			}
			if err != nil || parseErr != nil || !ok || live != want {
				configErr = errors.New("TUN live restore is unconfirmed")
			}
		}
	}
	if settingsErr == nil && configErr == nil {
		return cause
	}
	m.stopCoreOnUnlock.Store(true)
	_, err := m.updateStateLocked(recovery, state.CommandMeta{ID: op.ID, Source: op.Source}, func(s state.Snapshot) (state.Snapshot, error) { err := m.enterMutationDegraded(&s); return s, err })
	return err
}
