package runtime

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

// PrepareCoreStart prepares saved configuration and holds mutation ownership
// until the caller has started the child and invoked the returned release.
func (m *Manager) PrepareCoreStart(ctx context.Context) (func(), error) {
	if guard, ok := m.installer.(interface{ CheckExecution(context.Context) error }); ok {
		if err := guard.CheckExecution(ctx); err != nil {
			return nil, err
		}
	}
	if err := m.lockMutation(ctx); err != nil {
		return nil, err
	}
	m.coreEpoch++
	epoch := m.coreEpoch
	current := m.store.Load().Core
	current.Status, current.PID = "starting", 0
	m.setCoreStateLocked(current)
	settings, generation := m.configInputs()
	catalog := m.loggingCatalog()
	sourceHash, err := m.loggingCacheHash(catalog.ActiveID)
	m.releaseMutation()
	if err != nil {
		return nil, err
	}

	candidate, err := m.prepareCatalogConfigWithSettings(ctx, catalog, settings, generation)
	if err != nil {
		return nil, err
	}
	success := false
	var cleanupErr error
	defer func() {
		cleanupErr = candidate.cleanup()
		if !success {
			m.reportWarning(ctx, "logging", "candidate.cleanup.failed", cleanupErr)
		}
	}()
	if err := m.lockMutation(ctx); err != nil {
		return nil, err
	}
	defer func() {
		if !success {
			m.releaseMutation()
		}
	}()
	currentHash, err := m.loggingCacheHash(catalog.ActiveID)
	if err != nil || currentHash != sourceHash {
		return nil, routingConflict()
	}
	if generation != m.currentConfigGeneration() || epoch != m.coreEpoch || !reflect.DeepEqual(catalog, m.loggingCatalog()) {
		return nil, protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "configuration inputs changed during startup"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sha256.Sum256(candidate.content) != candidate.hash {
		return nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "validated startup configuration changed"}
	}
	if m.trustedCore != nil {
		capability, err := m.trustedCore.Publish(ctx, candidate.generated, candidate.hash)
		if err != nil {
			var api protocol.APIError
			if errors.As(err, &api) && api.Details["degraded"] == true {
				m.mutationDegraded.Store(true)
				_, _ = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{Source: "logging-startup"}, func(s state.Snapshot) (state.Snapshot, error) {
					s.Health, s.Core.Status = "degraded", "degraded"
					s.LastError = "Startup configuration recovery could not be confirmed"
					return s, nil
				}) // The mutation fence is active even if status publication fails.
			}
			return nil, err
		}
		if err := capability.Close(); err != nil {
			return nil, err
		}
	} else if err := config.AtomicWrite(m.runtimeConfig, candidate.content, 0o600); err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "publish startup configuration"}, err)
	}
	m.settingsMu.Lock()
	m.configGeneration++
	m.settingsMu.Unlock()
	m.loggingUnsaved = false
	m.loggingObservation = loggingObservation{}
	success = true
	return func() {
		m.releaseMutation()
		m.reportWarning(ctx, "logging", "candidate.cleanup.failed", cleanupErr)
	}, nil
}

func (m *Manager) loggingCatalog() subscription.Catalog {
	if m.subscriptions == nil {
		return subscription.Catalog{}
	}
	return m.subscriptions.Snapshot()
}

func (m *Manager) loggingCacheHash(id string) ([32]byte, error) {
	if id == "" {
		return [32]byte{}, nil
	}
	raw, _, err := m.subscriptions.ReadCache(id)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
