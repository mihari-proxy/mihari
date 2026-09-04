package runtime

import (
	"context"
	"errors"
	"reflect"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
)

type settingsCandidate struct {
	before  config.Settings
	after   config.Settings
	changed bool
}

func (m *Manager) settingsSnapshot() config.Settings {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.settings.Clone()
}

func (m *Manager) prepareSettings(update func(*config.Settings) error) (settingsCandidate, error) {
	before := m.settingsSnapshot()
	after := before.Clone()
	if err := update(&after); err != nil {
		return settingsCandidate{}, err
	}
	if err := after.Validate(); err != nil {
		return settingsCandidate{}, err
	}
	return settingsCandidate{before: before, after: after, changed: !reflect.DeepEqual(before, after)}, nil
}

func (m *Manager) saveSettingsCandidate(candidate settingsCandidate) (config.CommitResult, error) {
	if !candidate.changed {
		return config.CommitResult{Committed: true}, nil
	}
	if m.settingsPath == "" {
		return config.CommitResult{Committed: true}, nil
	}
	result, err := m.saveSettings(m.settingsPath, candidate.after.Clone())
	if (err != nil && result.Committed) || (err == nil && !result.Committed) {
		return config.CommitResult{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist settings"}
	}
	if err != nil {
		return result, protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist settings"}
	}
	if result.Warning != nil {
		m.reportBackground("settings", errors.New("parent directory sync failed after commit"))
	}
	return result, nil
}

func (m *Manager) publishSettings(candidate settingsCandidate) {
	if !candidate.changed {
		return
	}
	m.settingsMu.Lock()
	m.settings = candidate.after.Clone()
	m.settingsMu.Unlock()
}

func (m *Manager) updateSettings(update func(*config.Settings) error) (settingsCandidate, error) {
	if m.mutationDegraded.Load() {
		return settingsCandidate{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mutation compensation failed; restart required"}
	}
	candidate, err := m.prepareSettings(update)
	if err != nil || !candidate.changed {
		return candidate, err
	}
	if _, err := m.saveSettingsCandidate(candidate); err != nil {
		return settingsCandidate{}, err
	}
	m.publishSettings(candidate)
	return candidate, nil
}

func (m *Manager) restoreSettings(settings config.Settings) (config.CommitResult, error) {
	before := m.settingsSnapshot()
	candidate := settingsCandidate{before: before, after: settings.Clone(), changed: !reflect.DeepEqual(before, settings)}
	result, err := m.saveSettingsCandidate(candidate)
	if err != nil {
		return result, err
	}
	if result.Committed {
		m.publishSettings(candidate)
	}
	return result, nil
}

func (m *Manager) enterMutationDegraded(snapshot *state.Snapshot) error {
	m.mutationDegraded.Store(true)
	snapshot.Health = "degraded"
	snapshot.LastError = "mutation compensation failed; restart required"
	return state.CommittedError{Err: protocol.APIError{Code: protocol.CodeDataFailure, Message: "mutation compensation failed"}}
}

func (m *Manager) checkIfRevision(revision *uint64) error {
	if revision == nil || *revision == m.store.Load().Revision {
		return nil
	}
	return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "state revision changed", Details: map[string]any{
		"expected_revision": *revision,
		"current_revision":  m.store.Load().Revision,
	}}
}

func (m *Manager) lockMaintenance(ctx context.Context) error {
	select {
	case <-m.maintenance:
		if err := m.checkOpen(); err != nil {
			m.unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) lockMutation(ctx context.Context) error {
	if err := m.lockMaintenance(ctx); err != nil {
		return err
	}
	if m.mutationDegraded.Load() {
		m.unlock()
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mutation compensation failed; restart required"}
	}
	return nil
}

func (m *Manager) updateStateLocked(ctx context.Context, meta state.CommandMeta, update func(state.Snapshot) (state.Snapshot, error)) (state.Snapshot, error) {
	return m.coordinator.Do(ctx, meta, update)
}

func (m *Manager) persistSettings() error {
	_, err := m.saveSettingsCandidate(settingsCandidate{after: m.settings.Clone(), changed: true})
	return err
}
