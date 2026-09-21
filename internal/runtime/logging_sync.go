package runtime

import (
	"context"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/state"
)

type loggingObservation struct {
	level   string
	state   string
	message string
}

// SyncLogging observes and adopts the current core level without changing it.
func (m *Manager) SyncLogging(ctx context.Context) error {
	ctx, finish, admissionErr := m.beginApplicationWork(ctx)
	if admissionErr != nil {
		return nil
	} // Paused background observation does not start a new operation.
	defer finish()
	ctx, batch := newOperationDiagnostics(ctx, "logging-sync:")
	defer m.flushDiagnostics(ctx, batch)
	if m.logging == nil || !m.businessMutationAllowed() || m.mutationDegraded.Load() {
		return nil
	}
	if err := m.lockMaintenance(ctx); err != nil {
		return err
	}
	if m.store.Load().Core.Status != "running" {
		m.unlock()
		return nil
	}
	epoch, generation := m.coreEpoch, m.currentConfigGeneration()
	m.unlock()
	_, readErr := m.observeLoggingLevel(ctx)
	if err := m.lockMutation(ctx); err != nil {
		return err
	}
	defer m.unlock()
	if epoch != m.coreEpoch || generation != m.currentConfigGeneration() || m.store.Load().Core.Status != "running" {
		return nil
	}
	if readErr != nil {
		m.publishLoggingObservation(ctx, loggingObservation{state: "unknown", message: "Core logging state is unavailable"})
		return readErr
	}
	// Re-read after acquiring ownership; never retry a captured old payload.
	live, err := m.observeLoggingLevel(ctx)
	if err != nil {
		m.publishLoggingObservation(ctx, loggingObservation{state: "unknown", message: "Core logging state is unavailable"})
		return err
	}
	candidate, err := m.prepareSettings(func(settings *config.Settings) error {
		log := settings.EffectiveLogging()
		log.Level = live
		settings.SetLogging(log)
		return nil
	})
	if err == nil && candidate.before.EffectiveLogging().Level != live {
		_, err = m.saveSettingsCandidate(ctx, candidate)
		if err == nil {
			m.publishSettings(candidate)
			cfg, _ := loggingConfig(candidate.after.EffectiveLogging()) // prepareSettings validated all fields.
			m.logging.Apply(ctx, cfg)
		}
	}
	if err != nil {
		m.loggingUnsaved = true
		m.publishLoggingObservation(ctx, loggingObservation{level: live, state: "unsaved", message: "Core logging changed; saving failed and will be retried"})
		return err
	}
	m.loggingUnsaved = false
	m.publishLoggingObservation(ctx, loggingObservation{level: live, state: "applied"})
	return nil
}

func (m *Manager) publishLoggingObservation(ctx context.Context, observed loggingObservation) {
	if m.loggingObservation == observed {
		return
	}
	m.loggingObservation = observed
	_, _ = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{Source: "logging-sync"}, func(s state.Snapshot) (state.Snapshot, error) { return s, nil })
	// No fallible state work is performed; publication only advances revision.
}

func (m *Manager) runLoggingObserver(ctx context.Context) {
	wait := m.loggingWait
	if wait == nil {
		wait = func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	var lastError string
	for wait(ctx, 2*time.Second) == nil {
		err := m.SyncLogging(ctx)
		if err == nil {
			lastError = ""
			continue
		}
		// Consecutive identical failures retain their UI state without producing
		// a warning every tick. A recovered or changed failure is reported anew.
		if err.Error() != lastError {
			m.reportWarning(ctx, "logging", "sync.failed", err)
			lastError = err.Error()
		}
	}
}
