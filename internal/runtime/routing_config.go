package runtime

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
)

// commitRuntimeConfig keeps GLOBAL restoration in the same subscription/config
// mutation. The catalog already identifies the candidate subscription; the
// caller restores it on failure. Never re-enter the coordinator here.
func (m *Manager) commitRuntimeConfig(ctx context.Context, candidate configCandidate) error {
	return m.commitRuntimeConfigTracked(ctx, candidate, nil)
}

type routingCheckpoint struct {
	content []byte
	mode    string
	exit    string
}

// commitRuntimeConfigTracked shares the captured state with settings transactions
// that may need to compensate a successful reload after a later persistence failure.
func (m *Manager) commitRuntimeConfigTracked(ctx context.Context, candidate configCandidate, outer *routingCheckpoint) error {
	if m.loggingUnsaved && ctx.Value(controlledLoggingContextKey{}) != true {
		return loggingCoreError("save the observed core logging level before reloading configuration", nil)
	}
	if candidate.generationBound && candidate.generation != m.currentConfigGeneration() {
		return routingConflict()
	}
	settings := m.settingsSnapshot()
	if settings.Routing == nil && outer == nil {
		return m.commitRuntimeConfigBytes(ctx, candidate)
	}
	previous, err := m.egressPreviousConfig(ctx)
	if err != nil {
		return routingUnavailable("capture configuration before routing reload", err)
	}
	before := routingCheckpoint{content: previous}
	if settings.Routing == nil {
		*outer = before
		return m.commitRuntimeConfigBytes(ctx, candidate)
	}
	oldMode, err := m.observeRoutingMode(ctx)
	if err != nil {
		return err
	}
	oldGlobal, globalErr := m.observeGlobal(ctx)
	// Rule/Direct can also have a preselected exit. Reload must not start
	// without capturing the old selection needed for compensation.
	if globalErr != nil {
		return globalErr
	}
	before.mode, before.exit = oldMode, oldGlobal.Now
	if outer != nil {
		*outer = before
	}
	err = m.commitRuntimeConfigBytes(ctx, candidate)
	if err == nil {
		_, _, err = m.changeRoutingLocked(ctx, settings.RoutingMode(), "", true, false)
	}
	if err == nil {
		return nil
	}
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	restoreErr := m.restoreRoutingCheckpoint(recovery, before)
	if restoreErr != nil {
		m.mutationDegraded.Store(true)
		m.stopCoreOnUnlock.Store(m.trustedCore != nil)
		return degradedConfigError(err, restoreErr)
	}
	return err
}

func (m *Manager) restoreRoutingCheckpoint(ctx context.Context, before routingCheckpoint) error {
	if err := m.restoreRoutingConfig(ctx, before.content); err != nil {
		return err
	}
	if before.mode != "" {
		return m.applyRoutingLive(ctx, before.mode, before.exit)
	}
	return nil
}

func (m *Manager) restoreRoutingConfig(ctx context.Context, content []byte) error {
	reloader, ok := m.controller.(configReloader)
	if !ok {
		return errors.New("configuration reloader is unavailable")
	}
	if m.trustedCore == nil {
		if err := config.AtomicWrite(m.runtimeConfig, content, 0o600); err != nil {
			return err
		}
		return reloader.Reload(ctx, m.runtimeConfig, true)
	}
	cap, err := m.trustedCore.RestoreConfig(ctx, content)
	if err != nil {
		return err
	}
	defer func() { _ = cap.Close() }() // Read-only capability has no pending writes.
	if _, err := cap.Path(ctx); err != nil {
		return err
	}
	return reloader.Reload(ctx, "", true)
}

// settleRoutingSettings retains any reload fallback in an outer settings
// transaction that staged its file before config publication (trusted TUN).
func (m *Manager) settleRoutingSettings(ctx context.Context, candidate settingsCandidate) (settingsCandidate, error) {
	current := m.settingsSnapshot()
	if reflect.DeepEqual(candidate.after.Routing, current.Routing) {
		return candidate, nil
	}
	candidate.after.Routing = current.Routing
	candidate.changed = !reflect.DeepEqual(candidate.before, candidate.after)
	if _, err := m.saveSettingsCandidate(ctx, candidate); err != nil {
		return candidate, err
	}
	return candidate, nil
}
