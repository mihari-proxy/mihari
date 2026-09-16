package runtime

import (
	"context"
	"errors"
	"os"
	"reflect"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
)

type controlledLoggingContextKey struct{}

func (m *Manager) loggingCoreStopped() bool {
	core := m.store.Load().Core
	return core.PID == 0 && (core.Status == "stopped" || core.Status == "missing" || (core.Status == "" && !m.running.Load()))
}

// prepareLoggingUpdate returns mutation ownership only on success. Validation
// runs outside ownership; all captured inputs are rechecked before publication.
func (m *Manager) prepareLoggingUpdate(ctx context.Context, op Operation, update LoggingUpdate) (settingsCandidate, configCandidate, error) {
	if err := m.lockMutation(ctx); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	owned := true
	success := false
	defer func() {
		if owned && !success {
			m.unlock()
		}
	}()
	if err := m.checkIfRevision(op.IfRevision); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	candidate, err := m.prepareSettings(func(settings *config.Settings) error {
		effective := settings.EffectiveLogging()
		if update.Level != nil {
			effective.Level = *update.Level
		}
		if update.MaxSizeMB != nil {
			effective.MaxSizeMB = *update.MaxSizeMB
		}
		if update.MaxFiles != nil {
			effective.MaxFiles = *update.MaxFiles
		}
		settings.SetLogging(effective)
		return nil
	})
	if err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	if candidate.before.EffectiveLogging() == candidate.after.EffectiveLogging() {
		candidate.after, candidate.changed = candidate.before.Clone(), false
	}
	if err := ctx.Err(); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	if update.Level == nil || m.loggingCoreStopped() {
		success = true
		return candidate, configCandidate{}, nil
	}
	if m.store.Load().Core.Status != "running" {
		return settingsCandidate{}, configCandidate{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core logging state is not ready"}
	}
	catalog, epoch := m.loggingCatalog(), m.coreEpoch
	sourceHash, err := m.loggingCacheHash(catalog.ActiveID)
	if err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	m.unlock()
	owned = false
	generated, err := m.prepareCatalogConfigWithSettings(ctx, catalog, candidate.after, candidate.generation)
	if err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	defer func() {
		if !success {
			collectWarning(ctx, "logging", "candidate.cleanup.failed", generated.cleanup())
		}
	}()
	if err := m.lockMutation(ctx); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	owned = true
	currentHash, err := m.loggingCacheHash(catalog.ActiveID)
	if err != nil || sourceHash != currentHash {
		return settingsCandidate{}, configCandidate{}, routingConflict()
	}
	if candidate.generation != m.currentConfigGeneration() || epoch != m.coreEpoch || !reflect.DeepEqual(catalog, m.loggingCatalog()) || m.store.Load().Core.Status != "running" {
		return settingsCandidate{}, configCandidate{}, routingConflict()
	}
	if err := m.checkIfRevision(op.IfRevision); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	success = true
	return candidate, generated, nil
}

func (m *Manager) observeLoggingLevel(ctx context.Context) (string, error) {
	if m.controller == nil {
		return "", loggingCoreError("core logging controller is unavailable", nil)
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	document, err := m.controller.Configs(readCtx)
	if err != nil {
		return "", loggingCoreError("read core logging level", err)
	}
	value, ok := document["log-level"].(string)
	level, valid := config.ObservedLoggingLevel(value)
	if !ok || !valid {
		return "", loggingCoreError("core returned an unsupported logging level", nil)
	}
	return level, nil
}

func (m *Manager) patchLoggingLevel(ctx context.Context, level string) error {
	if level == "warn" {
		level = "warning"
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return m.controller.PatchConfigs(writeCtx, map[string]any{"log-level": level})
}

func loggingCoreError(message string, cause error) error {
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: message}, cause)
}

func (m *Manager) confirmLoggingLevel(ctx context.Context, want string) error {
	got, err := m.observeLoggingLevel(ctx)
	if err != nil {
		return err
	}
	if got != want {
		return loggingCoreError("core logging change could not be confirmed", nil)
	}
	return nil
}

func (m *Manager) commitLoggingCore(ctx context.Context, candidate settingsCandidate, generated configCandidate) (settingsCandidate, error) {
	oldLevel, err := m.observeLoggingLevel(ctx)
	if err != nil {
		return candidate, err
	}
	var previous []byte
	if m.trustedCore != nil {
		previous, err = m.trustedCore.PreviousConfig(ctx)
	} else {
		previous, err = os.ReadFile(m.runtimeConfig)
	}
	if err != nil {
		return candidate, loggingCoreError("capture runtime configuration before logging change", err)
	}
	var oldMode, oldExit string
	if candidate.before.Routing != nil {
		oldMode, err = m.observeRoutingMode(ctx)
		if err != nil {
			return candidate, err
		}
		group, err := m.observeGlobal(ctx)
		if err != nil {
			return candidate, err
		}
		oldExit = group.Now
	}
	runtimeAttempted := false
	before := candidate.before
	compensate := func(cause error) (settingsCandidate, error) {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		var restoreErr error
		if runtimeAttempted {
			restoreErr = m.restoreRoutingConfig(recovery, previous)
			if oldMode != "" {
				restoreErr = errors.Join(restoreErr, m.applyRoutingLive(recovery, oldMode, oldExit))
			}
		}
		// A failed response may still have changed the core. Readback, not the
		// response, decides whether compensation succeeded.
		patchErr := m.patchLoggingLevel(recovery, oldLevel)
		if checkErr := m.confirmLoggingLevel(recovery, oldLevel); checkErr != nil {
			restoreErr = errors.Join(restoreErr, patchErr, checkErr)
		}
		if !reflect.DeepEqual(before, m.settingsSnapshot()) {
			_, saveErr := m.restoreSettings(recovery, before)
			restoreErr = errors.Join(restoreErr, saveErr)
		}
		if restoreErr != nil {
			m.mutationDegraded.Store(true)
			m.stopCoreOnUnlock.Store(m.trustedCore != nil)
			cause = diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "logging recovery could not be confirmed; restart required", Details: map[string]any{"degraded": true}}, errors.Join(cause, restoreErr))
			_, _ = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{Source: "logging"}, func(s state.Snapshot) (state.Snapshot, error) {
				s.Health, s.LastError = "degraded", "Logging recovery could not be confirmed; restart required"
				return s, nil
			}) // The mutation fence already protects callers if publication fails.
		}
		return candidate, cause
	}
	want := candidate.after.EffectiveLogging().Level
	m.loggingObservation = loggingObservation{} // A failed write must not retain an old "applied" observation.
	if err := m.patchLoggingLevel(ctx, want); err != nil {
		return compensate(loggingCoreError("update core logging level", err))
	}
	if err := m.confirmLoggingLevel(ctx, want); err != nil {
		return compensate(err)
	}
	runtimeAttempted = true
	if err := m.commitRuntimeConfig(context.WithValue(ctx, controlledLoggingContextKey{}, true), generated); err != nil {
		return compensate(err)
	}
	if err := m.confirmLoggingLevel(ctx, want); err != nil {
		return compensate(err)
	}
	// A routing fallback during reload may have committed a new selection.
	// Retain it instead of replacing it with the pre-validation snapshot.
	target := candidate.after.EffectiveLogging()
	candidate, err = m.prepareSettings(func(settings *config.Settings) error { settings.SetLogging(target); return nil })
	if err != nil {
		return compensate(err)
	}
	if _, err := m.saveSettingsCandidate(ctx, candidate); err != nil {
		return compensate(err)
	}
	return candidate, nil
}
