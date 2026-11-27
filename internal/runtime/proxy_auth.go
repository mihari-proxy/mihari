package runtime

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
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

func (m *Manager) proxyAuthStatus(settings config.Settings, reveal bool) (protocol.ProxyAuthStatus, error) {
	auth := settings.EffectiveProxyAuth()
	document, err := m.catalogSourceDocument(m.loggingCatalog())
	if err != nil {
		return protocol.ProxyAuthStatus{}, err
	}
	sources, err := subscription.ApplyConfigOverrides(document, settings.Overrides)
	if err != nil {
		return protocol.ProxyAuthStatus{}, err
	}
	users, err := subscription.ApplyProxyAuthWithSources(document, auth, sources)
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
		update := func(s *config.Settings) error {
			if request.Reset {
				s.ProxyAuth = nil
				return nil
			}
			auth := resolveProxyAuthPasswords(request.Config, s.EffectiveProxyAuth())
			s.SetProxyAuth(auth)
			return nil
		}
		return m.applyGeneratedSettings(ctx, op, "proxy-auth", update, func(settings config.Settings, committed bool) (any, error) {
			if !committed {
				return m.proxyAuthStatus(settings, false)
			}
			return m.committedProxyAuthStatus(ctx, settings)
		})
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
