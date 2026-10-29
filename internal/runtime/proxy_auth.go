package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"reflect"
	"time"
)

// ProxyAuth returns effective users; only explicit reveal reads include passwords.
func (m *Manager) ProxyAuth(ctx context.Context, reveal bool) (protocol.ProxyAuthStatus, error) {
	if err := m.lockMaintenance(ctx); err != nil {
		return protocol.ProxyAuthStatus{}, err
	}
	settings := m.settingsSnapshot()
	status, err := m.proxyAuthStatus(settings, reveal)
	revision := m.store.Load().Revision
	m.unlock()
	if err != nil {
		m.reportWarning(ctx, "proxy-auth", "status.unresolved", err)
		return proxyAuthFallback(settings, revision, "Saved proxy authentication cannot be resolved; inspect the subscription or reset proxy authentication", reveal), nil
	}
	return status, nil
}

func (m *Manager) proxyAuthDocument() (subscription.Document, error) {
	document := subscription.Document{}
	catalog := m.loggingCatalog()
	if catalog.ActiveID != "" {
		_, base, err := m.subscriptions.ReadCache(catalog.ActiveID)
		if err != nil {
			return nil, err
		}
		for k, v := range base {
			document[k] = v
		}
	}
	return document, nil
}
func (m *Manager) proxyAuthStatus(settings config.Settings, reveal bool) (protocol.ProxyAuthStatus, error) {
	auth := settings.EffectiveProxyAuth()
	document, err := m.proxyAuthDocument()
	if err != nil {
		return protocol.ProxyAuthStatus{}, err
	}
	users, err := subscription.ApplyProxyAuth(document, auth)
	if err != nil {
		return protocol.ProxyAuthStatus{}, err
	}
	prefixes := []string{}
	switch values := document["skip-auth-prefixes"].(type) {
	case []string:
		prefixes = append(prefixes, values...)
	case []any:
		for _, v := range values {
			if s, ok := v.(string); ok {
				prefixes = append(prefixes, s)
			}
		}
	}
	if !reveal {
		for i := range auth.Users {
			auth.Users[i].Password = ""
		}
		for i := range users {
			users[i].Password = ""
		}
	}
	return protocol.ProxyAuthStatus{Schema: "mihari/v1", Revision: m.store.Load().Revision, Config: auth, Users: users, BypassPrefixes: prefixes}, nil
}

// UpdateProxyAuth validates off the commit gate and applies through the shared configuration transaction.
func (m *Manager) UpdateProxyAuth(ctx context.Context, op Operation, request protocol.ProxyAuthUpdateRequest) (protocol.ProxyAuthStatus, error) {
	if op.ID == "" {
		return protocol.ProxyAuthStatus{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "operation ID is required"}
	}
	result, err := m.doOperation(ctx, "proxy-auth:"+op.ID, func(ctx context.Context) (any, error) {
		if m.recoveryOnly {
			if !request.Reset || !m.authResetAllowed {
				return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "only proxy-auth reset is available; other startup errors require data reset"}
			}
			if err := m.lockMaintenance(ctx); err != nil {
				return nil, err
			}
			defer m.unlock()
			if err := m.checkIfRevision(op.IfRevision); err != nil {
				return nil, err
			}
			candidate, err := m.prepareSettings(func(s *config.Settings) error { s.ProxyAuth = nil; return nil })
			if err != nil {
				return nil, err
			}
			candidate.changed = true
			if _, err = m.saveSettingsCandidate(ctx, candidate); err != nil {
				return nil, err
			}
			m.publishSettings(candidate)
			m.onboardingRestartRequired.Store(true)
			_, err = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{ID: op.ID, Source: op.Source}, func(s state.Snapshot) (state.Snapshot, error) {
				s.LastError = "Proxy authentication reset; restart the daemon"
				return s, nil
			})
			if err != nil {
				return nil, err
			}
			return m.committedProxyAuthStatus(ctx, candidate.after)
		}
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
		candidate, err := m.prepareSettings(func(s *config.Settings) error {
			if request.Reset {
				s.ProxyAuth = nil
				return nil
			}
			auth := request.Config
			old := s.EffectiveProxyAuth()
			// Empty passwords preserve an existing override; new credentials must be explicit.
			auth.Users = append([]protocol.ProxyAuthUser{}, auth.Users...)
			for i := range auth.Users {
				auth.Users[i].Source = ""
				if auth.Users[i].Password == "" {
					for _, u := range old.Users {
						if u.Username == auth.Users[i].Username {
							auth.Users[i].Password = u.Password
						}
					}
				}
			}
			s.SetProxyAuth(auth)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if !candidate.changed {
			return m.proxyAuthStatus(candidate.after, false)
		}
		if _, err := m.proxyAuthStatus(candidate.after, false); err != nil {
			return nil, err
		}
		catalog, epoch := m.loggingCatalog(), m.coreEpoch
		hash, err := m.loggingCacheHash(catalog.ActiveID)
		if err != nil {
			return nil, err
		}
		stopped := m.routingCoreStopped()
		if !stopped && m.store.Load().Core.Status != "running" {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core is not ready for authentication changes"}
		}
		m.unlock()
		owned = false
		var generated configCandidate
		if stopped && !m.binaryExists() {
			document, e := m.proxyAuthDocument()
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
		defer func() { collectWarning(ctx, "proxy-auth", "candidate.cleanup.failed", generated.cleanup()) }()
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
			next := candidate.after.ProxyAuth
			candidate, err = m.prepareSettings(func(s *config.Settings) error { s.ProxyAuth = next; return nil })
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
					err = diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "proxy authentication recovery failed; restart required", Details: map[string]any{"degraded": true}}, errors.Join(err, restoreErr))
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
		return m.committedProxyAuthStatus(ctx, candidate.after)
	})
	if err != nil {
		return protocol.ProxyAuthStatus{}, err
	}
	return result.(protocol.ProxyAuthStatus), nil
}

func proxyAuthFallback(settings config.Settings, revision uint64, problem string, reveal bool) protocol.ProxyAuthStatus {
	auth := settings.EffectiveProxyAuth()
	if !reveal {
		for i := range auth.Users {
			auth.Users[i].Password = ""
		}
	}
	return protocol.ProxyAuthStatus{Schema: "mihari/v1", Revision: revision, Config: auth, Users: []protocol.ProxyAuthUser{}, BypassPrefixes: []string{}, Problem: problem}
}
func (m *Manager) committedProxyAuthStatus(ctx context.Context, settings config.Settings) (protocol.ProxyAuthStatus, error) {
	status, err := m.proxyAuthStatus(settings, false)
	if err == nil {
		return status, nil
	}
	collectWarning(ctx, "proxy-auth", "status.unresolved", err)
	return proxyAuthFallback(settings, m.store.Load().Revision, "Authentication settings saved; effective status is unavailable. Inspect the subscription or restart the daemon.", false), nil
}
