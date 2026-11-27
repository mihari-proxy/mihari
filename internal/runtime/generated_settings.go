package runtime

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

// applyGeneratedSettings updates a single editor's settings and publishes its reply
// while the committed revision is still current. update can run again after reload
// to preserve unrelated changes made by the core (such as GLOBAL selection).
func (m *Manager) applyGeneratedSettings(ctx context.Context, op Operation, component string,
	update func(*config.Settings) error, status func(config.Settings, bool) (any, error)) (any, error) {
	if err := m.lockMutation(ctx); err != nil {
		return nil, err
	}
	owned := true
	defer func() {
		if owned {
			m.unlock()
		}
	}()
	if err := m.checkIfRevision(op.IfRevision); err != nil {
		return nil, err
	}
	candidate, err := m.prepareSettings(update)
	if err != nil {
		return nil, err
	}
	if !candidate.changed {
		return status(candidate.after, false)
	}
	catalog, epoch := m.loggingCatalog(), m.coreEpoch
	hash, err := m.loggingCacheHash(catalog.ActiveID)
	if err != nil {
		return nil, err
	}
	stopped := m.routingCoreStopped()
	if !stopped && m.store.Load().Core.Status != "running" {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core is not ready for configuration changes"}
	}
	m.unlock()
	owned = false
	var generated configCandidate
	if stopped && !m.binaryExists() {
		document, e := m.catalogSourceDocument(catalog)
		if e != nil {
			return nil, e
		}
		_, err = subscription.Generate(document, nil, candidate.after)
	} else {
		generated, err = m.prepareCatalogConfigWithSettings(ctx, catalog, candidate.after, candidate.generation)
	}
	if err != nil {
		return nil, err
	}
	defer func() { collectWarning(ctx, component, "candidate.cleanup.failed", generated.cleanup()) }()
	if err := m.lockMutation(ctx); err != nil {
		return nil, err
	}
	owned = true
	newHash, err := m.loggingCacheHash(catalog.ActiveID)
	if err != nil {
		return nil, err
	}
	if hash != newHash || candidate.generation != m.currentConfigGeneration() || epoch != m.coreEpoch || !reflect.DeepEqual(catalog, m.loggingCatalog()) || stopped != m.routingCoreStopped() {
		return nil, routingConflict()
	}
	if err := m.checkIfRevision(op.IfRevision); err != nil {
		return nil, err
	}
	var previous []byte
	if !stopped {
		if m.loggingUnsaved {
			return nil, loggingCoreError("save the observed core logging level before reloading configuration", nil)
		}
		previous, err = m.egressPreviousConfig(ctx)
		if err != nil {
			return nil, err
		}
		if err = m.commitRuntimeConfig(ctx, generated); err != nil {
			m.markConfigDegraded(ctx, err)
			return nil, err
		}
		// Reload may publish a restored GLOBAL selection. Retain that result.
		candidate, err = m.prepareSettings(update)
		if err != nil {
			return nil, err
		}
	}
	if _, err = m.saveSettingsCandidate(ctx, candidate); err != nil {
		if !stopped {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			if restoreErr := m.restoreRoutingConfig(recovery, previous); restoreErr != nil {
				m.mutationDegraded.Store(true)
				m.stopCoreOnUnlock.Store(true)
				err = diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: component + " recovery failed; restart required", Details: map[string]any{"degraded": true}}, errors.Join(err, restoreErr))
				m.markConfigDegraded(recovery, err)
			}
		}
		return nil, err
	}
	m.publishSettings(candidate)
	if _, err = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{ID: op.ID, Source: op.Source}, func(s state.Snapshot) (state.Snapshot, error) {
		if !stopped {
			markConfigApplied(&s)
		}
		return s, nil
	}); err != nil {
		return nil, err
	}
	return status(candidate.after, true)
}
